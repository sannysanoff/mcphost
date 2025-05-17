package mcp

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadModelsConfig reads and processes the models configuration file.
// It populates model IDs if they are missing and checks for duplicate IDs.
func LoadModelsConfig(filePath string) (*ModelsConfig, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read models config file '%s': %w", filePath, err)
	}

	var config ModelsConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal models config file '%s': %w", filePath, err)
	}

	// seenIDs tracks model IDs to detect duplicates.
	// Key: model ID, Value: struct containing provider and model name for informative error messages.
	seenIDs := make(map[string]struct {
		ProviderName string
		ModelName    string
	})

	for i, provider := range config.Providers {
		for j, model := range provider.Models {
			currentModelID := model.ID
			if currentModelID == "" {
				// Derive ID from name: last part after '/', otherwise full name.
				derivedID := model.Name
				if lastSlash := strings.LastIndex(model.Name, "/"); lastSlash != -1 {
					derivedID = model.Name[lastSlash+1:]
				}
				currentModelID = derivedID
			}

			if currentModelID == "" {
				// This case should ideally not happen if model.Name is always non-empty.
				// Consider adding validation for non-empty model.Name if necessary.
				return nil, fmt.Errorf("model ID is empty for model with name '%s' under provider '%s' after derivation", model.Name, provider.Name)
			}

			// Store the processed ID back into the model struct.
			config.Providers[i].Models[j].ID = currentModelID

			// Check for duplicates
			if existing, found := seenIDs[currentModelID]; found {
				return nil, fmt.Errorf(
					"duplicate model ID '%s' found. Original: Provider '%s', Model Name '%s'. Conflicting: Provider '%s', Model Name '%s'",
					currentModelID,
					existing.ProviderName,
					existing.ModelName,
					provider.Name,
					model.Name,
				)
			}
			seenIDs[currentModelID] = struct {
				ProviderName string
				ModelName    string
			}{
				ProviderName: provider.Name,
				ModelName:    model.Name,
			}
		}
	}

	return &config, nil
}
