package modules

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// NewGatewayProxy reverse-proxies path and subdomain-style IPFS requests to
// the local Kubo HTTP gateway (typically http://127.0.0.1:8080).
func NewGatewayProxy(target string) *httputil.ReverseProxy {
	backend, err := url.Parse(target)
	if err != nil {
		log.Printf("[gateway] invalid IPFS_GATEWAY %q: %v", target, err)
		return nil
	}

	proxy := httputil.NewSingleHostReverseProxy(backend)
	proxy.FlushInterval = 100 * time.Millisecond
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("[gateway] proxy error path=%s err=%v", r.URL.Path, err)
		setCORS(w)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":   "IPFS gateway unavailable",
			"details": err.Error(),
		})
	}
	return proxy
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		scheme = strings.ToLower(strings.TrimSpace(strings.Split(proto, ",")[0]))
	}
	host := r.Host
	if fwd := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); fwd != "" {
		host = strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	if host == "" {
		host = fmt.Sprintf("127.0.0.1:%d", Port)
	}
	return scheme + "://" + host
}

func gatewayStatus(r *http.Request) map[string]any {
	status := map[string]any{
		"enabled": GatewayEnabled,
		"serving": GatewayEnabled,
		"path":    "/ipfs/",
		"ipns":    "/ipns/",
	}
	if GatewayEnabled {
		base := requestBaseURL(r)
		status["url"] = base + "/ipfs/{cid}"
		status["ipnsUrl"] = base + "/ipns/{name}"
		status["kubo"] = strings.TrimRight(IPFSGateway, "/") + "/ipfs/{cid}"
	}
	return status
}

func (h *Handler) Gateway(w http.ResponseWriter, r *http.Request) {
	if !GatewayEnabled || h.gateway == nil {
		setCORS(w)
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error":  "IPFS gateway is disabled",
			"status": "disabled",
			"hint":   "Set ENABLE_GATEWAY=true (the default) to serve /ipfs and /ipns from this node",
		})
		return
	}
	h.gateway.ServeHTTP(w, r)
}

func gatewayMetricPath(path string) string {
	switch {
	case path == "/ipfs" || strings.HasPrefix(path, "/ipfs/"):
		return "/ipfs"
	case path == "/ipns" || strings.HasPrefix(path, "/ipns/"):
		return "/ipns"
	case path == "/down" || strings.HasPrefix(path, "/down/"):
		return "/down"
	case path == "/records" || strings.HasPrefix(path, "/records/"):
		return "/records"
	default:
		return path
	}
}

func gatewayRequestLabel(r *http.Request) string {
	if isSubdomainGatewayHost(r.Host) {
		if strings.Contains(hostnameOnly(r.Host), ".ipns.") {
			return "/ipns"
		}
		return "/ipfs"
	}
	return gatewayMetricPath(r.URL.Path)
}

func isGatewayPath(path string) bool {
	return path == "/ipfs" || strings.HasPrefix(path, "/ipfs/") ||
		path == "/ipns" || strings.HasPrefix(path, "/ipns/")
}

func isGatewayRequest(r *http.Request) bool {
	return isGatewayPath(r.URL.Path) || isSubdomainGatewayHost(r.Host)
}

// isSubdomainGatewayHost reports Kubo origin-isolation hosts such as
// {cid}.ipfs.localhost:3232. Browsers resolve *.localhost to loopback, so
// those requests hit this process — they must go to Kubo, not the dashboard.
func isSubdomainGatewayHost(host string) bool {
	name := hostnameOnly(host)
	return strings.Contains(name, ".ipfs.") || strings.Contains(name, ".ipns.")
}

func hostnameOnly(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "[") {
		if end := strings.Index(host, "]"); end > 1 {
			return strings.ToLower(host[1:end])
		}
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return strings.ToLower(h)
	}
	return strings.ToLower(host)
}
