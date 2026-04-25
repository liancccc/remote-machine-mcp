package tools

import (
	"strings"

	"remote-machine-mcp/internal/filesystem"
	"remote-machine-mcp/internal/patch"
)

type ApplyPatch struct{ guard *filesystem.Guard }

func (ApplyPatch) Name() string { return "apply_patch" }
func (ApplyPatch) Description() string {
	return "Apply a Codex-style patch block. Accepts either `patch` or Codex JSON-tool-compatible `input`."
}
func (ApplyPatch) InputSchema() map[string]any {
	return objectSchema(map[string]any{
		"patch":   stringSchema("Patch beginning with *** Begin Patch and ending with *** End Patch."),
		"input":   stringSchema("Codex-compatible alias for patch."),
		"workdir": stringSchema("Base directory for relative paths. Defaults to the server default workdir."),
	}, []string{})
}
func (t ApplyPatch) Call(args map[string]any) (string, any, error) {
	input := stringArg(args, "patch", "")
	if input == "" {
		input = stringArg(args, "input", "")
	}
	if input == "" {
		return "", nil, errText("patch or input is required")
	}
	wd, err := t.guard.ResolveDir(stringArg(args, "workdir", ""))
	if err != nil {
		return "", nil, err
	}
	affected, err := patch.Apply(input, wd, t.guard.Resolve)
	if err != nil {
		return "", map[string]any{"affected": affected}, err
	}
	lines := append([]string{"Success. Updated the following files:"}, affected...)
	return strings.Join(lines, "\n") + "\n", map[string]any{"affected": affected}, nil
}

type errText string

func (e errText) Error() string { return string(e) }
