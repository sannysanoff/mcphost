package agents

import (
	"fmt"
	"github.com/sannysanoff/mcphost/pkg/system"
)

type DefaultAgent struct {
	system.AgentImplementationBase
}

// GetSystemPrompt returns the agent's default system prompt
func (*DefaultAgent) GetSystemPrompt() string {
	return fmt.Sprintf(`
You are helpful agent (default agent). You can use tool calling, you have a lot of tools. 
Today is: %s ." 
`)
}

func DefaultNew() *DefaultAgent {
	return &DefaultAgent{system.AgentImplementationBase{}}
}
