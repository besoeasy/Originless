package modules

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func testRouter() http.Handler {
	mockUI := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("ui")},
	}
	mockExamples := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("examples")},
	}
	return NewRouter(nil, nil, mockUI, mockExamples)
}

func TestGatewayDisabledReturns404(t *testing.T) {
	router := testRouter()

	req := httptest.NewRequest(http.MethodGet, "/ipfs/QmY7Yh4UquoXHLPFo2XbhXkhBvFoPwmQUSa92pxnxjQuPU", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when gateway is disabled, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("expected JSON error body: %v", err)
	}
	if payload["status"] != "disabled" {
		t.Errorf("expected status=disabled, got %#v", payload["status"])
	}
	if payload["recommended"] != "https://github.com/ipfs/rainbow" {
		t.Errorf("expected recommended Rainbow URL, got %#v", payload["recommended"])
	}
}

func TestSubdomainGatewayDisabledReturns404(t *testing.T) {
	router := testRouter()

	req := httptest.NewRequest(http.MethodGet, "http://bafybeiabc.ipfs.localhost:3232/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when gateway is disabled, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() == "ui" {
		t.Fatal("disabled gateway must not fall through to the dashboard")
	}
}

func TestGatewayStatusReportsDisabled(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://localhost:3232/status", nil)
	got := gatewayStatus(req)
	if got["enabled"] != false || got["serving"] != false {
		t.Fatalf("expected enabled/serving false, got %#v", got)
	}
	if got["recommended"] != "https://github.com/ipfs/rainbow" {
		t.Errorf("expected recommendation, got %#v", got["recommended"])
	}
}

func TestAPIHasSingleOriginlessCORS(t *testing.T) {
	router := testRouter()

	req := httptest.NewRequest(http.MethodOptions, "/health", nil)
	req.Header.Set("Origin", "https://gupt.app")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	origins := rec.Header().Values("Access-Control-Allow-Origin")
	if len(origins) != 1 || origins[0] != "*" {
		t.Fatalf("expected a single CORS origin *, got %#v", origins)
	}
}

func TestGatewayMetricPath(t *testing.T) {
	tests := map[string]string{
		"/ipfs/QmAbc/file.txt": "/ipfs",
		"/ipfs":                "/ipfs",
		"/ipns/name":           "/ipns",
		"/status":              "/status",
	}
	for in, want := range tests {
		if got := gatewayMetricPath(in); got != want {
			t.Errorf("gatewayMetricPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsGatewayPath(t *testing.T) {
	if !isGatewayPath("/ipfs/QmX") || !isGatewayPath("/ipns/foo") {
		t.Fatal("expected /ipfs and /ipns to be gateway paths")
	}
	if isGatewayPath("/status") || isGatewayPath("/upload") {
		t.Fatal("did not expect API paths to be treated as gateway paths")
	}
}

func TestIsSubdomainGatewayHost(t *testing.T) {
	yes := []string{
		"bafybeichqkffkyfqetlaucpshgkel2wtwm57twvyvp6sp6ok45cnggxu24.ipfs.localhost:3232",
		"bafybeiabc.ipfs.localhost",
		"k51qzi.ipns.localhost:3232",
		"QmY7Yh4UquoXHLPFo2XbhXkhBvFoPwmQUSa92pxnxjQuPU.ipfs.127.0.0.1:8080",
	}
	for _, host := range yes {
		if !isSubdomainGatewayHost(host) {
			t.Errorf("expected subdomain gateway host %q", host)
		}
	}
	no := []string{
		"",
		"localhost:3232",
		"localhost",
		"127.0.0.1:3232",
		"ipfs.localhost:3232",
		"example.com",
	}
	for _, host := range no {
		if isSubdomainGatewayHost(host) {
			t.Errorf("did not expect subdomain gateway host %q", host)
		}
	}
}
