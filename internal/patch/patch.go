package patch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Resolver func(path string, allowDir bool) (string, error)

type Hunk struct {
	Kind    string
	Path    string
	MoveTo  string
	Content []string
	Chunks  []Chunk
}

type Chunk struct {
	Context  string
	OldLines []string
	NewLines []string
	EOF      bool
}

func Apply(input, workdir string, resolve Resolver) ([]string, error) {
	hunks, err := Parse(input)
	if err != nil {
		return nil, err
	}
	affected := []string{}
	for _, h := range hunks {
		path := h.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(workdir, path)
		}
		abs, err := resolve(path, false)
		if err != nil {
			return affected, err
		}
		switch h.Kind {
		case "add":
			if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
				return affected, err
			}
			if err := os.WriteFile(abs, []byte(strings.Join(h.Content, "\n")+"\n"), 0644); err != nil {
				return affected, err
			}
			affected = append(affected, "A "+h.Path)
		case "delete":
			if err := os.Remove(abs); err != nil {
				return affected, err
			}
			affected = append(affected, "D "+h.Path)
		case "update":
			data, err := os.ReadFile(abs)
			if err != nil {
				return affected, err
			}
			lines := splitLines(string(data))
			updated, err := applyChunks(lines, h.Chunks, abs)
			if err != nil {
				return affected, err
			}
			out := strings.Join(updated, "\n")
			if !strings.HasSuffix(out, "\n") {
				out += "\n"
			}
			target := abs
			shown := h.Path
			if h.MoveTo != "" {
				dest := h.MoveTo
				if !filepath.IsAbs(dest) {
					dest = filepath.Join(workdir, dest)
				}
				target, err = resolve(dest, false)
				if err != nil {
					return affected, err
				}
				if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
					return affected, err
				}
				shown = h.MoveTo
			}
			if err := os.WriteFile(target, []byte(out), 0644); err != nil {
				return affected, err
			}
			if h.MoveTo != "" {
				if err := os.Remove(abs); err != nil {
					return affected, err
				}
			}
			affected = append(affected, "M "+shown)
		}
	}
	return affected, nil
}

func Parse(input string) ([]Hunk, error) {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")
	lines := strings.Split(input, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "*** Begin Patch" {
		return nil, fmt.Errorf("invalid patch: missing *** Begin Patch")
	}
	hunks := []Hunk{}
	for i := 1; i < len(lines); {
		line := strings.TrimRight(lines[i], " \t")
		if line == "*** End Patch" {
			return hunks, nil
		}
		var h Hunk
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			h = Hunk{Kind: "add", Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))}
			i++
			for i < len(lines) && !strings.HasPrefix(lines[i], "*** ") {
				if strings.HasPrefix(lines[i], "+") {
					h.Content = append(h.Content, strings.TrimPrefix(lines[i], "+"))
				}
				i++
			}
		case strings.HasPrefix(line, "*** Delete File: "):
			h = Hunk{Kind: "delete", Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))}
			i++
		case strings.HasPrefix(line, "*** Update File: "):
			h = Hunk{Kind: "update", Path: strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))}
			i++
			if i < len(lines) && strings.HasPrefix(lines[i], "*** Move to: ") {
				h.MoveTo = strings.TrimSpace(strings.TrimPrefix(lines[i], "*** Move to: "))
				i++
			}
			for i < len(lines) && !strings.HasPrefix(lines[i], "*** ") {
				if !strings.HasPrefix(lines[i], "@@") {
					i++
					continue
				}
				c := Chunk{Context: strings.TrimSpace(strings.TrimPrefix(lines[i], "@@"))}
				i++
				for i < len(lines) && !strings.HasPrefix(lines[i], "@@") && !strings.HasPrefix(lines[i], "*** ") {
					l := lines[i]
					if l == "*** End of File" {
						c.EOF = true
						i++
						continue
					}
					if strings.HasPrefix(l, "+") {
						c.NewLines = append(c.NewLines, strings.TrimPrefix(l, "+"))
					} else if strings.HasPrefix(l, "-") {
						c.OldLines = append(c.OldLines, strings.TrimPrefix(l, "-"))
					} else if strings.HasPrefix(l, " ") {
						t := strings.TrimPrefix(l, " ")
						c.OldLines = append(c.OldLines, t)
						c.NewLines = append(c.NewLines, t)
					}
					i++
				}
				h.Chunks = append(h.Chunks, c)
			}
		default:
			return nil, fmt.Errorf("invalid patch line: %s", line)
		}
		hunks = append(hunks, h)
	}
	return nil, fmt.Errorf("invalid patch: missing *** End Patch")
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func applyChunks(lines []string, chunks []Chunk, path string) ([]string, error) {
	idx := 0
	for _, c := range chunks {
		if c.Context != "" {
			pos := findSeq(lines, []string{c.Context}, idx)
			if pos < 0 {
				return nil, fmt.Errorf("failed to find context %q in %s", c.Context, path)
			}
			idx = pos + 1
		}
		if len(c.OldLines) == 0 {
			insertAt := idx
			if c.EOF || c.Context == "" {
				insertAt = len(lines)
			}
			lines = append(lines[:insertAt], append(append([]string{}, c.NewLines...), lines[insertAt:]...)...)
			idx = insertAt + len(c.NewLines)
			continue
		}
		pos := findSeq(lines, c.OldLines, idx)
		if pos < 0 {
			return nil, fmt.Errorf("failed to find expected lines in %s:\n%s", path, strings.Join(c.OldLines, "\n"))
		}
		updated := append([]string{}, lines[:pos]...)
		updated = append(updated, c.NewLines...)
		updated = append(updated, lines[pos+len(c.OldLines):]...)
		lines = updated
		idx = pos + len(c.NewLines)
	}
	return lines, nil
}

func findSeq(lines, needle []string, start int) int {
	if len(needle) == 0 {
		return start
	}
	for i := start; i+len(needle) <= len(lines); i++ {
		ok := true
		for j := range needle {
			if lines[i+j] != needle[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}
