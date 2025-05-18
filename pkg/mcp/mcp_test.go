package mcp

import (
	"context"
	"fmt"
	"github.com/sannysanoff/mcphost/pkg/history"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

type mockMCPClient struct {
	tools       []mcp.Tool
	prompts     []mcp.Prompt
	resources   []mcp.Resource
	rateLimited bool
	lastCall    time.Time
}

func (m *mockMCPClient) Initialize(ctx context.Context, request mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	return &mcp.InitializeResult{}, nil
}

func (m *mockMCPClient) Ping(ctx context.Context) error {
	return nil
}

func (m *mockMCPClient) ListResourcesByPage(ctx context.Context, request mcp.ListResourcesRequest) (*mcp.ListResourcesResult, error) {
	return &mcp.ListResourcesResult{
		Resources: m.resources,
	}, nil
}

func (m *mockMCPClient) ListResources(ctx context.Context, request mcp.ListResourcesRequest) (*mcp.ListResourcesResult, error) {
	return m.ListResourcesByPage(ctx, request)
}

func (m *mockMCPClient) ListResourceTemplatesByPage(ctx context.Context, request mcp.ListResourceTemplatesRequest) (*mcp.ListResourceTemplatesResult, error) {
	return &mcp.ListResourceTemplatesResult{}, nil
}

func (m *mockMCPClient) ListResourceTemplates(ctx context.Context, request mcp.ListResourceTemplatesRequest) (*mcp.ListResourceTemplatesResult, error) {
	return m.ListResourceTemplatesByPage(ctx, request)
}

func (m *mockMCPClient) ReadResource(ctx context.Context, request mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return &mcp.ReadResourceResult{}, nil
}

func (m *mockMCPClient) Subscribe(ctx context.Context, request mcp.SubscribeRequest) error {
	return nil
}

func (m *mockMCPClient) Unsubscribe(ctx context.Context, request mcp.UnsubscribeRequest) error {
	return nil
}

func (m *mockMCPClient) ListPromptsByPage(ctx context.Context, request mcp.ListPromptsRequest) (*mcp.ListPromptsResult, error) {
	return &mcp.ListPromptsResult{
		Prompts: m.prompts,
	}, nil
}

func (m *mockMCPClient) ListPrompts(ctx context.Context, request mcp.ListPromptsRequest) (*mcp.ListPromptsResult, error) {
	return m.ListPromptsByPage(ctx, request)
}

func (m *mockMCPClient) GetPrompt(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	return nil, fmt.Errorf("prompt not found")
}

func (m *mockMCPClient) ListToolsByPage(ctx context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	return &mcp.ListToolsResult{
		Tools: m.tools,
	}, nil
}

func (m *mockMCPClient) ListTools(ctx context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	return m.ListToolsByPage(ctx, request)
}

func (m *mockMCPClient) CallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if m.rateLimited {
		if time.Since(m.lastCall) < time.Second {
			return nil, fmt.Errorf("rate limited")
		}
		m.lastCall = time.Now()
	}

	for _, t := range m.tools {
		if t.Name == request.Params.Name {
			return &mcp.CallToolResult{
				Content: []mcp.Content{
					&mcp.TextContent{
						Text: "mock response",
					},
				},
			}, nil
		}
	}
	return nil, fmt.Errorf("tool not found")
}

func (m *mockMCPClient) SetLevel(ctx context.Context, request mcp.SetLevelRequest) error {
	return nil
}

func (m *mockMCPClient) Complete(ctx context.Context, request mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	return &mcp.CompleteResult{}, nil
}

func (m *mockMCPClient) Close() error {
	return nil
}

func (m *mockMCPClient) OnNotification(handler func(notification mcp.JSONRPCNotification)) {}

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

	mockClient := &mockMCPClient{
		tools: []mcp.Tool{
			{
				Name:        "test_tool",
				Description: "Test tool",
			},
		},
		prompts: []mcp.Prompt{
			{
				Name: "test_prompt",
				Description: "Test prompt",
				Arguments: []mcp.PromptArgument{
					{
						Name: "test_arg",
						Description: "Test argument",
					},
				},
			},
		},
		resources: []mcp.Resource{
			{
				Name: "test_resource",
				URI:  "test://resource",
			},
		},
	}
	require.NotNil(t, mockClient)

	// TODO: Add actual test cases using the mock provider and client
	// runPrompt(currentJobCtx, mockProvider, mockClient, nil, nil, nil, nil, nil, nil)
}
