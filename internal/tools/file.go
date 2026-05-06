package tools

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"remote-machine-mcp/internal/filesystem"
)

type ListDir struct{ guard *filesystem.Guard }
type ListFiles struct{ guard *filesystem.Guard }
type ReadFile struct{ guard *filesystem.Guard }
type WriteFile struct{ guard *filesystem.Guard }
type EditFile struct{ guard *filesystem.Guard }
type CopyPath struct{ guard *filesystem.Guard }
type MovePath struct{ guard *filesystem.Guard }
type ViewImage struct{ guard *filesystem.Guard }

func (ListDir) Name() string { return "list_dir" }
func (ListDir) Description() string {
	return "Codex-compatible directory listing with 1-indexed entry numbers and simple type labels."
}
func (ListDir) InputSchema() map[string]any {
	return objectSchema(map[string]any{"dir_path": stringSchema("Directory path to list."), "offset": numberSchema("1-indexed entry number to start from."), "limit": numberSchema("Maximum entries to return."), "depth": numberSchema("Maximum directory depth to traverse.")}, []string{"dir_path"})
}
func (t ListDir) Call(args map[string]any) (string, any, error) {
	offset := intArg(args, "offset", 1)
	if offset < 1 {
		offset = 1
	}
	limit := intArg(args, "limit", 200)
	if limit <= 0 {
		limit = 200
	}
	entries, err := listEntries(t.guard, stringArg(args, "dir_path", ""), intArg(args, "depth", 1), offset+limit-1)
	if err != nil {
		return "", nil, err
	}
	start := offset - 1
	if start > len(entries) {
		start = len(entries)
	}
	entries = entries[start:]
	if len(entries) > limit {
		entries = entries[:limit]
	}
	lines := make([]string, 0, len(entries))
	for i, e := range entries {
		lines = append(lines, fmt.Sprintf("%d. [%s] %s", offset+i, e["type"], e["relative"]))
	}
	return strings.Join(lines, "\n"), map[string]any{"entries": entries}, nil
}

func (ListFiles) Name() string { return "list_files" }
func (ListFiles) Description() string {
	return "List files under a directory. This is a machine-agent convenience tool."
}
func (ListFiles) InputSchema() map[string]any {
	return objectSchema(map[string]any{"path": stringSchema("Directory path."), "recursive": boolSchema("Recurse into subdirectories."), "max_entries": numberSchema("Maximum entries to return.")}, []string{"path"})
}
func (t ListFiles) Call(args map[string]any) (string, any, error) {
	depth := 1
	if boolArg(args, "recursive", false) {
		depth = 64
	}
	entries, err := listEntries(t.guard, stringArg(args, "path", ""), depth, intArg(args, "max_entries", 200))
	if err != nil {
		return "", nil, err
	}
	b, _ := json.MarshalIndent(entries, "", "  ")
	return string(b), map[string]any{"entries": entries}, nil
}

func listEntries(guard *filesystem.Guard, dir string, depth, limit int) ([]map[string]any, error) {
	root, err := guard.Resolve(dir, true)
	if err != nil {
		return nil, err
	}
	if depth <= 0 {
		depth = 1
	}
	if limit <= 0 {
		limit = 200
	}
	rootDepth := strings.Count(root, string(os.PathSeparator))
	entries := []map[string]any{}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		currentDepth := strings.Count(path, string(os.PathSeparator)) - rootDepth
		if currentDepth > depth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if len(entries) >= limit {
			return filepath.SkipDir
		}
		info, _ := d.Info()
		rel, _ := filepath.Rel(root, path)
		kind := "file"
		if d.IsDir() {
			kind = "dir"
		}
		entries = append(entries, map[string]any{"path": path, "relative": rel, "type": kind, "is_dir": d.IsDir(), "size": fileSize(info)})
		return nil
	})
	if err != nil && err != filepath.SkipDir {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return fmt.Sprint(entries[i]["relative"]) < fmt.Sprint(entries[j]["relative"]) })
	return entries, nil
}

