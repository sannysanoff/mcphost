package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sannysanoff/mcphost/pkg/history"
	"github.com/sannysanoff/mcphost/pkg/system"
	"github.com/sannysanoff/mcphost/pkg/testing_stuff"
	"io"        // Added for io.ReadAll
	"math/rand" // Added for random number generation
	"os"
	"path/filepath" // Added for path manipulation
	"regexp"
	"strings"
	"time"

	"bytes"
	"text/template"

	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
	"github.com/charmbracelet/log"

	"github.com/charmbracelet/glamour"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sannysanoff/mcphost/pkg/llm/anthropic"
	"github.com/sannysanoff/mcphost/pkg/llm/google"
	"github.com/sannysanoff/mcphost/pkg/llm/ollama"
	"github.com/sannysanoff/mcphost/pkg/llm/openai"
	"github.com/spf13/cobra"

	"golang.org/x/term"
	"net/http"  // Added for HTTP server
	"os/signal" // Added for signal handling
	"sync"      // Added for mutex
	"syscall"   // Added for signal handling
)

var (
	// CLI Mode specific
	renderer *glamour.TermRenderer

	// Shared flags/config
	configFile string
	// systemPromptFile string // Removed
	messageWindow    int
	agentNameFlag    string // Agent name (e.g., "default")
	TracesDir        string // Directory for trace files
	modelsConfigFile string // Path to models.yaml

	loadedModelsConfig *ModelsConfig // Parsed models.yaml

	// Server Mode specific
	serverMode       bool
	serverPort       int
	currentJobID     string
	jobMutex         sync.Mutex
	currentJobCtx    context.Context
	currentJobCancel context.CancelFunc
	userPromptCLI    string // For --user-prompt flag
	enableCaching    bool   // Global flag to enable/disable caching
)

const (
	initialBackoff = 1 * time.Second
	maxBackoff     = 30 * time.Second
	maxRetries     = 5 // Will reach close to max backoff
	traceFilePerm  = 0644
	llmCacheDir    = "llm_cache" // Directory for LLM call caching
)

var rootCmd = &cobra.Command{
	Use:   "mcphost",
	Short: "Chat with AI models through a unified interface",
	Long: `MCPHost is a CLI tool that allows you to interact with various AI models
through a unified interface. It supports various tools through MCP servers
and provides streaming responses.

model selection is driven by agents. Specify an agent using the --agent flag (default: "default").
The agent determines a task, and MCPHost selects the best model for that task from 'models.yaml'.

Example (CLI):
  mcphost --agent default --models path/to/your/models.yaml
  mcphost --agent research_agent

Server Mode:
  mcphost --server --traces /path/to/traces --models path/to/models.yaml [--port 9262]
  curl -X POST -H "Content-Type: application/json" \
       -d '{"agent_name": "default", "system_message": "You are a helpful assistant.", "user_query": "Hello, world!"}' \
       http://localhost:9262/start
  # 'agent_name' is optional in the /start payload. If omitted, the "default" agent will be used.
  curl http://localhost:9262/status
  curl http://localhost:9262/stop?id=TRACE_ID
  curl http://localhost:9262/models
  curl http://localhost:9262/agents`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate --traces flag early, as it's required for both modes
		if TracesDir == "" {
			return fmt.Errorf("--traces flag is required")
		}
		if _, err := os.Stat(TracesDir); os.IsNotExist(err) {
			if err := os.MkdirAll(TracesDir, 0755); err != nil {
				return fmt.Errorf("traces directory does not exist and could not be created: %s, error: %w", TracesDir, err)
			}
			log.Info("Created traces directory", "path", TracesDir)
		} else if err != nil {
			return fmt.Errorf("error checking traces directory %s: %w", TracesDir, err)
		}

		// Set up logging based on debug flag
		if debugMode {
			log.SetLevel(log.DebugLevel)
			log.SetReportCaller(true)
		} else {
			log.SetLevel(log.InfoLevel)
			log.SetReportCaller(false)
		}

		// Load models configuration
		var err error
		loadedModelsConfig, err = LoadModelsConfig(modelsConfigFile)
		if err != nil {
			return fmt.Errorf("failed to load models configuration from '%s': %w", modelsConfigFile, err)
		}
		log.Info("Successfully loaded models configuration", "path", modelsConfigFile, "providers", len(loadedModelsConfig.Providers))

		// Agent name validation (e.g., ensuring it's not an empty string if required)
		// can be done here if needed, but typically agent loading handles non-existence.
		if agentNameFlag == "" {
			// This case should ideally be prevented by Cobra's default value mechanism.
			// If it can still occur, assign the default explicitly.
			agentNameFlag = "default"
			log.Warn("Agent name flag was empty, defaulting to 'default'. This might indicate an issue with flag parsing or default value setting.")
		}
		log.Debug("Using agent", "name", agentNameFlag)

		// cyclic ref
		system.PerformLLMCallHook = RunSubAgent

		if serverMode {
			return runServerMode(context.Background(), loadedModelsConfig)
		}
		// For CLI mode, agentNameFlag will be used within runMCPHost.
		return runMCPHost(context.Background(), loadedModelsConfig)
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var debugMode bool

func init() {
	rootCmd.PersistentFlags().
		StringVar(&configFile, "config", "", "config file (default is $HOME/mcp.json)")
	// rootCmd.PersistentFlags().
	// 	StringVar(&systemPromptFile, "system-prompt", "", "system prompt json file") // Removed
	rootCmd.PersistentFlags().
		IntVar(&messageWindow, "message-window", 10, "number of messages to keep in context")
	rootCmd.PersistentFlags().
		StringVarP(&agentNameFlag, "agent", "a", "default",
			"name of the agent to use (e.g., 'default', 'research_agent'). Agents are located in the 'agents' directory.")
	rootCmd.PersistentFlags().
		StringVar(&modelsConfigFile, "models", "models.yaml", "path to the models.yaml configuration file")

	// Add debug flag
	rootCmd.PersistentFlags().
		BoolVar(&debugMode, "debug", false, "enable debug logging")
	rootCmd.PersistentFlags().
		StringVar(&TracesDir, "traces", "./traces", "directory to store trace files (required)")

	// Server mode flags
	rootCmd.Flags().BoolVar(&serverMode, "server", false, "Run in server mode")
	rootCmd.Flags().IntVar(&serverPort, "port", 9262, "Port for server mode")

	// CLI direct prompt flag
	rootCmd.Flags().StringVar(&userPromptCLI, "user-prompt", "", "User prompt to send directly. If specified, runs non-interactively and exits after response.")

	// Caching flag
	rootCmd.PersistentFlags().
		BoolVar(&enableCaching, "enable-caching", false, "enable LLM and tool call caching")
}

// createProvider initializes an LLM provider based on the model ID and models configuration.
func createProvider(ctx context.Context, modelID, systemPrompt string, config *ModelsConfig) (history.Provider, error) {
	if modelID == "" {
		return nil, fmt.Errorf("model ID cannot be empty for provider creation")
	}
	if config == nil {
		return nil, fmt.Errorf("models configuration is not loaded for provider creation")
	}

	for _, providerCfg := range config.Providers {
		for _, modelCfg := range providerCfg.Models {
			if modelCfg.ID == modelID {
				// Found the model ID
				apiKey := providerCfg.APIKey   // APIKey is at the provider level in models.yaml
				baseURL := providerCfg.BaseURL // BaseURL is also at the provider level

				// The modelCfg.Name is the specific model name for the SDK (e.g., "claude-3-5-sonnet-latest")
				// The providerCfg.Name is the type of provider (e.g., "anthropic", "openai")

				log.Debug("Creating provider",
					"providerType", providerCfg.Name,
					"modelID", modelID,
					"sdkModelName", modelCfg.Name,
					"baseURL", baseURL,
					"apiKeyIsSet", apiKey != "")

				switch strings.ToLower(providerCfg.Name) {
				case "anthropic":
					if apiKey == "" {
						return nil, fmt.Errorf("Anthropic API key not found in models.yaml for provider '%s' (model ID '%s')", providerCfg.Name, modelID)
					}
					return anthropic.NewProvider(apiKey, baseURL, modelCfg.Name, systemPrompt), nil
				case "ollama":
					return ollama.NewProvider(baseURL, modelCfg.Name)
				case "openai":
					if apiKey == "" {
						return nil, fmt.Errorf("OpenAI API key not found in models.yaml for provider '%s' (model ID '%s')", providerCfg.Name, modelID)
					}
					return openai.NewProvider(apiKey, baseURL, modelCfg.Name, systemPrompt), nil
				case "google":
					if apiKey == "" {
						return nil, fmt.Errorf("Google API key not found in models.yaml for provider '%s' (model ID '%s')", providerCfg.Name, modelID)
					}
					return google.NewProvider(ctx, apiKey, modelCfg.Name, systemPrompt)
				default:
					return nil, fmt.Errorf("unsupported provider type '%s' found in models.yaml for model ID '%s'", providerCfg.Name, modelID)
				}
			}
		}
	}

	return nil, fmt.Errorf("model with ID '%s' not found in the loaded models configuration ('%s') during provider creation", modelID, modelsConfigFile)
}

// selectModelForTask selects the best model ID for a given task based on preferences.
func selectModelForTask(task string, modelsCfg *ModelsConfig) (string, error) {
	if modelsCfg == nil {
		return "", fmt.Errorf("models configuration is not loaded, cannot select model for task '%s'", task)
	}
	if task == "" {
		log.Warn("Task for model selection is empty, this may lead to suboptimal model choice or failure.")
		// Depending on desired behavior, could default to a generic task or return error.
		// For now, proceed, and if no model has a preference for an empty task, it will fail.
	}

	bestScore := -1 // Use -1 to ensure any actual score (even 0) is better if it's the only one
	var bestModelID string
	foundModelWithPreference := false

	for _, providerCfg := range modelsCfg.Providers {
		for _, modelCfg := range providerCfg.Models {
			score, ok := modelCfg.PreferencesPerTask[task]
			if ok { // Only consider models that explicitly list the task
				log.Debug("Considering model for task", "task", task, "modelID", modelCfg.ID, "provider", providerCfg.Name, "score", score)
				if score > bestScore {
					bestScore = score
					bestModelID = modelCfg.ID
					foundModelWithPreference = true
				}
			}
		}
	}

	if !foundModelWithPreference {
		// If no model has a preference for this specific task, try to find a default or first available model.
		// This part can be adjusted based on desired fallback behavior.
		// For now, let's try to pick the first model overall if no specific preference is found.
		log.Warnf("No model found with a specific preference for task '%s'. Attempting to select a default model.", task)
		if len(modelsCfg.Providers) > 0 && len(modelsCfg.Providers[0].Models) > 0 {
			bestModelID = modelsCfg.Providers[0].Models[0].ID
			log.Infof("Selected first available model as default: %s", bestModelID)
			return bestModelID, nil
		}
		return "", fmt.Errorf("no model found with preferences for task '%s', and no default model could be selected from '%s'", task, modelsConfigFile)
	}

	log.Info("Selected model for task", "task", task, "modelID", bestModelID, "score", bestScore)
	return bestModelID, nil
}

func pruneMessages(messages []history.HistoryMessage) []history.HistoryMessage {
	if len(messages) <= messageWindow {
		return messages
	}

	// Keep only the most recent messages based on window size
	messages = messages[len(messages)-messageWindow:]

	// Handle messages
	toolUseIds := make(map[string]bool)
	toolResultIds := make(map[string]bool)

	// First pass: collect all tool use and result IDs
	for _, msg := range messages {
		for _, block := range msg.Content {
			if block.Type == "tool_use" {
				toolUseIds[block.ID] = true
			} else if block.Type == "tool_result" {
				toolResultIds[block.ToolUseID] = true
			}
		}
	}

	// Second pass: filter out orphaned tool calls/results
	var prunedMessages []history.HistoryMessage
	for _, msg := range messages {
		var prunedBlocks []history.ContentBlock
		for _, block := range msg.Content {
			keep := true
			if block.Type == "tool_use" {
				keep = toolResultIds[block.ID]
			} else if block.Type == "tool_result" {
				keep = toolUseIds[block.ToolUseID]
			}
			if keep {
				prunedBlocks = append(prunedBlocks, block)
			}
		}
		// Only include messages that have content or are not assistant messages
		if (len(prunedBlocks) > 0 && system.IsModelAnswerAny(msg)) ||
			!system.IsModelAnswerAny(msg) {
			hasTextBlock := false
			for _, block := range msg.Content {
				if block.Type == "text" {
					hasTextBlock = true
					break
				}
			}
			if len(prunedBlocks) > 0 || hasTextBlock {
				msg.Content = prunedBlocks
				prunedMessages = append(prunedMessages, msg)
			}
		}
	}
	return prunedMessages
}

func getTerminalWidth() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 80 // Fallback width
	}
	return width - 20
}

