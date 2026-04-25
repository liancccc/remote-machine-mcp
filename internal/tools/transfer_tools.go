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
	return "Create a resumable remote HTTP upload session for advanced local-to-remote transfer workflows. For ordinary file uploads, prefer the direct HTTP endpoint /transfer/upload?file_path=...&overwrite=true so curl can upload the file without prepare or chunk management. If you need to upload a directory, archive it first, preferably as zip."
}
func (PrepareUpload) InputSchema() map[string]any {
	return objectSchema(map[string]any{
		"path":        stringSchema("Remote destination path on this machine."),
		"entity_type": stringSchema("What the local source represents: file or directory. Prefer file; for directories, archive them first on the local side, preferably as zip, and upload the archive as a file when possible."),
		"size":        numberSchema("Total bytes that the local client will upload. If uploading a directory, this is the size of the archive bytes being sent, preferably a zip archive."),
		"overwrite":   boolSchema("Overwrite an existing remote destination."),
		"archive":     stringSchema("Archive format for directory uploads. Only zip is supported, but agents should usually avoid raw directory uploads and transfer an archive file instead."),
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
	return "Create a resumable remote HTTP download session for advanced remote-to-local transfer workflows. For ordinary file downloads, prefer the direct HTTP endpoint /transfer/download?file_path=... so curl or wget can fetch the file without prepare or chunk management. If you need a directory, archive it first on the remote machine, preferably as zip."
}
func (PrepareDownload) InputSchema() map[string]any {
	return objectSchema(map[string]any{
		"path": stringSchema("Remote file path on this machine. Directory paths may work, but agents should usually archive directories first with remote shell commands, preferably as zip, and then download the archive file."),
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
