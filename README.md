# remote-machine-mcp

Operate a remote Linux or Windows machine over MCP HTTP.

## Tools

| Tool | Description |
| --- | --- |
| `shell_command` | Run a shell script and return output, exit code, and timeout state. |
| `shell` | Run an argv-style command. |
| `exec_command` | Start a long-running command and return output or a `session_id`. |
| `write_stdin` | Write to an existing session and poll for more output. |
| `apply_patch` | Apply a Codex-style patch. |
| `prepare_upload` | Create a resumable upload session. |
| `prepare_download` | Create a resumable download session. |

## Usage

```bash
sudo curl -fL -o remote-machine-mcp https://github.com/liancccc/remote-machine-mcp/releases/latest/download/remote-machine-mcp-linux-amd64
sudo chmod +x remote-machine-mcp
./remote-machine-mcp --listen 0.0.0.0 --port 8765 --vps YOUR_PUBLIC_IP
```