func handleHistoryCommand(messages []history.HistoryMessage) {
	displayMessageHistory(messages)
}

func updateRenderer() error {
	width := getTerminalWidth()
	var err error
	renderer, err = glamour.NewTermRenderer(
		glamour.WithStandardStyle(styles.TokyoNightStyle),
		glamour.WithWordWrap(width),
	)
	return err
}

func generateCacheKey(providerName, modelName, systemPrompt, reqPrompt string, llmMessages []history.Message) (string, error) {
	// Serialize llmMessages to JSON to ensure consistent hashing
	messagesJSON, err := json.Marshal(llmMessages)
	if err != nil {
		return "", fmt.Errorf("failed to marshal messages for cache key: %w", err)
	}

	keyData := fmt.Sprintf("%s-%s-%s-%s-%s", providerName, modelName, systemPrompt, reqPrompt, string(messagesJSON))
	hasher := sha256.New()
	hasher.Write([]byte(keyData))
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// ensureCacheDirExists creates the directory if it doesn't exist.
func ensureCacheDirExists(dirPath string) error {
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		if mkdirErr := os.MkdirAll(dirPath, 0755); mkdirErr != nil {
			log.Error("Failed to create cache directory", "dir", dirPath, "error", mkdirErr)
			return mkdirErr
		}
		log.Info("Created cache directory", "path", dirPath)
	} else if err != nil {
		// Log if stat fails for reasons other than NotExist
		log.Error("Failed to stat cache directory", "dir", dirPath, "error", err)
		return err
	}
	return nil
}

