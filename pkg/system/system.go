package system

import (
	"github.com/sannysanoff/mcphost/pkg/history"
)

// Agent represents a loaded agent with its interpreter and methods
type Agent interface {
	// GetSystemPrompt returns the agent's default system prompt
	GetSystemPrompt() string

	// NormalizeHistory processes and normalizes message history
	NormalizeHistory(messages []history.HistoryMessage) []history.HistoryMessage

	// Filename returns the source file name of this agent
	Filename() string

	GetTaskForModelSelection() string

}

func NativeFunction(a string) string {
	return a + "X"
}

type AgentImplementation struct {
	AgentData any
}

type AgentImplementationBase struct {
	filename string

	coAgents map[string][]history.HistoryMessage

}

// GetSystemPrompt returns a default system prompt.
// Specific agents can override this.
func (e *AgentImplementationBase) GetSystemPrompt() string {
	return "You are helpful assistant"
}

// NormalizeHistory provides a default behavior for normalizing messages.
// Specific agents can override this.
func (e *AgentImplementationBase) NormalizeHistory(messages []history.HistoryMessage) []history.HistoryMessage {
	return messages
}

func (e *AgentImplementationBase) Filename() string {
	// Ensure this field is set by the agent's constructor or an init method.
	// It should typically return the name the agent was registered with.
	if e.filename == "" {
		// This is a fallback or a gentle reminder that it should be set.
		// Depending on strictness, could log a warning or even panic.
		// For now, let's return a placeholder.
		// log.Warn("AgentImplementationBase.filename is not set. Filename() will return 'unknown_agent_filename'.")
		return "unknown_agent_filename_please_set_in_constructor"
	}
	return e.filename
}

func (e *AgentImplementationBase) GetTaskForModelSelection() string {
	return "default"
}

var PerformLLMCallHook func(string, string) (string, string, error)

func PerformLLMCall(agent string, prompt string) (string, string, error) {
	return PerformLLMCallHook(agent, prompt)
}

func IsUserMessage(message history.HistoryMessage) bool {
	return message.Role == "user"
}

// Ensure ContentBlock implements yaml.Marshaler and yaml.Unmarshaler if custom logic for json.RawMessage or interface{} is needed.
// For now, relying on struct tags and default behavior of yaml.v3.

// IsModelAnswer checks if all content blocks in a message are of type "text".
// It returns true if the message content is purely textual, false otherwise (e.g., tool calls, empty content).
func IsModelAnswer(message history.HistoryMessage) bool {
	if (message.Role == "model") || (message.Role == "assistant") {
		if len(message.Content) == 0 {
			return false // No content means it's not a text answer.
		}
		for _, block := range message.Content {
			if block.Type != "text" {
				return false // Found a non-text block.
			}
		}
		return true // All blocks are of type "text".
	} else {
		return false
	}
}

func IsModelAnswerAny(message history.HistoryMessage) bool {
	if (message.Role == "model") || (message.Role == "assistant") {
		return true // All blocks are of type "text".
	} else {
		return false
	}
}

var AllAgents = map[string]func() Agent{}

func RegisterAgent(agentName string, constructor func() Agent) {
	AllAgents[agentName] = constructor
}
