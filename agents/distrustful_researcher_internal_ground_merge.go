package agents

import (
	"fmt"
	"github.com/sannysanoff/mcphost/pkg/system"
	"time"
)

// DistrustfulResearcherInternalGroundMergeAgent defines the agent structure.
type DistrustfulResearcherInternalGroundMergeAgent struct {
	system.AgentImplementationBase
}

// DistrustfulResearcherInternalGroundMergeNew creates a new DistrustfulResearcherInternalGroundMergeAgent.
//goland:noinspection GoUnusedExportedFunction
func DistrustfulResearcherInternalGroundMergeNew() *DistrustfulResearcherInternalGroundMergeAgent {
	return &DistrustfulResearcherInternalGroundMergeAgent{system.AgentImplementationBase{}}
}

// GetSystemPrompt returns the system prompt for the DistrustfulResearcherInternalGroundMergeAgent.
func (a *DistrustfulResearcherInternalGroundMergeAgent) GetSystemPrompt() string {
	return fmt.Sprintf(`
You are balanced research reviewer. There are two research results.
One is done quickly, and incomplete.
Another is done thoroughly, but may be non-factual.
Please perform additional research online, check if some facts has been made up,
and combine the results from both researches.

<example>
user:
<research_goal>when was last time we saw XXX</research_goal>
<research_initial_result>last summer</research_initial_result>
<research_forced_result>last autumn</research_forced_result>
(assistant performs additional web search using tools on both facts and leaves final verdict, without losing details)
assistant output: grounded verification did not find any proof about XXX was observed last autumn, so
most likely he was observed last summer.

</example>

<example>
<research_goal>did ever morra smile in moomin series, and where</research_goal>
<research_initial_result>no, she did not smile</research_initial_result>
<research_forced_result>yes, she did smile when she got hobgobiln's magic hat</research_forced_result>
(assistant performs additional web search using tools on both facts and leaves final verdict, without losing details)
assistant output:in fact, morra did smile  when she got hobgobiln's magic hat, it happened in book "hobgobiln's hat".
</example>

Today is: %s ." 

`, fmt.Sprintf("%v", time.Now()))
}

// DistrustfulResearcherInternalGroundMergeImplementation returns the system.AgentImplementation for this agent.
func DistrustfulResearcherInternalGroundMergeImplementation() system.AgentImplementation {
	agent := DistrustfulResearcherInternalGroundMergeNew()
	return system.AgentImplementation{
		AgentData:               nil,
		GetPrompt:               agent.GetSystemPrompt,
		DefaultNormalizeHistory: agent.NormalizeHistory, // Inherited from AgentImplementationBase
	}
}
