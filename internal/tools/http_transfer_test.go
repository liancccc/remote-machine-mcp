package tools

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"remote-machine-mcp/internal/filesystem"
)

func testGuard(t *testing.T) *filesystem.Guard {
	t.Helper()
	root := t.TempDir()
	return &filesystem.Guard{CurrentDir: root}
}

func newTransferHandler(t *testing.T) HTTPTransferHandler {
	t.Helper()
	return HTTPTransferHandler{
		guard:     testGuard(t),
		transfers: NewTransferManager(),
	}
}

func TestHTTPTransferUploadFileLifecycle(t *testing.T) {
	h := newTransferHandler(t)

	upload := createUpload(t, h, map[string]any{
		"path":        "uploads/blob.bin",
		"entity_type": "file",
		"size":        float64(10),
		"overwrite":   true,
	})
	uploadChunk(t, h, upload["upload_id"].(float64), 0, []byte("0123"))
	uploadChunk(t, h, upload["upload_id"].(float64), 4, []byte("456789"))
	completeUpload(t, h, upload["upload_id"].(float64), "")

	got, err := os.ReadFile(filepath.Join(h.guard.CurrentDir, "uploads", "blob.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "0123456789" {
		t.Fatalf("unexpected file contents: %q", string(got))
	}
}

func TestHTTPTransferUploadFileOutOfOrder(t *testing.T) {
	h := newTransferHandler(t)

	upload := createUpload(t, h, map[string]any{
		"path":        "uploads/blob.bin",
		"entity_type": "file",
		"size":        float64(10),
		"overwrite":   true,
	})
	uploadChunk(t, h, upload["upload_id"].(float64), 4, []byte("456789"))
	uploadChunk(t, h, upload["upload_id"].(float64), 0, []byte("0123"))
	completeUpload(t, h, upload["upload_id"].(float64), "")

	got, err := os.ReadFile(filepath.Join(h.guard.CurrentDir, "uploads", "blob.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "0123456789" {
		t.Fatalf("unexpected file contents: %q", string(got))
	}
}

func TestHTTPTransferRejectsIncompleteUploadAndAllowsAbort(t *testing.T) {
	h := newTransferHandler(t)

	upload := createUpload(t, h, map[string]any{
		"path":        "uploads/blob.bin",
		"entity_type": "file",
		"size":        float64(4),
		"overwrite":   true,
	})
	uploadChunk(t, h, upload["upload_id"].(float64), 2, []byte("ok"))
	req := httptest.NewRequest(http.MethodPost, "/transfer/upload-sessions/"+idString(upload["upload_id"].(float64))+"/complete", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected incomplete finalize to fail, got %d: %s", rec.Code, rec.Body.String())
	}
	tmp := findUploadTemp(t, h.guard.CurrentDir)
	if tmp == "" {
		t.Fatal("expected upload temp file to remain after failed finalize")
	}

	req = httptest.NewRequest(http.MethodDelete, "/transfer/upload-sessions/"+idString(upload["upload_id"].(float64)), nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("abort failed: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("expected temp file to be removed, stat err=%v", err)
	}
}

func TestHTTPTransferRejectsChecksumMismatch(t *testing.T) {
	h := newTransferHandler(t)

	upload := createUpload(t, h, map[string]any{
		"path":        "uploads/blob.bin",
		"entity_type": "file",
		"size":        float64(4),
		"overwrite":   true,
	})
	uploadChunk(t, h, upload["upload_id"].(float64), 0, []byte("test"))
	req := httptest.NewRequest(http.MethodPost, "/transfer/upload-sessions/"+idString(upload["upload_id"].(float64))+"/complete", bytes.NewReader([]byte(`{"sha256":"deadbeef"}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected checksum mismatch, got %d: %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodDelete, "/transfer/upload-sessions/"+idString(upload["upload_id"].(float64)), nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected abort after checksum mismatch, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPTransferDownloadResumable(t *testing.T) {
	h := newTransferHandler(t)
	payload := []byte("0123456789")
	if err := os.WriteFile(filepath.Join(h.guard.CurrentDir, "artifact.bin"), payload, 0644); err != nil {
		t.Fatal(err)
	}

	download := createDownload(t, h, "artifact.bin")
	first := downloadChunk(t, h, download["download_id"].(float64), 0, 4)
	second := downloadChunk(t, h, download["download_id"].(float64), 4, 8)
	got := append(first.body, second.body...)
	if string(got) != string(payload) {
		t.Fatalf("unexpected download contents: %q", string(got))
	}
	if second.nextOffset != int64(len(payload)) || !second.eof {
		t.Fatalf("unexpected download progress: next=%d eof=%v", second.nextOffset, second.eof)
	}
}

func TestHTTPTransferDownloadDirectoryZip(t *testing.T) {
	h := newTransferHandler(t)
	if err := os.MkdirAll(filepath.Join(h.guard.CurrentDir, "logs", "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.guard.CurrentDir, "logs", "nested", "out.txt"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	download := createDownload(t, h, "logs")
	chunk := downloadChunk(t, h, download["download_id"].(float64), 0, defaultTransferChunkLimit)
	names := zipNames(t, chunk.body)
	if !contains(names, "logs/nested/out.txt") {
		t.Fatalf("zip entries missing file: %v", names)
	}
}

func TestHTTPTransferUploadDirectoryZip(t *testing.T) {
	h := newTransferHandler(t)
	archive := buildZip(t, map[string]string{
		"payload/nested/out.txt": "ok",
	})

	upload := createUpload(t, h, map[string]any{
		"path":        "restored",
		"entity_type": "directory",
		"size":        float64(len(archive)),
		"overwrite":   true,
		"archive":     "zip",
	})
	uploadChunk(t, h, upload["upload_id"].(float64), 0, archive)
	completeUpload(t, h, upload["upload_id"].(float64), "")

	data, err := os.ReadFile(filepath.Join(h.guard.CurrentDir, "restored", "nested", "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ok" {
		t.Fatalf("unexpected uploaded directory file: %q", string(data))
	}
}

func TestHTTPTransferRejectsOverwriteFalse(t *testing.T) {
	h := newTransferHandler(t)
	target := filepath.Join(h.guard.CurrentDir, "artifact.bin")
	if err := os.WriteFile(target, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/transfer/upload-sessions", bytes.NewReader([]byte(`{"path":"artifact.bin","entity_type":"file","size":1,"overwrite":false}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected conflict, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPTransferExpiredSessionReturnsGone(t *testing.T) {
	h := newTransferHandler(t)
	upload := createUpload(t, h, map[string]any{
		"path":        "artifact.bin",
		"entity_type": "file",
		"size":        float64(1),
		"overwrite":   true,
	})
	id := int(upload["upload_id"].(float64))
	h.transfers.mu.Lock()
	session := h.transfers.uploads[id]
	delete(h.transfers.uploads, id)
	h.transfers.expiredUploadIDs[id] = time.Now()
	h.transfers.mu.Unlock()
	cleanupUploadSession(session)

	req := httptest.NewRequest(http.MethodPut, "/transfer/upload-sessions/"+strconv.Itoa(id)+"/chunks?offset=0", bytes.NewReader([]byte("x")))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("expected gone, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPTransferDirectDownloadFile(t *testing.T) {
	h := newTransferHandler(t)
	payload := []byte("hello direct download")
	target := filepath.Join(h.guard.CurrentDir, "artifact.bin")
	if err := os.WriteFile(target, payload, 0644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/transfer/download?file_path=artifact.bin", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected ok, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.Bytes(); string(got) != string(payload) {
		t.Fatalf("unexpected body: %q", string(got))
	}
	if rec.Header().Get("X-Transfer-Sha256") == "" {
		t.Fatal("expected sha256 header")
	}
}

func TestHTTPTransferDirectDownloadRejectsDirectory(t *testing.T) {
	h := newTransferHandler(t)
	if err := os.MkdirAll(filepath.Join(h.guard.CurrentDir, "logs"), 0755); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/transfer/download?file_path=logs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPTransferDirectUploadFile(t *testing.T) {
	h := newTransferHandler(t)

	req := httptest.NewRequest(http.MethodPut, "/transfer/upload?file_path=uploads/blob.bin&overwrite=true", bytes.NewReader([]byte("direct upload")))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected ok, got %d: %s", rec.Code, rec.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(h.guard.CurrentDir, "uploads", "blob.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "direct upload" {
		t.Fatalf("unexpected file contents: %q", string(got))
	}
}

func TestHTTPTransferDirectUploadRespectsOverwriteFalse(t *testing.T) {
	h := newTransferHandler(t)
	target := filepath.Join(h.guard.CurrentDir, "artifact.bin")
	if err := os.WriteFile(target, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPut, "/transfer/upload?file_path=artifact.bin&overwrite=false", bytes.NewReader([]byte("new")))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected conflict, got %d: %s", rec.Code, rec.Body.String())
	}
}

func createUpload(t *testing.T, h HTTPTransferHandler, body map[string]any) map[string]any {
	t.Helper()
	payload, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/transfer/upload-sessions", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create upload failed: %d %s", rec.Code, rec.Body.String())
	}
	return decodeBody(t, rec.Body.Bytes())
}

func uploadChunk(t *testing.T, h HTTPTransferHandler, id float64, offset int64, chunk []byte) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/transfer/upload-sessions/"+idString(id)+"/chunks?offset="+strconv.FormatInt(offset, 10), bytes.NewReader(chunk))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload chunk failed: %d %s", rec.Code, rec.Body.String())
	}
	return decodeBody(t, rec.Body.Bytes())
}

func completeUpload(t *testing.T, h HTTPTransferHandler, id float64, sha string) map[string]any {
	t.Helper()
	body := []byte(`{}`)
	if sha != "" {
		body = []byte(`{"sha256":"` + sha + `"}`)
	}
	req := httptest.NewRequest(http.MethodPost, "/transfer/upload-sessions/"+idString(id)+"/complete", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete upload failed: %d %s", rec.Code, rec.Body.String())
	}
	return decodeBody(t, rec.Body.Bytes())
}

func createDownload(t *testing.T, h HTTPTransferHandler, path string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/transfer/download-sessions", bytes.NewReader([]byte(`{"path":"`+path+`"}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create download failed: %d %s", rec.Code, rec.Body.String())
	}
	return decodeBody(t, rec.Body.Bytes())
}

type chunkResponse struct {
	body       []byte
	nextOffset int64
	eof        bool
}

func downloadChunk(t *testing.T, h HTTPTransferHandler, id float64, offset int64, limit int) chunkResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/transfer/download-sessions/"+idString(id)+"/chunks?offset="+strconv.FormatInt(offset, 10)+"&limit="+strconv.Itoa(limit), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download chunk failed: %d %s", rec.Code, rec.Body.String())
	}
	nextOffset, err := strconv.ParseInt(rec.Header().Get("X-Transfer-Next-Offset"), 10, 64)
	if err != nil {
		t.Fatalf("invalid next offset: %v", err)
	}
	eof, err := strconv.ParseBool(rec.Header().Get("X-Transfer-Eof"))
	if err != nil {
		t.Fatalf("invalid eof header: %v", err)
	}
	return chunkResponse{body: rec.Body.Bytes(), nextOffset: nextOffset, eof: eof}
}

func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode body: %v body=%s", err, string(body))
	}
	return out
}

func idString(id float64) string {
	return strconv.Itoa(int(id))
}

func findUploadTemp(t *testing.T, root string) string {
	t.Helper()
	var found string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && strings.Contains(d.Name(), ".upload") {
			found = path
			return io.EOF
		}
		return nil
	})
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	return found
}

func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipNames(t *testing.T, data []byte) []string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
	}
	return names
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