func hashHistoryMessages(messages []history.Message) (string, error) {
	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		return "", fmt.Errorf("failed to marshal messages for hashing: %w", err)
	}
	hasher := sha256.New()
	hasher.Write(messagesJSON)
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func generateToolCallCacheKey(precedingMessagesHash string, toolName string, toolArgsJSON []byte) (string, error) {
	keyData := fmt.Sprintf("%s-%s-%s", precedingMessagesHash, toolName, string(toolArgsJSON))
	hasher := sha256.New()
	hasher.Write([]byte(keyData))
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func createMessageWithCache(ctx context.Context, provider history.Provider, reqPrompt string, llmMessages []history.Message, effectiveTools []history.Tool) (history.Message, error) {
	if !enableCaching {
		log.Debug("Caching is disabled, calling provider directly.")
		return provider.CreateMessage(ctx, reqPrompt, llmMessages, effectiveTools)
	}

	cacheKey, err := generateCacheKey(provider.Name(), provider.GetModel(), provider.GetSystemPrompt(), reqPrompt, llmMessages)
	if err != nil {
		log.Error("Failed to generate cache key, proceeding without cache", "error", err)
		// Fallback to direct call if key generation fails
		return provider.CreateMessage(ctx, reqPrompt, llmMessages, effectiveTools)
	}

	// Ensure cache directory exists
	if err := ensureCacheDirExists(llmCacheDir); err != nil {
		// Logged by ensureCacheDirExists, proceed without caching for this call
		return provider.CreateMessage(ctx, reqPrompt, llmMessages, effectiveTools)
	}

	cacheFilePath := filepath.Join(llmCacheDir, cacheKey+".json")

	// Attempt to read from cache
	cachedData, readErr := os.ReadFile(cacheFilePath)
	if readErr == nil {
		var cachedMsg history.HistoryMessage
		var unmarshalErr error // Declare unmarshalErr here
		if unmarshalErr = json.Unmarshal(cachedData, &cachedMsg); unmarshalErr == nil {
			log.Debug("LLM cache hit", "key", cacheKey, "file", cacheFilePath)
			return &cachedMsg, nil // Return as history.Message
		}
		// Now unmarshalErr is in scope
		log.Warn("LLM cache hit but failed to unmarshal, proceeding to fetch", "key", cacheKey, "error", unmarshalErr)
	} else if !os.IsNotExist(readErr) {
		log.Warn("Failed to read LLM cache file, proceeding to fetch", "key", cacheKey, "error", readErr)
	} else {
		log.Debug("LLM cache miss", "key", cacheKey)
	}

	// Cache miss or error reading cache, call the provider
	message, err := provider.CreateMessage(ctx, reqPrompt, llmMessages, effectiveTools)
	if err != nil {
		return nil, err
	}

	// Save to cache
	// Type assert to *history.HistoryMessage or ensure message is directly marshalable.
	// Most providers return *history.HistoryMessage which implements history.Message.
	messageToCache, ok := message.(*history.HistoryMessage)
	if !ok {
		// If it's not *history.HistoryMessage, we might need a more generic way or skip caching
		// For now, let's try to marshal it directly if it's not *history.HistoryMessage.
		// This might fail if `message` is an interface with no concrete struct to marshal.
		// However, standard providers should return *history.HistoryMessage.
		log.Warn("Message from provider is not *history.HistoryMessage, attempting direct marshal for cache", "type", fmt.Sprintf("%T", message))
		// If direct marshalling of an interface is problematic, this part needs refinement.
		// For now, we assume it's either *history.HistoryMessage or something else json.Marshal can handle.
	}

	jsonData, marshalErr := json.Marshal(messageToCache) // Use messageToCache if assertion was successful, else message
	if messageToCache == nil {                           // if assertion failed, try to marshal original message
		jsonData, marshalErr = json.Marshal(message)
	}

	if marshalErr != nil {
		log.Error("Failed to marshal message for LLM cache, response not cached", "key", cacheKey, "error", marshalErr)
		// Return the original message even if caching fails
		return message, nil
	}

	if writeErr := os.WriteFile(cacheFilePath, jsonData, traceFilePerm); writeErr != nil {
		log.Error("Failed to write message to LLM cache", "key", cacheKey, "file", cacheFilePath, "error", writeErr)
	} else {
		log.Debug("LLM response cached", "key", cacheKey, "file", cacheFilePath)
	}

	return message, nil
}

// performActualToolCall encapsulates the non-cached logic of executing a tool.
func performActualToolCall(
	ctx context.Context,
	mcpClient mcpclient.MCPClient,
	fullToolName string, // serverName__toolName
	toolArgsJSON []byte, // Marshalled arguments from toolCall.GetArguments()
	rateLimit time.Duration,
	isInteractive bool,
	simpleToolNameForDisplay string, // Just toolName for display
) (*mcp.CallToolResult, error) {
	var toolArgs map[string]interface{}
	if err := json.Unmarshal(toolArgsJSON, &toolArgs); err != nil {
		errMsg := fmt.Sprintf("Error parsing tool arguments for %s before actual call: %v", fullToolName, err)
		log.Error(errMsg, "args_json", string(toolArgsJSON))
		return nil, fmt.Errorf(errMsg)
	}

	var toolResultPtr *mcp.CallToolResult
	var callErr error

	toolAction := func() {
		if rateLimit > 0 {
			time.Sleep(rateLimit)
		}
		req := mcp.CallToolRequest{}
		parts := strings.Split(fullToolName, "__")
		if len(parts) != 2 {
			callErr = fmt.Errorf("invalid tool name format for MCP request: %s", fullToolName)
			return
		}
		req.Params.Name = parts[1] // Use the part after "__" (the simple tool name)
		req.Params.Arguments = toolArgs
		toolResultPtr, callErr = mcpClient.CallTool(ctx, req)
	}

	if isInteractive {
		_ = spinner.New().
			Title(fmt.Sprintf("Running tool %s...", simpleToolNameForDisplay)).
			Action(toolAction).
			Run()
	} else {
		toolAction()
	}

	if callErr != nil {
		return nil, callErr
	}
	return toolResultPtr, nil
}

// callToolWithCache handles caching for tool calls.
func callToolWithCache(
	ctx context.Context,
	mcpClient mcpclient.MCPClient,
	fullToolName string, // serverName__toolName
	toolArgsJSON []byte, // Marshalled arguments from toolCall.GetArguments()
	precedingMessagesHash string, // Hash of the LLM input context
	rateLimit time.Duration,
	isInteractive bool,
) (*mcp.CallToolResult, error) {
	partsForDisplay := strings.Split(fullToolName, "__")
	simpleToolNameForDisplay := fullToolName
	if len(partsForDisplay) == 2 {
		simpleToolNameForDisplay = partsForDisplay[1]
	}

	if !enableCaching {
		log.Debug("Caching is disabled, calling tool directly.", "tool", fullToolName)
		return performActualToolCall(ctx, mcpClient, fullToolName, toolArgsJSON, rateLimit, isInteractive, simpleToolNameForDisplay)
	}

	cacheKey, err := generateToolCallCacheKey(precedingMessagesHash, fullToolName, toolArgsJSON)
	if err != nil {
		log.Error("Failed to generate tool call cache key, proceeding without cache", "tool", fullToolName, "error", err)
		return performActualToolCall(ctx, mcpClient, fullToolName, toolArgsJSON, rateLimit, isInteractive, simpleToolNameForDisplay)
	}

	if err := ensureCacheDirExists(llmCacheDir); err != nil {
		// Logged by ensureCacheDirExists, proceed without caching for this call
		return performActualToolCall(ctx, mcpClient, fullToolName, toolArgsJSON, rateLimit, isInteractive, simpleToolNameForDisplay)
	}

	cacheFilePath := filepath.Join(llmCacheDir, "tool_"+cacheKey+".json")

	cachedData, readErr := os.ReadFile(cacheFilePath)
	if readErr == nil {
		var cachedResult mcp.CallToolResult
		var unmarshalErr error // Declare unmarshalErr here
		if unmarshalErr = json.Unmarshal(cachedData, &cachedResult); unmarshalErr == nil {
			log.Debug("Tool call cache hit", "tool", fullToolName, "key", cacheKey, "file", cacheFilePath)
			if isInteractive { // Brief pause to simulate work for UX, as cache is instant
				_ = spinner.New().Title(fmt.Sprintf("Using cached result for %s...", simpleToolNameForDisplay)).Action(func() { time.Sleep(50 * time.Millisecond) }).Run()
			}
			return &cachedResult, nil
		}
		// Now unmarshalErr is in scope
		log.Warn("Tool call cache hit but failed to unmarshal, proceeding to fetch", "tool", fullToolName, "key", cacheKey, "error", unmarshalErr)
	} else if !os.IsNotExist(readErr) {
		log.Warn("Failed to read tool call cache file, proceeding to fetch", "tool", fullToolName, "key", cacheKey, "error", readErr)
	} else {
		log.Debug("Tool call cache miss", "tool", fullToolName, "key", cacheKey)
	}

	toolResult, err := performActualToolCall(ctx, mcpClient, fullToolName, toolArgsJSON, rateLimit, isInteractive, simpleToolNameForDisplay)
	if err != nil {
		return nil, err
	}

	jsonData, marshalErr := json.Marshal(toolResult)
	if marshalErr != nil {
		log.Error("Failed to marshal tool call result for cache, response not cached", "tool", fullToolName, "key", cacheKey, "error", marshalErr)
		return toolResult, nil // Return the original result even if caching fails
	}

	if writeErr := os.WriteFile(cacheFilePath, jsonData, traceFilePerm); writeErr != nil {
		log.Error("Failed to write tool call result to cache", "tool", fullToolName, "key", cacheKey, "file", cacheFilePath, "error", writeErr)
	} else {
		log.Debug("Tool call result cached", "tool", fullToolName, "key", cacheKey, "file", cacheFilePath)
	}

	return toolResult, nil
}

type PeerAgentInstance struct {
	key      string
	pctx     *PromptContext
	provider history.Provider
}

type PromptContext struct {
	ctx           context.Context
	mcpClients    map[string]mcpclient.MCPClient // unfiltered
	tools         []history.Tool                 // unfiltered
	agent         system.Agent
	messages      *[]history.HistoryMessage
	tweaker       PromptRuntimeTweaks
	isInteractive bool
	peers         map[string]*PeerAgentInstance
}

func NewPromptContext(ctx context.Context, mcpClients map[string]mcpclient.MCPClient, tools []history.Tool, agent system.Agent, messages *[]history.HistoryMessage, tweaker PromptRuntimeTweaks, isInteractive bool) *PromptContext {
	return &PromptContext{
		ctx:           ctx,
		mcpClients:    mcpClients,
		tools:         tools,
		agent:         agent,
		messages:      messages,
		tweaker:       tweaker,
		isInteractive: isInteractive,
		peers:         make(map[string]*PeerAgentInstance),
	}
}

// Method implementations for simpleMessage
func assignAndRecord(ctx *PromptContext, msg *history.HistoryMessage, label string) {
	if ctx.tweaker != nil {
		ctx.tweaker.AssignIDsToNewMessage(msg, *ctx.messages)
	}
	*ctx.messages = append(*ctx.messages, *msg)
	if ctx.tweaker != nil && label != "" {
		if err := ctx.tweaker.RecordState(*ctx.messages, label); err != nil {
			log.Error("Failed to record trace", "label", label, "error", err)
		}
	}
}

func assignAndRecordPtr(ctx *PromptContext, msg *history.HistoryMessage, label string) {
	assignAndRecord(ctx, msg, label)
}

func assignAndRecordVal(ctx *PromptContext, msg history.HistoryMessage, label string) {
	assignAndRecord(ctx, &msg, label)
}

type AgentReference struct {
	AgentName string
	Key       string
	Text      string
}
type AgentReferenceParseResult struct {
	CommonText string
	Refs       []AgentReference
}

// ParsePeersReferences parses a string for @agent[key] references and returns the common text and a slice of AgentReference.
func ParsePeersReferences(text string, downstreamAgents []string) AgentReferenceParseResult {
	var refs []AgentReference
	commonText := text
	// Build a regex to match @agent[key] or @agent
	// Example: @doctor[professor] or @doctor
	// We'll use a simple approach for now, can be improved for edge cases.
	// Only match agents in downstreamAgents
	agentPattern := strings.Join(downstreamAgents, "|")
	// Example pattern: `@(?:doctor|mother)(?:\[[^\]]*\])?`
	// We'll use FindAllStringIndex to get all matches and then extract.
	pattern := `@(` + agentPattern + `)(?:\[([^\]]*)\])?`
	re := regexp.MustCompile(pattern)

	matches := re.FindAllStringSubmatchIndex(text, -1)
	lastEnd := 0
	var prevAgentRef *AgentReference
	var commonParts []string

	for _, match := range matches {
		start := match[0]
		end := match[1]
		agentName := text[match[2]:match[3]]
		var key string
		if match[4] != -1 && match[5] != -1 {
			key = text[match[4]:match[5]]
		}
		// Text between lastEnd and start is common text or previous agent's text
		if prevAgentRef == nil {
			// First match, so text before is common
			commonParts = append(commonParts, strings.TrimSpace(text[lastEnd:start]))
		} else {
			prevAgentRef.Text = strings.TrimSpace(text[lastEnd:start])
			refs = append(refs, *prevAgentRef)
		}
		prevAgentRef = &AgentReference{
			AgentName: agentName,
			Key:       key,
			Text:      "",
		}
		lastEnd = end
	}
	// Handle trailing text
	if prevAgentRef != nil {
		prevAgentRef.Text = strings.TrimSpace(text[lastEnd:])
		refs = append(refs, *prevAgentRef)
	} else {
		// No agent refs, all is common text
		commonParts = append(commonParts, strings.TrimSpace(text))
	}
	commonText = strings.TrimSpace(strings.Join(commonParts, " "))
	return AgentReferenceParseResult{
		CommonText: commonText,
		Refs:       refs,
	}
}

func runPromptIteration(ctx *PromptContext, provider history.Provider, prompt *history.HistoryMessage) error {
	// Display the user's prompt if it's not empty and in interactive mode
	if prompt != nil && ctx.isInteractive {
		fmt.Printf("\n%s\n", promptStyle.Render("You: "+prompt.GetContent()))
	}
	var message history.Message
	var err error
	backoff := initialBackoff
	retries := 0

	// Convert MessageParam to llm.Message for provider.
	// These llmMessages represent the history *before* the current `prompt` string.
	llmMessages := make([]history.Message, len(*ctx.messages))
	for i := range *ctx.messages {
		llmMessages[i] = &(*ctx.messages)[i]
	}

	// Calculate hash of this history, this is the context for the upcoming LLM call, used for tool caching.
	llmCallHistoryHashForToolCache := GetHistoryCache(llmMessages)

	log.Debug("Using provided PromptRuntimeTweaks for tool filtering and tracing")

	var effectiveTools []history.Tool = filterToolsWithTweaker(ctx.tweaker, ctx.tools, nil)
	effectiveTools = filterToolsWithAgent(ctx.agent, effectiveTools)

	for {
		//
		// OVERLOAD-RETRY, LLM INVOCATION LOOP
		//
		action := func() {
			reqPrompt := ""
			if prompt != nil {
				reqPrompt = prompt.GetContent()
			}
			// Use the caching wrapper function
			message, err = createMessageWithCache(
				ctx.ctx,
				provider,
				reqPrompt,
				llmMessages,
				effectiveTools,
			)
		}
		if ctx.isInteractive {
			_ = spinner.New().Title("Thinking...").Action(action).Run()
		} else {
			action() // Run directly without spinner
		}

		if err != nil {
			// Check if it's an overloaded error
			if strings.Contains(err.Error(), "overloaded_error") {
				if retries >= maxRetries {
					return fmt.Errorf(
						"claude is currently overloaded. please wait a few minutes and try again",
					)
				}

				log.Warn("LLM is overloaded, backing off...",
					"attempt", retries+1,
					"backoff", backoff.String())

				time.Sleep(backoff)
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				retries++
				continue
			}
			// If it's not an overloaded error, return the error immediately
			return err
		}
		// If we got here, the request succeeded
		break
	}

	if prompt != nil {
		//
		// INITIAL ENTRY
		//
		assignAndRecordPtr(ctx, prompt, "user_prompt_sent")
	}

	// Assistant's turn to respond, prepare its message structure
	assistantMessage := history.HistoryMessage{
		Role:    message.GetRole(), // Should be "assistant"
		Content: []history.ContentBlock{},
	}

	// Handle the message response (text part)
	if message.GetContent() != "" {
		if ctx.isInteractive {
			if renderer == nil { // Ensure renderer is initialized for interactive mode
				if err := updateRenderer(); err != nil {
					log.Warn("Failed to update renderer, continuing without styled output", "error", err)
				}
			}
			if renderer != nil {
				if str, errRender := renderer.Render("\nAssistant: "); errRender == nil {
					fmt.Print(str)
				}
				// updateRenderer() might be too frequent here if terminal size changes often.
				// Let's assume it's updated once per prompt loop or if explicitly needed.
				str, errRender := renderer.Render(message.GetContent() + "\n")
				if errRender != nil {
					log.Error("Failed to render response, printing raw", "error", errRender)
					fmt.Print(message.GetContent() + "\n")
				} else {
					fmt.Print(str)
				}
			} else { // Fallback for non-styled output if renderer failed
				fmt.Print("\nAssistant: " + message.GetContent() + "\n")
			}
		} else {
			log.Info("Assistant response (text)", "content", message.GetContent())
		}
		assistantMessage.Content = append(assistantMessage.Content, history.ContentBlock{
			Type: "text",
			Text: message.GetContent(),
		})
	}

	toolCallResponses := maybeHandleToolCalls(ctx, message, &assistantMessage, llmCallHistoryHashForToolCache)

	// Add the assistant's message (text and tool_use calls) to history
	if len(assistantMessage.Content) > 0 {
		assignAndRecordPtr(ctx, &assistantMessage, "assistant_response_and_tool_calls")
	}

	// Add all tool responses to history and record state after each
	if len(toolCallResponses) > 0 {
		for _, toolRespMsg := range toolCallResponses {
			assignAndRecordVal(ctx, toolRespMsg, fmt.Sprintf("tool_response_%s", toolRespMsg.GetToolResponseID()))
		}
		// Make another call to the LLM with the tool results
		// Pass the tweaker and isInteractive flags down
		return runPromptIteration(ctx, provider, nil) // Pass empty prompt
	}

	// --- Refactored agent reference parsing ---
	// For each text block in assistantMessage, parse agent references
	downstreamAgents := ctx.agent.GetDownstreamAgents()
	for _, conte := range assistantMessage.Content {
		if conte.Type != "text" || len(downstreamAgents) == 0 {
			continue
		}
		parsed := ParsePeersReferences(conte.Text, downstreamAgents)
		if parsed.Refs != nil {
			for _, ref := range parsed.Refs {
				peerKey := ref.AgentName
				if ref.Key != "" {
					peerKey = fmt.Sprintf("%s[%s]", ref.AgentName, ref.Key)
				}
				if _, ok := ctx.peers[peerKey]; !ok {
					agi, err := LoadAgentByName(ref.AgentName)
					if err != nil {
						log.Error("Failed to load agent", "agent", ref.AgentName, "error", err)
						continue
					}
					// Use createPromptContext to get provider and PromptContext for the peer agent instance
					peerMessages := make([]history.HistoryMessage, 0)
					peerTweaker := NewDefaultPromptRuntimeTweaks("-peer-"+ref.AgentName, ref.AgentName)
					// Use the same model selection logic as in runMCPHost
					taskForModelSelection := agi.GetTaskForModelSelection()
					selectedModelID, err := selectModelForTask(taskForModelSelection, loadedModelsConfig)
					if err != nil {
						log.Error("Failed to select model for peer agent", "agent", ref.AgentName, "error", err)
						continue
					}
					systemPrompt := createFullPrompt(agi)

					effectiveToolsForPeer := filterToolsWithAgent(agi, AllTools)

					nprovider, err, peerPctx, done := createPromptContext(
						ctx.ctx,
						"", // jobID not needed for peer
						selectedModelID,
						agi,
						systemPrompt,
						peerMessages,
						peerTweaker,
						McpClients,
						effectiveToolsForPeer,
						loadedModelsConfig,
					)
					if done || err != nil {
						log.Error("Failed to create prompt context for peer agent", "agent", ref.AgentName, "error", err)
						continue
					}
					ctx.peers[peerKey] = &PeerAgentInstance{
						key:      ref.Key,
						pctx:     peerPctx,
						provider: nprovider,
					}
				}
			}
			peerResponse := ""
			// (Further logic for using the peer agent instance can be added here)
			for _, ref := range parsed.Refs {
				peerKey := ref.AgentName
				if ref.Key != "" {
					peerKey = fmt.Sprintf("%s[%s]", ref.AgentName, ref.Key)
				}
				if peer, ok := ctx.peers[peerKey]; ok {
					errpeer := runPromptIteration(peer.pctx, peer.provider, history.NewUserMessage(ref.Text))
					if errpeer != nil {
						log.Error("calling peer: %v", errpeer)
						return errpeer
					}
					peerResponse0 := (*peer.pctx.messages)[len(*peer.pctx.messages)-1]
					peerResponse += "from @" + ref.AgentName
					if ref.Key != "" {
						peerResponse += "[" + ref.Key + "]"
					}
					peerResponse += ": \n"
					peerResponse += peerResponse0.GetContent()
				}
			}
			*ctx.messages = append(*ctx.messages, *history.NewUserMessage(peerResponse))
		}
	}

	*ctx.messages = ctx.agent.NormalizeHistory(context.WithValue(ctx.ctx, "PromptRuntimeTweaks", ctx.tweaker), *ctx.messages)
	lastMessage := (*ctx.messages)[len(*ctx.messages)-1]
	if system.IsUserMessage(lastMessage) {
		*ctx.messages = (*ctx.messages)[:len(*ctx.messages)-1] // cut last message
		return runPromptIteration(ctx, provider, &lastMessage) // Pass empty prompt
	}

	if ctx.tweaker != nil {
		ctx.tweaker.RecordState(*ctx.messages, "final_save")
	}

	if ctx.isInteractive {
		fmt.Println() // Add spacing
	}
	return nil
}

func maybeHandleToolCalls(ctx *PromptContext, message history.Message, assistantMessage *history.HistoryMessage, llmCallHistoryHashForToolCache string) []history.HistoryMessage {
	// Handle tool calls requested by the assistant
	toolCallResponses := []history.HistoryMessage{} // To store tool responses separately for now

	for _, toolCall := range message.GetToolCalls() {

		inputBytes, errMarshal := json.Marshal(toolCall.GetArguments())
		if errMarshal != nil {
			log.Error("Failed to marshal tool arguments", "tool", toolCall.GetName(), "error", errMarshal)
			// Potentially skip this tool call or add an error message
			continue
		}
		log.Info("🔧 Using tool", "name", toolCall.GetName(), "args", toolCall.GetArguments())
		assistantMessage.Content = append(assistantMessage.Content, history.ContentBlock{
			Type:  "tool_use",
			ID:    toolCall.GetID(),
			Name:  toolCall.GetName(),
			Input: inputBytes, // Store as json.RawMessage or []byte
		})

		// Log usage statistics if available
		inputTokens, outputTokens := message.GetUsage()
		if inputTokens > 0 || outputTokens > 0 {
			log.Info("Usage statistics",
				"input_tokens", inputTokens,
				"output_tokens", outputTokens,
				"total_tokens", inputTokens+outputTokens)
		}

		parts := strings.Split(toolCall.GetName(), "__")
		if len(parts) != 2 {
			errMsg := fmt.Sprintf("Error: Invalid tool name format: %s", toolCall.GetName())
			log.Error(errMsg)
			if ctx.isInteractive {
				fmt.Printf("\n%s\n", errorStyle.Render(errMsg))
			}
			// Add error as tool result? Or skip? For now, skip.
			continue
		}

		serverName, _ := parts[0], parts[1] // toolName assigned to _
		mcpClient, ok := ctx.mcpClients[serverName]
		if !ok {
			errMsg := fmt.Sprintf("Error: Server not found for tool: %s (server: %s)", toolCall.GetName(), serverName)
			log.Error(errMsg)
			if ctx.isInteractive {
				fmt.Printf("\n%s\n", errorStyle.Render(errMsg))
			}
			continue
		}

		// Get rate limit from config if available
		var rateLimit time.Duration
		// Ensure mcpClient is of type MCPClientWithConfig to access GetConfig()
		if clientWithConfig, okClientCfg := mcpClient.(MCPClientWithConfig); okClientCfg {
			if serverCfg := clientWithConfig.GetConfig(); serverCfg != nil { // serverCfg is ServerConfig interface
				if stdioCfg, okStdio := serverCfg.(STDIOServerConfig); okStdio && stdioCfg.RateLimit > 0 {
					rateLimit = time.Duration(float64(time.Second) / stdioCfg.RateLimit)
				} else if sseCfg, okSse := serverCfg.(SSEServerConfig); okSse && sseCfg.RateLimit > 0 {
					rateLimit = time.Duration(float64(time.Second) / sseCfg.RateLimit)
				}
			}
		}

		// Call tool using the caching wrapper
		// inputBytes are the marshalled tool arguments.
		// llmCallHistoryHashForToolCache was computed earlier.
		toolResultPtr, err := callToolWithCache(ctx.ctx, mcpClient, toolCall.GetName(), inputBytes, llmCallHistoryHashForToolCache, rateLimit, ctx.isInteractive)

		if err != nil {
			// err already contains tool name if it comes from performActualToolCall or mcpClient.CallTool
			// For consistency, format it like existing error messages if needed, or rely on callToolWithCache's logging.
			errMsg := fmt.Sprintf("Error during tool call for %s (ID: %s): %v", toolCall.GetName(), toolCall.GetID(), err)
			log.Error(errMsg)
			if ctx.isInteractive {
				fmt.Printf("\n%s\n", errorStyle.Render(errMsg))
			}

			// Add error message as tool result
			toolResponseContent := history.ContentBlock{
				Type:      "tool_result",
				ToolUseID: toolCall.GetID(),
				Content: []history.ContentBlock{{ // Anthropic expects content for tool_result to be a list of blocks
					Type: "text", // Representing error as text content
					Text: errMsg,
				}},
			}
			// Create a new message for this tool's error response
			toolResponseMessage := history.HistoryMessage{
				Role:    "tool", // Or "user" if provider expects tool results from user. Anthropic uses "user" role with tool_results content block.
				Content: []history.ContentBlock{toolResponseContent},
			}
			toolCallResponses = append(toolCallResponses, toolResponseMessage)
			continue
		}

		toolResult := *toolResultPtr

		if toolResult.Content != nil { // mcp.Content is []mcp.ContentBlock which is []interface{}
			log.Debug("raw tool result content", "content", toolResult.Content)

			// Convert mcp.ContentBlock (interface{}) to history.ContentBlock for storage and tracing
			var historyToolResultContent []history.ContentBlock
			var resultTextForDisplay string
			for _, item := range toolResult.Content { // item is mcp.ContentBlock
				if textContent, ok := item.(mcp.TextContent); ok { // Assuming mcp.TextContent is map[string]interface{}{"type":"text", "text":"..."}
					historyToolResultContent = append(historyToolResultContent, history.ContentBlock{
						Type: "text",
						Text: textContent.Text,
					})
					resultTextForDisplay += textContent.Text + " "
				} else {
					// Handle other mcp.ContentBlock types if necessary
					log.Warn("Unsupported tool result content block type", "type", fmt.Sprintf("%T", item))
				}
			}

			// Log tool response
			log.Info("🔧 Tool response", "name", toolCall.GetName(), "response", strings.TrimSpace(resultTextForDisplay))

			// Create the tool result block for the assistant's message
			toolResponseBlockForHistory := history.ContentBlock{
				Type:      "tool_result",
				ToolUseID: toolCall.GetID(),
				Content:   historyToolResultContent, // Store the structured content
				Name:      toolCall.GetName(),
				Text:      strings.TrimSpace(resultTextForDisplay), // Store concatenated text for GetContent() compatibility if needed
			}
			log.Debug("created tool result block for history", "block", toolResponseBlockForHistory)

			// Create a new message for this tool's response
			toolResponseMessage := history.HistoryMessage{
				Role:    "user", // Anthropic expects tool results in a "user" message.
				Content: []history.ContentBlock{toolResponseBlockForHistory},
			}
			toolCallResponses = append(toolCallResponses, toolResponseMessage)
		}
	}
	return toolCallResponses
}

func filterToolsWithAgent(agent system.Agent, tools []history.Tool) []history.Tool {
	enabledTools := agent.GetEnabledTools()
	if enabledTools == nil {
		return nil
	}
	enabledSet := make(map[string]struct{}, len(enabledTools))
	for _, name := range enabledTools {
		enabledSet[name] = struct{}{}
	}
	var filteredTools []history.Tool
	for _, tool := range tools {
		if _, ok := enabledSet[tool.Name]; ok {
			filteredTools = append(filteredTools, tool)
		} else {
			tool0 := strings.Split(tool.Name, "__")[0]
			// Since history.Tool is not comparable, use a manual check for existence
			alreadyIncluded := false
			for _, t := range filteredTools {
				if t.Name == tool.Name && t.Description == tool.Description {
					alreadyIncluded = true
					break
				}
			}
			if _, ok = enabledSet[tool0]; ok && !alreadyIncluded {
				filteredTools = append(filteredTools, tool)
			}
		}
	}
	return filteredTools
}

func filterToolsWithTweaker(tweaker PromptRuntimeTweaks, tools []history.Tool, effectiveTools []history.Tool) []history.Tool {
	if tweaker != nil { // tweaker might be nil if tracing is disabled (though NewDefaultPromptRuntimeTweaks("") handles it)
		for _, tool := range tools {
			// Ensure to use GetName() method from the llm.Tool interface
			if tweaker.IsToolEnabled(tool.Name) {
				effectiveTools = append(effectiveTools, tool)
			}
		}
	} else {
		// If tweaker is nil (should not happen with NewDefaultPromptRuntimeTweaks), use all tools
		effectiveTools = tools
	}
	return effectiveTools
}

func GetHistoryCache(llmMessages []history.Message) string {
	llmCallHistoryHashForToolCache := "" // Default to empty if error or no history
	if len(llmMessages) > 0 {
		var hashErr error
		llmCallHistoryHashForToolCache, hashErr = hashHistoryMessages(llmMessages)
		if hashErr != nil {
			log.Warn("Failed to hash llmMessages for tool caching, tool cache might be less effective or fail.", "error", hashErr)
			llmCallHistoryHashForToolCache = "error_hashing_history" // Use a distinct string on error to avoid empty string collisions
		}
	} else {
		llmCallHistoryHashForToolCache = "empty_history" // Distinct string for empty history
	}
	return llmCallHistoryHashForToolCache
}

func generateTraceID(fileSuffix string) string {
	now := time.Now()
	// YYYYMMDDHHMMSSmmm
	timestamp := now.Format("20060102150405.000")
	timestamp = strings.ReplaceAll(timestamp, ".", "") // Remove millisecond separator

	// XXXX (random 4 digits)
	// For simplicity, using a pseudo-random number. For true randomness, use crypto/rand.
	// rand.Seed(time.Now().UnixNano()) // Deprecated in Go 1.20+
	// No explicit seed needed for math/rand >= Go 1.20, it's auto-seeded.
	// If using Go < 1.20, uncomment and import "math/rand" and add rand.Seed(time.Now().UnixNano()).
	randomSuffix := fmt.Sprintf("%04d", rand.Intn(10000))

	return fmt.Sprintf("%s-%s%s", timestamp, randomSuffix, fileSuffix)
}

var McpClients map[string]mcpclient.MCPClient
var AllTools []history.Tool

// runMCPHost uses the loaded models configuration.
func runMCPHost(ctx context.Context, modelsCfg *ModelsConfig) error {

	mcpConfig, err := loadMCPConfig()
	if McpClients == nil {

		if err != nil {
			return fmt.Errorf("error loading MCP config: %v", err)
		}

		McpClients, err = createMCPClients(mcpConfig)
		if err != nil {
			return fmt.Errorf("error creating MCP clients: %v", err)
		}

		for name := range McpClients {
			log.Info("Server connected", "name", name)
		}

		AllTools = GenerateToolsFromMCPClients(ctx, McpClients, AllTools)

	}
	// model flag validation (presence and default) is now handled in rootCmd.RunE

	// Generate trace file path for CLI mode
	traceID := generateTraceID("")
	traceFileName := fmt.Sprintf("%s.yaml", traceID)
	fullTracePath := filepath.Join(TracesDir, traceFileName)
	log.Info("Trace file will be saved to", "path", fullTracePath)

	// Load the agent specified by agentNameFlag
	agent, err := LoadAgentByName(agentNameFlag) // Assuming LoadAgentByName resolves "default" etc.
	if err != nil {
		return fmt.Errorf("error loading agent '%s': %w", agentNameFlag, err)
	}
	log.Info("Loaded agent for CLI mode", "agent", agent.Filename())

	systemPrompt := createFullPrompt(agent)
	taskForModelSelection := agent.GetTaskForModelSelection()
	log.Info("Agent details for CLI mode", "systemPromptProvided", systemPrompt != "", "taskForModelSelection", taskForModelSelection)

	defer func() {
		log.Info("Shutting down MCP servers...")
		for name, client := range McpClients {
			if err := client.Close(); err != nil {
				log.Warn("Failed to close server", "name", name, "error", err)
			} else {
				log.Info("Server closed", "name", name)
			}
		}
	}()

	messages := make([]history.HistoryMessage, 0)

	// Select model based on task
	selectedModelID, err := selectModelForTask(taskForModelSelection, modelsCfg)
	if err != nil {
		return fmt.Errorf("error selecting model for task '%s' (agent '%s'): %w", taskForModelSelection, agentNameFlag, err)
	}

	// Create the provider using the selected model ID
	provider, err := createProvider(ctx, selectedModelID, systemPrompt, modelsCfg)
	if err != nil {
		// Error from createProvider already includes modelID.
		return fmt.Errorf("error creating provider (agent '%s', task '%s'): %w", agentNameFlag, taskForModelSelection, err)
	}

	log.Info("Provider and model loaded for CLI mode",
		"providerType", provider.Name(),
		"selectedModelID", selectedModelID,
		"agent", agentNameFlag,
		"task", taskForModelSelection)

	// Initialize PromptRuntimeTweaks with the trace path for CLI mode
	cliTweaker := NewDefaultPromptRuntimeTweaks("", agentNameFlag)
	// The tweaker is now passed as a direct argument to runPromptIteration,
	// so setting it in context here is not strictly necessary for runPromptIteration itself,
	// but other functions might still expect it if not refactored.
	// For now, keep it in context for broader compatibility during refactoring.
	ctx = context.WithValue(ctx, PromptRuntimeTweaksKey, cliTweaker)

	pctx := NewPromptContext(ctx, McpClients, AllTools, agent, &messages, cliTweaker, false)
	if userPromptCLI != "" {
		// Non-interactive mode: process the single prompt and exit
		log.Info("Running in non-interactive mode with provided user prompt.", "prompt", userPromptCLI)

		// runPromptIteration with isInteractive=false will use logging for output.
		// It modifies 'messages' in place.
		err := runPromptIteration(pctx, provider, history.NewUserMessage(userPromptCLI))
		if err != nil {
			// Error is already logged by runPromptIteration or its callees.
			// Return the error to indicate failure to the main Execute function.
			return fmt.Errorf("error processing non-interactive prompt: %w", err)
		}

		// Print the assistant's final textual response to stdout.
		// Search backwards for the last assistant message with text content.
		var lastAssistantText string
		for i := len(messages) - 1; i >= 0; i-- {
			if system.IsModelAnswerAny(messages[i]) {
				for _, contentBlock := range messages[i].Content {
					if contentBlock.Type == "text" && contentBlock.Text != "" {
						lastAssistantText = contentBlock.Text
						break
					}
				}
				if lastAssistantText != "" {
					break
				}
			}
		}

		if lastAssistantText != "" {
			fmt.Println(lastAssistantText) // Print raw text to stdout
		} else {
			log.Info("No final text response from assistant to print for non-interactive mode.")
		}
		return nil // Successful non-interactive run
	}

	// Interactive mode
	if err := updateRenderer(); err != nil {
		return fmt.Errorf("error initializing renderer for interactive mode: %v", err)
	}

	// Main interaction loop
	for {
		var prompt string
		err := huh.NewForm(huh.NewGroup(huh.NewText().
			Title("Enter your prompt (Type /help for commands, Ctrl+C to quit)").
			Value(&prompt).
			CharLimit(5000)),
		).WithWidth(getTerminalWidth()).
			WithTheme(huh.ThemeCharm()).
			Run()

		if err != nil {
			if errors.Is(err, huh.ErrUserAborted) { // Check if it's a user abort (Ctrl+C)
				fmt.Println("\nGoodbye!")
				return nil // Exit cleanly
			}
			return fmt.Errorf("error reading prompt: %w", err) // Return other errors normally
		}

		if prompt == "" {
			continue
		}

		// Handle slash commands
		handled, err := handleSlashCommand(
			prompt,
			mcpConfig,
			McpClients,
			messages,
			modelsCfg,     // Pass loadedModelsConfig here
			agentNameFlag, // Pass current agent name for context if needed by commands
		)
		if err != nil {
			return err
		}
		if handled {
			continue
		}

		if len(messages) > 0 {
			messages = pruneMessages(messages)
		}
		// Pass cliTweaker and true for isInteractive
		err = runPromptIteration(pctx, provider, history.NewUserMessage(prompt))
		if err != nil {
			log.Error("Error from runPromptIteration in interactive mode", "error", err)
			fmt.Printf("\n%s\n", errorStyle.Render(fmt.Sprintf("Error: %v", err)))
			// Continue loop in interactive mode even after an error.
		}
	}
}

func GenerateToolsFromMCPClients(ctx context.Context, mcpClients map[string]mcpclient.MCPClient, allTools []history.Tool) []history.Tool {
	for serverName, mcpClient := range mcpClients {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		toolsResult, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
		cancel()

		if err != nil {
			log.Error(
				"Error fetching tools",
				"server",
				serverName,
				"error",
				err,
			)
			continue
		}

		serverTools := mcpToolsToAnthropicTools(serverName, toolsResult.Tools)
		allTools = append(allTools, serverTools...)
		log.Info(
			"Tools loaded",
			"server",
			serverName,
			"count",
			len(toolsResult.Tools),
		)
	}
	return allTools
}

// --- Server Mode Implementation ---

// StartJobRequest defines the expected JSON structure for the /start endpoint.
type StartJobRequest struct {
	AgentName     string `json:"agent_name,omitempty"` // If empty, "default" agent is used.
	SystemMessage string `json:"system_message,omitempty"`
	UserQuery     string `json:"user_query"`
}

// ModelInfo defines the structure for information about a single model returned by the /models endpoint.
type ModelInfo struct {
	ID                 string         `json:"id"`
	PreferencesPerTask map[string]int `json:"preferences_per_task,omitempty"`
}

// runServerMode uses the loaded models configuration.
func runServerMode(ctx context.Context, modelsCfg *ModelsConfig) error {
	log.Info("Starting server mode", "port", serverPort, "tracesDir", TracesDir, "modelsFile", modelsConfigFile)

	// --- Pre-initialize MCP Clients and Tools for Server Mode ---
	mcpConfig, err := loadMCPConfig()
	if err != nil {
		return fmt.Errorf("server mode: error loading MCP config: %w", err)
	}

	// Create MCP clients. These will be reused by all jobs.
	// The createMCPClients function uses its own short-lived contexts for initialization.
	serverMcpClients, err := createMCPClients(mcpConfig)
	if err != nil {
		return fmt.Errorf("server mode: error creating MCP clients: %w", err)
	}
	defer func() {
		log.Info("Server shutting down, closing MCP clients...")
		for name, client := range serverMcpClients {
			if err := client.Close(); err != nil {
				log.Warn("Failed to close MCP client during server shutdown", "name", name, "error", err)
			} else {
				log.Info("MCP client closed", "name", name)
			}
		}
	}()

	for name := range serverMcpClients {
		log.Info("Server mode: MCP client connected and initialized", "name", name)
	}

	// Pre-fetch all tools from all MCP clients.
	var serverAllTools []history.Tool
	for serverName, mcpClient := range serverMcpClients {
		// Use a background context with timeout for listing tools during server startup
		listToolsCtx, listToolsCancel := context.WithTimeout(context.Background(), 30*time.Second) // Increased timeout for initial tool listing
		toolsResult, errList := mcpClient.ListTools(listToolsCtx, mcp.ListToolsRequest{})
		listToolsCancel()

		if errList != nil {
			// Log error but continue, some tools might be unavailable
			log.Error("Server mode: error fetching tools for client during startup", "server", serverName, "error", errList)
			// Depending on requirements, you might want to prevent server startup if tools can't be listed.
			// For now, we'll allow the server to start, but this client might not offer tools.
			continue
		}
		serverTools := mcpToolsToAnthropicTools(serverName, toolsResult.Tools)
		serverAllTools = append(serverAllTools, serverTools...)
		log.Info("Server mode: tools loaded", "server", serverName, "count", len(toolsResult.Tools))
	}
	log.Info("Server mode: All MCP clients initialized and tools listed.")
	// --- End Pre-initialization ---

	mux := http.NewServeMux()
	// Update handleStartJob to pass serverMcpClients, serverAllTools, and modelsCfg
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		handleStartJob(w, r, serverMcpClients, serverAllTools, modelsCfg)
	})
	mux.HandleFunc("/status", handleJobStatus)
	mux.HandleFunc("/stop", handleStopJob)
	mux.HandleFunc("/models", func(w http.ResponseWriter, r *http.Request) {
		handleListModels(w, r, modelsCfg)
	})
	mux.HandleFunc("/agents", HandleListAgents) // Added /agents route

	serverAddr := fmt.Sprintf(":%d", serverPort)
	srv := &http.Server{
		Addr:    serverAddr,
		Handler: mux,
	}

	// Channel to listen for interrupt or terminate signals
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	// Goroutine to start the server
	go func() {
		log.Info("Server listening on", "address", serverAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("Server failed to start or unexpectedly closed", "error", err)
			// If server fails to start, we might want to signal main goroutine to exit.
			// For now, this error will be logged, and the server might not be running.
			// Consider a mechanism to propagate this error if startup failure needs to halt everything.
		}
	}()

	// Block until a signal is received
	sig := <-stopChan
	log.Info("Received signal, shutting down server gracefully...", "signal", sig.String())

	// Create a context with a timeout for the shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second) // 30-second timeout for graceful shutdown
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("Server shutdown failed", "error", err)
		return fmt.Errorf("server shutdown failed: %w", err)
	}

	log.Info("Server gracefully stopped.")
	return nil
}

