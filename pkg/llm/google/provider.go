package google

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/genai"
	//"github.com/google/generative-ai-go/genai"
	"github.com/mark3labs/mcphost/pkg/history"
	"github.com/mark3labs/mcphost/pkg/llm"
	"google.golang.org/api/option"
)

type Provider struct {
	client *genai.Client

	toolCallID int // This might need to be a string if IDs are not sequential integers
}

func NewProvider(ctx context.Context, apiKey, model, systemPrompt string) (*Provider, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:      apiKey,
		Backend:     0,
		Project:     "",
		Location:    "",
		Credentials: nil,
		HTTPClient:  nil,
		HTTPOptions: genai.HTTPOptions{},
	})
	if err != nil {
		return nil, err
	}
	m := client.GenerativeModel(model)
	// If systemPrompt is provided, set the system prompt for the model.
	if systemPrompt != "" {
		// Assuming system prompt takes the "user" role, or a default if "" is fine.
		// types.go NewContentFromText defaults to user role if role is empty.
		m.SystemInstruction = genai.NewContentFromText(systemPrompt, "")
	}
	return &Provider{
		client: client,
		model:  m,
		chat:   m.StartChat(),
	}, nil
}

func (p *Provider) CreateMessage(ctx context.Context, prompt string, messages []llm.Message, tools []llm.Tool) (llm.Message, error) {
	var hist []*genai.Content
	for _, msg := range messages {
		role := mappingRole(msg.GetRole())
		var parts []genai.Part

		isToolResponse := msg.IsToolResponse()
		toolCalls := msg.GetToolCalls()

		if len(toolCalls) > 0 {
			// This message contains tool calls (outgoing from model)
			for _, call := range toolCalls {
				parts = append(parts, genai.NewPartFromFunctionCall(call.GetName(), call.GetArguments()))
			}
		}

		if isToolResponse {
			// This message is a tool response (incoming to model)
			if historyMsg, ok := msg.(*history.HistoryMessage); ok {
				// Assuming historyMsg.ToolName is populated for tool responses
				funcName := historyMsg.ToolName
				if funcName == "" {
					// Attempt to get it from a block if available, though ToolName on msg is preferred
					for _, block := range historyMsg.Content {
						if block.Type == "tool_result" && block.Name != "" { // Assuming block.Name exists
							funcName = block.Name
							break
						}
					}
				}
				if funcName == "" {
					return nil, fmt.Errorf("tool response message missing function name: %+v", historyMsg)
				}

				// Consolidate response text from blocks or GetContent()
				responseText := ""
				if len(historyMsg.Content) > 0 {
					for _, block := range historyMsg.Content {
						if block.Type == "tool_result" {
							responseText = block.Text // Or concatenate if multiple
							break
						}
					}
				}
				if responseText == "" { // Fallback if no specific tool_result block found
					responseText = msg.GetContent()
				}

				var responseData map[string]any
				if err := json.Unmarshal([]byte(responseText), &responseData); err != nil {
					// If not a JSON object, Gemini might require it to be.
					// Wrapping as {"output": responseText} is a common pattern if the tool returns a simple string.
					// However, the function's schema should define the output structure.
					// For now, strict parsing; if this fails often, consider a fallback.
					return nil, fmt.Errorf("tool response content for %s is not a valid JSON object: %s, error: %w", funcName, responseText, err)
				}
				parts = append(parts, genai.NewPartFromFunctionResponse(funcName, responseData))
				// Ensure role for tool response is "user" as genai lib only supports "user" & "model"
				// mappingRole should handle mapping "tool" to "user"
				role = genai.RoleUser
			}
		}

		// Append text content if it exists and is not part of a tool response already handled
		if text := strings.TrimSpace(msg.GetContent()); text != "" && !isToolResponse && len(toolCalls) == 0 {
			parts = append(parts, genai.NewPartFromText(text))
		}

		if len(parts) > 0 {
			hist = append(hist, &genai.Content{Role: role, Parts: parts})
		}
	}

	p.model.Tools = nil
	for _, tool := range tools {
		p.model.Tools = append(p.model.Tools, &genai.Tool{
			FunctionDeclarations: []*genai.FunctionDeclaration{
				{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  translateToGoogleSchema(tool.InputSchema),
				},
			},
		})
	}

	p.chat.History = hist
	p.model.ToolConfig = &genai.ToolConfig{
		FunctionCallingConfig: &genai.FunctionCallingConfig{
			Mode: genai.FunctionCallingConfigModeAuto, // Use new constant
		}}

	// Send the new prompt as a distinct part.
	promptPart := genai.NewPartFromText(prompt)
	resp, err := p.chat.SendMessage(ctx, *promptPart)
	if err != nil {
		return nil, err
	}

	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return nil, fmt.Errorf("no response candidate from model")
	}

	candidate := resp.Candidates[0]
	// The library enforces a generation config with 1 candidate.
	// The Message struct will need to handle the new Candidate structure if it changed.
	// For toolCallID, we need to count FunctionCall parts in the response.
	numNewToolCalls := 0
	if candidate.Content != nil {
		for _, part := range candidate.Content.Parts {
			if fc := part.FunctionCall; fc != nil {
				numNewToolCalls++
			}
		}
	}

	// Assuming Message struct can take candidate and a base ID for its internal tool call ID generation
	// This part depends on how Message struct is implemented and how it assigns IDs.
	// If toolCallID is a simple integer counter, this works. If IDs are strings from the model, it's different.
	// For now, let's assume Message struct handles parsing of Candidate.
	m := &Message{
		Candidate:  candidate,
		toolCallID: p.toolCallID, // Pass the current base ID
	}

	p.toolCallID += numNewToolCalls // Increment by the number of new calls
	return m, nil
}

