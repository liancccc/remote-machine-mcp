package tools

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"remote-machine-mcp/internal/filesystem"
)

const (
	defaultTransferChunkLimit = 4 * 1024 * 1024
	defaultTransferTTL        = 15 * time.Minute
)

var errTransferExpired = errors.New("transfer session expired")

type TransferManager struct {
	mu                 sync.Mutex
	nextID             int
	uploadTTL          time.Duration
	downloadTTL        time.Duration
	uploads            map[int]*uploadSession
	downloads          map[int]*downloadSession
	expiredUploadIDs   map[int]time.Time
	expiredDownloadIDs map[int]time.Time
}

type uploadSession struct {
	id         int
	path       string
	entityType string
	archive    string
	size       int64
	overwrite  bool
	tmpPath    string
	file       *os.File
	wrote      map[int64]int64
	lastUsed   time.Time
}

type downloadSession struct {
	id         int
	path       string
	entityType string
	archive    string
	tmpPath    string
	file       *os.File
	size       int64
	sha256     string
	lastUsed   time.Time
}

type HTTPTransferHandler struct {
	guard     *filesystem.Guard
	transfers *TransferManager
}

type TransferService struct {
	guard        *filesystem.Guard
	transfers    *TransferManager
	transferPath string
}

type createUploadRequest struct {
	Path       string `json:"path"`
	EntityType string `json:"entity_type"`
	Size       int64  `json:"size"`
	Overwrite  bool   `json:"overwrite"`
	Archive    string `json:"archive"`
}

type completeUploadRequest struct {
	SHA256 string `json:"sha256"`
}

type createDownloadRequest struct {
	Path string `json:"path"`
}

func NewTransferManager() *TransferManager {
	m := &TransferManager{
		nextID:             1,
		uploadTTL:          defaultTransferTTL,
		downloadTTL:        defaultTransferTTL,
		uploads:            map[int]*uploadSession{},
		downloads:          map[int]*downloadSession{},
		expiredUploadIDs:   map[int]time.Time{},
		expiredDownloadIDs: map[int]time.Time{},
	}
	go m.reap()
	return m
}

func NewHTTPTransferHandler(guard *filesystem.Guard) http.Handler {
	return NewHTTPTransferHandlerWithManager(guard, NewTransferManager())
}

func NewHTTPTransferHandlerWithManager(guard *filesystem.Guard, transfers *TransferManager) http.Handler {
	return HTTPTransferHandler{
		guard:     guard,
		transfers: transfers,
	}
}

func NewTransferService(guard *filesystem.Guard, transfers *TransferManager, transferPath string) *TransferService {
	if transferPath == "" {
		transferPath = "/transfer"
	}
	return &TransferService{
		guard:        guard,
		transfers:    transfers,
		transferPath: strings.TrimRight(transferPath, "/"),
	}
}

