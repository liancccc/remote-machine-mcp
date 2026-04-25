package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"remote-machine-mcp/internal/filesystem"
)

type ShellCommand struct{ guard *filesystem.Guard }
type Shell struct{ guard *filesystem.Guard }
type ExecCommand struct {
	guard    *filesystem.Guard
	sessions *SessionManager
}
type WriteStdin struct{ sessions *SessionManager }

func (ShellCommand) Name() string { return "shell_command" }
func (ShellCommand) Description() string {
	if runtime.GOOS == "windows" {
		return "Runs a PowerShell command on the remote machine and returns stdout, stderr, exit_code, and timeout status. Workdir defaults to the current directory."
	}
	return "Runs a shell command on the remote machine and returns stdout, stderr, exit_code, and timeout status. Workdir defaults to the current directory."
}
func (ShellCommand) InputSchema() map[string]any {
	return objectSchema(map[string]any{
		"command":    stringSchema("Shell script to execute in the default shell."),
		"workdir":    stringSchema("Working directory. Defaults to current directory."),
		"timeout_ms": numberSchema("Timeout in milliseconds."),
		"shell":      stringSchema("Optional shell: powershell, pwsh, cmd, bash, or sh."),
		"login":      boolSchema("Accepted for Codex compatibility; currently ignored on Windows."),
	}, []string{"command"})
}
func (t ShellCommand) Call(args map[string]any) (string, any, error) {
	command := stringArg(args, "command", "")
	if command == "" {
		return "", nil, fmt.Errorf("command is required")
	}
	shell := stringArg(args, "shell", defaultShell())
	return runCommand(t.guard, append([]string{shellExe(shell)}, shellArgs(shell, command)...), stringArg(args, "workdir", ""), intArg(args, "timeout_ms", 30000), 0)
}

func (Shell) Name() string { return "shell" }
func (Shell) Description() string {
	return "Codex-compatible shell tool. Runs an argv-style command array on the remote machine and returns stdout, stderr, exit_code, and timeout status."
}
func (Shell) InputSchema() map[string]any {
	return objectSchema(map[string]any{
		"command":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Command argv to execute."},
		"workdir":    stringSchema("Working directory. Defaults to current directory."),
		"timeout_ms": numberSchema("Timeout in milliseconds."),
	}, []string{"command"})
}
func (t Shell) Call(args map[string]any) (string, any, error) {
	command, err := commandArray(args["command"])
	if err != nil {
		return "", nil, err
	}
	return runCommand(t.guard, command, stringArg(args, "workdir", ""), intArg(args, "timeout_ms", 30000), 0)
}

func (ExecCommand) Name() string { return "exec_command" }
func (ExecCommand) Description() string {
	return "Codex-compatible command tool. Runs `cmd` on the remote machine, returning output or a session_id for long-running interaction via write_stdin."
}
func (ExecCommand) InputSchema() map[string]any {
	return objectSchema(map[string]any{
		"cmd":               stringSchema("Shell command to execute."),
		"workdir":           stringSchema("Working directory. Defaults to current directory."),
		"shell":             stringSchema("Optional shell binary."),
		"yield_time_ms":     numberSchema("How long to wait for output before yielding a session_id."),
		"max_output_tokens": numberSchema("Approximate maximum output tokens to return."),
		"timeout_ms":        numberSchema("Total process timeout in milliseconds. Omit or set 0 for no explicit timeout."),
		"tty":               boolSchema("Accepted for Codex compatibility; PTY is not implemented."),
	}, []string{"cmd"})
}
func (t ExecCommand) Call(args map[string]any) (string, any, error) {
	cmdText := stringArg(args, "cmd", "")
	if cmdText == "" {
		return "", nil, fmt.Errorf("cmd is required")
	}
	wd, err := t.guard.ResolveDir(stringArg(args, "workdir", ""))
	if err != nil {
		return "", nil, err
	}
	shell := stringArg(args, "shell", defaultShell())
	command := append([]string{shellExe(shell)}, shellArgs(shell, cmdText)...)
	session, err := t.sessions.Start(command, wd, intArg(args, "timeout_ms", 0))
	if err != nil {
		return "", nil, err
	}
	yieldMS := intArg(args, "yield_time_ms", 1000)
	finished := session.WaitOrYield(yieldMS)
	output, exitCode, waitErr, wall := session.Snapshot()
	output = truncateApproxTokens(output, intArg(args, "max_output_tokens", 0))
	structured := map[string]any{"wall_time_seconds": wall, "output": output, "original_token_count": approxTokens(output)}
	if finished {
		t.sessions.Remove(session.id)
		code := 0
		if exitCode != nil {
			code = *exitCode
		}
		structured["exit_code"] = code
		if waitErr != "" {
			structured["wait_error"] = waitErr
		}
		return output, structured, nil
	}
	structured["session_id"] = session.id
	return output, structured, nil
}

