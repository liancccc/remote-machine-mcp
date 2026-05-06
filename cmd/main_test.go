package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintStartupBanner(t *testing.T) {
	var out bytes.Buffer
	printStartupBanner(&out, "127.0.0.1", "8765", "/mcp", "secret-token", "")
	text := out.String()

	for _, want := range []string{
		"mcp: http://127.0.0.1:8765/mcp",
		`config: {"headers":{"Authorization":"Bearer secret-token"},"type":"http","url":"http://127.0.0.1:8765/mcp"}`,
		"Remote Machine MCP Server",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("startup banner missing %q in:\n%s", want, text)
		}
	}
}

func TestPrintStartupBannerWithVPS(t *testing.T) {
	var out bytes.Buffer
	printStartupBanner(&out, "127.0.0.1", "8765", "/mcp", "secret-token", "154.37.220.171")
	text := out.String()

	for _, want := range []string{
		"mcp: http://154.37.220.171:8765/mcp",
		`config: {"headers":{"Authorization":"Bearer secret-token"},"type":"http","url":"http://154.37.220.171:8765/mcp"}`,
		"make sure port 8765 is reachable",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("VPS banner missing %q in:\n%s", want, text)
		}
	}
}

func TestPrintStartupBannerNoWarningOnWildcard(t *testing.T) {
	var out bytes.Buffer
	printStartupBanner(&out, "0.0.0.0", "8765", "/mcp", "secret-token", "154.37.220.171")
	if strings.Contains(out.String(), "make sure port") {
		t.Fatalf("should not warn when listen is 0.0.0.0:\n%s", out.String())
	}
}

func TestRandomTokenGeneratesHexToken(t *testing.T) {
	token, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 64 {
		t.Fatalf("unexpected token length: %d", len(token))
	}
	for _, r := range token {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("token contains non-hex rune %q: %s", r, token)
		}
	}
}
