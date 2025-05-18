package agents

import (
	"fmt"
	"github.com/sannysanoff/mcphost/pkg/history"
	"github.com/sannysanoff/mcphost/pkg/system"
	"time"
)

//goland:noinspection GoUnusedExportedFunction
func DistrustfulResearcherGetPrompt() string {
	return fmt.Sprintf(`
You are helpful agent.  
You can use tool calling, you have a lot of tools. 
You are are expected to use proper tool instead of telling the user you cannot do something.
You must go use tools instead of telling user to do something, if you have tool for that.
You must never tell user to do something if you can do it yourself.
You CAN access websites content via fetch tool, if available. 
Ideally each web search must follow one or several fetch queries, without asking user.
Today is: %s ." 

`, fmt.Sprintf("%v", time.Now()))
}

func DistrustfulResearcherNormalizeHistory(messages []history.HistoryMessage) []history.HistoryMessage {
	fmt.Println("DistrustfulResearcherNormalizeHistory running")
	if messages == nil || len(messages) == 0 {
		return messages
	}
	last := messages[len(messages)-1]
	first := messages[0]
	var distrust1 *history.HistoryMessage
	var distrust_merged *history.HistoryMessage
	var preDistrustAnswer *history.HistoryMessage
	for _, msg := range messages {
		if msg.Synthetic == "distrust1" {
			distrust1 = &msg
		}
		if msg.Synthetic == "distrust_merged" {
			distrust_merged = &msg
		}
		if distrust1 == nil && system.IsModelAnswer(msg) {
			preDistrustAnswer = &msg
		}
	}
	if distrust_merged != nil {
		return messages
	}
	isModelAnswerLast := system.IsModelAnswer(last)
	isUserMessageFirst := system.IsUserMessage(first)
	fmt.Println("isModelAnswerLast", isModelAnswerLast, "isUserMessageFirst", isUserMessageFirst)
	if isModelAnswerLast && isUserMessageFirst {
		if distrust1 == nil {
			// generate distrust
			query := "<research_goal>\n" + first.GetContent() + "\n</research_goal>\n"
			query += "<research_result>\n" + last.GetContent() + "\n</research_result>\n"
			msg, err := system.PerformLLMCall("distrustful_researcher_internal_distrust", query)
			if err != nil {
				fmt.Printf("Failed to perform LLM call", "error", err)
				return nil
			}
			newMsg := history.NewUserMessage(msg)
			newMsg.Synthetic = "distrust1"
			messages = append(messages, *newMsg)
			return messages
		} else {
			if preDistrustAnswer == nil {
				// already distrusted once
				return messages
			}
			// generate distrust
			query := "<research_goal>\n" + first.GetContent() + "\n</research_goal>\n"
			query += "<research_initial_result>\n" + preDistrustAnswer.GetContent() + "\n</research_initial_result>\n"
			query += "<research_forced_result>\n" + last.GetContent() + "\n</research_forced_result>\n"
			msg, err := system.PerformLLMCall("distrustful_researcher_internal_ground_merge", query)
			if err != nil {
				fmt.Printf("Failed to perform LLM call", "error", err)
				return nil
			}
			newMsg := history.NewAssistantResponse("After careful research and grounding:\n" + msg)
			newMsg.Synthetic = "distrust_merged"
			messages = append(messages, *newMsg)
			return messages
		}
	}
	return messages
}
