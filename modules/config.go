package modules

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	Port             = 3232
	Host             = "0.0.0.0"
	MaxConcurrentOps = 3
	JanitorInterval  = 60 // minutes

	// PinThresholdPercent is the % of the storage quota at which the janitor
	// starts evicting LRU blobs.
	PinThresholdPercent = 75
)

var (
	StorageMax      string
	StorageMaxBytes int64
	FileLimit       int64
	BlobDir         = "/data/blobs"
)

// Blob retention policy (size-weighted):
//   min_age  = 30 days  (applies at max_size)
//   max_age  = 1 year   (applies at size 0)
//   max_size = 512 MiB  (normalization point + upload cap)
// retention(size) = min_age + (min_age - max_age) * (size/max_size - 1)^3
// Small blobs are retained longest; the guarantee decays cubically to
// min_age as the blob approaches max_size.
const (
	BlobMinAgeDays   = 30
	BlobMaxAgeDays   = 365
	BlobMaxSizeBytes = 512 * 1024 * 1024 // 512 MiB
)

var sizePattern = regexp.MustCompile(`(?i)^(\d+(?:\.\d+)?)\s*(B|KB|MB|GB|TB)$`)

func init() {
	StorageMax = envOrDefault("STORAGE_MAX", "100GB")

	storageMaxBytes, err := ParseSize(StorageMax)
	if err != nil {
		panic(fmt.Sprintf("invalid STORAGE_MAX: %v", err))
	}

	StorageMaxBytes = storageMaxBytes
	// Per-blob uploads are capped at BlobMaxSizeBytes; the quota-derived
	// cap still applies on small STORAGE_MAX deployments.
	FileLimit = storageMaxBytes / 100
	if FileLimit > BlobMaxSizeBytes {
		FileLimit = BlobMaxSizeBytes
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
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

func ParseSize(sizeStr string) (int64, error) {
	match := sizePattern.FindStringSubmatch(sizeStr)
	if match == nil {
		return 0, fmt.Errorf("invalid size format: %s", sizeStr)
	}

	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, err
	}

	var unit int64
	switch match[2] {
	case "B", "b":
		unit = 1
	case "KB", "Kb", "kb":
		unit = 1024
	case "MB", "Mb", "mb":
		unit = 1024 * 1024
	case "GB", "Gb", "gb":
		unit = 1024 * 1024 * 1024
	case "TB", "Tb", "tb":
		unit = 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("unknown size unit: %s", match[2])
	}

	return int64(math.Floor(value * float64(unit))), nil
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
