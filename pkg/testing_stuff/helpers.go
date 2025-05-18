package testing_stuff

import (
	"context"
	"errors"
	"fmt"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sannysanoff/mcphost/pkg/history"
	"time"
)

type MockMCPClient struct {
	Tools       []mcp.Tool
	prompts     []mcp.Prompt
	resources   []mcp.Resource
	rateLimited bool
	MockHander  func(request mcp.CallToolRequest) *mcp.CallToolResult
	lastCall    time.Time
}

func (m *MockMCPClient) Initialize(ctx context.Context, request mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	return &mcp.InitializeResult{}, nil
}

func (m *MockMCPClient) Ping(ctx context.Context) error {
	return nil
}

func (m *MockMCPClient) ListResourcesByPage(ctx context.Context, request mcp.ListResourcesRequest) (*mcp.ListResourcesResult, error) {
	return &mcp.ListResourcesResult{
		Resources: m.resources,
	}, nil
}

func (m *MockMCPClient) ListResources(ctx context.Context, request mcp.ListResourcesRequest) (*mcp.ListResourcesResult, error) {
	return m.ListResourcesByPage(ctx, request)
}

func (m *MockMCPClient) ListResourceTemplatesByPage(ctx context.Context, request mcp.ListResourceTemplatesRequest) (*mcp.ListResourceTemplatesResult, error) {
	return &mcp.ListResourceTemplatesResult{}, nil
}

func (m *MockMCPClient) ListResourceTemplates(ctx context.Context, request mcp.ListResourceTemplatesRequest) (*mcp.ListResourceTemplatesResult, error) {
	return m.ListResourceTemplatesByPage(ctx, request)
}

func (m *MockMCPClient) ReadResource(ctx context.Context, request mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return &mcp.ReadResourceResult{}, nil
}

func (m *MockMCPClient) Subscribe(ctx context.Context, request mcp.SubscribeRequest) error {
	return nil
}

func (m *MockMCPClient) Unsubscribe(ctx context.Context, request mcp.UnsubscribeRequest) error {
	return nil
}

func (m *MockMCPClient) ListPromptsByPage(ctx context.Context, request mcp.ListPromptsRequest) (*mcp.ListPromptsResult, error) {
	return &mcp.ListPromptsResult{
		Prompts: m.prompts,
	}, nil
}

func (m *MockMCPClient) ListPrompts(ctx context.Context, request mcp.ListPromptsRequest) (*mcp.ListPromptsResult, error) {
	return m.ListPromptsByPage(ctx, request)
}

func (m *MockMCPClient) GetPrompt(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	return nil, fmt.Errorf("prompt not found")
}

func (m *MockMCPClient) ListToolsByPage(ctx context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	return &mcp.ListToolsResult{
		Tools: m.Tools,
	}, nil
}

func (m *MockMCPClient) ListTools(ctx context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	return m.ListToolsByPage(ctx, request)
}

func (m *MockMCPClient) CallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if m.rateLimited {
		if time.Since(m.lastCall) < time.Second {
			return nil, fmt.Errorf("rate limited")
		}
		m.lastCall = time.Now()
	}

	resp := m.MockHander(request)
	if resp == nil {
		return nil, fmt.Errorf("tool not found")
	}
	return resp, nil

}

func (m *MockMCPClient) SetLevel(ctx context.Context, request mcp.SetLevelRequest) error {
	return nil
}

func (m *MockMCPClient) Complete(ctx context.Context, request mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	return &mcp.CompleteResult{}, nil
}

func (m *MockMCPClient) Close() error {
	return nil
}

func (m *MockMCPClient) OnNotification(handler func(notification mcp.JSONRPCNotification)) {}

type MockProvider struct {
	TheName        string
	Responses      map[string]history.Message
	OtherResponses func(string) string
}

func (m *MockProvider) CreateMessage(ctx context.Context, prompt string, messages []history.Message, tools []history.Tool) (history.Message, error) {
	if prompt == "" {
		lastMessage := messages[len(messages)-1]
		if lastMessage.IsToolResponse() {
			hm, ok := lastMessage.(*history.HistoryMessage)
			if ok {
				content := hm.Content
				cb, ok := content[0].Content.([]history.ContentBlock)
				if ok {
					prompt = fmt.Sprintf("%v", cb[0].Text)
				}
			}
			//tr := history.GetToolResults(lastMessage)

		}
	}
	if prompt == "" {
		return nil, errors.New("Error, unable to obtain content from last message")
	}
	message, found := m.Responses[prompt]
	if found {
		return message, nil
	}
	if m.OtherResponses != nil {
		messageS := m.OtherResponses(prompt)
		if messageS != "" {
			return history.NewAssistantResponse(messageS), nil
		}
	}
	return nil, fmt.Errorf("prompt not found")
}

func (m *MockProvider) CreateToolResponse(toolCallID string, content interface{}) (history.Message, error) {
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

func (m *MockProvider) SupportsTools() bool {
	return true
}

func (m *MockProvider) Name() string {
	return m.TheName
}

func MakeCallToolResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Annotated: mcp.Annotated{},
				Type:      "text",
				Text:      text,
			},
		},
	}
}

func MakeMockWebSearch() mcp.Tool {
	return mcp.Tool{
		Name:        "web_search",
		Description: "Web search tool",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "The search query",
				},
				"count": map[string]interface{}{
					"type":        "number",
					"description": "Number of results to return",
					"minimum":     1,
					"maximum":     10,
				},
			},
			Required: []string{"query"},
		},
	}
}

func MakeMockWebFetch() mcp.Tool {
	return mcp.Tool{
		Name:        "web_fetch",
		Description: "Web fetch tool",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"url": map[string]interface{}{
					"type":        "string",
					"description": "The URL to fetch",
					"format":      "uri",
				},
			},
			Required: []string{"url"},
		},
	}
}

func MockPerformLLMCall(agent string, prompt string) (string, error) {
	return "response_from:" + prompt, nil
}
