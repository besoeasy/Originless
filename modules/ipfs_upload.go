package modules

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"unicode"
)

const (
	// MaxIPFSFolderFiles prevents multipart metadata from becoming an
	// unbounded memory/CPU cost even when file byte limits are disabled.
	MaxIPFSFolderFiles = 10_000

	maxIPFSUploadPathBytes = 1024
)

var (
	ErrIPFSNoFile       = errors.New("no file uploaded")
	ErrIPFSFileTooLarge = errors.New("IPFS upload too large")
	ErrIPFSInvalidPath  = errors.New("invalid IPFS upload path")
	ErrIPFSTooManyFiles = errors.New("too many files in IPFS folder upload")
)

// StagedIPFSFile is a temporary local file plus its sanitized path in IPFS.
type StagedIPFSFile struct {
	Path string
	Name string
	Size int64
}

// StagedIPFSFolder is the complete temporary representation of a folder upload.
type StagedIPFSFolder struct {
	Files []StagedIPFSFile
	Count int
	Total int64
}

func stageSingleIPFSFile(r *http.Request, tempDir string, limit int64, guard *DiskGuard) (*StagedIPFSFile, error) {
	file, err := stageIPFSFiles(r, tempDir, limit, guard, false)
	if err != nil {
		return nil, err
	}
	return &file.Files[0], nil
}

func stageIPFSFolder(r *http.Request, tempDir string, limit int64, guard *DiskGuard) (*StagedIPFSFolder, error) {
	files, err := stageIPFSFiles(r, tempDir, limit, guard, true)
	if err != nil {
		return nil, err
	}
	return &StagedIPFSFolder{Files: files.Files, Count: files.Count, Total: files.Total}, nil
}

func stageIPFSFiles(r *http.Request, tempDir string, limit int64, guard *DiskGuard, folder bool) (*StagedIPFSFolder, error) {
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return nil, fmt.Errorf("create IPFS upload temp directory: %w", err)
	}
	if err := os.Chmod(tempDir, 0o700); err != nil {
		return nil, fmt.Errorf("secure IPFS upload temp directory: %w", err)
	}

	reader, err := r.MultipartReader()
	if err != nil {
		return nil, fmt.Errorf("expected multipart/form-data: %w", err)
	}

	staged := &StagedIPFSFolder{}
	seen := make(map[string]struct{})
	cleanup := func() {
		for _, file := range staged.Files {
			_ = os.Remove(file.Path)
		}
	}

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			cleanup()
			return nil, err
		}

		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		if !folder && staged.Count > 0 {
			_ = part.Close()
			cleanup()
			return nil, fmt.Errorf("single-file endpoint accepts exactly one file")
		}
		if folder && staged.Count >= MaxIPFSFolderFiles {
			_ = part.Close()
			cleanup()
			return nil, fmt.Errorf("%w: maximum is %d files", ErrIPFSTooManyFiles, MaxIPFSFolderFiles)
		}

		name, err := sanitizeIPFSUploadPath(rawMultipartFilename(part), folder)
		if err != nil {
			_ = part.Close()
			cleanup()
			return nil, err
		}
		if _, exists := seen[name]; exists {
			_ = part.Close()
			cleanup()
			return nil, fmt.Errorf("%w: duplicate path %q", ErrIPFSInvalidPath, name)
		}
		seen[name] = struct{}{}

		remaining := int64(0)
		if limit > 0 {
			remaining = limit - staged.Total
			if remaining <= 0 {
				probe, readErr := io.ReadAll(io.LimitReader(part, 1))
				if readErr != nil {
					_ = part.Close()
					cleanup()
					return nil, readErr
				}
				if len(probe) > 0 {
					_ = part.Close()
					cleanup()
					return nil, fmt.Errorf("%w: maximum total size is %s", ErrIPFSFileTooLarge, FormatBytes(limit))
				}
			}
		}
		path, size, err := stageIPFSPart(part, tempDir, remaining, guard)
		_ = part.Close()
		if err != nil {
			cleanup()
			if errors.Is(err, errDiskCapExceeded) {
				return nil, fmt.Errorf("%w: maximum total size is %s", ErrIPFSFileTooLarge, FormatBytes(limit))
			}
			return nil, err
		}
		staged.Files = append(staged.Files, StagedIPFSFile{Path: path, Name: name, Size: size})
		staged.Count++
		staged.Total += size
	}

	if staged.Count == 0 {
		return nil, ErrIPFSNoFile
	}
	return staged, nil
}

func stageIPFSPart(part *multipart.Part, tempDir string, remaining int64, guard *DiskGuard) (string, int64, error) {
	tmp, err := os.CreateTemp(tempDir, "ipfs-upload-*")
	if err != nil {
		return "", 0, fmt.Errorf("create IPFS upload temp file: %w", err)
	}
	tmpPath := tmp.Name()
	abort := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	maxBytes := int64(0)
	if remaining > 0 {
		maxBytes = remaining
	}
	writer := &ceilingWriter{w: tmp, g: guard, maxBytes: maxBytes}
	written, err := io.Copy(writer, part)
	if err != nil {
		abort()
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", 0, err
	}
	return tmpPath, written, nil
}

// rawMultipartFilename returns the unshortened filename. Part.FileName applies
// filepath.Base, but Kubo's boxo/files parser intentionally reads the original
// Content-Disposition value to preserve recursive directory paths.
func rawMultipartFilename(part *multipart.Part) string {
	if part == nil {
		return ""
	}
	disposition := part.Header.Get("Content-Disposition")
	if disposition != "" {
		if _, params, err := mime.ParseMediaType(disposition); err == nil {
			if filename := params["filename"]; filename != "" {
				return filename
			}
		}
	}
	return part.FileName()
}

func sanitizeIPFSUploadPath(raw string, folder bool) (string, error) {
	if decoded, err := url.QueryUnescape(raw); err == nil {
		raw = decoded
	}
	raw = strings.ReplaceAll(raw, "\\", "/")
	if raw == "" {
		return "", fmt.Errorf("%w: filename is empty", ErrIPFSInvalidPath)
	}
	if strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("%w: filename contains NUL", ErrIPFSInvalidPath)
	}

	if !folder {
		raw = path.Base(raw)
	} else {
		if strings.HasPrefix(raw, "/") || hasWindowsVolumeName(raw) {
			return "", fmt.Errorf("%w: folder paths must be relative", ErrIPFSInvalidPath)
		}
		for _, segment := range strings.Split(raw, "/") {
			if segment == ".." {
				return "", fmt.Errorf("%w: parent traversal is not allowed", ErrIPFSInvalidPath)
			}
		}
		raw = path.Clean(raw)
	}

	if raw == "" || raw == "." || raw == "/" || raw == ".." || strings.HasPrefix(raw, "../") {
		return "", fmt.Errorf("%w: %q", ErrIPFSInvalidPath, raw)
	}
	if len(raw) > maxIPFSUploadPathBytes {
		return "", fmt.Errorf("%w: path exceeds %d bytes", ErrIPFSInvalidPath, maxIPFSUploadPathBytes)
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: path contains control characters", ErrIPFSInvalidPath)
		}
	}
	return raw, nil
}

func hasWindowsVolumeName(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	c := value[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func removeStagedIPFSFiles(files []StagedIPFSFile) {
	for _, file := range files {
		_ = os.Remove(file.Path)
	}
}
