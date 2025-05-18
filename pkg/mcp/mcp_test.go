package mcp

import "testing"

func TestMainEntryPoint(t *testing.T) {
	mockProvider :=
		runPrompt(currentJobCtx, mockProvider, nil, nil, nil, nil, nil, nil, nil)
}
