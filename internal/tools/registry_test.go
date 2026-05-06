package tools

import (
	"runtime"
	"slices"
	"strings"
	"testing"

	"remote-machine-mcp/internal/filesystem"
)

func TestRegistryToolDescriptionsIncludeRemoteContext(t *testing.T) {
	guard := &filesystem.Guard{CurrentDir: "/remote/pwd"}
	registry := NewRegistry(guard)

	for _, tool := range registry.Tools() {
		description := tool.Description()
		for _, want := range []string{
			"REMOTE CONTEXT",
			"not the local client",
			runtime.GOOS + "/" + runtime.GOARCH,
			`Current directory is "/remote/pwd"`,
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
	guard := &filesystem.Guard{CurrentDir: "/remote/pwd", HomeDir: "/home/tester"}
	registry := NewRegistry(guard)
	text := registry.ServerInstructions()

	for _, want := range []string{
		"remote machine MCP server",
		"not the user's local machine",
		runtime.GOOS,
		runtime.GOARCH,
		`pwd="/remote/pwd"`,
		`home="/home/tester"`,
		"path_separator",
		"default_shell",
		"All shell commands and file paths refer to the remote machine",
		"listing, reading, writing, editing, copying, moving, and viewing files",
		"If workdir is omitted, commands run in the current directory shown above",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("server instructions missing %q:\n%s", want, text)
		}
	}
}

func TestRegistryIncludesFileAndShellTools(t *testing.T) {
	guard := &filesystem.Guard{CurrentDir: "/remote/pwd"}
	registry := NewRegistry(guard)

	var names []string
	for _, tool := range registry.Tools() {
		names = append(names, tool.Name())
	}

	for _, want := range []string{
		"list_dir",
		"list_files",
		"read_file",
		"write_file",
		"edit_file",
		"copy",
		"move",
		"view_image",
		"shell_command",
		"shell",
		"exec_command",
		"write_stdin",
		"apply_patch",
	} {
		if !slices.Contains(names, want) {
			t.Fatalf("registry missing tool %q: %v", want, names)
		}
	}
}
