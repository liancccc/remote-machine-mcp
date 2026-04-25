# Repository Guidelines

## Project Structure & Module Organization
`remote-machine-mcp` is a small Go module. The CLI entrypoint lives in `cmd/main.go`. Core implementation is organized under `internal/`: `internal/mcp` handles the HTTP MCP server, `internal/tools` contains tool registration and shell/transfer helpers, `internal/filesystem` enforces allowed-root rules, and `internal/patch` applies Codex-style patches. GitHub Actions live in `.github/workflows/build.yml`. There is no separate `test/` directory; tests sit beside the code as `*_test.go`.

## Build, Test, and Development Commands
Use standard Go tooling from the repository root:

- `go test ./...` runs the full test suite used by CI.
- `go build ./cmd` builds the local binary entrypoint.
- `go run ./cmd --stdio` starts the server over stdio for local MCP testing.
- `go run ./cmd --listen 127.0.0.1 --port 8765` runs the HTTP server locally.
- `gofmt -w cmd internal` formats the codebase before review.

CI also cross-builds release archives for Linux, macOS, and Windows on `amd64` and `arm64`.

## Coding Style & Naming Conventions
Follow normal Go conventions: tabs for indentation, `gofmt` formatting, and package names in short lowercase form. Keep exported identifiers concise and descriptive (`NewRegistry`, `ListenAndServeHTTP`). Prefer colocated helpers over large utility files, and keep MCP/tool-specific logic within the matching `internal/*` package.

## Testing Guidelines
Write table-driven Go tests in `*_test.go` files next to the code they cover. Name tests with standard Go patterns such as `TestHTTPServerRejectsBadAuth` or `TestPrepareDownload`. Cover success paths and boundary checks, especially path-guarding, transfer lifecycle behavior, and long-running command/session handling. Run `go test ./...` before opening a PR.

## Commit & Pull Request Guidelines
Recent history uses short, imperative commit subjects with sentence-style capitalization, for example `Fix build workflow: remove reference to deleted AGENTS.md`. Keep commits focused and scoped to one change. PRs should include a clear summary, test evidence (`go test ./...`), and linked issues when relevant. Add terminal output or screenshots only when behavior or startup output changed.

## Security & Configuration Tips
Do not commit bearer tokens, host-specific paths, or temporary transfer data. Use `MCP_ALLOWED_ROOTS` to constrain remote file access, and prefer binding the HTTP server to `127.0.0.1` behind an SSH tunnel unless direct exposure is intentionally secured.
