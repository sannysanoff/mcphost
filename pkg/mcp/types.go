package mcp

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mark3labs/mcphost/pkg/history"
	"gopkg.in/yaml.v3"
)

// PromptRuntimeTweaks allows for dynamic adjustments during prompt execution,
// such as filtering available tools and recording state for tracing.
type PromptRuntimeTweaks interface {
	IsToolEnabled(toolName string) bool
	AssignIDsToNewMessage(newMessage *history.HistoryMessage, existingMessages []history.HistoryMessage)
	RecordState(messages []history.HistoryMessage, stepName string) error
}

// PromptRuntimeTweaksKey is used as a key for storing and retrieving PromptRuntimeTweaks from a context.
type PromptRuntimeTweaksKeyType struct{}

var PromptRuntimeTweaksKey = PromptRuntimeTweaksKeyType{}

// DefaultPromptRuntimeTweaks implements PromptRuntimeTweaks.
// It enables any tool by default and handles trace recording.
type DefaultPromptRuntimeTweaks struct {
	traceFilePath string
	nextHistoryID int
}

// NewDefaultPromptRuntimeTweaks creates a new instance of DefaultPromptRuntimeTweaks.
// traceFilePath can be empty if tracing is disabled.
func NewDefaultPromptRuntimeTweaks(traceFilePath string) *DefaultPromptRuntimeTweaks {
	return &DefaultPromptRuntimeTweaks{
		traceFilePath: traceFilePath,
		nextHistoryID: 1, // Start IDs from 1
	}
}

// IsToolEnabled for DefaultPromptRuntimeTweaks always returns true.
func (d *DefaultPromptRuntimeTweaks) IsToolEnabled(toolName string) bool {
	return true
}

// AssignIDsToNewMessage assigns a new ID and PreviousID to the newMessage.
func (d *DefaultPromptRuntimeTweaks) AssignIDsToNewMessage(newMessage *history.HistoryMessage, existingMessages []history.HistoryMessage) {
	if d.traceFilePath == "" { // Tracing disabled
		return
	}
	newMessage.ID = d.nextHistoryID
	d.nextHistoryID++
	if len(existingMessages) > 0 {
		newMessage.PreviousID = existingMessages[len(existingMessages)-1].ID
	} else {
		newMessage.PreviousID = 0 // No previous message, or use a specific sentinel like -1 if 0 is a valid ID.
	}
}

// RecordState writes the current state of messages to the trace file.
func (d *DefaultPromptRuntimeTweaks) RecordState(messages []history.HistoryMessage, stepName string) error {
	if d.traceFilePath == "" { // Tracing disabled
		return nil
	}

	data, err := yaml.Marshal(messages)
	if err != nil {
		return fmt.Errorf("failed to marshal messages to YAML for step '%s': %w", stepName, err)
	}

	// Write to a temporary file first for safety
	tempFilePattern := filepath.Base(d.traceFilePath) + "*.tmp"
	tempFile, err := os.CreateTemp(filepath.Dir(d.traceFilePath), tempFilePattern)
	if err != nil {
		return fmt.Errorf("failed to create temporary trace file for step '%s': %w", stepName, err)
	}
	defer os.Remove(tempFile.Name()) // Clean up temp file if rename fails or on error

	if _, err := tempFile.Write(data); err != nil {
		tempFile.Close()
		return fmt.Errorf("failed to write to temporary trace file for step '%s': %w", stepName, err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temporary trace file for step '%s': %w", stepName, err)
	}

	// Rename temporary file to the actual trace file
	if err := os.Rename(tempFile.Name(), d.traceFilePath); err != nil {
		return fmt.Errorf("failed to rename temporary trace file to %s for step '%s': %w", d.traceFilePath, stepName, err)
	}

	// log.Debug("Trace recorded", "step", stepName, "file", d.traceFilePath) // Assuming log is available
	return nil
}

// ModelsConfig represents the top-level structure of models.yaml
type ModelsConfig struct {
	Providers []ModelProvider `yaml:"providers"`
}

// ModelProvider represents a single provider section in models.yaml
type ModelProvider struct {
	Name    string  `yaml:"provider"`
	BaseURL string  `yaml:"base_url,omitempty"`
	APIKey  string  `yaml:"api_key,omitempty"`
	Models  []Model `yaml:"models"`
}

// Model represents a single model definition within a provider
type Model struct {
	Name               string         `yaml:"name"`
	ID                 string         `yaml:"id,omitempty"` // Will be populated with derived ID if empty, and validated for uniqueness
	PreferencesPerTask map[string]int `yaml:"preferences_per_task,omitempty"`
}