func (ReadFile) Name() string        { return "read_file" }
func (ReadFile) Description() string { return "Read a file." }
func (ReadFile) InputSchema() map[string]any {
	return objectSchema(map[string]any{"path": stringSchema("File path."), "offset": numberSchema("Byte offset."), "limit_bytes": numberSchema("Maximum bytes to read.")}, []string{"path"})
}
func (t ReadFile) Call(args map[string]any) (string, any, error) {
	path, err := t.guard.Resolve(stringArg(args, "path", ""), false)
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	offset := intArg(args, "offset", 0)
	limit := intArg(args, "limit_bytes", 1024*1024)
	if offset < 0 {
		offset = 0
	}
	if offset > len(data) {
		offset = len(data)
	}
	end := len(data)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	chunk := data[offset:end]
	return string(chunk), map[string]any{"path": path, "offset": offset, "bytes": len(chunk), "total_bytes": len(data)}, nil
}

func (WriteFile) Name() string        { return "write_file" }
func (WriteFile) Description() string { return "Create or overwrite a file." }
func (WriteFile) InputSchema() map[string]any {
	return objectSchema(map[string]any{"path": stringSchema("File path."), "content": stringSchema("File content."), "create_dirs": boolSchema("Create parent directories."), "overwrite": boolSchema("Overwrite existing file.")}, []string{"path", "content"})
}
func (t WriteFile) Call(args map[string]any) (string, any, error) {
	path, err := t.guard.Resolve(stringArg(args, "path", ""), false)
	if err != nil {
		return "", nil, err
	}
	content, ok := args["content"].(string)
	if !ok {
		return "", nil, fmt.Errorf("content is required")
	}
	if boolArg(args, "create_dirs", true) {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "", nil, err
		}
	}
	if !boolArg(args, "overwrite", true) {
		if _, err := os.Stat(path); err == nil {
			return "", nil, fmt.Errorf("file exists and overwrite is false")
		}
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", nil, err
	}
	return "wrote " + path, map[string]any{"path": path, "bytes": len(content)}, nil
}