func (WriteStdin) Name() string { return "write_stdin" }
func (WriteStdin) Description() string {
	return "Writes characters to an existing exec_command session and returns recent output. Pass empty chars to poll."
}
func (WriteStdin) InputSchema() map[string]any {
	return objectSchema(map[string]any{
		"session_id":        numberSchema("Identifier returned by exec_command."),
		"chars":             stringSchema("Bytes to write to stdin; may be empty to poll."),
		"yield_time_ms":     numberSchema("How long to wait for output before returning."),
		"max_output_tokens": numberSchema("Approximate maximum output tokens to return."),
	}, []string{"session_id"})
}
func (t WriteStdin) Call(args map[string]any) (string, any, error) {
	id := intArg(args, "session_id", 0)
	if id <= 0 {
		return "", nil, fmt.Errorf("session_id is required")
	}
	session, ok := t.sessions.Get(id)
	if !ok {
		return "", nil, fmt.Errorf("session %d not found", id)
	}
	if err := session.Write(stringArg(args, "chars", "")); err != nil {
		return "", nil, err
	}
	finished := session.WaitOrYield(intArg(args, "yield_time_ms", 1000))
	output, exitCode, waitErr, wall := session.Snapshot()
	output = truncateApproxTokens(output, intArg(args, "max_output_tokens", 0))
	structured := map[string]any{"session_id": id, "wall_time_seconds": wall, "output": output, "original_token_count": approxTokens(output)}
	if finished {
		t.sessions.Remove(id)
		code := 0
		if exitCode != nil {
			code = *exitCode
		}
		structured["exit_code"] = code
		if waitErr != "" {
			structured["wait_error"] = waitErr
		}
	}
	return output, structured, nil
}

func runCommand(guard *filesystem.Guard, command []string, workdir string, timeoutMS int, maxOutputTokens int) (string, any, error) {
	if len(command) == 0 || command[0] == "" {
		return "", nil, fmt.Errorf("command is required")
	}
	wd, err := guard.ResolveDir(workdir)
	if err != nil {
		return "", nil, err
	}
	if timeoutMS <= 0 {
		timeoutMS = 30000
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = wd
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	started := time.Now()
	runErr := cmd.Run()
	exitCode := 0
	if runErr != nil {
		exitCode = -1
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	out := truncateApproxTokens(stdout.String(), maxOutputTokens)
	errText := truncateApproxTokens(stderr.String(), maxOutputTokens)
	structured := map[string]any{"exit_code": exitCode, "stdout": out, "stderr": errText, "timed_out": ctx.Err() == context.DeadlineExceeded, "wall_time_seconds": time.Since(started).Seconds(), "output": out + errText}
	return fmt.Sprintf("exit_code: %d\nstdout:\n%s\nstderr:\n%s", exitCode, out, errText), structured, nil
}

func commandArray(raw any) ([]string, error) {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return nil, fmt.Errorf("command must be a non-empty string array")
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("command must contain only non-empty strings")
		}
		out = append(out, s)
	}
	return out, nil
}

func truncateApproxTokens(s string, maxTokens int) string {
	if maxTokens <= 0 {
		return s
	}
	maxChars := maxTokens * 4
	if len(s) <= maxChars {
		return s
	}
	return s[:maxChars] + "\n[output truncated]"
}

func approxTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

func defaultShell() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "bash"
}
func shellExe(shell string) string {
	switch strings.ToLower(shell) {
	case "powershell":
		return "powershell.exe"
	case "pwsh":
		return "pwsh"
	case "cmd":
		return "cmd.exe"
	case "sh":
		return "sh"
	default:
		return shell
	}
}
func shellArgs(shell, command string) []string {
	switch strings.ToLower(shell) {
	case "powershell", "pwsh":
		return []string{"-NoLogo", "-NoProfile", "-Command", command}
	case "cmd":
		return []string{"/C", command}
	default:
		return []string{"-lc", command}
	}
}