func handleStartJob(
	w http.ResponseWriter,
	r *http.Request,
	mcpClients map[string]mcpclient.MCPClient,
	allTools []history.Tool,
	modelsCfg *ModelsConfig, // Changed from apiKeys
) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	jobMutex.Lock()
	if currentJobID != "" {
		jobMutex.Unlock()
		log.Warn("Attempted to start job while another is running", "existing_job_id", currentJobID)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "already_running", "job_id": currentJobID})
		return
	}

	// Read and parse JSON body
	var req StartJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jobMutex.Unlock()
		log.Error("Failed to decode JSON request body for /start", "error", err)
		http.Error(w, "Invalid JSON format: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Determine agent to use
	agentNameToUse := req.AgentName
	if agentNameToUse == "" {
		agentNameToUse = "default"
		log.Info("No 'agent_name' provided in /start request. Using 'default' agent.")
	}

	// Load agent
	agent, err := LoadAgentByName(agentNameToUse)
	if err != nil {
		jobMutex.Unlock()
		log.Error("Failed to load agent for /start request", "agent_name", agentNameToUse, "error", err)
		http.Error(w, fmt.Sprintf("Failed to load agent '%s': %s", agentNameToUse, err.Error()), http.StatusInternalServerError)
		return
	}
	log.Info("Loaded agent for job", "agent_name", agentNameToUse, "agent_filename", agent.Filename())

	// Determine system prompt: use request's if provided, else agent's default
	systemPromptToUse := req.SystemMessage
	if systemPromptToUse == "" || systemPromptToUse[0] == '+' {
		systemPromptToUse = agent.GetSystemPrompt()
		if req.SystemMessage[0] == '+' {
			systemPromptToUse += "\n" + req.SystemMessage[1:]
		}
		log.Info("Using system prompt from agent", "agent_name", agentNameToUse)
	} else {
		log.Info("Using system prompt from request payload", "agent_name", agentNameToUse)
	}

	// Get task from agent and select model
	taskForModelSelection := agent.GetTaskForModelSelection()
	log.Info("Agent details for job", "taskForModelSelection", taskForModelSelection)

	modelIDToUse, err := selectModelForTask(taskForModelSelection, modelsCfg)
	if err != nil {
		jobMutex.Unlock()
		log.Error("Failed to select model for task via agent", "agent_name", agentNameToUse, "task", taskForModelSelection, "error", err)
		http.Error(w, fmt.Sprintf("Failed to select model for task '%s' (agent '%s'): %s", taskForModelSelection, agentNameToUse, err.Error()), http.StatusInternalServerError)
		return
	}
	log.Info("Selected model for job via agent", "agent_name", agentNameToUse, "task", taskForModelSelection, "model_id", modelIDToUse)

	if req.UserQuery == "" {
		jobMutex.Unlock()
		log.Error("Missing 'user_query' in /start request")
		http.Error(w, "Missing required field: user_query", http.StatusBadRequest)
		return
	}

	jobCtx, jobCancel := context.WithCancel(context.Background())
	currentJobCtx = jobCtx
	currentJobCancel = jobCancel

	jobTweaker := NewDefaultPromptRuntimeTweaks("", agentNameToUse)

	// Construct initial message from user_query
	initialUserMessage := history.HistoryMessage{
		Role: "user",
		Content: []history.ContentBlock{
			{
				Type: "text",
				Text: req.UserQuery,
			},
		},
	}

	// Assign ID to the initial user message and record it
	// For the very first message, PreviousID will be 0.
	var messagesForHistory []history.HistoryMessage
	jobTweaker.AssignIDsToNewMessage(&initialUserMessage, messagesForHistory)
	messagesForHistory = append(messagesForHistory, initialUserMessage)

	if err := jobTweaker.RecordState(messagesForHistory, "job_start_initial_message"); err != nil {
		currentJobID = "" // Rollback state
		currentJobCtx = nil
		currentJobCancel = nil
		jobMutex.Unlock()
		log.Error("Failed to record initial state for job", "job_id", jobTweaker.JobId, "error", err)
		http.Error(w, "Failed to save initial state", http.StatusInternalServerError)
		return
	}

	jobMutex.Unlock() // Unlock before starting goroutine to avoid holding lock for too long

	log.Info("Starting job", "job_id", jobTweaker.JobId, "agent_name", agentNameToUse, "selected_model_id", modelIDToUse, "trace_file", jobTweaker.traceFilePath)
	// Pass selected model_id, determined system_message, mcpClients, allTools, and modelsCfg to processJob
	go processJob(jobCtx, jobTweaker.JobId, modelIDToUse, agent, systemPromptToUse, messagesForHistory, jobTweaker, mcpClients, allTools, modelsCfg)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started", "job_id": jobTweaker.JobId, "agent_used": agentNameToUse, "model_selected": modelIDToUse})
}

