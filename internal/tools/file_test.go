package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"remote-machine-mcp/internal/filesystem"
)

func TestWriteReadAndEditFile(t *testing.T) {
	root := t.TempDir()
	guard := &filesystem.Guard{CurrentDir: root, HomeDir: root}

	write := WriteFile{guard: guard}
	if _, _, err := write.Call(map[string]any{
		"path":    "notes/todo.txt",
		"content": "alpha\nbeta\n",
	}); err != nil {
		t.Fatalf("write_file failed: %v", err)
	}

	read := ReadFile{guard: guard}
	text, _, err := read.Call(map[string]any{"path": "notes/todo.txt"})
	if err != nil {
		t.Fatalf("read_file failed: %v", err)
	}
	if text != "alpha\nbeta\n" {
		t.Fatalf("unexpected read_file output: %q", text)
	}

	edit := EditFile{guard: guard}
	if _, _, err := edit.Call(map[string]any{
		"path":     "notes/todo.txt",
		"old_text": "beta",
		"new_text": "gamma",
	}); err != nil {
		t.Fatalf("edit_file failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "notes", "todo.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alpha\ngamma\n" {
		t.Fatalf("unexpected edited content: %q", string(data))
	}
}

func TestListDirUsesOneIndexedOffset(t *testing.T) {
	root := t.TempDir()
	guard := &filesystem.Guard{CurrentDir: root, HomeDir: root}
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}

	list := ListDir{guard: guard}
	text, structured, err := list.Call(map[string]any{
		"dir_path": ".",
		"offset":   2,
		"limit":    2,
	})
	if err != nil {
		t.Fatalf("list_dir failed: %v", err)
	}
	if !strings.Contains(text, "2. [file] b.txt") || !strings.Contains(text, "3. [file] c.txt") {
		t.Fatalf("unexpected list_dir output:\n%s", text)
	}
	entries := structured.(map[string]any)["entries"].([]map[string]any)
	if len(entries) != 2 {
		t.Fatalf("unexpected entry count: %d", len(entries))
	}
}

func TestCopyAndMovePath(t *testing.T) {
	root := t.TempDir()
	guard := &filesystem.Guard{CurrentDir: root, HomeDir: root}
	if err := os.WriteFile(filepath.Join(root, "src.txt"), []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}

	copyTool := CopyPath{guard: guard}
	if _, _, err := copyTool.Call(map[string]any{
		"src": "src.txt",
		"dst": "copies/src.txt",
	}); err != nil {
		t.Fatalf("copy failed: %v", err)
	}

	moveTool := MovePath{guard: guard}
	if _, _, err := moveTool.Call(map[string]any{
		"src": "copies/src.txt",
		"dst": "moved/final.txt",
	}); err != nil {
		t.Fatalf("move failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "moved", "final.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "payload" {
		t.Fatalf("unexpected moved content: %q", string(data))
	}
	if _, err := os.Stat(filepath.Join(root, "copies", "src.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected source to be moved away, stat err=%v", err)
	}
}
