package google

import (
	"fmt"
	"strings"

	"github.com/mark3labs/mcphost/pkg/llm"
	"google.golang.org/genai"
)

type ToolCall struct {
	genai.FunctionCall

	toolCallID int
}

func (t *ToolCall) GetName() string {
	return t.Name
}

func (t *ToolCall) GetArguments() map[string]any {
	return t.Args
}

func (t *ToolCall) GetID() string {
	return fmt.Sprintf("Tool<%d>", t.toolCallID)
}

type Message struct {
	*genai.Candidate

	toolCallID int
}

func (m *Message) GetRole() string {
	return m.Candidate.Content.Role
}

func (m *Message) GetContent() string {
	var sb strings.Builder
	if m.Candidate != nil && m.Candidate.Content != nil {
		for _, part := range m.Candidate.Content.Parts {
			// Direct access to Text field for *genai.Part
			if part.Text != "" {
				sb.WriteString(part.Text)
			}
		}
	}
	return sb.String()
}

func (m *Message) GetToolCalls() []llm.ToolCall {
	var calls []llm.ToolCall
	if m.Candidate != nil && m.Candidate.Content != nil {
		for i, part := range m.Candidate.Content.Parts {
			if part.FunctionCall != nil {
				// Create a new genai.FunctionCall instance for the ToolCall struct
				// as genai.FunctionCall is a struct, not a pointer in Part.
				fc := *part.FunctionCall 
				calls = append(calls, &ToolCall{fc, m.toolCallID + i})
			}
		}
	}
	return calls
}

func (m *Message) IsToolResponse() bool {
	if m.Candidate != nil && m.Candidate.Content != nil {
		for _, part := range m.Candidate.Content.Parts {
			// Direct access to FunctionResponse field for *genai.Part
			if part.FunctionResponse != nil {
				return true
			}
		}
	}
	return false
}

func (m *Message) GetToolResponseID() string {
	return fmt.Sprintf("Tool<%d>", m.toolCallID)
}

func (m *Message) GetUsage() (input int, output int) {
	return 0, 0
}
