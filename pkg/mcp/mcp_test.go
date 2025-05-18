package mcp

import (
	"context"
	"github.com/sannysanoff/mcphost/pkg/history"
	"testing"

	"github.com/stretchr/testify/require"
)

type mockProvider struct {
	name string
}

func (m *mockProvider) CreateMessage(ctx context.Context, prompt string, messages []history.Message, tools []history.Tool) (history.Message, error) {
	return &history.HistoryMessage{
		Role: "assistant",
		Content: []history.ContentBlock{
			{
				Type: "text",
				Text: "Mock response",
			},
		},
	}, nil
}

func (m *mockProvider) CreateToolResponse(toolCallID string, content interface{}) (history.Message, error) {
	return &history.HistoryMessage{
		Role: "tool",
		Content: []history.ContentBlock{
			{
				Type:      "tool_result",
				ToolUseID: toolCallID,
				Content:   content,
			},
		},
	}, nil
}

func (m *mockProvider) SupportsTools() bool {
	return true
}

func (m *mockProvider) Name() string {
	return m.name
}

func TestMainEntryPoint(t *testing.T) {
	mockProvider := &mockProvider{name: "mock"}
	require.NotNil(t, mockProvider)

	// TODO: Add actual test cases using the mock provider
	runPrompt(currentJobCtx, mockProvider, nil, nil, nil, nil, nil, nil, nil)
}