func (EditFile) Name() string        { return "edit_file" }
func (EditFile) Description() string { return "Replace exact text in a file." }
func (EditFile) InputSchema() map[string]any {
	return objectSchema(map[string]any{"path": stringSchema("File path."), "old_text": stringSchema("Exact text to replace."), "new_text": stringSchema("Replacement text."), "replace_all": boolSchema("Replace all occurrences.")}, []string{"path", "old_text", "new_text"})
}
func (t EditFile) Call(args map[string]any) (string, any, error) {
	path, err := t.guard.Resolve(stringArg(args, "path", ""), false)
	if err != nil {
		return "", nil, err
	}
	oldText := stringArg(args, "old_text", "")
	newText, ok := args["new_text"].(string)
	if oldText == "" || !ok {
		return "", nil, fmt.Errorf("old_text and new_text are required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	content := string(data)
	count := strings.Count(content, oldText)
	if count == 0 {
		return "", nil, fmt.Errorf("old_text not found")
	}
	if count > 1 && !boolArg(args, "replace_all", false) {
		return "", nil, fmt.Errorf("old_text appears %d times; set replace_all=true or make it unique", count)
	}
	n := 1
	if boolArg(args, "replace_all", false) {
		n = -1
	}
	updated := strings.Replace(content, oldText, newText, n)
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("edited %s (%d replacement(s))", path, count), map[string]any{"path": path, "replacements": count}, nil
}

func (CopyPath) Name() string { return "copy" }
func (CopyPath) Description() string {
	return "Copy a file or directory."
}
func (CopyPath) InputSchema() map[string]any {
	return objectSchema(map[string]any{
		"src":       stringSchema("Source file or directory path."),
		"dst":       stringSchema("Destination path."),
		"overwrite": boolSchema("Overwrite an existing destination."),
	}, []string{"src", "dst"})
}
func (t CopyPath) Call(args map[string]any) (string, any, error) {
	src, err := t.guard.Resolve(stringArg(args, "src", ""), true)
	if err != nil {
		return "", nil, err
	}
	dst, err := t.guard.Resolve(stringArg(args, "dst", ""), true)
	if err != nil {
		return "", nil, err
	}
	if err := copyAny(src, dst, boolArg(args, "overwrite", false)); err != nil {
		return "", nil, err
	}
	meta, err := metadataForPath(dst)
	if err != nil {
		return "", nil, err
	}
	meta["src"] = src
	return fmt.Sprintf("copied %s to %s", src, dst), meta, nil
}

func (MovePath) Name() string { return "move" }
func (MovePath) Description() string {
	return "Move or rename a file or directory."
}
func (MovePath) InputSchema() map[string]any {
	return objectSchema(map[string]any{
		"src":       stringSchema("Source file or directory path."),
		"dst":       stringSchema("Destination path."),
		"overwrite": boolSchema("Overwrite an existing destination."),
	}, []string{"src", "dst"})
}
func (t MovePath) Call(args map[string]any) (string, any, error) {
	src, err := t.guard.Resolve(stringArg(args, "src", ""), true)
	if err != nil {
		return "", nil, err
	}
	dst, err := t.guard.Resolve(stringArg(args, "dst", ""), true)
	if err != nil {
		return "", nil, err
	}
	if !boolArg(args, "overwrite", false) {
		if _, err := os.Stat(dst); err == nil {
			return "", nil, fmt.Errorf("destination exists and overwrite is false")
		}
	} else if err := os.RemoveAll(dst); err != nil && !os.IsNotExist(err) {
		return "", nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return "", nil, err
	}
	if err := os.Rename(src, dst); err != nil {
		return "", nil, err
	}
	meta, err := metadataForPath(dst)
	if err != nil {
		return "", nil, err
	}
	meta["src"] = src
	return fmt.Sprintf("moved %s to %s", src, dst), meta, nil
}

func (ViewImage) Name() string { return "view_image" }
func (ViewImage) Description() string {
	return "Read a local image and return a data URL plus detail hint."
}
func (ViewImage) InputSchema() map[string]any {
	return objectSchema(map[string]any{"path": stringSchema("Local image path."), "detail": stringSchema("Optional detail override; only `original` is meaningful.")}, []string{"path"})
}
func (t ViewImage) Call(args map[string]any) (string, any, error) {
	path, err := t.guard.Resolve(stringArg(args, "path", ""), false)
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	detail := any(nil)
	if stringArg(args, "detail", "") == "original" {
		detail = "original"
	}
	url := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
	return "loaded image " + path, map[string]any{"image_url": url, "detail": detail}, nil
}

func fileSize(info os.FileInfo) int64 {
	if info == nil {
		return 0
	}
	return info.Size()
}

func metadataForPath(path string) (map[string]any, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"path":   path,
		"size":   info.Size(),
		"mtime":  info.ModTime().Format("2006-01-02T15:04:05.999999999Z07:00"),
		"is_dir": info.IsDir(),
	}, nil
}

func copyAny(src, dst string, overwrite bool) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !overwrite {
		if _, err := os.Stat(dst); err == nil {
			return fmt.Errorf("destination exists and overwrite is false")
		}
	} else if err := os.RemoveAll(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	if info.IsDir() {
		rel, err := filepath.Rel(src, dst)
		if err == nil && (rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))) {
			return fmt.Errorf("cannot copy directory into itself")
		}
		return copyDir(src, dst, info.Mode())
	}
	return copyFile(src, dst, info.Mode())
}

func copyDir(src, dst string, perm os.FileMode) error {
	if err := os.MkdirAll(dst, perm); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "." {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
