package modules

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var blobHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// BlobFileName maps a sha256 hex digest to its on-disk name: the bare
// hash, no extension. Content is sniffed, never trusted by name.
func BlobFileName(hash string) string {
	return hash
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

// blockedContentTypes are sniffed MIME families the blob store refuses.
// The store holds opaque bytes, so renderable or plainly textual payloads
// (phishing HTML, images, PDFs, pasted text) are rejected no matter what
// filename the client claims. Everything else — including unknown binaries,
// archives, and media containers — is accepted as opaque bytes.
func isBlockedContentType(ctype string) bool {
	if strings.HasPrefix(ctype, "text/") {
		return true
	}
	if strings.HasPrefix(ctype, "image/") {
		return true
	}
	return ctype == "application/pdf"
}
