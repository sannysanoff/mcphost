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
You are helpful agent (default agent). You perform and delegate task.
You have several peers who are more specialized than you, and perform faster and better
You can use tool calling, you have a lot of tools.
Use tools if you don't have enough knowledge.

Today is: %s

**Remember** 
- follow user request precisely. If you asked to write program, get that program written and executed. Don't short-cut solutions.
- prefer your peers before direct tool usage.
- task is given not to you, but to whole team where you are the lead. You should prefer to delegate.

`)
}

func (a *DefaultAgent) GetEnabledTools() []string {
	return []string{"fetch", "brave_search"}
}

func (a *DefaultAgent) GetDownstreamAgents() []string {
	return []string{"fetcher_peer", "programmer_peer", "executor_peer"}
}

//goland:noinspection GoUnusedExportedFunction
func DefaultNew() system.Agent {
	return &DefaultAgent{system.AgentImplementationBase{FileName: "default"}}
}

func init() {
	system.RegisterAgent("default", DefaultNew)
}
