# remote-machine-mcp

Operate a remote Linux or Windows machine over MCP HTTP.

## Tools

| Tool | Description |
| --- | --- |
| `list_dir` | List directory entries in a Codex-compatible numbered format. |
| `list_files` | List files under an allowed root. |
| `read_file` | Read a file under an allowed root. |
| `write_file` | Create or overwrite a file under an allowed root. |
| `edit_file` | Replace exact text in a file under an allowed root. |
| `copy` | Copy a file or directory between allowed paths. |
| `move` | Move or rename a file or directory between allowed paths. |
| `view_image` | Read an image and return it as a data URL. |
| `shell_command` | Run a shell script and return output, exit code, and timeout state. |
| `shell` | Run an argv-style command. |
| `exec_command` | Start a long-running command and return output or a `session_id`. |
| `write_stdin` | Write to an existing session and poll for more output. |
| `apply_patch` | Apply a Codex-style patch. |

## Usage

```bash
sudo curl -fL -o remote-machine-mcp https://github.com/liancccc/remote-machine-mcp/releases/latest/download/remote-machine-mcp-linux-amd64
sudo chmod +x remote-machine-mcp
./remote-machine-mcp --listen 0.0.0.0 --port 8765 --vps YOUR_PUBLIC_IP
```

HTTP mode always requires a bearer token. Pass `--token YOUR_TOKEN`, or omit it and use the generated token printed in the startup banner.