func (p *Provider) CreateToolResponse(toolCallID string, content any) (llm.Message, error) {
	// UNUSED: Nothing in root.go calls this.
	return nil, nil
}

func (p *Provider) SupportsTools() bool {
	// UNUSED: Nothing in root.go calls this.
	return true
}

func (p *Provider) Name() string {
	return "Google"
}

func translateToGoogleSchema(schema llm.Schema) *genai.Schema {
	s := &genai.Schema{
		Type:       toType(schema.Type),
		Required:   schema.Required,
		Properties: make(map[string]*genai.Schema),
	}

	for name, prop := range schema.Properties {
		m, ok := prop.(map[string]any)
		if !ok || len(m) == 0 {
			continue
		}
		s.Properties[name] = propertyToGoogleSchema(m)
	}

	// Workaround for Gemini API: properties should be non-empty for OBJECT type.
	// Apply this only if the original schema intended an object type with no properties.
	if s.Type == genai.TypeObject && (schema.Properties == nil || len(schema.Properties) == 0) && len(s.Properties) == 0 {
		s.Nullable = genai.Ptr(true)                                  // Use Ptr for bool
		s.Properties["unused_placeholder_parameter"] = &genai.Schema{ // More descriptive name
			Type:        genai.TypeString, // String is often safer/simpler than Integer for placeholders
			Description: "This is a placeholder parameter to satisfy API requirements for empty objects. It should be ignored.",
			Nullable:    genai.Ptr(true),
		}
	}
	return s
}

func propertyToGoogleSchema(properties map[string]any) *genai.Schema {
	typ, ok := properties["type"].(string)
	if !ok {
		return nil
	}
	s := &genai.Schema{Type: toType(typ)}
	if desc, ok := properties["description"].(string); ok {
		s.Description = desc
	}

	// Objects and arrays need to have their properties recursively mapped.
	if s.Type == genai.TypeObject {
		objectProperties := properties["properties"].(map[string]any)
		s.Properties = make(map[string]*genai.Schema)
		for name, prop := range objectProperties {
			s.Properties[name] = propertyToGoogleSchema(prop.(map[string]any))
		}
	} else if s.Type == genai.TypeArray {
		itemProperties := properties["items"].(map[string]any)
		s.Items = propertyToGoogleSchema(itemProperties)
	}

	return s
}

func toType(typ string) genai.Type {
	switch typ {
	case "string":
		return genai.TypeString
	case "number":
		return genai.TypeNumber
	case "integer":
		return genai.TypeInteger
	case "boolean":
		return genai.TypeBoolean
	case "object":
		return genai.TypeObject
	case "array":
		return genai.TypeArray
	default:
		return genai.TypeUnspecified
	}
}

const (
	roleUser  = "user"
	roleModel = "model"
)

var roleMap = map[string]string{
	roleUser:  roleUser,
	roleModel: roleModel,
}

func mappingRole(role string) string {
	v, ok := roleMap[role]
	if !ok {
		return roleUser
	}
	return v
}
