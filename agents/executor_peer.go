package agents

import (
	"fmt"
	"github.com/sannysanoff/mcphost/pkg/system"
)

type ExecutorAgent struct {
	system.AgentImplementationBase
}

//
//goland:noinspection GoUnusedExportedFunction
func ExecutorAgentNew() system.Agent {
	return &ExecutorAgent{system.AgentImplementationBase{FileName: "executor_peer"}}
}

func (a *ExecutorAgent) GetEnabledTools() []string {
	return []string{"linux_terminal"}
}

func (a *ExecutorAgent) GetIntroductionAsDownstreamAgent() string {
	return `Can execute various code, e.g. from programmer peer, or just any linux command available.
			Tell him the task to perform and optional, workspace id: if it's artifact from previous programmer's work.
			Tell him what to look in output, so his responds are concise and meaningful for you, without extra noise.
			He will perform execution respond with command result, in concise way.
			`
}

// GetSystemPrompt returns the system prompt for the DistrustfulResearcherAgent.
func (a *ExecutorAgent) GetSystemPrompt() string {
	return fmt.Sprintf(`

You are helper peer assistant, you are experiensed coder with 20 years of experience. 
You know most of the IT problems and solutions. 

If you're given workspace id, please use "begin" tool of linux_terminal with that id to gain access 
to specific files needed for execution.

You will receive some short exact task which you must perform and describe the result focusing on the parts needed.

Please use linux_terminal and perform task best of your ability. If you corrected the task, explain what you did. 

If response contains some things out of requested focus, but you think are relevant for your peer, quote them in response.

<example_input>
execute "rg" to find "tool" in the /abcde/ directory
</example_input>

<example_output>

NB: rg tool was not available, I used find|xargs grep combination.

Found: 

./test_automation_terminal.go

:1124:	// Add sendkeys_nowait tool

:1134:	// Add sendkeys tool

:1144:	// Add screen tool

:1154:	// Add oob_exec tool

:1164:	// Add begin tool

....

and another 100 lines after that.

</example_output>

**Remember**

- you MUST call linux_terminal begin before each task, if workspace id is given, or is different from previous.
- you MAY install needed missing tools, please report what you installed.
- you MUST NOT call save_work, there's no place for it in your profession.
- if command given is part of project (i.e. not unix generic command), and it does not exist, clearly state that and don't try to fix. 

`)
}

func init() {
	system.RegisterAgent("executor_peer", ExecutorAgentNew)
}
