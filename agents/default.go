package agents

import (
	"fmt"
	"github.com/sannysanoff/mcphost/pkg/history"
	"time"
)

//goland:noinspection GoUnusedExportedFunction
func DefaultGetPrompt() string {
	return fmt.Sprintf(`
You are helpful agent (default agent). You can use tool calling, you have a lot of tools. 
Today is: %s ." 

`, fmt.Sprintf("%v", time.Now()))
}

func DefaultNormalizeHistory(messages []history.HistoryMessage) []history.HistoryMessage {
	return messages
}