func handleListModels(w http.ResponseWriter, r *http.Request, modelsCfg *ModelsConfig) {
	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}

	if modelsCfg == nil {
		log.Error("/models: models configuration is not loaded")
		http.Error(w, "Models configuration not loaded", http.StatusInternalServerError)
		return
	}

	var modelsInfo []ModelInfo
	for _, provider := range modelsCfg.Providers {
		for _, model := range provider.Models {
			modelsInfo = append(modelsInfo, ModelInfo{
				ID:                 model.ID,
				PreferencesPerTask: model.PreferencesPerTask,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(modelsInfo); err != nil {
		log.Error("/models: Failed to encode models information to JSON", "error", err)
		http.Error(w, "Failed to encode response: "+err.Error(), http.StatusInternalServerError)
	}
}

func handleJobStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}

	jobMutex.Lock()
	defer jobMutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if currentJobID != "" {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "already_running", "job_id": currentJobID})
	} else {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "idle"})
	}
}

func handleStopJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}

	jobIDToStop := r.URL.Query().Get("id")
	if jobIDToStop == "" {
		http.Error(w, "Missing 'id' query parameter", http.StatusBadRequest)
		return
	}

	jobMutex.Lock()
	defer jobMutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if currentJobID == "" {
		log.Info("Stop request received but no job running", "requested_id", jobIDToStop)
		json.NewEncoder(w).Encode(map[string]string{"status": "was_idle"})
	} else if currentJobID != jobIDToStop {
		log.Warn("Stop request for different job ID", "requested_id", jobIDToStop, "current_job_id", currentJobID)
		json.NewEncoder(w).Encode(map[string]string{"status": "already_running", "job_id": currentJobID})
	} else {
		// currentJobID == jobIDToStop
		if currentJobCancel != nil {
			log.Info("Stopping job", "job_id", jobIDToStop)
			currentJobCancel() // Signal cancellation
			// The processJob goroutine is responsible for clearing currentJobID, etc., upon termination.
		} else {
			// Should not happen if currentJobID is set and matches.
			log.Error("Inconsistent state: currentJobID set but currentJobCancel is nil", "job_id", currentJobID)
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "stopped", "job_id": jobIDToStop})
	}
}

