package agents

import (
	"fmt"
	"time"
)

//goland:noinspection GoUnusedExportedFunction
func Agent1GetPrompt() string {
	return "You are helpful agent1. Today is: " + fmt.Sprintf("%v", time.Now())
}
