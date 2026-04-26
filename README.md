# remote-machine-mcp

`remote-machine-mcp` lets a local MCP client operate a remote Linux or Windows machine over `stdio` or HTTP.

It is meant to be a small remote execution bridge: shell, long-running sessions, patching, and file transfer helpers.

Repository:

- `https://github.com/liancccc/remote-machine-mcp`

## Deploy

GitHub Releases should publish raw executable files directly, not compressed archives.

Examples:

### Linux amd64

```bash
sudo mkdir -p /opt/remote-machine-mcp
cd /opt/remote-machine-mcp
sudo curl -fL -o remote-machine-mcp \
  https://github.com/liancccc/remote-machine-mcp/releases/latest/download/remote-machine-mcp-linux-amd64
sudo chmod +x remote-machine-mcp
```

### Windows amd64

```powershell
New-Item -ItemType Directory -Force C:\remote-machine-mcp | Out-Null
Set-Location C:\remote-machine-mcp
curl.exe -fL -o remote-machine-mcp.exe `
  https://github.com/liancccc/remote-machine-mcp/releases/latest/download/remote-machine-mcp-windows-amd64.exe
```

## Run

### HTTP mode

Linux example:

```bash
cd /opt/remote-machine-mcp
./remote-machine-mcp --listen 0.0.0.0 --port 8765 --vps YOUR_PUBLIC_IP
```

Windows example:

```powershell
.\remote-machine-mcp.exe --listen 0.0.0.0 --port 8765 --vps YOUR_PUBLIC_IP
```

### stdio mode

```bash
./remote-machine-mcp --stdio
```

## Working Directory

If `workdir` is omitted, commands run in the server process current directory.

Relative paths are resolved from that current directory.

## Tools

| Tool | Description |
| --- | --- |
| `shell_command` | Run a shell script on the remote machine and return `stdout`, `stderr`, exit code, and timeout state. |
| `shell` | Run an argv-style command on the remote machine. |
| `exec_command` | Start a long-running command and return output immediately or a `session_id`. |
| `write_stdin` | Write to an existing `exec_command` session and poll for more output. |
| `apply_patch` | Apply a Codex-style patch on the remote machine. |
| `prepare_upload` | Create a resumable upload session for advanced transfer workflows. |
| `prepare_download` | Create a resumable download session for advanced transfer workflows. |

The server also exposes authenticated HTTP file transfer endpoints under `/transfer`.

For normal file transfer, prefer the direct file endpoints with `curl` or `wget`. If a directory needs to be transferred, archive it first, preferably as `zip`, then transfer the archive file.

## Local Development

```bash
go test ./...
go build ./cmd
go run ./cmd --stdio
go run ./cmd --listen 127.0.0.1 --port 8765
```