func processJob(jobCtx context.Context, jobID string, modelToUse string, agent system.Agent, systemPromptToUse string, messages []history.HistoryMessage, tweaker PromptRuntimeTweaks, mcpClients map[string]mcpclient.MCPClient, allTools []history.Tool, modelsCfg *ModelsConfig) {
	defer func() {
		jobMutex.Lock()
		if currentJobID == jobID { // Ensure this job is still the one to clear
			log.Info("Job processing finished, clearing active job state.", "job_id", jobID)
			currentJobID = ""
			currentJobCtx = nil
			if currentJobCancel != nil {
				// Call cancel if not already called, to release resources if job ended naturally
				// currentJobCancel() // This might be redundant if jobCtx.Done() was the cause of exit.
				currentJobCancel = nil
			}
		} else {
			log.Info("Job processing finished, but active job state was for a different/no job.", "finished_job_id", jobID, "active_job_id", currentJobID)
		}
		jobMutex.Unlock()
	}()

	// --- Environment Setup (adapted from runMCPHost) ---
	// Use modelToUse and systemPromptToUse passed as parameters.
	// systemPromptToUse can be an empty string if no system message was provided in the request.
	// The loadSystemPrompt(systemPromptFile) is not used here as the prompt comes from the request.

	provider, err, pctx, done := createPromptContext(jobCtx, jobID, modelToUse, agent, systemPromptToUse, messages, tweaker, mcpClients, allTools, modelsCfg)
	if done {
		return
	}
	err = runPromptIteration(pctx, provider, history.NewUserMessage("")) // isInteractive is false

	if err != nil {
		log.Error("Job: Error during LLM interaction", "job_id", jobID, "error", err)
		recordJobError(jobID, messages, tweaker, fmt.Errorf("error during LLM interaction: %w", err))
		return
	}

	log.Info("Job processing completed successfully", "job_id", jobID)
	// Final state is recorded by runPromptIteration's calls to tweaker.
}

