# remote-machine-mcp

`remote-machine-mcp` lets a local agent operate a remote Linux or Windows machine over MCP.

It is intended for cases such as:

- connecting Codex to a VPS
- connecting Codex to a remote Windows VM
- running shell commands, patches, and file transfer on the remote machine without installing the full agent stack there

## What It Exposes

- MCP over `stdio` or HTTP
- remote shell execution
- long-running command sessions with `write_stdin`
- Codex-style patch apply
- authenticated HTTP file transfer endpoints under `/transfer`

For ordinary file transfer, prefer the direct HTTP file endpoints:

- download: `/transfer/download?file_path=...`
- upload: `/transfer/upload?file_path=...&overwrite=true`

These work well with `curl`, `wget`, or similar tools.

If you need to transfer a directory, do not rely on raw directory transfer. Archive it first on the remote machine, preferably as `zip`, then transfer the archive file.

## Release Asset Recommendation

GitHub Releases should publish raw executable files directly, not compressed archives.

Recommended asset names:

- `remote-machine-mcp-linux-amd64`
- `remote-machine-mcp-linux-arm64`
- `remote-machine-mcp-darwin-amd64`
- `remote-machine-mcp-darwin-arm64`
- `remote-machine-mcp-windows-amd64.exe`
- `remote-machine-mcp-windows-arm64.exe`

That makes deployment simpler because users can download one file and run it immediately, without unpacking `.zip` or `.tar.gz`.

## Deploy From GitHub Release

Repository:

- `https://github.com/liancccc/remote-machine-mcp`

Latest release assets should be downloadable from:

- `https://github.com/liancccc/remote-machine-mcp/releases/latest/download/...`

### Linux amd64

```bash
sudo mkdir -p /opt/remote-machine-mcp
cd /opt/remote-machine-mcp
sudo curl -fL -o remote-machine-mcp \
  https://github.com/liancccc/remote-machine-mcp/releases/latest/download/remote-machine-mcp-linux-amd64
sudo chmod +x remote-machine-mcp
```

Or with `wget`:

```bash
sudo mkdir -p /opt/remote-machine-mcp
cd /opt/remote-machine-mcp
sudo wget -O remote-machine-mcp \
  https://github.com/liancccc/remote-machine-mcp/releases/latest/download/remote-machine-mcp-linux-amd64
sudo chmod +x remote-machine-mcp
```

### Linux arm64

```bash
sudo mkdir -p /opt/remote-machine-mcp
cd /opt/remote-machine-mcp
sudo curl -fL -o remote-machine-mcp \
  https://github.com/liancccc/remote-machine-mcp/releases/latest/download/remote-machine-mcp-linux-arm64
sudo chmod +x remote-machine-mcp
```

### Windows amd64

PowerShell:

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
MCP_ALLOWED_ROOTS=/root ./remote-machine-mcp --listen 0.0.0.0 --port 8765 --vps YOUR_PUBLIC_IP
```

Windows example:

```powershell
$env:MCP_ALLOWED_ROOTS='C:\Users\Administrator'
.\remote-machine-mcp.exe --listen 0.0.0.0 --port 8765 --vps YOUR_PUBLIC_IP
```

### stdio mode

```bash
./remote-machine-mcp --stdio
```

## MCP_ALLOWED_ROOTS

`MCP_ALLOWED_ROOTS` controls the default working directory and the allowed path roots.

Examples:

Linux:

```bash
export MCP_ALLOWED_ROOTS=/root:/opt:/srv
```

Windows:

```powershell
$env:MCP_ALLOWED_ROOTS='C:\Users\llianpo;D:\work'
```

If unset, the current working directory is used.

## Direct File Transfer

### Download a file

```bash
curl -H "Authorization: Bearer TOKEN" \
  -o ./artifact.zip \
  "http://HOST:8765/transfer/download?file_path=/root/artifact.zip"
```

### Upload a file

```bash
curl -X PUT \
  -H "Authorization: Bearer TOKEN" \
  --data-binary @./artifact.zip \
  "http://HOST:8765/transfer/upload?file_path=/root/artifact.zip&overwrite=true"
```

## Directory Transfer Pattern

Prefer `zip`.

### Remote Linux: archive a directory first

```bash
cd /root
zip -r cateye.zip cateye
```

If `zip` is unavailable, use another installed archiver as a fallback.

### Then download the archive

```bash
curl -H "Authorization: Bearer TOKEN" \
  -o ./cateye.zip \
  "http://HOST:8765/transfer/download?file_path=/root/cateye.zip"
```

## Local Development

From the repository root:

```bash
go test ./...
go build ./cmd
go run ./cmd --stdio
go run ./cmd --listen 127.0.0.1 --port 8765
```
