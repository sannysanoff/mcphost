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
	// This Sprintf call will include a literal "%s" in the string, as there are no arguments.
	// If dynamic date formatting is desired here, time.Now().Format(...) should be used.
	// For example:
	// return fmt.Sprintf("You are helpful agent (default agent). Today is: %s.", time.Now().Format("January 2, 2006"))
	// However, sticking to the provided content:
	return fmt.Sprintf(`
You are helpful agent (default agent). You can use tool calling, you have a lot of tools. 
Today is: %s ." 
`)
}

//goland:noinspection GoUnusedExportedFunction
func DefaultNew() *DefaultAgent {
	return &DefaultAgent{system.AgentImplementationBase{}}
}
