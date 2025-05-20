package system

import (
	"github.com/sannysanoff/mcphost/pkg/history"
)

func NativeFunction(a string) string {
	return a + "X"
}

type AgentImplementation struct {
	AgentData               any
	GetPrompt               func() string
	DefaultNormalizeHistory func(messages []history.HistoryMessage) []history.HistoryMessage
}

type AgentImplementationBase struct {
	filename string
}

func (e *AgentImplementationBase) GetPrompt() string {
	return "You are helpful assistant"
}

func (e *AgentImplementationBase) DefaultNormalizeHistory(messages []history.HistoryMessage) []history.HistoryMessage {
	return messages
}

func (e *AgentImplementationBase) Filename() string {
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
