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

// BlobMinAge is the minimum retention for /up blobs before LRU may evict them.
const BlobMinAgeDays = 7

var sizePattern = regexp.MustCompile(`(?i)^(\d+(?:\.\d+)?)\s*(B|KB|MB|GB|TB)$`)

func init() {
	StorageMax = envOrDefault("STORAGE_MAX", "100GB")

	storageMaxBytes, err := ParseSize(StorageMax)
	if err != nil {
		panic(fmt.Sprintf("invalid STORAGE_MAX: %v", err))
	}

	StorageMaxBytes = storageMaxBytes
	FileLimit = storageMaxBytes / 100
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
