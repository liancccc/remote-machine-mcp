package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"remote-machine-mcp/internal/filesystem"
	"remote-machine-mcp/internal/mcp"
	"remote-machine-mcp/internal/tools"
)

func main() {
	stdio := flag.Bool("stdio", false, "run stdio MCP transport instead of HTTP server")
	listen := flag.String("listen", "127.0.0.1", "HTTP listen address")
	port := flag.String("port", "8765", "HTTP listen port")
	path := flag.String("path", "/mcp", "HTTP MCP endpoint path")
	token := flag.String("token", "", "bearer token for HTTP auth; required in HTTP mode")
	vps := flag.String("vps", "", "public IP or hostname of the VPS/VM for generating MCP client config")
	flag.Parse()

	roots, err := filesystem.LoadAllowedRoots(os.Getenv("MCP_ALLOWED_ROOTS"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	registry := tools.NewRegistry(roots)
	server := mcp.NewServer("remote-machine-mcp", "0.2.0", registry)
	if *stdio {
		server.Run(os.Stdin, os.Stdout)
		return
	}
	authToken := *token
	if authToken == "" {
		var err error
		authToken, err = randomToken()
		if err != nil {
			fmt.Fprintln(os.Stderr, "generate auth token:", err)
			os.Exit(1)
		}
	}
	printStartupBanner(os.Stderr, *listen, *port, *path, authToken, *vps)
	if err := server.ListenAndServeHTTP(mcp.HTTPOptions{
		Addr:      *listen + ":" + *port,
		Path:      *path,
		AuthToken: authToken,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func printStartupBanner(w io.Writer, listen, port, path, authToken, vpsIP string) {
	publicHost := listen
	if vpsIP != "" {
		publicHost = vpsIP
	}
	baseURL := fmt.Sprintf("http://%s:%s", publicHost, port)
	mcpURL := baseURL + path

	logo := `█▄▄▄▄ ▄███▄   █▀▄▀█ ████▄    ▄▄▄▄▀ ▄███▄   █▀▄▀█ ██   ▄█▄     ▄  █ ▄█    ▄   ▄███▄   █▀▄▀█ ▄█▄    █ ▄▄  
█  ▄▀ █▀   ▀  █ █ █ █   █ ▀▀▀ █    █▀   ▀  █ █ █ █ █  █▀ ▀▄  █   █ ██     █  █▀   ▀  █ █ █ █▀ ▀▄  █   █ 
█▀▀▌  ██▄▄    █ ▄ █ █   █     █    ██▄▄    █ ▄ █ █▄▄█ █   ▀  ██▀▀█ ██ ██   █ ██▄▄    █ ▄ █ █   ▀  █▀▀▀  
█  █  █▄   ▄▀ █   █ ▀████    █     █▄   ▄▀ █   █ █  █ █▄  ▄▀ █   █ ▐█ █ █  █ █▄   ▄▀ █   █ █▄  ▄▀ █     
  █   ▀███▀      █          ▀      ▀███▀      █     █ ▀███▀     █   ▐ █  █ █ ▀███▀      █  ▀███▀   █    
 ▀              ▀                            ▀     █           ▀      █   ██           ▀            ▀   
                                                  ▀`
	fmt.Fprintf(w, "\n%s\n\n", logo)
	fmt.Fprintf(w, "Remote Machine MCP Server\n")
	fmt.Fprintf(w, "----------------------------------------\n")
	fmt.Fprintf(w, "mcp: %s\n", mcpURL)

	config := map[string]any{
		"type": "http",
		"url":  mcpURL,
		"headers": map[string]string{
			"Authorization": "Bearer " + authToken,
		},
	}
	configJSON, _ := json.Marshal(config)
	fmt.Fprintf(w, "config: %s\n", string(configJSON))

	if vpsIP != "" && (listen == "127.0.0.1" || listen == "::1") {
		fmt.Fprintf(w, "note: server is listening on %s, make sure port %s is reachable\n", listen, port)
		fmt.Fprintf(w, "(bind to 0.0.0.0 or set up an SSH tunnel)\n")
	}
}
