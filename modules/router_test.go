package modules

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestRouterExamplesEndpoints(t *testing.T) {
	mockUI := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte(`<!DOCTYPE html><html><body>Main Dashboard</body></html>`),
		},
	}
	mockExamples := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte(`<!DOCTYPE html><html><head><title>Originless Examples</title></head><body>Examples Index</body></html>`),
		},
		"upload-file.html": &fstest.MapFile{
			Data: []byte(`<!DOCTYPE html><html><head><title>Single File Uploader — Originless</title><meta name="description" content="Upload files to IPFS" /></head><body>File Uploader</body></html>`),
		},
	}

	router := NewRouter(nil, nil, mockUI, mockExamples)

	// 1. Test /api/examples
	t.Run("GET /api/examples", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/examples", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}

		var resp struct {
			Status string        `json:"status"`
			Tools  []ExampleTool `json:"tools"`
			Count  int           `json:"count"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode JSON response: %v", err)
		}

		if resp.Status != "success" {
			t.Errorf("expected status 'success', got %q", resp.Status)
		}
		if resp.Count < 1 {
			t.Errorf("expected at least 1 tool, got %d", resp.Count)
		}
	})

	// 2. Test /examples redirect
	t.Run("GET /examples redirect", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/examples", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("expected status 301, got %d", rec.Code)
		}
		location := rec.Header().Get("Location")
		if location != "/examples/" {
			t.Errorf("expected Location '/examples/', got %q", location)
		}
	})

	// 3. Test /examples/ serving index.html
	t.Run("GET /examples/", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/examples/", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Examples") {
			t.Errorf("expected body to contain Examples, got %q", body)
		}
	})

	// 4. Test /examples/upload-file.html serving tool page
	t.Run("GET /examples/upload-file.html", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/examples/upload-file.html", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "File Uploader") && !strings.Contains(body, "Upload") {
			t.Errorf("expected body to contain tool contents, got %q", body)
		}
	})

	// 5. Test /agent redirect to the standalone agent prompt page
	t.Run("GET /agent redirect", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/agent", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("expected status 301, got %d", rec.Code)
		}
		location := rec.Header().Get("Location")
		if location != "/agent.html" {
			t.Errorf("expected Location '/agent.html', got %q", location)
		}
	})

	// 6. Test /agent.html serves the standalone agent page
	t.Run("GET /agent.html serves agent page", func(t *testing.T) {
		mockAgentUI := fstest.MapFS{
			"agent.html": &fstest.MapFile{
				Data: []byte(`<!DOCTYPE html><html><head><title>Agent Prompt — Originless</title></head><body>Agent</body></html>`),
			},
		}
		router := NewRouter(nil, nil, mockAgentUI, &fstest.MapFS{})
		req := httptest.NewRequest("GET", "/agent.html", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Agent") {
			t.Errorf("expected body to contain Agent, got %q", body)
		}
	})
}
