package mcp

import (
	"context"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/sannysanoff/mcphost/pkg/history"
	"github.com/sannysanoff/mcphost/pkg/system"
	"github.com/sannysanoff/mcphost/pkg/testing_stuff"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestMainEntryPoint(t *testing.T) {
	mockProvider, mcpClients, allTools, msgs := MakeBratislavaMock(t)
	agent, err := LoadAgentByName("default")
	require.NotNil(t, err)
	err = runPromptIteration(currentJobCtx, mockProvider, agent, mcpClients, allTools, history.NewUserMessage("what_time_in_bratislava"), &msgs, nil, false)
	require.Nil(t, err)
}

func TestDistrustfulResearcher(t *testing.T) {
	mockProvider, mcpClients, allTools, msgs := MakeBratislavaMock(t)
	mockProvider.OtherResponses = func(q string) string {
		if strings.Contains(q, "<research_goal>") && strings.Contains(q, "response_from:") {
			return "ok_i_was_wrong"
		}
		return ""
	}
	//agents.DistrustfulResearcherNew()
	agent, err := LoadAgentByName("distrustful_researcher")
	require.Nil(t, err)
	err = runPromptIteration(currentJobCtx, mockProvider, agent, mcpClients, allTools, history.NewUserMessage("what_time_in_bratislava"), &msgs, nil, false)
	require.Nil(t, err)
}

func MakeBratislavaMock(t *testing.T) (*testing_stuff.MockProvider, map[string]mcpclient.MCPClient, []history.Tool, []history.HistoryMessage) {
	system.PerformLLMCallHook = testing_stuff.MockPerformLLMCall
	mockProvider := MakeMockProvider()
	require.NotNil(t, mockProvider)

	mockClient := MakeMockClient()
	require.NotNil(t, mockClient)

	mcpClients := map[string]mcpclient.MCPClient{"mock": mockClient}
	allTools := GenerateToolsFromMCPClients(context.Background(), mcpClients, nil)

	mockProvider.Responses["what_time_in_bratislava"] = history.NewToolCallMessage("mock__web_search", map[string]any{"query": "bratislava_links"})
	mockProvider.Responses["bratislava_links"] = history.NewToolCallMessage("mock__web_fetch", map[string]any{"query": "bratislava_url"})
	mockProvider.Responses["bratislava_url_fetch_response"] = history.NewAssistantResponse("bratislava_time_is_1430")
	var msgs []history.HistoryMessage
	mockClient.MockHander = func(request mcp.CallToolRequest) *mcp.CallToolResult {
		if request.Params.Name == "web_search" && request.Params.Arguments["query"] == "bratislava_links" {
			return testing_stuff.MakeCallToolResult("bratislava_links")
		} else if request.Params.Name == "web_fetch" {
			return testing_stuff.MakeCallToolResult("bratislava_url_fetch_response")
		}
		return nil
	}
	return mockProvider, mcpClients, allTools, msgs
}

func MakeMockClient() *testing_stuff.MockMCPClient {
	mockWebSearch := testing_stuff.MakeMockWebSearch()
	mockWebFetch := testing_stuff.MakeMockWebFetch()
	return &testing_stuff.MockMCPClient{
		Tools: []mcp.Tool{mockWebSearch, mockWebFetch},
	}
}
