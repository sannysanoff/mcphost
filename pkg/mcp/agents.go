package mcp

import (
	"encoding/json"
	"fmt"
	"github.com/sannysanoff/mcphost/pkg/system"
	"net/http"

	"github.com/charmbracelet/log"
)

// HandleListAgents lists available compiled-in agents.
func HandleListAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}

	type agentInfo struct {
		Name          string `json:"name"`
		DefaultPrompt string `json:"default_prompt"`
		TaskForModel  string `json:"task_for_model"`
	}

	var response []agentInfo
	for name, constructor := range system.AllAgents {
		agentInstance := constructor() // Create an instance to get its properties
		response = append(response, agentInfo{
			Name:          name, // Use the registered name
			DefaultPrompt: agentInstance.GetSystemPrompt(),
			TaskForModel:  agentInstance.GetTaskForModelSelection(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Error("/agents: Failed to encode agents information to JSON", "error", err)
		http.Error(w, "Failed to encode response: "+err.Error(), http.StatusInternalServerError)
	}
}

// GetDefaultAgent loads and returns the default agent implementation from compiled-in agents.
func GetDefaultAgent() (system.Agent, error) {
	return LoadAgentByName("default")
}

// LoadAgentByName loads a specific compiled-in agent by its registered name.
func LoadAgentByName(agentName string) (system.Agent, error) {
	if agentName == "" {
		return nil, fmt.Errorf("agent name cannot be empty")
	}

	constructor, ok := system.AllAgents[agentName]
	if !ok {
		// If a "default" agent is requested but not registered,
		// it's an issue with agent registration, not file system.
		// The user must ensure a "default" agent is registered.
		log.Error("Agent not found in AllAgents map", "name", agentName)
		return nil, fmt.Errorf("agent '%s' not registered. Ensure it is compiled in and registered in an init() function", agentName)
	}

	agentInstance := constructor()
	log.Info("Loaded agent from compiled-in map", "name", agentName, "filename_method_output", agentInstance.Name())
	return agentInstance, nil
}
