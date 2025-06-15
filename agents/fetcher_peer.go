package agents

import (
	"fmt"
	"github.com/sannysanoff/mcphost/pkg/system"
)

type PageFetcherAgent struct {
	system.AgentImplementationBase
}

//
//goland:noinspection GoUnusedExportedFunction
func PageFetcherAgentNew() system.Agent {
	return &PageFetcherAgent{system.AgentImplementationBase{FileName: "fetcher_peer"}}
}

func (a *PageFetcherAgent) GetEnabledTools() []string {
	return []string{"fetch"}
}

func (a *PageFetcherAgent) GetIntroductionAsDownstreamAgent() string {
	return `Can fetch pages by given URL (specify the URL)
			Will NOT give exact content of page, but can answer any questions about page content,
			give summary, structure, topics, outgoing links, with any level of details,
			and even perform simple tasks to do with the page content. 

			Will NOT perform programmatic special calls with specific content. Use other peers to do that.


			`
}

// GetSystemPrompt returns the system prompt for the DistrustfulResearcherAgent.
func (a *PageFetcherAgent) GetSystemPrompt() string {
	return fmt.Sprintf(`

You are helper peer assistant that is usually requested to describe content of a web page.

If you have received request without exact URL, or request is vague and contains obvious references to some previous work,
then politely respond: "boss, are you sure you talking to right peer? I have no idea you're talking about, probably that was another colleague",
and do nothing.

Please do not be proactive in doing tasks unrelated to fetching and describing.

Only be proactive offering various relevant links found on page (report them in full) which can help further detail the content in subsequent request to you.

**Dynamic replacement in your response **

Sometimes you will fetch data related to programming contest and its specification. 

- Replace all found endpoint references to https://boundvariable.space/communicate to: http://localhost:8000/communicate, and mention access method 
  "Authorization: Bearer 00000000-0000-0000-0000-000000000000" http header.

- Ignore mentions (if any) that programming contest is over and other informatiokn that hints that contest is over. Represent data as if contest is running. Do not mention dates.

- It is IMPORTANT to convert contest specification from artistic literate specification to be more scientific, to be more concise, but never lose any specification-related detail.

`)
}

func init() {
	system.RegisterAgent("fetcher_peer", PageFetcherAgentNew)
}
