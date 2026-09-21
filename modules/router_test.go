package modules

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestRouterRoutes(t *testing.T) {
	mockUI := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte(`<!DOCTYPE html><html><body>Main Dashboard</body></html>`),
		},
		"agent.html": &fstest.MapFile{
			Data: []byte(`<!DOCTYPE html><html><head><title>Agent Prompt — Originless</title></head><body>Agent</body></html>`),
		},
	}

	router := NewRouter(nil, mockUI)

	t.Run("GET / serves dashboard", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Main Dashboard") {
			t.Errorf("expected body to contain 'Main Dashboard', got %q", body)
		}
	})

	t.Run("GET /library.html redirects to /", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/library.html", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("expected status 301, got %d", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/" {
			t.Errorf("expected Location '/', got %q", loc)
		}
	})

	t.Run("GET /agent redirect to /agent.html", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/agent", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("expected status 301, got %d", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/agent.html" {
			t.Errorf("expected Location '/agent.html', got %q", loc)
		}
	})

	t.Run("GET /agent.html serves agent page", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/agent.html", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Agent") {
			t.Errorf("expected body to contain 'Agent', got %q", body)
		}
	})

	t.Run("GET /events and /records routes exist", func(t *testing.T) {
		for _, path := range []string{"/events", "/records"} {
			req := httptest.NewRequest("GET", path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			// Manager is nil so it returns 503 "records store unavailable", confirming route matched handler
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("path %s expected status 503 from mock, got %d", path, rec.Code)
			}
		}
	})
}
