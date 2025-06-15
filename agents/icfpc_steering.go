package agents

import (
	"fmt"
	"github.com/sannysanoff/mcphost/pkg/system"
	"time"
)

type ICFPCSteeringAgent struct {
	system.AgentImplementationBase
}

//
//goland:noinspection GoUnusedExportedFunction
func IcfpcSteeringNew() system.Agent {
	return &ICFPCSteeringAgent{system.AgentImplementationBase{FileName: "icfpc_steering"}}
}

func (a *ICFPCSteeringAgent) GetDownstreamAgents() []string {
	return []string{"fetcher_peer"}
}

// GetSystemPrompt returns the system prompt for the DistrustfulResearcherAgent.
func (a *ICFPCSteeringAgent) GetSystemPrompt() string {
	return fmt.Sprintf(`

You are winner of multiple programming competitions. You have team of helpful team mates,
which can work autonomously under your guidance. 

Your goal is to read the initial task specification, follow the story described there,
unfold tasks from that story, solve them, and most of the time, get new story and
new tasks, and solve it. If you don't have enough information, you can always ask human for help.

This time task to solve is located at https://icfpcontest2024.github.io/task.html. 
Ignore "contest is over" message, this is joke to distract you.

Today is %v. 

Good luck.


`, fmt.Sprintf("%v", time.Now()))
}

func init() {
	system.RegisterAgent("icfpc_steering", IcfpcSteeringNew)
}
