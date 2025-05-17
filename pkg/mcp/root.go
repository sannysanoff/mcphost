package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"        // Added for io.ReadAll
	"math/rand" // Added for random number generation
	"os"
	"path/filepath" // Added for path manipulation
	"strings"
	"time"

	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
	"github.com/charmbracelet/log"
	"github.com/spf13/pflag" // Added for flag inspection

	"github.com/charmbracelet/glamour"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcphost/pkg/history"
	"github.com/mark3labs/mcphost/pkg/llm"
	"github.com/mark3labs/mcphost/pkg/llm/anthropic"
	"github.com/mark3labs/mcphost/pkg/llm/google"
	"github.com/mark3labs/mcphost/pkg/llm/ollama"
	"github.com/mark3labs/mcphost/pkg/llm/openai"
	"github.com/spf13/cobra"

	agentshandler "github.com/mark3labs/mcphost/agents" // Added for /agents endpoint
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
	configFile       string
	systemPromptFile string
	messageWindow    int
	modelFlag        string // Model ID (from models.yaml)
	tracesDir        string // Directory for trace files
	modelsConfigFile string // Path to models.yaml

	loadedModelsConfig *ModelsConfig // Parsed models.yaml

	// Server Mode specific
	serverMode       bool
	serverPort       int
	currentJobID     string
	jobMutex         sync.Mutex
	currentJobCtx    context.Context
	currentJobCancel context.CancelFunc
)

const (
	initialBackoff = 1 * time.Second
	maxBackoff     = 30 * time.Second
	maxRetries     = 5 // Will reach close to max backoff
	traceFilePerm  = 0644
)