func createPromptContext(jobCtx context.Context, jobID string, modelToUse string, agent system.Agent, systemPromptToUse string, messages []history.HistoryMessage, tweaker PromptRuntimeTweaks, mcpClients map[string]mcpclient.MCPClient, allTools []history.Tool, modelsCfg *ModelsConfig) (history.Provider, error, *PromptContext, bool) {
	provider, err := createProvider(jobCtx, modelToUse, systemPromptToUse, modelsCfg) // Pass modelsCfg
	if err != nil {
		log.Error("Job: Error creating provider", "job_id", jobID, "model_id", modelToUse, "error", err)
		recordJobError(jobID, messages, tweaker, fmt.Errorf("error creating provider for model ID %s: %w", modelToUse, err))
		return nil, nil, nil, true
	}
	log.Info("Job: Provider and model loaded", "job_id", jobID, "providerType", provider.Name(), "modelID", modelToUse)

	// MCP clients and tools are now passed in, no need to load/create/list them here.
	// The mcpClients passed in are shared; do not close them here.
	// Their lifecycle is managed by runServerMode.
	log.Info("Job: Using pre-initialized MCP clients and tools.", "job_id", jobID, "num_clients", len(mcpClients), "num_tools", len(allTools))
	// --- End Environment Setup ---

	// The initial `messages` are already recorded by handleStartJob.
	// The `runPromptIteration` function expects the `prompt` string to be the *newest* user message text.
	// Since `messages` already contains the full history (including the latest user turn from YAML),
	// the `prompt` string for `runPromptIteration` should be empty.

	// The `runPromptIteration` function will append new messages (assistant, tool_use, tool_result)
	// to the `messages` slice passed by address.
	pctx := NewPromptContext(jobCtx, McpClients, AllTools, agent, &messages, tweaker, false)
	return provider, err, pctx, false
}

