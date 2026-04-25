package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Guard struct {
	Roots       []string
	DefaultRoot string
	HomeDir     string
}

func LoadAllowedRoots(raw string) (*Guard, error) {
	if raw == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		raw = cwd
	}
	parts := strings.Split(raw, string(os.PathListSeparator))
	roots := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		abs, err := filepath.Abs(filepath.Clean(part))
		if err != nil {
			return nil, err
		}
		roots = append(roots, abs)
	}
	if len(roots) == 0 {
		return nil, errors.New("MCP_ALLOWED_ROOTS produced no usable roots")
	}
	home, _ := os.UserHomeDir()
	return &Guard{Roots: roots, DefaultRoot: roots[0], HomeDir: home}, nil
}

func (g *Guard) Resolve(path string, allowDir bool) (string, error) {
	if path == "" {
		return "", errors.New("path is required")
	}
	path = g.expandHome(path)
	if runtime.GOOS != "windows" && looksLikeWindowsPath(path) {
		return "", errors.New("path looks like a Windows path but this server is not Windows; omit workdir or use a remote path such as " + g.DefaultRoot)
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(g.DefaultRoot, abs)
	}
	abs, _ = filepath.Abs(filepath.Clean(abs))
	if !allowDir {
		if info, err := os.Stat(abs); err == nil && info.IsDir() {
			return "", errors.New("path is a directory: " + abs)
		}
	}
	return abs, nil
}

func (g *Guard) expandHome(path string) string {
	if g.HomeDir == "" {
		return path
	}
	if path == "~" {
		return g.HomeDir
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		return filepath.Join(g.HomeDir, path[2:])
	}
	return path
}

func looksLikeWindowsPath(path string) bool {
	if len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return true
	}
	return strings.HasPrefix(path, `\\`)
}

func (g *Guard) ResolveDir(path string) (string, error) {
	if path == "" {
		path = g.DefaultRoot
	}
	abs, err := g.Resolve(path, true)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("not a directory: " + abs)
	}
	return abs, nil
}
