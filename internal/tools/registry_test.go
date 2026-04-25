package tools

import (
	"runtime"
	"strings"
	"testing"

	"remote-machine-mcp/internal/filesystem"
)

func TestRegistryToolDescriptionsIncludeRemoteContext(t *testing.T) {
	guard := &filesystem.Guard{Roots: []string{"/remote/root"}, DefaultRoot: "/remote/root"}
	registry := NewRegistry(guard, NewTransferManager())

	for _, tool := range registry.Tools() {
		description := tool.Description()
		for _, want := range []string{
			"REMOTE CONTEXT",
			"not the local client",
			runtime.GOOS + "/" + runtime.GOARCH,
			`Default workdir is "/remote/root"`,
		} {
			if !strings.Contains(description, want) {
				t.Fatalf("%s description missing %q:\n%s", tool.Name(), want, description)
			}
		}
		if strings.Contains(description, "allowed roots") {
			t.Fatalf("%s description should not mention allowed roots anymore:\n%s", tool.Name(), description)
		}
	}
}

func TestRegistryServerInstructionsIncludeRemoteContext(t *testing.T) {
	guard := &filesystem.Guard{Roots: []string{"/remote/root"}, DefaultRoot: "/remote/root", HomeDir: "/home/tester"}
	registry := NewRegistry(guard, NewTransferManager())
	text := registry.ServerInstructions()

	for _, want := range []string{
		"remote machine MCP server",
		"not the user's local machine",
		runtime.GOOS,
		runtime.GOARCH,
		`default_workdir="/remote/root"`,
		`home="/home/tester"`,
		"path_separator",
		"default_shell",
		"/transfer",
		"prefer those endpoints for bulk bytes",
		"All shell commands and file paths refer to the remote machine",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("server instructions missing %q:\n%s", want, text)
		}
	}
}
