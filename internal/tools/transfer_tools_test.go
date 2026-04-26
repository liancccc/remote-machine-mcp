package tools

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestPrepareUploadCreatesSessionUsableByHTTPTransfer(t *testing.T) {
	guard := testGuard(t)
	transfers := NewTransferManager()
	service := NewTransferService(guard, transfers, "/transfer")
	tool := PrepareUpload{service: service}
	handler := NewHTTPTransferHandlerWithManager(guard, transfers)

	_, structured, err := tool.Call(map[string]any{
		"path":        "uploads/blob.bin",
		"entity_type": "file",
		"size":        float64(4),
		"overwrite":   true,
	})
	if err != nil {
		t.Fatalf("prepare_upload failed: %v", err)
	}
	plan := structured.(map[string]any)
	session := plan["session"].(map[string]any)
	uploadID := int(session["upload_id"].(int))

	req := httptest.NewRequest(http.MethodPut, "/transfer/upload-sessions/"+itoa(uploadID)+"/chunks?offset=0", bytes.NewReader([]byte("test")))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("chunk upload failed: %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/transfer/upload-sessions/"+itoa(uploadID)+"/complete", bytes.NewReader([]byte(`{}`)))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete upload failed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPrepareDownloadReturnsLocalHandoffPlan(t *testing.T) {
	guard := testGuard(t)
	transfers := NewTransferManager()
	service := NewTransferService(guard, transfers, "/transfer")
	tool := PrepareDownload{service: service}
	if err := os.WriteFile(filepath.Join(guard.CurrentDir, "artifact.bin"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	_, structured, err := tool.Call(map[string]any{
		"path": "artifact.bin",
	})
	if err != nil {
		t.Fatalf("prepare_download failed: %v", err)
	}
	plan := structured.(map[string]any)
	if plan["type"] != "remote_http_download_plan" {
		t.Fatalf("unexpected plan type: %#v", plan["type"])
	}
	session := plan["session"].(map[string]any)
	if err := transfers.abortDownload(session["download_id"].(int)); err != nil {
		t.Fatalf("abort download session: %v", err)
	}
	httpInfo := plan["http"].(map[string]any)
	if httpInfo["chunk_path"] == "" || httpInfo["authorization"] == "" {
		t.Fatalf("expected http handoff fields, got %#v", httpInfo)
	}
}

func itoa(v int) string {
	return strconv.Itoa(v)
}
