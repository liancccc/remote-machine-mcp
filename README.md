# remote-machine-mcp

`remote-machine-mcp` is a remote-machine control server for MCP agents. Run it on a VPS, lab host, or workstation; connect to it from your local agent over HTTP MCP; then operate that machine through shell, patch, and dedicated HTTP transfer endpoints.

The normal shape is:

```text
local Codex / MCP agent
  -> HTTP MCP endpoint, usually through SSH tunnel
  -> remote-machine-mcp on the remote machine
  -> shell + patch tools on the remote machine
  -> resumable HTTP transfer endpoints for large files and directories
```

## Install

Use the prebuilt binaries from GitHub Actions artifacts or tagged releases. Local users should not need to build the project manually.

Latest push artifacts are available in the repository Actions page:

```bash
gh run download --repo liancccc/remote-machine-mcp --name remote-machine-mcp_linux_amd64
```

Choose the artifact matching the remote machine:

- `remote-machine-mcp_linux_amd64`
- `remote-machine-mcp_linux_arm64`
- `remote-machine-mcp_windows_amd64`
- `remote-machine-mcp_windows_arm64`
- `remote-machine-mcp_darwin_amd64`
- `remote-machine-mcp_darwin_arm64`

Unpack the archive on the remote host and place the binary somewhere stable, for example `/opt/remote-machine-mcp/remote-machine-mcp` or `C:\Tools\remote-machine-mcp\remote-machine-mcp.exe`.

## Start The Remote Server

`MCP_ALLOWED_ROOTS` is now just used to seed the default working directory for relative paths. If it is unset, the server process working directory is used.

Linux VPS:

```bash
export MCP_ALLOWED_ROOTS=/home/ubuntu/projects
/opt/remote-machine-mcp/remote-machine-mcp --listen 127.0.0.1 --port 8765
```

Windows host:

```powershell
$env:MCP_ALLOWED_ROOTS = 'C:\Code'
C:\Tools\remote-machine-mcp\remote-machine-mcp.exe --listen 127.0.0.1 --port 8765
```

On startup, the server prints the MCP endpoint, transfer endpoint base, and bearer token:

```text
url: http://127.0.0.1:8765/mcp
transfer_url: http://127.0.0.1:8765/transfer
auth: Bearer <token>
```

The bearer token is generated randomly at startup and cannot be configured. Copy the token into your local MCP client configuration for that server process.

HTTP requests to both `/mcp` and `/transfer` must include `Authorization: Bearer <token>`. Browser-style requests with an `Origin` header are accepted only when the origin matches the request host, or both sides are loopback hosts. The server logs startup, rejected requests, and tool call start/end events to stderr without logging bearer tokens or tool arguments.

## Connect From Your Local Agent

For a VPS, prefer an SSH tunnel so the server can stay bound to `127.0.0.1`:

```bash
ssh -L 8765:127.0.0.1:8765 ubuntu@your-vps
```

Codex MCP config:

```toml
[mcp_servers.abcdef123456_vps_main]
url = "http://127.0.0.1:8765/mcp"
bearer_token = "<token from server startup output>"
```

Direct remote access also works if your network policy allows it:

```toml
[mcp_servers.abcdef123456_vps_main]
url = "http://your-vps:8765/mcp"
bearer_token = "<token from server startup output>"
```

For multiple VPS machines, add one MCP server block per machine and use descriptive names such as `vps_tokyo`, `vps_lab`, or `vps_buildbox`.

If you expose the port directly, use firewall rules or a private network. Do not expose it broadly to the internet.

## Available Tools

Core remote-operation tools:

- `machine_info`: return remote machine context, including OS, arch, user, home, default workdir, and shell.
- `shell_command`: run a shell script and return stdout, stderr, exit code, and timeout status.
- `shell`: run an argv-style command array.
- `exec_command`: start a command and return output or a `session_id` for long-running work.
- `write_stdin`: send input to, or poll, an `exec_command` session.
- `apply_patch`: apply Codex-style patch blocks.
- `prepare_upload`: create a remote upload session and return an agent-friendly handoff plan for the local transport layer.
- `prepare_download`: create a remote download session and return an agent-friendly handoff plan for the local transport layer.

Large file and directory movement should use the dedicated `/transfer` HTTP endpoints instead of MCP tools.

Recommended agent flow:

- local-to-remote: call `prepare_upload`, then let the local client stream bytes to the returned `/transfer` upload paths
- remote-to-local: call `prepare_download`, then let the local client fetch bytes from the returned `/transfer` download paths
- the agent should not manually manage chunk offsets unless it is itself implementing the transport loop

Transfer API summary:

- `POST /transfer/upload-sessions`: create an upload session for a file or directory target.
- `PUT /transfer/upload-sessions/{id}/chunks?offset=<bytes>`: send a raw chunk for that upload session.
- `POST /transfer/upload-sessions/{id}/complete`: verify optional SHA-256 and publish the upload.
- `DELETE /transfer/upload-sessions/{id}`: abort an upload and remove temporary data.
- `POST /transfer/download-sessions`: create a download session for a file or directory target.
- `GET /transfer/download-sessions/{id}/chunks?offset=<bytes>&limit=<bytes>`: fetch a raw chunk from a download session.
- `DELETE /transfer/download-sessions/{id}`: abort a download and clean up temporary data.

Transfer semantics:

- uploads and downloads are resumable within the lifetime of the server process
- directories are transferred as `zip`
- directory downloads produce a ZIP archive rooted at the source directory name
- directory uploads require a ZIP payload and are unpacked on finalize
- transfer sessions are kept in memory and expire automatically if abandoned

Paths are interpreted on the remote machine. Omit `workdir` to use the server default workdir returned by `machine_info`. `~` expands to the remote user's home.

Every non-`machine_info` tool description includes a compact remote context prefix with OS, architecture, default workdir, path separator, and shell. This gives agents the remote machine facts during tool selection even when they forget to call `machine_info` first.

## Long-Running Commands

Use `exec_command` with a short `yield_time_ms` when a command may keep running. If the command is still active, the response includes `session_id`. Call `write_stdin` with that ID to send input, or pass empty `chars` to poll for more output.

## Development

The project is a small Go module using `github.com/mark3labs/mcp-go` for MCP protocol and transport handling. For code changes, use standard Go tooling:

```bash
go test ./...
go run ./cmd --stdio
```

The GitHub workflow builds release artifacts automatically for Linux, Windows, and macOS on `amd64` and `arm64`.
