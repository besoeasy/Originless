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
	// MaxSSESubscribers caps concurrent /records/stream connections;
	// <= 0 disables the cap. Guarded atomically in TrySubscribe.
	MaxSSESubscribers int
)

// Blob retention policy (size-weighted):
//
//	min_age  = 30 days  (applies at max_size)
//	max_age  = 1 year   (applies at size 0)
//	max_size = 512 MiB  (normalization point)
//
// retention(size) = min_age + (min_age - max_age) * (size/max_size - 1)^3
// Small blobs are retained longest; the guarantee decays cubically to
// min_age as the blob approaches max_size.
const (
	BlobMinAgeDays   = 30
	BlobMaxAgeDays   = 365
	BlobMaxSizeBytes = 512 * 1024 * 1024 // 512 MiB
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
