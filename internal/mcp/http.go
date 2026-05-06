package mcp

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	sdkserver "github.com/mark3labs/mcp-go/server"
)

type HTTPOptions struct {
	Addr      string
	Path      string
	AuthToken string
}

func (s *Server) ListenAndServeHTTP(options HTTPOptions) error {
	if options.Addr == "" {
		options.Addr = formatListenAddr("", "")
	}
	if options.Path == "" {
		options.Path = "/mcp"
	}

	mcpHandler := sdkserver.NewStreamableHTTPServer(
		s.sdk,
		sdkserver.WithEndpointPath(options.Path),
		sdkserver.WithStateLess(true),
		sdkserver.WithDisableStreaming(true),
		sdkserver.WithLogger(discardLogger{}),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.Handle(options.Path, authorizedHandler(options.AuthToken, mcpHandler))
	log.Printf("remote-machine-mcp listening addr=%s path=%s auth=%t", options.Addr, options.Path, options.AuthToken != "")
	return http.ListenAndServe(options.Addr, mux)
}

func authorizedHandler(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="remote-machine-mcp"`)
			log.Printf("reject request method=%s path=%s remote=%s reason=unauthorized", r.Method, r.URL.Path, r.RemoteAddr)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authorized(r *http.Request, token string) bool {
	if token == "" {
		return true
	}
	got := r.Header.Get("Authorization")
	if !strings.HasPrefix(got, "Bearer ") {
		return false
	}
	got = strings.TrimSpace(strings.TrimPrefix(got, "Bearer "))
	if len(got) != len(token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}
