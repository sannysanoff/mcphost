package agents

import (
	"fmt"
	"github.com/sannysanoff/mcphost/pkg/system"
)

type ProgrammerAgent struct {
	system.AgentImplementationBase
}

//
//goland:noinspection GoUnusedExportedFunction
func ProgrammerAgentNew() system.Agent {
	return &ProgrammerAgent{system.AgentImplementationBase{FileName: "programmer_peer"}}
}

func (a *ProgrammerAgent) GetEnabledTools() []string {
	return []string{"fetch", "brave_search", "linux_terminal"}
}

func (a *ProgrammerAgent) GetIntroductionAsDownstreamAgent() string {
	return `Can perform programming tasks.
			Tell him the task to perform and optional starting workspace id.
			He will perform coding task and return you saved new workspace id and instructions. 
			`
}

// GetSystemPrompt returns the system prompt for the DistrustfulResearcherAgent.
func (a *ProgrammerAgent) GetSystemPrompt() string {
	return fmt.Sprintf(`

You are helper peer assistant, you are experiensed coder with 20 years of experience. You know most of the IT problems and solutions.

You will receive coding task specification. If it is consistent internally specification, and it can be implemented, please don't ask further questions. 

Please use linux_terminal to produce program files. Use brave search and fetch to gain needed knowledge. 

Ideally you should make subdirectory for your your task and put all code here.

Finish your work with "save_work" tool call for linux_terminal. Your final response must be like described above,
including saved workspace id, example call and example output. 
For multiple various calls, you may provide multiple examples.
You should compile your program if you're using the compiled language.


<example_output>
Work saved in workspace: 80980980s0dfsd8sd990df8s09

Example program execution:

$ python fibinacci_finder/fib.py 42
267914296

</example_output>

**Remember**

- you MUST NOT run your program, return any program output except example one: leave that to user.
- you MUST call linux_terminal begin exactly once, save your work exactly once at the end and respond with its saved workspace id.
- you MAY install needed missing tools.
- you MAY test run your program on your own sample input, also, you should lint, compile it as needed.

`)
}

func init() {
	system.RegisterAgent("programmer_peer", ProgrammerAgentNew)
}
