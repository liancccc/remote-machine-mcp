package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"time"

	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
)

type Tool interface {
	Name() string
	Description() string
	InputSchema() map[string]any
	Call(args map[string]any) (text string, structured any, err error)
}

type Registry interface {
	Tools() []Tool
	Call(name string, args map[string]any) (string, any, error)
}

type instructionSource interface {
	ServerInstructions() string
}

type Server struct {
	sdk      *sdkserver.MCPServer
	registry Registry
}

func NewServer(name, version string, registry Registry) *Server {
	s := &Server{
		sdk: sdkserver.NewMCPServer(
			name,
			version,
			sdkserver.WithRecovery(),
			sdkserver.WithToolCapabilities(false),
			sdkserver.WithInstructions(serverInstructions(registry)),
		),
		registry: registry,
	}
	s.registerTools()
	return s
}

func (s *Server) Run(r io.Reader, w io.Writer) {
	stdio := sdkserver.NewStdioServer(s.sdk)
	stdio.SetErrorLogger(log.New(io.Discard, "", 0))
	_ = stdio.Listen(context.Background(), r, w)
}

func (s *Server) registerTools() {
	for _, tool := range s.registry.Tools() {
		rawSchema, err := json.Marshal(tool.InputSchema())
		if err != nil {
			rawSchema = []byte(`{"type":"object","additionalProperties":true}`)
		}
		sdkTool := sdkmcp.NewToolWithRawSchema(tool.Name(), tool.Description(), rawSchema)
		name := tool.Name()
		s.sdk.AddTool(sdkTool, func(ctx context.Context, req sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			start := time.Now()
			log.Printf("tool call start name=%s", name)
			args := req.GetArguments()
			if args == nil {
				args = map[string]any{}
			}
			text, structured, err := s.registry.Call(name, args)
			if err != nil {
				log.Printf("tool call end name=%s status=error duration=%s error=%q", name, time.Since(start).Round(time.Millisecond), err.Error())
				return toolResult(err.Error(), structured, true), nil
			}
			log.Printf("tool call end name=%s status=ok duration=%s", name, time.Since(start).Round(time.Millisecond))
			return toolResult(text, structured, false), nil
		})
	}
}

func toolResult(text string, structured any, isError bool) *sdkmcp.CallToolResult {
	result := &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			sdkmcp.NewTextContent(text),
		},
		IsError: isError,
	}
	if structured != nil {
		result.StructuredContent = structured
	}
	return result
}

type discardLogger struct{}

func (discardLogger) Infof(string, ...any)  {}
func (discardLogger) Errorf(string, ...any) {}

func serverInstructions(registry Registry) string {
	if src, ok := registry.(instructionSource); ok {
		if text := src.ServerInstructions(); text != "" {
			return text
		}
	}

	hostname, _ := os.Hostname()
	return fmt.Sprintf(
		"You are connected to a remote machine MCP server, not the user's local machine. Remote host=%q os=%s arch=%s cwd=%q path_separator=%q. Use tools against the remote machine only.",
		hostname,
		runtime.GOOS,
		runtime.GOARCH,
		".",
		string(os.PathSeparator),
	)
}

func formatListenAddr(host, port string) string {
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "8765"
	}
	return fmt.Sprintf("%s:%s", host, port)
}