var rootCmd = &cobra.Command{
	Use:   "mcphost",
	Short: "Chat with AI models through a unified interface",
	Long: `MCPHost is a CLI tool that allows you to interact with various AI models
through a unified interface. It supports various tools through MCP servers
and provides streaming responses.

Models are defined in a 'models.yaml' file. Specify the model ID using the --model flag (for CLI mode)
or in the JSON payload (for server mode's /start endpoint).

Example (CLI):
  mcphost --models path/to/your/models.yaml -m claude-3-5-sonnet-latest
  mcphost -m my-custom-ollama-model-id

Server Mode:
  mcphost --server --traces /path/to/traces --models path/to/models.yaml [--port 9262]
  curl -X POST -H "Content-Type: application/json" \
       -d '{"model_id": "claude-3-5-sonnet-latest", "system_message": "You are a helpful assistant.", "user_query": "Hello, world!"}' \
       http://localhost:9262/start
  # 'model_id' is optional in the /start payload. If omitted, the server will use the first model from models.yaml.
  curl http://localhost:9262/status
  curl http://localhost:9262/stop?id=TRACE_ID
  curl http://localhost:9262/models
  curl http://localhost:9262/agents`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate --traces flag early, as it's required for both modes
		if tracesDir == "" {
			return fmt.Errorf("--traces flag is required")
		}
		if _, err := os.Stat(tracesDir); os.IsNotExist(err) {
			if err := os.MkdirAll(tracesDir, 0755); err != nil {
				return fmt.Errorf("traces directory does not exist and could not be created: %s, error: %w", tracesDir, err)
			}
			log.Info("Created traces directory", "path", tracesDir)
		} else if err != nil {
			return fmt.Errorf("error checking traces directory %s: %w", tracesDir, err)
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

		// Validate modelFlag - it should now be a model ID.
		// Basic validation: ensure it doesn't look like a flag itself.
		if strings.HasPrefix(modelFlag, "-") && modelFlag != "-" {
			// Check if the value of modelFlag matches any known flag name.
			isModelFlagLikelyConsumedAnotherFlag := false
			checkFlag := func(f *pflag.Flag) {
				if modelFlag == "--"+f.Name || (f.Shorthand != "" && modelFlag == "-"+f.Shorthand) {
					isModelFlagLikelyConsumedAnotherFlag = true
				}
			}
			cmd.PersistentFlags().VisitAll(checkFlag)
			if !isModelFlagLikelyConsumedAnotherFlag {
				cmd.Flags().VisitAll(checkFlag)
			}

			if isModelFlagLikelyConsumedAnotherFlag {
				return fmt.Errorf(
					"error: The --model (-m) flag was given the value \"%s\", which appears to be another flag. "+
						"Please ensure --model (-m) is followed by a model ID defined in your '%s' file. "+
						"Example: mcphost -m your_model_id",
					modelFlag, modelsConfigFile,
				)
			}
			// If it starts with a hyphen but isn't a known flag, it might be a valid model ID.
			// We can't definitively say it's wrong without checking against loadedModelsConfig.
			// The createProvider function will ultimately validate if the ID exists.
			log.Debugf(
				"Warning: The value for the --model (-m) flag is \"%s\", which starts with a hyphen. "+
					"This will be treated as a model ID.",
				modelFlag,
			)
		}

		if !serverMode {
			if modelFlag == "" { // User did not specify a model
				if loadedModelsConfig != nil && len(loadedModelsConfig.Providers) > 0 &&
					len(loadedModelsConfig.Providers[0].Models) > 0 && loadedModelsConfig.Providers[0].Models[0].ID != "" {
					defaultModelID := loadedModelsConfig.Providers[0].Models[0].ID
					modelFlag = defaultModelID
					log.Infof("No model specified via --model flag. Using the first model from '%s' as default: %s", modelsConfigFile, defaultModelID)
				} else {
					// No model specified, and no default could be determined
					return fmt.Errorf("CLI mode requires a model. Please specify one with --model <model_id>, or ensure '%s' contains at least one model definition to use as a default.", modelsConfigFile)
				}
			}
			// At this point, modelFlag should be set, either by user or by default.
			// The createProvider function will later validate if this modelFlag (whether user-provided or default) actually exists.
		}

		if serverMode {
			return runServerMode(context.Background(), loadedModelsConfig)
		}
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
		StringVar(&configFile, "config", "", "config file (default is $HOME/.mcp.json)")
	rootCmd.PersistentFlags().
		StringVar(&systemPromptFile, "system-prompt", "", "system prompt json file")
	rootCmd.PersistentFlags().
		IntVar(&messageWindow, "message-window", 10, "number of messages to keep in context")
	rootCmd.PersistentFlags().
		StringVarP(&modelFlag, "model", "m", "",
			"model ID to use (defined in models.yaml). If not specified, the first model in models.yaml will be used as default.")
	rootCmd.PersistentFlags().
		StringVar(&modelsConfigFile, "models", "models.yaml", "path to the models.yaml configuration file")

	// Add debug flag
	rootCmd.PersistentFlags().
		BoolVar(&debugMode, "debug", false, "enable debug logging")
	rootCmd.PersistentFlags().
		StringVar(&tracesDir, "traces", "", "directory to store trace files (required)")

	// Server mode flags
	rootCmd.Flags().BoolVar(&serverMode, "server", false, "Run in server mode")
	rootCmd.Flags().IntVar(&serverPort, "port", 9262, "Port for server mode")

	// Mark --traces as required: Validation is done in RunE.
	// Mark --model as required for CLI mode (if no default is set and it's always needed)
	// This can also be checked in RunE. If a default is set, it's not strictly "required" by the flag parser.
	// For now, we'll check in RunE if modelFlag is empty for CLI mode.
}

// createProvider initializes an LLM provider based on the model ID and models configuration.
func createProvider(ctx context.Context, modelID, systemPrompt string, config *ModelsConfig) (llm.Provider, error) {
	if modelID == "" {
		return nil, fmt.Errorf("model ID cannot be empty")
	}
	if config == nil {
		return nil, fmt.Errorf("models configuration is not loaded")
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
						// Try environment variable as a fallback if not in models.yaml, though models.yaml should be the primary source.
						// For strictness, one might remove this env var check and require it in models.yaml.
						// apiKey = os.Getenv("ANTHROPIC_API_KEY") // Decided against env var fallback to keep models.yaml as SoT.
						return nil, fmt.Errorf("Anthropic API key not found in models.yaml for provider '%s', model ID '%s'", providerCfg.Name, modelID)
					}
					return anthropic.NewProvider(apiKey, baseURL, modelCfg.Name, systemPrompt), nil
				case "ollama":
					// Ollama uses baseURL. API key is typically not required or handled differently.
					// The system prompt for Ollama is typically set within the Modelfile or via API parameters during generation,
					// not usually at provider initialization. If needed, it would be passed to CreateMessage.
					return ollama.NewProvider(baseURL, modelCfg.Name)
				case "openai":
					if apiKey == "" {
						return nil, fmt.Errorf("OpenAI API key not found in models.yaml for provider '%s', model ID '%s'", providerCfg.Name, modelID)
					}
					return openai.NewProvider(apiKey, baseURL, modelCfg.Name, systemPrompt), nil
				case "google":
					if apiKey == "" {
						return nil, fmt.Errorf("Google API key not found in models.yaml for provider '%s', model ID '%s'", providerCfg.Name, modelID)
					}
					return google.NewProvider(ctx, apiKey, modelCfg.Name, systemPrompt) // Google provider might not use baseURL in the same way.
				default:
					return nil, fmt.Errorf("unsupported provider type '%s' found in models.yaml for model ID '%s'", providerCfg.Name, modelID)
				}
			}
		}
	}

	return nil, fmt.Errorf("model ID '%s' not found in models configuration file '%s'", modelID, modelsConfigFile)
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
		if (len(prunedBlocks) > 0 && msg.Role == "assistant") ||
			msg.Role != "assistant" {
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

// Method implementations for simpleMessage
func runPrompt(
	ctx context.Context,
	provider llm.Provider,
	mcpClients map[string]mcpclient.MCPClient,
	tools []llm.Tool,
	prompt string,
	messages *[]history.HistoryMessage,
	tweaker PromptRuntimeTweaks, // Added tweaker parameter
	isInteractive bool, // Added isInteractive flag
) error {
	// Display the user's prompt if it's not empty and in interactive mode
	if prompt != "" && isInteractive {
		fmt.Printf("\n%s\n", promptStyle.Render("You: "+prompt))
	}
	var message llm.Message
	var err error
	backoff := initialBackoff
	retries := 0

	// Convert MessageParam to llm.Message for provider.
	// These llmMessages represent the history *before* the current `prompt` string.
	llmMessages := make([]llm.Message, len(*messages))
	for i := range *messages {
		llmMessages[i] = &(*messages)[i]
	}

	log.Debug("Using provided PromptRuntimeTweaks for tool filtering and tracing")

	var effectiveTools []llm.Tool
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

	for {
		action := func() {
			message, err = provider.CreateMessage(
				ctx,
				prompt,      // Pass the current prompt string
				llmMessages, // Pass the history *before* this prompt
				effectiveTools,
			)
		}
		if isInteractive {
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

				log.Warn("Claude is overloaded, backing off...",
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

	// If a prompt was provided by the user (i.e., prompt string is not empty),
	// add it to the history now. This is done after the LLM call succeeds.
	if prompt != "" {
		userMessage := history.HistoryMessage{
			Role: "user",
			Content: []history.ContentBlock{{
				Type: "text",
				Text: prompt,
			}},
		}
		// *messages here is the history *before* this userMessage.
		// tweaker.AssignIDsToNewMessage will correctly link it.
		tweaker.AssignIDsToNewMessage(&userMessage, *messages)
		*messages = append(*messages, userMessage) // Add user's current prompt to the main history slice

		// Record state *after* adding the user message
		if err := tweaker.RecordState(*messages, "user_prompt_sent"); err != nil {
			log.Error("Failed to record trace after user prompt was sent", "error", err)
			// Continue execution even if tracing fails
		}
	}

	// Assistant's turn to respond, prepare its message structure
	assistantMessage := history.HistoryMessage{
		Role:    message.GetRole(), // Should be "assistant"
		Content: []history.ContentBlock{},
	}

	// Handle the message response (text part)
	if message.GetContent() != "" {
		if isInteractive {
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

	// Handle tool calls requested by the assistant
	toolCallResponses := []history.HistoryMessage{} // To store tool responses separately for now

	for _, toolCall := range message.GetToolCalls() {
		log.Info("🔧 Using tool", "name", toolCall.GetName(), "id", toolCall.GetID())

		inputBytes, errMarshal := json.Marshal(toolCall.GetArguments())
		if errMarshal != nil {
			log.Error("Failed to marshal tool arguments", "tool", toolCall.GetName(), "error", errMarshal)
			// Potentially skip this tool call or add an error message
			continue
		}
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
			if isInteractive {
				fmt.Printf("\n%s\n", errorStyle.Render(errMsg))
			}
			// Add error as tool result? Or skip? For now, skip.
			continue
		}

		serverName, toolName := parts[0], parts[1]
		mcpClient, ok := mcpClients[serverName]
		if !ok {
			errMsg := fmt.Sprintf("Error: Server not found for tool: %s (server: %s)", toolCall.GetName(), serverName)
			log.Error(errMsg)
			if isInteractive {
				fmt.Printf("\n%s\n", errorStyle.Render(errMsg))
			}
			continue
		}

		var toolArgs map[string]interface{}
		if err := json.Unmarshal(inputBytes, &toolArgs); err != nil {
			errMsg := fmt.Sprintf("Error parsing tool arguments for %s: %v", toolCall.GetName(), err)
			log.Error(errMsg)
			if isInteractive {
				fmt.Printf("\n%s\n", errorStyle.Render(errMsg))
			}
			continue
		}

		var toolResultPtr *mcp.CallToolResult
		toolAction := func() { // Renamed to toolAction to avoid conflict
			req := mcp.CallToolRequest{}
			req.Params.Name = toolName
			req.Params.Arguments = toolArgs
			// Use the passed context `ctx` for the tool call for cancellability
			toolResultPtr, err = mcpClient.CallTool(ctx, req)
		}

		if isInteractive {
			_ = spinner.New().
				Title(fmt.Sprintf("Running tool %s...", toolName)).
				Action(toolAction).
				Run()
		} else {
			toolAction()
		}

		if err != nil {
			errMsg := fmt.Sprintf("Error calling tool %s: %v", toolName, err)
			log.Error(errMsg)
			if isInteractive {
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

			// Create the tool result block for the assistant's message
			toolResponseBlockForHistory := history.ContentBlock{
				Type:      "tool_result",
				ToolUseID: toolCall.GetID(),
				Content:   historyToolResultContent,                // Store the structured content
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

	// Add the assistant's message (text and tool_use calls) to history
	if len(assistantMessage.Content) > 0 {
		if tweaker != nil {
			tweaker.AssignIDsToNewMessage(&assistantMessage, *messages)
		}
		*messages = append(*messages, assistantMessage)
		if tweaker != nil {
			if err := tweaker.RecordState(*messages, "assistant_response_and_tool_calls"); err != nil {
				log.Error("Failed to record trace after assistant response", "error", err)
			}
		}
	}

	// Add all tool responses to history and record state after each
	if len(toolCallResponses) > 0 {
		for _, toolRespMsg := range toolCallResponses {
			if tweaker != nil {
				// Assign IDs to each tool response message before appending
				// Note: The previous message ID will link to the last thing added to *messages,
				// which could be the assistant's message or a prior tool response.
				tweaker.AssignIDsToNewMessage(&toolRespMsg, *messages)
			}
			*messages = append(*messages, toolRespMsg) // Add tool response to the main history
			if tweaker != nil {
				if err := tweaker.RecordState(*messages, fmt.Sprintf("tool_response_%s", toolRespMsg.GetToolResponseID())); err != nil { // GetToolResponseID might need to be robust if ID is not set
					log.Error("Failed to record trace after tool response", "tool_id", toolRespMsg.GetToolResponseID(), "error", err)
				}
			}
		}
		// Make another call to the LLM with the tool results
		// Pass the tweaker and isInteractive flags down
		return runPrompt(ctx, provider, mcpClients, tools, "", messages, tweaker, isInteractive) // Pass empty prompt
	}

	if isInteractive {
		fmt.Println() // Add spacing
	}
	return nil
}

func generateTraceID() string {
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

	return fmt.Sprintf("%s-%s", timestamp, randomSuffix)
}

// runMCPHost uses the loaded models configuration.
func runMCPHost(ctx context.Context, modelsCfg *ModelsConfig) error {
	// Model flag validation (presence and default) is now handled in rootCmd.RunE

	// Generate trace file path for CLI mode
	traceID := generateTraceID()
	traceFileName := fmt.Sprintf("%s.yaml", traceID)
	fullTracePath := filepath.Join(tracesDir, traceFileName)
	log.Info("Trace file will be saved to", "path", fullTracePath)

	// Initialize PromptRuntimeTweaks with the trace path for CLI mode
	cliTweaker := NewDefaultPromptRuntimeTweaks(fullTracePath)
	// The tweaker is now passed as a direct argument to runPrompt,
	// so setting it in context here is not strictly necessary for runPrompt itself,
	// but other functions might still expect it if not refactored.
	// For now, keep it in context for broader compatibility during refactoring.
	ctx = context.WithValue(ctx, PromptRuntimeTweaksKey, cliTweaker)

	systemPrompt, err := loadSystemPrompt(systemPromptFile)
	if err != nil {
		return fmt.Errorf("error loading system prompt: %v", err)
	}

	// Create the provider using the model ID from modelFlag and the loaded modelsCfg
	provider, err := createProvider(ctx, modelFlag, systemPrompt, modelsCfg)
	if err != nil {
		return fmt.Errorf("error creating provider for model ID '%s': %v", modelFlag, err)
	}

	log.Info("Provider and model loaded for CLI mode",
		"providerType", provider.Name(), // This will be like "anthropic", "openai"
		"modelID", modelFlag) // This is the ID used for lookup

	mcpConfig, err := loadMCPConfig()
	if err != nil {
		return fmt.Errorf("error loading MCP config: %v", err)
	}

	mcpClients, err := createMCPClients(mcpConfig)
	if err != nil {
		return fmt.Errorf("error creating MCP clients: %v", err)
	}

	defer func() {
		log.Info("Shutting down MCP servers...")
		for name, client := range mcpClients {
			if err := client.Close(); err != nil {
				log.Error("Failed to close server", "name", name, "error", err)
			} else {
				log.Info("Server closed", "name", name)
			}
		}
	}()

	for name := range mcpClients {
		log.Info("Server connected", "name", name)
	}

	var allTools []llm.Tool
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

	if err := updateRenderer(); err != nil {
		return fmt.Errorf("error initializing renderer: %v", err)
	}

	messages := make([]history.HistoryMessage, 0)

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
			mcpClients,
			messages,
			modelsCfg, // Pass loadedModelsConfig here
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
		err = runPrompt(ctx, provider, mcpClients, allTools, prompt, &messages, cliTweaker, true)
		if err != nil {
			// In interactive mode, errors are usually printed by runPrompt or its callees.
			// If a fatal error that runPrompt couldn't handle nicely, then return it.
			// For now, assume runPrompt's error handling is sufficient for user feedback.
			log.Error("Error from runPrompt", "error", err)
			// Decide if this error should terminate the CLI or allow continuation.
			// For now, let's print it and allow continuation.
			fmt.Printf("\n%s\n", errorStyle.Render(fmt.Sprintf("Error: %v", err)))
			// return err // Original behavior: terminate on error.
			// Let's allow continuing the loop for now, user can Ctrl+C.
			// If we want to terminate:
			// return fmt.Errorf("error during prompt execution: %w", err)
		}
	}
}

// loadSystemPrompt loads the system prompt from a JSON file
func loadSystemPrompt(filePath string) (string, error) {
	if filePath == "" {
		return "", nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("error reading config file: %v", err)
	}

	// Parse only the systemPrompt field
	var config struct {
		SystemPrompt string `json:"systemPrompt"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("error parsing config file: %v", err)
	}

	return config.SystemPrompt, nil
}

// --- Server Mode Implementation ---

// StartJobRequest defines the expected JSON structure for the /start endpoint.
type StartJobRequest struct {
	ModelID       string `json:"model_id"` // Changed from "model" to "model_id" for clarity
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
	log.Info("Starting server mode", "port", serverPort, "tracesDir", tracesDir, "modelsFile", modelsConfigFile)

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
				log.Error("Failed to close MCP client during server shutdown", "name", name, "error", err)
			} else {
				log.Info("MCP client closed", "name", name)
			}
		}
	}()

	for name := range serverMcpClients {
		log.Info("Server mode: MCP client connected and initialized", "name", name)
	}

	// Pre-fetch all tools from all MCP clients.
	var serverAllTools []llm.Tool
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
	mux.HandleFunc("/agents", agentshandler.HandleListAgents) // Added /agents route

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

// handleStartJob now accepts mcpClients and allTools to pass to processJob
func handleStartJob(
	w http.ResponseWriter,
	r *http.Request,
	mcpClients map[string]mcpclient.MCPClient,
	allTools []llm.Tool,
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

	// Validate required fields
	modelIDToUse := req.ModelID
	if modelIDToUse == "" {
		// ModelID is optional, try to use the default from modelsCfg
		if modelsCfg != nil && len(modelsCfg.Providers) > 0 &&
			len(modelsCfg.Providers[0].Models) > 0 && modelsCfg.Providers[0].Models[0].ID != "" {
			defaultModelID := modelsCfg.Providers[0].Models[0].ID
			modelIDToUse = defaultModelID
			log.Info("No 'model_id' provided in /start request. Using default model.", "default_model_id", defaultModelID)
		} else {
			jobMutex.Unlock()
			log.Error("Missing 'model_id' in /start request and no default model available in models.yaml")
			http.Error(w, "Missing 'model_id' in request and no default model configured on server.", http.StatusBadRequest)
			return
		}
	}

	if req.UserQuery == "" {
		jobMutex.Unlock()
		log.Error("Missing 'user_query' in /start request")
		http.Error(w, "Missing required field: user_query", http.StatusBadRequest)
		return
	}

	jobID := generateTraceID()
	currentJobID = jobID
	jobCtx, jobCancel := context.WithCancel(context.Background())
	currentJobCtx = jobCtx
	currentJobCancel = jobCancel

	traceFilePath := filepath.Join(tracesDir, fmt.Sprintf("%s.yaml", jobID))
	jobTweaker := NewDefaultPromptRuntimeTweaks(traceFilePath)

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
		log.Error("Failed to record initial state for job", "job_id", jobID, "error", err)
		http.Error(w, "Failed to save initial state", http.StatusInternalServerError)
		return
	}

	jobMutex.Unlock() // Unlock before starting goroutine to avoid holding lock for too long

	log.Info("Starting job", "job_id", jobID, "model_id", modelIDToUse, "trace_file", traceFilePath)
	// Pass model_id, system_message, mcpClients, allTools, and modelsCfg from the request/server to processJob
	go processJob(jobCtx, jobID, modelIDToUse, req.SystemMessage, messagesForHistory, jobTweaker, mcpClients, allTools, modelsCfg)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started", "job_id": jobID})
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
		json.NewEncoder(w).Encode(map[string]string{"status": "already_running", "job_id": currentJobID})
	} else {
		json.NewEncoder(w).Encode(map[string]string{"status": "idle"})
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

// processJob is the goroutine that handles a single LLM interaction task.
// It now accepts modelToUse and systemPromptToUse for job-specific configuration.
func processJob(
	jobCtx context.Context,
	jobID string,
	modelToUse string,
	systemPromptToUse string,
	messages []history.HistoryMessage,
	tweaker PromptRuntimeTweaks,
	mcpClients map[string]mcpclient.MCPClient,
	allTools []llm.Tool,
	modelsCfg *ModelsConfig, // Changed from apiKeys
) {
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

	provider, err := createProvider(jobCtx, modelToUse, systemPromptToUse, modelsCfg) // Pass modelsCfg
	if err != nil {
		log.Error("Job: Error creating provider", "job_id", jobID, "model_id", modelToUse, "error", err)
		recordJobError(jobID, messages, tweaker, fmt.Errorf("error creating provider for model ID %s: %w", modelToUse, err))
		return
	}
	log.Info("Job: Provider and model loaded", "job_id", jobID, "providerType", provider.Name(), "modelID", modelToUse)

	// MCP clients and tools are now passed in, no need to load/create/list them here.
	// The mcpClients passed in are shared; do not close them here.
	// Their lifecycle is managed by runServerMode.
	log.Info("Job: Using pre-initialized MCP clients and tools.", "job_id", jobID, "num_clients", len(mcpClients), "num_tools", len(allTools))
	// --- End Environment Setup ---

	// The initial `messages` are already recorded by handleStartJob.
	// The `runPrompt` function expects the `prompt` string to be the *newest* user message text.
	// Since `messages` already contains the full history (including the latest user turn from YAML),
	// the `prompt` string for `runPrompt` should be empty.

	// The `runPrompt` function will append new messages (assistant, tool_use, tool_result)
	// to the `messages` slice passed by address.
	err = runPrompt(jobCtx, provider, mcpClients, allTools, "", &messages, tweaker, false) // isInteractive is false

	if err != nil {
		log.Error("Job: Error during LLM interaction", "job_id", jobID, "error", err)
		recordJobError(jobID, messages, tweaker, fmt.Errorf("error during LLM interaction: %w", err))
		return
	}

	log.Info("Job processing completed successfully", "job_id", jobID)
	// Final state is recorded by runPrompt's calls to tweaker.
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
