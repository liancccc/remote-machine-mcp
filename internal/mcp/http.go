package mcp

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"

	sdkserver "github.com/mark3labs/mcp-go/server"
)

type HTTPOptions struct {
	Addr            string
	Path            string
	AuthToken       string
	TransferPath    string
	TransferHandler http.Handler
}

func (s *Server) ListenAndServeHTTP(options HTTPOptions) error {
	if options.Addr == "" {
		options.Addr = formatListenAddr("", "")
	}
	if options.Path == "" {
		options.Path = "/mcp"
	}
	if options.TransferPath == "" {
		options.TransferPath = "/transfer"
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
	if options.TransferHandler != nil {
		mux.Handle(options.TransferPath+"/", authorizedHandler(options.AuthToken, options.TransferHandler))
		mux.Handle(options.TransferPath, authorizedHandler(options.AuthToken, options.TransferHandler))
	}
	log.Printf("remote-machine-mcp listening addr=%s path=%s transfer_path=%s auth=%t", options.Addr, options.Path, options.TransferPath, options.AuthToken != "")
	return http.ListenAndServe(options.Addr, mux)
}

func authorizedHandler(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validOrigin(r) {
			log.Printf("reject request method=%s path=%s remote=%s reason=invalid_origin origin=%q host=%q", r.Method, r.URL.Path, r.RemoteAddr, r.Header.Get("Origin"), r.Host)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
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

func validOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	originHost := canonicalHost(u.Host)
	requestHost := canonicalHost(r.Host)
	if originHost == "" || requestHost == "" {
		return false
	}
	if originHost == requestHost {
		return true
	}
	return isLoopbackHost(originHost) && isLoopbackHost(requestHost)
}

func canonicalHost(hostport string) string {
	host := hostport
	if parsed, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	return strings.ToLower(host)
}

func isLoopbackHost(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return strings.HasPrefix(host, "127.")
	}
}
