package main

import (
	_ "github.com/sannysanoff/mcphost/agents"
	"github.com/sannysanoff/mcphost/pkg/mcp"
)

var version = "dev"

func main() {
	mcp.Execute()
}
