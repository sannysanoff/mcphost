package main

import (
	"github.com/mark3labs/mcphost/pkg/mcp"
)

var version = "dev"

//go:generate yaegi extract ./system

func main() {
	mcp.Execute()
}
