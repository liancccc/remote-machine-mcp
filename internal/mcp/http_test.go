package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthorizedBearerToken(t *testing.T) {
	req := httptest.NewRequest("POST", "http://127.0.0.1:8765/mcp", nil)
	req.Header.Set("Authorization", "Bearer secret-token")

	if !authorized(req, "secret-token") {
		t.Fatal("expected bearer token to authorize")
	}
}

func TestAuthorizedRejectsMissingOrWrongToken(t *testing.T) {
	for _, header := range []string{"", "Basic secret-token", "Bearer wrong-token"} {
		req := httptest.NewRequest("POST", "http://127.0.0.1:8765/mcp", nil)
		req.Header.Set("Authorization", header)
		if authorized(req, "secret-token") {
			t.Fatalf("expected header %q to be rejected", header)
		}
	}
}

func TestValidOriginAllowsEmptySameHostAndLoopback(t *testing.T) {
	for _, origin := range []string{"", "http://127.0.0.1:8765", "http://localhost:8765"} {
		req := httptest.NewRequest("POST", "http://127.0.0.1:8765/mcp", nil)
		req.Header.Set("Origin", origin)
		if !validOrigin(req) {
			t.Fatalf("expected origin %q to be allowed", origin)
		}
	}
}

func TestValidOriginRejectsCrossSiteOrigin(t *testing.T) {
	req := httptest.NewRequest("POST", "http://127.0.0.1:8765/mcp", nil)
	req.Header.Set("Origin", "https://example.com")
	if validOrigin(req) {
		t.Fatal("expected cross-site origin to be rejected")
	}
}

func TestAuthorizedHandlerProtectsTransferPath(t *testing.T) {
	handler := authorizedHandler("secret-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest("POST", "http://127.0.0.1:8765/transfer/upload-sessions", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d", rec.Code)
	}

	req = httptest.NewRequest("POST", "http://127.0.0.1:8765/transfer/upload-sessions", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected handler success, got %d", rec.Code)
	}
}

func TestAuthorizedHandlerRejectsInvalidOriginOnTransferPath(t *testing.T) {
	handler := authorizedHandler("secret-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest("POST", "http://127.0.0.1:8765/transfer/upload-sessions", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden, got %d", rec.Code)
	}
}
