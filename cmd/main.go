package main

import (
	"github.com/sannysanoff/mcphost/pkg/mcp"
)

var version = "dev"

//go:generate yaegi github.com/sannysanoff/mcphost/pkg/system github.com/sannysanoff/mcphost/pkg/history

func main() {
	mcp.Execute()
}
