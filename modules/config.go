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

// ParseBytes parses a human-readable byte string (e.g. "500MB", "1GB", "2GiB", "1073741824")
// into int64 bytes. If val is empty, malformed, or negative, fallback is returned.
func ParseBytes(val string, fallback int64) int64 {
	val = strings.TrimSpace(val)
	if val == "" {
		return fallback
	}
	if n, err := strconv.ParseInt(val, 10, 64); err == nil {
		if n < 0 {
			return fallback
		}
		return n
	}
	lower := strings.ToLower(val)
	var multiplier int64 = 1
	var numStr string
	switch {
	case strings.HasSuffix(lower, "tib"):
		multiplier = 1 << 40
		numStr = strings.TrimSuffix(lower, "tib")
	case strings.HasSuffix(lower, "tb"):
		multiplier = 1 << 40
		numStr = strings.TrimSuffix(lower, "tb")
	case strings.HasSuffix(lower, "t"):
		multiplier = 1 << 40
		numStr = strings.TrimSuffix(lower, "t")
	case strings.HasSuffix(lower, "gib"):
		multiplier = 1 << 30
		numStr = strings.TrimSuffix(lower, "gib")
	case strings.HasSuffix(lower, "gb"):
		multiplier = 1 << 30
		numStr = strings.TrimSuffix(lower, "gb")
	case strings.HasSuffix(lower, "g"):
		multiplier = 1 << 30
		numStr = strings.TrimSuffix(lower, "g")
	case strings.HasSuffix(lower, "mib"):
		multiplier = 1 << 20
		numStr = strings.TrimSuffix(lower, "mib")
	case strings.HasSuffix(lower, "mb"):
		multiplier = 1 << 20
		numStr = strings.TrimSuffix(lower, "mb")
	case strings.HasSuffix(lower, "m"):
		multiplier = 1 << 20
		numStr = strings.TrimSuffix(lower, "m")
	case strings.HasSuffix(lower, "kib"):
		multiplier = 1 << 10
		numStr = strings.TrimSuffix(lower, "kib")
	case strings.HasSuffix(lower, "kb"):
		multiplier = 1 << 10
		numStr = strings.TrimSuffix(lower, "kb")
	case strings.HasSuffix(lower, "k"):
		multiplier = 1 << 10
		numStr = strings.TrimSuffix(lower, "k")
	case strings.HasSuffix(lower, "b"):
		multiplier = 1
		numStr = strings.TrimSuffix(lower, "b")
	default:
		return fallback
	}
	numStr = strings.TrimSpace(numStr)
	if f, err := strconv.ParseFloat(numStr, 64); err == nil && f >= 0 {
		return int64(f * float64(multiplier))
	}
	return fallback
}

func envOrDefaultBytes(key string, fallback int64) int64 {
	value := os.Getenv(key)
	return ParseBytes(value, fallback)
}

func envOrDefaultInt64(key string, fallback int64) int64 {
	return envOrDefaultBytes(key, fallback)
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
