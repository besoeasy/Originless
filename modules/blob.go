package modules

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var blobHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// BlobFileName maps a sha256 hex digest to its on-disk name.
func BlobFileName(hash string) string {
	return hash + ".bin"
}

// BlobPath returns the absolute path for a blob hash.
func BlobPath(dir, hash string) string {
	return filepath.Join(dir, BlobFileName(hash))
}

// NormalizeBlobHash lowercases, trims, and validates a 64-char sha256 hex string.
func NormalizeBlobHash(raw string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(raw))
	if !blobHashPattern.MatchString(h) {
		return "", fmt.Errorf("invalid hash: must be 64 lowercase hex chars (sha256)")
	}
	return h, nil
}

// EnsureBlobDir creates the blob directory when missing.
func EnsureBlobDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

// IsBinFile reports whether an uploaded filename qualifies for /up.
func IsBinFile(name string) bool {
	base := strings.ToLower(strings.TrimSpace(name))
	if base == "" || base == "." {
		return false
	}
	return strings.HasSuffix(base, ".bin")
}
