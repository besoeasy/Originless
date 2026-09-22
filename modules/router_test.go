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
		"agent.txt": &fstest.MapFile{
			Data: []byte("Originless — Agent Skill (plain text)\nUse this node for all event and blob operations.\n"),
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

	t.Run("GET /agent redirect to /agent.txt", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/agent", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("expected status 301, got %d", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/agent.txt" {
			t.Errorf("expected Location '/agent.txt', got %q", loc)
		}
	})

	t.Run("removed legacy routes stay gone", func(t *testing.T) {		for _, path := range []string{
			"/records", "/records/stream", "/agent.html", "/library.html",
		} {
			req := httptest.NewRequest("GET", path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code == http.StatusServiceUnavailable {
				t.Errorf("path %s still routed to a handler (got 503), want 404", path)
			}
			if rec.Code != http.StatusNotFound {
				t.Errorf("path %s expected 404, got %d", path, rec.Code)
			}
		}
	})

	t.Run("GET /events route exists", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/events", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		// Manager is nil so it returns 503 "records store unavailable", confirming route matched handler
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("path /events expected status 503 from mock, got %d", rec.Code)
		}
	})

	t.Run("GET /blob/{hash} route exists", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/blob/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		// Nil store -> 503 confirms the route matched Down (not the static fallback).
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("path /blob/{hash} expected status 503 from mock, got %d", rec.Code)
		}
	})
}