func recordJobError(jobID string, messages []history.HistoryMessage, tweaker PromptRuntimeTweaks, jobErr error) {
	if tweaker == nil {
		log.Error("Tweaker is nil, cannot record job error to trace", "job_id", jobID, "error", jobErr)
		return
	}
	// Construct an error message to append to the history
	errorContentBlock := history.ContentBlock{
		Type: "error", // Custom type for error
		Text: jobErr.Error(),
	}
	errorMessage := history.HistoryMessage{
		Role:    "system", // Or a dedicated "error" role
		Content: []history.ContentBlock{errorContentBlock},
	}

	// Assign ID and record this error message
	// This modifies the 'messages' slice which is local to processJob or its caller
	// If messages is passed by value, this won't affect the caller's slice.
	// However, tweaker.RecordState will use the current state of 'messages' + this new error.
	// For simplicity, let's assume 'messages' here is the most current list.
	var tempMessagesForErrorRecording []history.HistoryMessage
	tempMessagesForErrorRecording = append(tempMessagesForErrorRecording, messages...) // Make a copy

	tweaker.AssignIDsToNewMessage(&errorMessage, tempMessagesForErrorRecording)
	tempMessagesForErrorRecording = append(tempMessagesForErrorRecording, errorMessage)

	if err := tweaker.RecordState(tempMessagesForErrorRecording, "job_error"); err != nil {
		log.Error("Failed to record job error to trace file", "job_id", jobID, "original_error", jobErr, "record_error", err)
	}
}

// Helper to read all bytes from r.Body, needed for handleStartJob
func ReadAll(r io.Reader) ([]byte, error) {
	b := make([]byte, 0, 512)
	for {
		if len(b) == cap(b) {
			// Add more capacity (let append pick how much).
			b = append(b, 0)[:len(b)]
		}
		n, err := r.Read(b[len(b):cap(b)])
		b = b[:len(b)+n]
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return b, err
		}
	}
}

func MakeMockProvider() *testing_stuff.MockProvider {
	return &testing_stuff.MockProvider{TheName: "mock", Responses: map[string]history.Message{}}
}

func RunSubAgent(agentName string, prompt string) (string, string, error) {
	// Load the agent specified by agentNameFlag
	agent, err := LoadAgentByName(agentName) // Assuming LoadAgentByName resolves "default" etc.
	if err != nil {
		return "", "", fmt.Errorf("error loading agent '%s': %w", agentName, err)
	}
	taskForModelSelection := agent.GetTaskForModelSelection()

	messages := make([]history.HistoryMessage, 0)

	// Select model based on task
	selectedModelID, err := selectModelForTask(taskForModelSelection, loadedModelsConfig)
	if err != nil {
		return "", "", fmt.Errorf("error selecting model for task '%s' (agent '%s'): %w", taskForModelSelection, agentName, err)
	}

	// Create the provider using the selected model ID
	ctx := context.Background()
	baseSystemPrompt := createFullPrompt(agent)
	provider, err := createProvider(ctx, selectedModelID, baseSystemPrompt, loadedModelsConfig)
	if err != nil {
		// Error from createProvider already includes modelID.
		return "", "", fmt.Errorf("error creating provider (agent '%s', task '%s'): %w", agentName, taskForModelSelection, err)
	}

	// Initialize PromptRuntimeTweaks for this sub-agent run
	subAgentTweaker := NewDefaultPromptRuntimeTweaks("-sub-"+agentName, agentName)
	// The trace file path for sub-agent will be based on its own jobID.
	// Example: subAgentTweaker := NewDefaultPromptRuntimeTweaks(filepath.Join(TracesDir, fmt.Sprintf("%s_sub.yaml", subAgentTweaker.JobId)))
	// For simplicity, using default trace path generation within NewDefaultPromptRuntimeTweaks.

	// runPromptIteration with isInteractive=false will use logging for output.
	// It modifies 'messages' in place.
	// The prompt string itself becomes the content of the first user message.
	initialUserMessage := history.NewUserMessage(prompt)
	pctx := NewPromptContext(ctx, McpClients, AllTools, agent, &messages, subAgentTweaker, false)
	err = runPromptIteration(pctx, provider, initialUserMessage)
	if err != nil {
		// Error is already logged by runPromptIteration or its callees.
		// Return the error to indicate failure to the main Execute function.
		return "", subAgentTweaker.JobId, fmt.Errorf("error processing non-interactive prompt: %w", err)
	}

	// Print the assistant's final textual response to stdout.
	// Search backwards for the last assistant message with text content.
	var lastAssistantText string
	for i := len(messages) - 1; i >= 0; i-- {
		if system.IsModelAnswerAny(messages[i]) { // Changed to IsModelAnswerAny to align with other parts of the code
			// Iterate through content blocks to find text
			for _, block := range messages[i].Content {
				if block.Type == "text" && block.Text != "" {
					lastAssistantText = block.Text
					break
				}
			}
			if lastAssistantText != "" {
				break
			}
		}
	}

	// Get the job ID from the tweaker used in this sub-agent run.
	// This assumes subAgentTweaker was initialized earlier in this function.
	// If NewDefaultPromptRuntimeTweaks was called, subAgentTweaker.JobId would be set.
	// Let's retrieve it from the tweaker instance.
	jobIDForResult := ""
	// This part assumes subAgentTweaker is in scope and was initialized.
	// Based on the previous change, subAgentTweaker should be available.
	// If subAgentTweaker was passed as nil to runPromptIteration (as in original code), this would need adjustment.
	// However, the request is to *implement* jobTweaker, so we assume it's now non-nil.
	// Let's assume subAgentTweaker is the one created and used.
	// The variable `subAgentTweaker` was defined in the previous block.
	if subAgentTweaker != nil {
		jobIDForResult = subAgentTweaker.JobId
	}

	if lastAssistantText != "" {
		return lastAssistantText, jobIDForResult, nil
	} else {
		return "", jobIDForResult, fmt.Errorf("No final text response from assistant to print for non-interactive mode.")
	}

}

func createFullPrompt(agent system.Agent) string {
	baseSystemPrompt := agent.GetSystemPrompt()
	if agent.GetDownstreamAgents() != nil {
		baseSystemPrompt += "\n" + generateDownstreamAgentPrompt(agent.GetDownstreamAgents())
	}
	return baseSystemPrompt
}

func generateDownstreamAgentPrompt(agents []string) string {
	type AgentIntro struct {
		Name  string
		Intro string
	}
	var agentIntros []AgentIntro
	for _, agent := range agents {
		ag, err := LoadAgentByName(agent)
		if err != nil {
			log.Error("Error loading agent", "agent_name", agent, "error", err)
			continue
		}
		intro := ag.GetIntroductionAsDownstreamAgent()
		if intro == "" {
			log.Error("Empty agent introduction (used as downstream agent)", "agent_name")
		}
		agentIntros = append(agentIntros, AgentIntro{
			Name:  agent,
			Intro: intro,
		})
	}
	if len(agentIntros) == 0 {
		return ""
	}

	const tmplText = `
NB Here are peer colleagues available for you to delegate tasks, you can refer to them with their names prefixed with "@" and optional key, 
you can follow up with detailed requests etc.

==============

{{- range . }}
## @{{ .Name }}
{{ .Intro }}


{{- end }}

=============

Please refer peers by name with optional key (any word) in square brackets, there can be many helpers with same speciality. 
Put good efforts to delegate tasks to your colleagues, because they are not aware of your top-level managerial issues.
Each indexed peer remembers your previous communications with them and their work they done for you, they are not simply one-shot functions, 
so they keep context you gave them. They love concise, exact, precise communication.

<example>
@doctor[professor] please analyze my condition:
	* headache
	* vomiting
and give recommendation whether I need to go to the hospital

@doctor[paramedic] please tell me meds to take to relieve the headache

@mother please, make me a buckthorn tea

</example>

Remember:

NEVER thank, mention or refer your peer unless you task him. Any other mention will disrupt communication. Only tasks.
ALWAYS use same key to refer same peer if you used key once. Agent without key is different agent from agent with key.
ALWAYS use same peer key to continue task for same peer, if possible. 
Use multiple keys only if there are few unrelated tasks, or if you think that particular peer needs to rest.
Same peer colleague may do task after task, if it matches its profession, no worry.

`
	tmpl, err := template.New("downstreamAgents").Parse(tmplText)
	if err != nil {
		log.Error("Failed to parse downstream agent prompt template", "error", err)
		return ""
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, agentIntros); err != nil {
		log.Error("Failed to execute downstream agent prompt template", "error", err)
		return ""
	}
	return buf.String()
}
