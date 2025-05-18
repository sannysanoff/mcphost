package google

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/sannysanoff/mcphost/pkg/history"
	"strings"

	"google.golang.org/genai"
)

type Provider struct {
	client       *genai.Client
	model        string
	systemPrompt string
	toolCallID   int // This might need to be a string if IDs are not sequential integers
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
	return &Provider{
		client:       client,
		model:        model,
		systemPrompt: systemPrompt,
	}, nil
}

func (p *Provider) CreateMessage(ctx context.Context, prompt string, messages []history.Message, tools []history.Tool) (history.Message, error) {

	thinkingBudget := int32(2000)
	var config *genai.GenerateContentConfig = &genai.GenerateContentConfig{
		HTTPOptions: nil,
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{genai.NewPartFromText(p.systemPrompt)},
		},
		Temperature:          genai.Ptr[float32](0.6),
		TopP:                 nil,
		TopK:                 nil,
		CandidateCount:       0,
		MaxOutputTokens:      0,
		StopSequences:        nil,
		ResponseLogprobs:     false,
		Logprobs:             nil,
		PresencePenalty:      nil,
		FrequencyPenalty:     nil,
		Seed:                 nil,
		ResponseMIMEType:     "",
		ResponseSchema:       nil,
		RoutingConfig:        nil,
		ModelSelectionConfig: nil,
		SafetySettings:       nil,
		Tools:                nil,
		ToolConfig: &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{
				Mode: genai.FunctionCallingConfigModeAuto,
			},
		},
		Labels:             nil,
		CachedContent:      "",
		ResponseModalities: nil,
		MediaResolution:    "",
		SpeechConfig:       nil,
		AudioTimestamp:     false,
		ThinkingConfig: &genai.ThinkingConfig{
			IncludeThoughts: true,
			ThinkingBudget:  &thinkingBudget,
		},
	}

	var hist []*genai.Content
	for _, msg := range messages {
		role := mappingRole(msg.GetRole())
		var parts []*genai.Part

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
				var funcName string
				// Attempt to get the function name from a content block.
				// It's assumed that for a "tool_result", the 'Name' field of the ContentBlock
				// will be populated with the name of the function that was called.
				for _, block := range historyMsg.Content {
					if block.Type == "tool_result" && block.Name != "" {
						funcName = block.Name
						break
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
					responseData = map[string]any{"data": responseText}
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

	for _, tool := range tools {
		if config.Tools == nil {
			config.Tools = make([]*genai.Tool, 1)
			config.Tools[0] = &genai.Tool{}
		}
		decl := &genai.FunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  translateToGoogleSchema(tool.InputSchema),
		}
		config.Tools[0].FunctionDeclarations = append(config.Tools[0].FunctionDeclarations, decl)
	}

	config.ToolConfig = &genai.ToolConfig{
		FunctionCallingConfig: &genai.FunctionCallingConfig{
			Mode: genai.FunctionCallingConfigModeAuto, // Use new constant
		}}

	promptPart := genai.NewPartFromText(prompt)
	if prompt == "" {
		promptPart = hist[len(hist)-1].Parts[0]
		hist = hist[:len(hist)-1]
	}
	chat, err := p.client.Chats.Create(ctx, p.model, config, hist)
	resp, err := chat.SendMessage(ctx, *promptPart)
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

func (p *Provider) CreateToolResponse(toolCallID string, content any) (history.Message, error) {
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

func translateToGoogleSchema(schema history.Schema) *genai.Schema {
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
