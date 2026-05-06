package tools

import (
	"fmt"
	"os"
	"runtime"
	"sort"

	"remote-machine-mcp/internal/filesystem"
	"remote-machine-mcp/internal/mcp"
)

type Registry struct {
	guard *filesystem.Guard
	tools map[string]mcp.Tool
}

type remoteContextTool struct {
	tool   mcp.Tool
	prefix string
}

func NewRegistry(guard *filesystem.Guard) *Registry {
	sessions := NewSessionManager()
	r := &Registry{guard: guard, tools: map[string]mcp.Tool{}}
	prefix := remoteToolDescriptionPrefix(guard)
	for _, tool := range []mcp.Tool{
		ListDir{guard: guard},
		ListFiles{guard: guard},
		ReadFile{guard: guard},
		WriteFile{guard: guard},
		EditFile{guard: guard},
		CopyPath{guard: guard},
		MovePath{guard: guard},
		ViewImage{guard: guard},
		ShellCommand{guard: guard},
		Shell{guard: guard},
		ExecCommand{guard: guard, sessions: sessions},
		WriteStdin{sessions: sessions},
		ApplyPatch{guard: guard},
	} {
		r.tools[tool.Name()] = remoteContextTool{tool: tool, prefix: prefix}
	}
	return r
}

func (r *Registry) Tools() []mcp.Tool {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]mcp.Tool, 0, len(names))
	for _, name := range names {
		out = append(out, r.tools[name])
	}
	return out
}

func (r *Registry) Call(name string, args map[string]any) (string, any, error) {
	tool, ok := r.tools[name]
	if !ok {
		return "", nil, fmt.Errorf("unknown tool %q", name)
	}
	if args == nil {
		args = map[string]any{}
	}
	return tool.Call(args)
}

func (r *Registry) ServerInstructions() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = os.Getenv("ComSpec")
	}
	if shell == "" {
		shell = defaultShell()
	}
	hostname, _ := os.Hostname()
	return fmt.Sprintf(
		"You are connected to a remote machine MCP server, not the user's local machine. Remote host=%q os=%s arch=%s pwd=%q home=%q path_separator=%q path_list_separator=%q default_shell=%q. All shell commands and file paths refer to the remote machine. Use the built-in file tools for listing, reading, writing, editing, copying, moving, and viewing files inside allowed roots. If workdir is omitted, commands run in the current directory shown above.",
		hostname,
		runtime.GOOS,
		runtime.GOARCH,
		r.guard.CurrentDir,
		r.guard.HomeDir,
		string(os.PathSeparator),
		string(os.PathListSeparator),
		shell,
	)
}

func (t remoteContextTool) Name() string                { return t.tool.Name() }
func (t remoteContextTool) InputSchema() map[string]any { return t.tool.InputSchema() }
func (t remoteContextTool) Call(args map[string]any) (string, any, error) {
	return t.tool.Call(args)
}
func (t remoteContextTool) Description() string {
	return t.prefix + " " + t.tool.Description()
}

func remoteToolDescriptionPrefix(guard *filesystem.Guard) string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = os.Getenv("ComSpec")
	}
	if shell == "" {
		shell = defaultShell()
	}
	return fmt.Sprintf(
		"REMOTE CONTEXT: runs on the remote %s/%s machine, not the local client. Current directory is %q; path separator is %q; default shell is %q. Use remote paths/commands for this OS.",
		runtime.GOOS,
		runtime.GOARCH,
		guard.CurrentDir,
		string(os.PathSeparator),
		shell,
	)
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}
func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}
func numberSchema(description string) map[string]any {
	return map[string]any{"type": "number", "description": description}
}
func boolSchema(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}
func stringArg(m map[string]any, key, fallback string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return fallback
}
func boolArg(m map[string]any, key string, fallback bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return fallback
}
func intArg(m map[string]any, key string, fallback int) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	if v, ok := m[key].(int); ok {
		return v
	}
	return fallback
}
