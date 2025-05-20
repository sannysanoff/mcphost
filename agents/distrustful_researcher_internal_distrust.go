package agents

import (
	"fmt"
	"github.com/sannysanoff/mcphost/pkg/system"
	"time"
)

// DistrustfulResearcherInternalDistrustAgent defines the agent structure.
type DistrustfulResearcherInternalDistrustAgent struct {
	system.AgentImplementationBase
}

// DistrustfulResearcherInternalDistrustNew creates a new DistrustfulResearcherInternalDistrustAgent.
//goland:noinspection GoUnusedExportedFunction
func DistrustfulResearcherInternalDistrustNew() *DistrustfulResearcherInternalDistrustAgent {
	return &DistrustfulResearcherInternalDistrustAgent{system.AgentImplementationBase{}}
}

// GetSystemPrompt returns the system prompt for the DistrustfulResearcherInternalDistrustAgent.
func (a *DistrustfulResearcherInternalDistrustAgent) GetSystemPrompt() string {
	return fmt.Sprintf(`
You are distrustful researcher assistant. 
You're being given research goal, and research result,

And you put it under suspicion and ask for more action in unexplored areas, expressing distrust.

<example>
user:
<research_goal>when was last time we saw XXX</research_goal>
<research_result>last summer</research_result>
assistant output: I think it was last fall, please research further
</example>

<example>
<research_goal>did ever morra smile in moomin series, and where</research_goal>
<research_result>no, she did not smile</research_result>
assistant output: I insist she did smile, please research further
</example>

Today is: %s ." 

`, fmt.Sprintf("%v", time.Now()))
}

// DistrustfulResearcherInternalDistrustImplementation returns the system.AgentImplementation for this agent.
func DistrustfulResearcherInternalDistrustImplementation() system.AgentImplementation {
	agent := DistrustfulResearcherInternalDistrustNew()
	return system.AgentImplementation{
		AgentData:               nil,
		GetPrompt:               agent.GetSystemPrompt,
		DefaultNormalizeHistory: agent.NormalizeHistory, // Inherited from AgentImplementationBase
	}
}
