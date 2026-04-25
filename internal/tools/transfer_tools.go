package tools

import (
	"encoding/json"
	"fmt"

	"remote-machine-mcp/internal/filesystem"
)

type PrepareUpload struct {
	service *TransferService
}

type PrepareDownload struct {
	service *TransferService
}

func NewTransferTools(guard *filesystem.Guard, transfers *TransferManager, transferPath string) []any {
	service := NewTransferService(guard, transfers, transferPath)
	return []any{
		PrepareUpload{service: service},
		PrepareDownload{service: service},
	}
}

func (PrepareUpload) Name() string { return "prepare_upload" }
func (PrepareUpload) Description() string {
	return "Create a remote HTTP upload session for local-to-remote transfer so the local client can stream bytes without making the agent manage offsets or chunk loops."
}
func (PrepareUpload) InputSchema() map[string]any {
	return objectSchema(map[string]any{
		"path":        stringSchema("Remote destination path on this machine."),
		"entity_type": stringSchema("What the local source represents: file or directory."),
		"size":        numberSchema("Total bytes that the local client will upload. For directories, this is the size of the ZIP archive that will be sent."),
		"overwrite":   boolSchema("Overwrite an existing remote destination."),
		"archive":     stringSchema("Archive format for directory uploads. Only zip is supported."),
	}, []string{"path", "entity_type", "size"})
}
func (t PrepareUpload) Call(args map[string]any) (string, any, error) {
	resp, err := t.service.CreateUploadSession(createUploadRequest{
		Path:       stringArg(args, "path", ""),
		EntityType: stringArg(args, "entity_type", ""),
		Size:       int64(intArg(args, "size", -1)),
		Overwrite:  boolArg(args, "overwrite", false),
		Archive:    stringArg(args, "archive", ""),
	})
	if err != nil {
		return "", nil, err
	}
	out := transferPlanFromUpload(resp, t.service.transferPath)
	body, _ := json.MarshalIndent(out, "", "  ")
	return string(body), out, nil
}

func (PrepareDownload) Name() string { return "prepare_download" }
func (PrepareDownload) Description() string {
	return "Create a remote HTTP download session for remote-to-local transfer so the local client can fetch bytes without making the agent manage offsets or chunk loops."
}
func (PrepareDownload) InputSchema() map[string]any {
	return objectSchema(map[string]any{
		"path": stringSchema("Remote file or directory path on this machine."),
	}, []string{"path"})
}
func (t PrepareDownload) Call(args map[string]any) (string, any, error) {
	resp, err := t.service.CreateDownloadSession(createDownloadRequest{
		Path: stringArg(args, "path", ""),
	})
	if err != nil {
		return "", nil, err
	}
	out := transferPlanFromDownload(resp, t.service.transferPath)
	body, _ := json.MarshalIndent(out, "", "  ")
	return string(body), out, nil
}

func transferPlanFromUpload(resp map[string]any, transferPath string) map[string]any {
	uploadID := int(resp["upload_id"].(int))
	return map[string]any{
		"type":             "remote_http_upload_plan",
		"handoff_required": true,
		"message":          "Local transport should upload bytes to the returned chunk endpoint, then finalize via the complete endpoint using the same host and bearer token as the MCP HTTP server.",
		"session": map[string]any{
			"upload_id":        uploadID,
			"path":             resp["path"],
			"entity_type":      resp["entity_type"],
			"archive":          resp["archive"],
			"size":             resp["size"],
			"overwrite":        resp["overwrite"],
			"chunk_size_limit": resp["chunk_size_limit"],
			"ttl_seconds":      resp["ttl_seconds"],
		},
		"http": map[string]any{
			"base_path":          transferPath,
			"create_path":        transferPath + "/upload-sessions",
			"chunk_path":         fmt.Sprintf("%s/upload-sessions/%d/chunks", transferPath, uploadID),
			"chunk_query":        "offset=<byte_offset>",
			"complete_path":      fmt.Sprintf("%s/upload-sessions/%d/complete", transferPath, uploadID),
			"abort_path":         fmt.Sprintf("%s/upload-sessions/%d", transferPath, uploadID),
			"authorization":      "Reuse the same Bearer token as the current MCP HTTP server connection.",
			"host_resolution":    "Reuse the same host and port as the current MCP HTTP server connection.",
			"chunk_content_type": "application/octet-stream",
		},
	}
}

func transferPlanFromDownload(resp map[string]any, transferPath string) map[string]any {
	downloadID := int(resp["download_id"].(int))
	return map[string]any{
		"type":             "remote_http_download_plan",
		"handoff_required": true,
		"message":          "Local transport should download bytes from the returned chunk endpoint using the same host and bearer token as the MCP HTTP server.",
		"session": map[string]any{
			"download_id":      downloadID,
			"path":             resp["path"],
			"entity_type":      resp["entity_type"],
			"archive":          resp["archive"],
			"size":             resp["size"],
			"sha256":           resp["sha256"],
			"chunk_size_limit": resp["chunk_size_limit"],
			"ttl_seconds":      resp["ttl_seconds"],
		},
		"http": map[string]any{
			"base_path":          transferPath,
			"create_path":        transferPath + "/download-sessions",
			"chunk_path":         fmt.Sprintf("%s/download-sessions/%d/chunks", transferPath, downloadID),
			"chunk_query":        "offset=<byte_offset>&limit=<byte_count>",
			"abort_path":         fmt.Sprintf("%s/download-sessions/%d", transferPath, downloadID),
			"authorization":      "Reuse the same Bearer token as the current MCP HTTP server connection.",
			"host_resolution":    "Reuse the same host and port as the current MCP HTTP server connection.",
			"chunk_content_type": "application/octet-stream",
		},
	}
}