func (h HTTPTransferHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route := strings.TrimPrefix(r.URL.Path, "/transfer")
	switch {
	case r.Method == http.MethodGet && route == "/download":
		h.downloadFile(w, r)
	case r.Method == http.MethodPut && route == "/upload":
		h.uploadFile(w, r)
	case r.Method == http.MethodPost && route == "/upload-sessions":
		h.createUploadSession(w, r)
	case r.Method == http.MethodPut && strings.HasSuffix(route, "/chunks") && strings.HasPrefix(route, "/upload-sessions/"):
		h.uploadChunk(w, r, route)
	case r.Method == http.MethodPost && strings.HasSuffix(route, "/complete") && strings.HasPrefix(route, "/upload-sessions/"):
		h.completeUpload(w, r, route)
	case r.Method == http.MethodDelete && strings.HasPrefix(route, "/upload-sessions/"):
		h.abortUpload(w, r, route)
	case r.Method == http.MethodPost && route == "/download-sessions":
		h.createDownloadSession(w, r)
	case r.Method == http.MethodGet && strings.HasSuffix(route, "/chunks") && strings.HasPrefix(route, "/download-sessions/"):
		h.downloadChunk(w, r, route)
	case r.Method == http.MethodDelete && strings.HasPrefix(route, "/download-sessions/"):
		h.abortDownload(w, r, route)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (h HTTPTransferHandler) downloadFile(w http.ResponseWriter, r *http.Request) {
	path, err := h.guard.Resolve(r.URL.Query().Get("file_path"), false)
	if err != nil {
		writeTransferError(w, err)
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		writeTransferError(w, err)
		return
	}
	if info.IsDir() {
		http.Error(w, "file_path must be a file; archive directories with shell commands first", http.StatusBadRequest)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		writeTransferError(w, err)
		return
	}
	defer file.Close()

	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	meta, err := transferMetadataForPath(path)
	if err != nil {
		writeTransferError(w, err)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("X-Transfer-Path", path)
	w.Header().Set("X-Transfer-Sha256", meta["sha256"].(string))
	w.Header().Set("X-Transfer-Size", strconv.FormatInt(info.Size(), 10))
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}

func (h HTTPTransferHandler) uploadFile(w http.ResponseWriter, r *http.Request) {
	path, err := h.guard.Resolve(r.URL.Query().Get("file_path"), false)
	if err != nil {
		writeTransferError(w, err)
		return
	}
	overwrite, err := parseOptionalBool(r.URL.Query().Get("overwrite"), false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			http.Error(w, "destination exists and overwrite is false", http.StatusConflict)
			return
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmp, file, err := createTransferTemp(path, "file")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	removeTmp := true
	defer func() {
		if file != nil {
			_ = file.Close()
		}
		if removeTmp {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := io.Copy(file, r.Body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := file.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	file = nil
	if overwrite {
		if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	removeTmp = false
	meta, err := transferMetadataForPath(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (h HTTPTransferHandler) createUploadSession(w http.ResponseWriter, r *http.Request) {
	var req createUploadRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := NewTransferService(h.guard, h.transfers, "/transfer").CreateUploadSession(req)
	if err != nil {
		writeTransferError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h HTTPTransferHandler) uploadChunk(w http.ResponseWriter, r *http.Request, route string) {
	id, err := sessionIDFromRoute(route, "/upload-sessions/", "/chunks")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	offset, err := parseNonNegativeInt64(r.URL.Query().Get("offset"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, err := readChunkBody(r)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "exceeds") {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, err.Error(), status)
		return
	}
	session, err := h.transfers.getUpload(id)
	if err != nil {
		writeTransferError(w, err)
		return
	}
	if offset+int64(len(data)) > session.size {
		http.Error(w, "chunk exceeds declared upload size", http.StatusBadRequest)
		return
	}
	n, err := session.file.WriteAt(data, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	session.wrote[offset] = int64(n)
	h.transfers.touchUpload(id)
	committed := session.coveredBytes()
	writeJSON(w, http.StatusOK, map[string]any{
		"upload_id":       id,
		"offset":          offset,
		"bytes_received":  n,
		"committed_bytes": committed,
		"size":            session.size,
	})
}

func (h HTTPTransferHandler) completeUpload(w http.ResponseWriter, r *http.Request, route string) {
	id, err := sessionIDFromRoute(route, "/upload-sessions/", "/complete")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req completeUploadRequest
	if err := decodeJSONAllowEmpty(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	session, err := h.transfers.getUpload(id)
	if err != nil {
		writeTransferError(w, err)
		return
	}
	meta, err := finalizeUpload(session, strings.ToLower(strings.TrimSpace(req.SHA256)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := h.transfers.removeUpload(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	meta["upload_id"] = id
	writeJSON(w, http.StatusOK, meta)
}

func (h HTTPTransferHandler) abortUpload(w http.ResponseWriter, r *http.Request, route string) {
	id, err := sessionIDFromRoute(route, "/upload-sessions/", "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.transfers.abortUpload(id); err != nil {
		writeTransferError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"upload_id": id, "aborted": true})
}

func (h HTTPTransferHandler) createDownloadSession(w http.ResponseWriter, r *http.Request) {
	var req createDownloadRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := NewTransferService(h.guard, h.transfers, "/transfer").CreateDownloadSession(req)
	if err != nil {
		writeTransferError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *TransferService) CreateUploadSession(req createUploadRequest) (map[string]any, error) {
	entityType := normalizeEntityType(req.EntityType)
	if entityType == "" {
		return nil, fmt.Errorf("entity_type must be file or directory")
	}
	if req.Size < 0 {
		return nil, fmt.Errorf("size must be non-negative")
	}
	path, err := s.guard.Resolve(req.Path, entityType == "directory")
	if err != nil {
		return nil, err
	}
	if !req.Overwrite {
		if _, err := os.Stat(path); err == nil {
			return nil, fmt.Errorf("destination exists and overwrite is false")
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	archive := ""
	if entityType == "directory" {
		archive = strings.ToLower(strings.TrimSpace(req.Archive))
		if archive == "" {
			archive = "zip"
		}
		if archive != "zip" {
			return nil, fmt.Errorf("directory uploads require archive=zip")
		}
	}
	tmp, file, err := createTransferTemp(path, entityType)
	if err != nil {
		return nil, err
	}
	if err := file.Truncate(req.Size); err != nil {
		_ = file.Close()
		_ = os.Remove(tmp)
		return nil, err
	}
	session := &uploadSession{
		path:       path,
		entityType: entityType,
		archive:    archive,
		size:       req.Size,
		overwrite:  req.Overwrite,
		tmpPath:    tmp,
		file:       file,
		wrote:      map[int64]int64{},
		lastUsed:   time.Now(),
	}
	id := s.transfers.addUpload(session)
	return map[string]any{
		"upload_id":        id,
		"path":             path,
		"entity_type":      entityType,
		"archive":          archiveOrNil(archive),
		"size":             req.Size,
		"overwrite":        req.Overwrite,
		"chunk_size_limit": defaultTransferChunkLimit,
		"ttl_seconds":      int(s.transfers.uploadTTL.Seconds()),
	}, nil
}

func (s *TransferService) CreateDownloadSession(req createDownloadRequest) (map[string]any, error) {
	path, err := s.guard.Resolve(req.Path, true)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	openPath := path
	entityType := "file"
	archive := ""
	if info.IsDir() {
		entityType = "directory"
		archive = "zip"
		openPath, err = zipDirectoryTemp(path)
		if err != nil {
			return nil, err
		}
	}
	file, err := os.Open(openPath)
	if err != nil {
		if openPath != path {
			_ = os.Remove(openPath)
		}
		return nil, err
	}
	meta, err := transferMetadataForPath(openPath)
	if err != nil {
		_ = file.Close()
		if openPath != path {
			_ = os.Remove(openPath)
		}
		return nil, err
	}
	session := &downloadSession{
		path:       path,
		entityType: entityType,
		archive:    archive,
		tmpPath:    tempPathForDownload(path, openPath),
		file:       file,
		size:       meta["size"].(int64),
		sha256:     meta["sha256"].(string),
		lastUsed:   time.Now(),
	}
	id := s.transfers.addDownload(session)
	return map[string]any{
		"download_id":      id,
		"path":             path,
		"entity_type":      entityType,
		"archive":          archiveOrNil(archive),
		"size":             session.size,
		"sha256":           session.sha256,
		"chunk_size_limit": defaultTransferChunkLimit,
		"ttl_seconds":      int(s.transfers.downloadTTL.Seconds()),
	}, nil
}

func (h HTTPTransferHandler) downloadChunk(w http.ResponseWriter, r *http.Request, route string) {
	id, err := sessionIDFromRoute(route, "/download-sessions/", "/chunks")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	offset, err := parseNonNegativeInt64(r.URL.Query().Get("offset"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	limit, err := parseChunkLimit(r.URL.Query().Get("limit"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	session, err := h.transfers.getDownload(id)
	if err != nil {
		writeTransferError(w, err)
		return
	}
	buf := make([]byte, limit)
	n, readErr := session.file.ReadAt(buf, offset)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		http.Error(w, readErr.Error(), http.StatusInternalServerError)
		return
	}
	buf = buf[:n]
	nextOffset := offset + int64(n)
	eof := nextOffset >= session.size
	h.transfers.touchDownload(id)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Transfer-Download-Id", strconv.Itoa(id))
	w.Header().Set("X-Transfer-Offset", strconv.FormatInt(offset, 10))
	w.Header().Set("X-Transfer-Bytes", strconv.Itoa(n))
	w.Header().Set("X-Transfer-Next-Offset", strconv.FormatInt(nextOffset, 10))
	w.Header().Set("X-Transfer-Size", strconv.FormatInt(session.size, 10))
	w.Header().Set("X-Transfer-Sha256", session.sha256)
	w.Header().Set("X-Transfer-Eof", strconv.FormatBool(eof))
	if session.archive != "" {
		w.Header().Set("X-Transfer-Archive", session.archive)
	}
	if eof {
		_ = h.transfers.abortDownload(id)
	}
	_, _ = w.Write(buf)
}

func (h HTTPTransferHandler) abortDownload(w http.ResponseWriter, r *http.Request, route string) {
	id, err := sessionIDFromRoute(route, "/download-sessions/", "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.transfers.abortDownload(id); err != nil {
		writeTransferError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"download_id": id, "aborted": true})
}

func (m *TransferManager) addUpload(s *uploadSession) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextID
	m.nextID++
	s.id = id
	m.uploads[id] = s
	return id
}

func (m *TransferManager) getUpload(id int) (*uploadSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.expiredUploadIDs[id]; ok {
		return nil, errTransferExpired
	}
	s, ok := m.uploads[id]
	if !ok {
		return nil, fmt.Errorf("upload session %d not found", id)
	}
	return s, nil
}

func (m *TransferManager) touchUpload(id int) {
	m.mu.Lock()
	if s, ok := m.uploads[id]; ok {
		s.lastUsed = time.Now()
	}
	m.mu.Unlock()
}

func (m *TransferManager) removeUpload(id int) (*uploadSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.expiredUploadIDs[id]; ok {
		return nil, errTransferExpired
	}
	s, ok := m.uploads[id]
	if !ok {
		return nil, fmt.Errorf("upload session %d not found", id)
	}
	delete(m.uploads, id)
	return s, nil
}

func (m *TransferManager) abortUpload(id int) error {
	s, err := m.removeUpload(id)
	if err != nil {
		return err
	}
	cleanupUploadSession(s)
	return nil
}

func (m *TransferManager) addDownload(s *downloadSession) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextID
	m.nextID++
	s.id = id
	m.downloads[id] = s
	return id
}

func (m *TransferManager) getDownload(id int) (*downloadSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.expiredDownloadIDs[id]; ok {
		return nil, errTransferExpired
	}
	s, ok := m.downloads[id]
	if !ok {
		return nil, fmt.Errorf("download session %d not found", id)
	}
	return s, nil
}

func (m *TransferManager) touchDownload(id int) {
	m.mu.Lock()
	if s, ok := m.downloads[id]; ok {
		s.lastUsed = time.Now()
	}
	m.mu.Unlock()
}

func (m *TransferManager) abortDownload(id int) error {
	m.mu.Lock()
	if _, ok := m.expiredDownloadIDs[id]; ok {
		m.mu.Unlock()
		return errTransferExpired
	}
	s, ok := m.downloads[id]
	if ok {
		delete(m.downloads, id)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("download session %d not found", id)
	}
	cleanupDownloadSession(s)
	return nil
}

func (m *TransferManager) reap() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		var staleUploads []*uploadSession
		var staleDownloads []*downloadSession

		m.mu.Lock()
		for id, s := range m.uploads {
			if now.Sub(s.lastUsed) > m.uploadTTL {
				delete(m.uploads, id)
				m.expiredUploadIDs[id] = now
				staleUploads = append(staleUploads, s)
			}
		}
		for id, s := range m.downloads {
			if now.Sub(s.lastUsed) > m.downloadTTL {
				delete(m.downloads, id)
				m.expiredDownloadIDs[id] = now
				staleDownloads = append(staleDownloads, s)
			}
		}
		for id, ts := range m.expiredUploadIDs {
			if now.Sub(ts) > m.uploadTTL {
				delete(m.expiredUploadIDs, id)
			}
		}
		for id, ts := range m.expiredDownloadIDs {
			if now.Sub(ts) > m.downloadTTL {
				delete(m.expiredDownloadIDs, id)
			}
		}
		m.mu.Unlock()

		for _, s := range staleUploads {
			cleanupUploadSession(s)
		}
		for _, s := range staleDownloads {
			cleanupDownloadSession(s)
		}
	}
}

func (s *uploadSession) coveredBytes() int64 {
	type span struct {
		start int64
		end   int64
	}
	spans := make([]span, 0, len(s.wrote))
	for offset, n := range s.wrote {
		if n <= 0 {
			continue
		}
		spans = append(spans, span{start: offset, end: offset + n})
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	var covered, end int64
	for _, span := range spans {
		if span.start > end {
			return covered
		}
		if span.end > end {
			covered += span.end - end
			end = span.end
		}
	}
	return covered
}

func finalizeUpload(session *uploadSession, expectedSHA256 string) (map[string]any, error) {
	if session.file != nil {
		if err := session.file.Sync(); err != nil {
			return nil, err
		}
	}
	info, err := os.Stat(session.tmpPath)
	if err != nil {
		return nil, err
	}
	if info.Size() != session.size {
		return nil, fmt.Errorf("uploaded size mismatch: got %d want %d", info.Size(), session.size)
	}
	if covered := session.coveredBytes(); covered != session.size {
		return nil, fmt.Errorf("upload is incomplete: %d of %d bytes received", covered, session.size)
	}
	meta, err := transferMetadataForPath(session.tmpPath)
	if err != nil {
		return nil, err
	}
	if expectedSHA256 != "" && expectedSHA256 != meta["sha256"] {
		return nil, fmt.Errorf("sha256 mismatch: got %s", meta["sha256"])
	}
	if session.entityType == "directory" {
		if session.archive != "zip" {
			return nil, fmt.Errorf("directory uploads require zip archive")
		}
		if err := publishDirectoryUpload(session); err != nil {
			return nil, err
		}
	} else {
		if err := publishFileUpload(session); err != nil {
			return nil, err
		}
	}
	cleanupUploadSession(session)
	return transferMetadataForPath(session.path)
}

func publishFileUpload(session *uploadSession) error {
	if session.file != nil {
		if err := session.file.Close(); err != nil {
			return err
		}
		session.file = nil
	}
	if session.overwrite {
		if err := os.RemoveAll(session.path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(session.tmpPath, session.path); err != nil {
		return err
	}
	session.tmpPath = ""
	return nil
}

func publishDirectoryUpload(session *uploadSession) error {
	if session.file != nil {
		if err := session.file.Close(); err != nil {
			return err
		}
		session.file = nil
	}
	extractRoot, err := os.MkdirTemp(filepath.Dir(session.path), "."+filepath.Base(session.path)+".extract-*")
	if err != nil {
		return err
	}
	removeExtractRoot := true
	defer func() {
		if removeExtractRoot {
			_ = os.RemoveAll(extractRoot)
		}
	}()
	if err := extractZipArchive(session.tmpPath, extractRoot); err != nil {
		return err
	}
	publishPath, cleanupPath, err := normalizeExtractedDirectory(extractRoot)
	if err != nil {
		return err
	}
	if cleanupPath != "" {
		defer os.RemoveAll(cleanupPath)
	}
	if session.overwrite {
		if err := os.RemoveAll(session.path); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if _, err := os.Stat(session.path); err == nil {
		return fmt.Errorf("destination exists and overwrite is false")
	}
	if err := os.Rename(publishPath, session.path); err != nil {
		return err
	}
	removeExtractRoot = false
	session.tmpPath = ""
	return nil
}

func normalizeExtractedDirectory(extractRoot string) (publishPath string, cleanupPath string, err error) {
	entries, err := os.ReadDir(extractRoot)
	if err != nil {
		return "", "", err
	}
	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(extractRoot, entries[0].Name()), extractRoot, nil
	}
	publishPath, err = os.MkdirTemp(filepath.Dir(extractRoot), "."+filepath.Base(extractRoot)+".publish-*")
	if err != nil {
		return "", "", err
	}
	for _, entry := range entries {
		src := filepath.Join(extractRoot, entry.Name())
		dst := filepath.Join(publishPath, entry.Name())
		if err := os.Rename(src, dst); err != nil {
			_ = os.RemoveAll(publishPath)
			return "", "", err
		}
	}
	return publishPath, publishPath, nil
}

func cleanupUploadSession(session *uploadSession) {
	if session.file != nil {
		_ = session.file.Close()
	}
	if session.tmpPath != "" {
		_ = os.Remove(session.tmpPath)
	}
}

func cleanupDownloadSession(session *downloadSession) {
	if session.file != nil {
		_ = session.file.Close()
	}
	if session.tmpPath != "" {
		_ = os.Remove(session.tmpPath)
	}
}

func createTransferTemp(path, entityType string) (string, *os.File, error) {
	suffix := ".upload"
	if entityType == "directory" {
		suffix = ".upload.zip"
	}
	pattern := "." + filepath.Base(path) + ".*" + suffix
	file, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return "", nil, err
	}
	return file.Name(), file, nil
}

func extractZipArchive(path, dst string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, file := range reader.File {
		name, err := sanitizeArchiveName(file.Name)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, name)
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		rc, err := file.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		_ = rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func zipDirectoryTemp(root string) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(root), "."+filepath.Base(root)+".*.zip")
	if err != nil {
		return "", err
	}
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(file.Name())
		}
	}()
	writer := zip.NewWriter(file)
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(filepath.Dir(root), path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = name
		if d.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}
		w, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(w, in)
		return err
	})
	closeErr := writer.Close()
	if err == nil {
		err = closeErr
	}
	closeFileErr := file.Close()
	if err == nil {
		err = closeFileErr
	}
	if err != nil {
		return "", err
	}
	removeTmp = false
	return file.Name(), nil
}

func transferMetadataForPath(path string) (map[string]any, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return map[string]any{
			"path":   path,
			"size":   int64(0),
			"is_dir": true,
			"mtime":  info.ModTime().Format(time.RFC3339Nano),
		}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return nil, err
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return map[string]any{
		"path":   path,
		"size":   size,
		"sha256": hex.EncodeToString(hash.Sum(nil)),
		"mtime":  info.ModTime().Format(time.RFC3339Nano),
		"mime":   mimeType,
		"is_dir": false,
	}, nil
}

func archiveOrNil(archive string) any {
	if archive == "" {
		return nil
	}
	return archive
}

func normalizeEntityType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "file":
		return "file"
	case "directory", "dir", "folder":
		return "directory"
	default:
		return ""
	}
}

func parseChunkLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return defaultTransferChunkLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("limit must be an integer")
	}
	if n <= 0 {
		return 0, fmt.Errorf("limit must be positive")
	}
	if n > defaultTransferChunkLimit {
		return 0, fmt.Errorf("limit exceeds maximum chunk size")
	}
	return n, nil
}

func parseNonNegativeInt64(raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, fmt.Errorf("offset is required")
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("offset must be an integer")
	}
	if n < 0 {
		return 0, fmt.Errorf("offset must be non-negative")
	}
	return n, nil
}

func parseOptionalBool(raw string, fallback bool) (bool, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("overwrite must be true or false")
	}
	return value, nil
}

func readChunkBody(r *http.Request) ([]byte, error) {
	reader := io.LimitReader(r.Body, defaultTransferChunkLimit+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if len(data) > defaultTransferChunkLimit {
		return nil, fmt.Errorf("chunk exceeds maximum chunk size")
	}
	return data, nil
}

func sessionIDFromRoute(route, prefix, suffix string) (int, error) {
	rest := strings.TrimPrefix(route, prefix)
	if rest == route {
		return 0, fmt.Errorf("invalid transfer path")
	}
	if suffix != "" {
		if !strings.HasSuffix(rest, suffix) {
			return 0, fmt.Errorf("invalid transfer path")
		}
		rest = strings.TrimSuffix(rest, suffix)
	}
	rest = strings.Trim(rest, "/")
	if rest == "" || strings.Contains(rest, "/") {
		return 0, fmt.Errorf("invalid transfer path")
	}
	id, err := strconv.Atoi(rest)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid transfer session id")
	}
	return id, nil
}

func sanitizeArchiveName(name string) (string, error) {
	clean := filepath.Clean(strings.ReplaceAll(name, "\\", "/"))
	clean = strings.TrimPrefix(clean, "./")
	if clean == "." || clean == "" {
		return "", fmt.Errorf("archive entry path is empty")
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry escapes destination: %s", name)
	}
	return clean, nil
}

func tempPathForDownload(original, opened string) string {
	if original == opened {
		return ""
	}
	return opened
}

func decodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return fmt.Errorf("request body is required")
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if decoder.More() {
		return fmt.Errorf("request body must contain a single JSON object")
	}
	return nil
}

func decodeJSONAllowEmpty(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if decoder.More() {
		return fmt.Errorf("request body must contain a single JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeTransferError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errTransferExpired):
		http.Error(w, err.Error(), http.StatusGone)
	case strings.Contains(err.Error(), "overwrite is false"):
		http.Error(w, err.Error(), http.StatusConflict)
	case strings.Contains(err.Error(), "not found"):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}
