package history

import (
	"encoding/json"
	"strings"

	_ "gopkg.in/yaml.v3" // Used for YAML struct tags, actual (un)marshalling happens elsewhere
)

// HistoryMessage implements the llm.Message interface for stored messages
type HistoryMessage struct {
	ID         int            `json:"id" yaml:"id"`
	PreviousID int            `json:"previous_id" yaml:"previous_id"`
	Role       string         `json:"role" yaml:"role"`
	Content    []ContentBlock `json:"content" yaml:"content"`
	Synthetic  string         `json:"synthetic,omitempty" yaml:"synthetic,omitempty"` // Information about agent/phase acting as user
}

// Ensure HistoryMessage implements yaml.Marshaler and yaml.Unmarshaler if custom logic is needed.
// For now, relying on struct tags.

func (m *HistoryMessage) GetRole() string {
	return m.Role
}

func (m *HistoryMessage) GetContent() string {
	// Concatenate all text content blocks
	var content string
	for _, block := range m.Content {
		if block.Type == "text" {
			content += block.Text + " "
		}
	}
	return strings.TrimSpace(content)
}

func (m *HistoryMessage) GetToolCalls() []ToolCall {
	var calls []ToolCall
	for _, block := range m.Content {
		if block.Type == "tool_use" {
			calls = append(calls, &HistoryToolCall{
				id:   block.ID,
				name: block.Name,
				args: block.Input,
			})
		}
	}
	return calls
}

func (m *HistoryMessage) IsToolResponse() bool {
	for _, block := range m.Content {
		if block.Type == "tool_result" {
			return true
		}
	}
	return false
}

func GetToolResults(m *HistoryMessage) any {
	for _, block := range m.Content {
		if block.Type == "tool_result" {
			return block.Content
		}
	}
	return ""
}

func (m *HistoryMessage) GetToolResponseID() string {
	for _, block := range m.Content {
		if block.Type == "tool_result" {
			return block.ToolUseID
		}
	}
	return ""
}

func (m *HistoryMessage) GetUsage() (int, int) {
	return 0, 0 // History doesn't track usage
}

// HistoryToolCall implements llm.ToolCall for stored tool calls
type HistoryToolCall struct {
	id   string
	name string
	args json.RawMessage
}

func (t *HistoryToolCall) GetID() string {
	return t.id
}

func (t *HistoryToolCall) GetName() string {
	return t.name
}

func (t *HistoryToolCall) GetArguments() map[string]interface{} {
	var args map[string]interface{}
	if err := json.Unmarshal(t.args, &args); err != nil {
		return make(map[string]interface{})
	}
	return args
}

// ContentBlock represents a block of content in a message
type ContentBlock struct {
	Type      string          `json:"type" yaml:"type"`
	Text      string          `json:"text,omitempty" yaml:"text,omitempty"`
	ID        string          `json:"id,omitempty" yaml:"id,omitempty"`                   // Used for tool_use block
	ToolUseID string          `json:"tool_use_id,omitempty" yaml:"tool_use_id,omitempty"` // Used for tool_result block
	Name      string          `json:"name,omitempty" yaml:"name,omitempty"`               // Used for tool_use block
	Input     json.RawMessage `json:"input,omitempty" yaml:"input,omitempty"`             // Used for tool_use block
	Content   interface{}     `json:"content,omitempty" yaml:"content,omitempty"`         // Used for tool_result block, can be string or []ContentBlock
}

// NewAssistantResponse creates a new assistant message with simple text content
func NewAssistantResponse(text string) *HistoryMessage {
	return &HistoryMessage{
		Role: "assistant",
		Content: []ContentBlock{
			{
				Type: "text",
				Text: text,
			},
		},
	}
}

func NewUserMessage(text string) *HistoryMessage {
	return &HistoryMessage{
		Role: "user",
		Content: []ContentBlock{
			{
				Type: "text",
				Text: text,
			},
		},
	}
}

// NewToolCallMessage creates a new assistant message with a tool call request
func NewToolCallMessage(toolName string, args map[string]interface{}) *HistoryMessage {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil
	}

	return &HistoryMessage{
		Role: "assistant",
		Content: []ContentBlock{
			{
				Type:  "tool_use",
				Name:  toolName,
				Input: argsJSON,
			},
		},
	}
}

func IsModelAnswer2(msg Message) bool {
	return msg.GetRole() == "assistant" || msg.GetRole() == "model"
}
