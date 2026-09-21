package modules

import (
	"net"
	"net/http"
	"strings"
)

func gatewayStatus(r *http.Request) map[string]any {
	return map[string]any{
		"enabled":     false,
		"serving":     false,
		"recommended": "https://github.com/ipfs/rainbow",
	}
}

// Gateway responds to /ipfs and /ipns requests with 404 and recommendation to use Rainbow.
func (h *Handler) Gateway(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	writeJSON(w, http.StatusNotFound, map[string]any{
		"error":       "IPFS HTTP gateway is disabled on this node",
		"status":      "disabled",
		"recommended": "https://github.com/ipfs/rainbow",
		"hint":        "For gateway fetching, use Rainbow (https://github.com/ipfs/rainbow) or a public gateway (e.g. inbrowser.link or ipfs.io)",
	})
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
// those requests hit this process.
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
