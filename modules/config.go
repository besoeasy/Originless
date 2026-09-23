package modules

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

const (
	Port             = 3232
	Host             = "0.0.0.0"
	MaxConcurrentOps = 3
	JanitorInterval  = 60 // minutes
)

var (
	BlobDir = "/data/blobs"
	// MaxSSESubscribers caps concurrent /events/stream connections;
	// <= 0 disables the cap. Guarded atomically in TrySubscribe.
	MaxSSESubscribers int
)

// Blob lifecycle (reference-driven):
//
// A blob is owned by the events that reference it via data.blob. It is
// protected from eviction while at least one live (unexpired) event
// references it; once no live event references it, it is an orphan and
// survives at most BlobOrphanGraceDays counted from its upload before
// the janitor evicts it. There is no size-weighted retention.
const (
	BlobOrphanGraceDays = 7
)

func init() {
	MaxSSESubscribers = envOrDefaultInt("SSE_MAX_SUBSCRIBERS", 256)
}

func envOrDefaultInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func envOrDefaultInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

// envOrDefaultBool reads a truthy/falsey env var. Unset or unrecognized
// values use fallback. Accepted truthy: 1, true, yes, on. Falsey: 0, false, no, off.
func envOrDefaultBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func FormatBytes(bytes int64) string {
	sizes := []string{"Bytes", "KB", "MB", "GB", "TB"}
	if bytes == 0 {
		return "0 Bytes"
	}

	exp := int(math.Floor(math.Log(float64(bytes)) / math.Log(1024)))
	if exp >= len(sizes) {
		exp = len(sizes) - 1
	}

	value := float64(bytes) / math.Pow(1024, float64(exp))
	return fmt.Sprintf("%.2f %s", value, sizes[exp])
}
