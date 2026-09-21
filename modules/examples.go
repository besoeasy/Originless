package modules

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ExampleTool represents metadata for a dynamically discovered client example or tool.
type ExampleTool struct {
	File        string `json:"file"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Endpoint    string `json:"endpoint"`
	Badge       string `json:"badge"`
	Order       int    `json:"order"`
}

var (
	titleRegex       = regexp.MustCompile(`(?i)<title[^>]*>([^<]+)</title>`)
	metaDescRegex    = regexp.MustCompile(`(?i)<meta\s+[^>]*name=["']description["'][^>]*content=["']([^"']*)["']|(?i)<meta\s+[^>]*content=["']([^"']*)["'][^>]*name=["']description["']`)
	metaCatRegex     = regexp.MustCompile(`(?i)<meta\s+[^>]*name=["']category["'][^>]*content=["']([^"']*)["']|(?i)<meta\s+[^>]*content=["']([^"']*)["'][^>]*name=["']category["']`)
	metaEndpRegex    = regexp.MustCompile(`(?i)<meta\s+[^>]*name=["']endpoint["'][^>]*content=["']([^"']*)["']|(?i)<meta\s+[^>]*content=["']([^"']*)["'][^>]*name=["']endpoint["']`)
	metaOrderRegex   = regexp.MustCompile(`(?i)<meta\s+[^>]*name=["']order["'][^>]*content=["']([^"']*)["']|(?i)<meta\s+[^>]*content=["']([^"']*)["'][^>]*name=["']order["']`)
	titleSuffixRegex = regexp.MustCompile(`\s*[-—–|]\s*(Originless\s*Examples?|Originless).*$`)
)

// ScanExamples reads all .html files (except index.html) from the given filesystem and extracts their metadata.
func ScanExamples(examplesFS fs.FS) ([]ExampleTool, error) {
	entries, err := fs.ReadDir(examplesFS, ".")
	if err != nil {
		return nil, fmt.Errorf("failed to read examples dir: %w", err)
	}

	var tools []ExampleTool

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".html") || strings.EqualFold(name, "index.html") {
			continue
		}

		data, err := fs.ReadFile(examplesFS, name)
		if err != nil {
			continue
		}

		content := string(data)
		tool := parseExampleMetadata(name, content)
		tools = append(tools, tool)
	}

	sort.SliceStable(tools, func(i, j int) bool {
		if tools[i].Order != tools[j].Order {
			return tools[i].Order < tools[j].Order
		}
		if tools[i].Category != tools[j].Category {
			return tools[i].Category < tools[j].Category
		}
		return tools[i].Title < tools[j].Title
	})

	return tools, nil
}

func parseExampleMetadata(filename string, content string) ExampleTool {
	tool := ExampleTool{
		File:  filename,
		Order: 99,
	}

	// 1. Extract Title
	if match := titleRegex.FindStringSubmatch(content); len(match) > 1 {
		title := strings.TrimSpace(match[1])
		// Strip common branding suffixes
		title = titleSuffixRegex.ReplaceAllString(title, "")
		tool.Title = strings.TrimSpace(title)
	}
	if tool.Title == "" {
		base := strings.TrimSuffix(filename, filepath.Ext(filename))
		parts := strings.Split(base, "-")
		for i, p := range parts {
			if len(p) > 0 {
				parts[i] = strings.ToUpper(p[:1]) + p[1:]
			}
		}
		tool.Title = strings.Join(parts, " ")
	}

	// 2. Extract Description
	if match := metaDescRegex.FindStringSubmatch(content); len(match) > 0 {
		for i := 1; i < len(match); i++ {
			if match[i] != "" {
				tool.Description = strings.TrimSpace(match[i])
				break
			}
		}
	}

	// 3. Extract Category
	if match := metaCatRegex.FindStringSubmatch(content); len(match) > 0 {
		for i := 1; i < len(match); i++ {
			if match[i] != "" {
				tool.Category = strings.TrimSpace(match[i])
				break
			}
		}
	}

	// 4. Extract Endpoint / Tag
	if match := metaEndpRegex.FindStringSubmatch(content); len(match) > 0 {
		for i := 1; i < len(match); i++ {
			if match[i] != "" {
				tool.Endpoint = strings.TrimSpace(match[i])
				break
			}
		}
	}

	// 5. Extract Order
	if match := metaOrderRegex.FindStringSubmatch(content); len(match) > 0 {
		for i := 1; i < len(match); i++ {
			if match[i] != "" {
				if orderNum, err := strconv.Atoi(strings.TrimSpace(match[i])); err == nil {
					tool.Order = orderNum
				}
				break
			}
		}
	}

	// Default inferences if metadata wasn't explicit
	lowerContent := strings.ToLower(content)
	lowerFile := strings.ToLower(filename)

	if tool.Category == "" {
		if strings.Contains(lowerFile, "picture") || strings.Contains(lowerFile, "post") || strings.Contains(lowerFile, "article") || strings.Contains(lowerFile, "nostr") || strings.Contains(lowerContent, "nostr") {
			tool.Category = "Nostr Event Generators"
		} else {
			tool.Category = "Storage & Publishing Tools"
		}
	}

	if tool.Endpoint == "" {
		if strings.Contains(lowerFile, "folder") || strings.Contains(lowerContent, "/uploadfolder") {
			tool.Endpoint = "POST /uploadfolder"
		} else if strings.Contains(lowerFile, "article") || strings.Contains(lowerContent, "kind 30023") {
			tool.Endpoint = "NIP-23 · Kind 30023"
		} else if strings.Contains(lowerFile, "picture") || strings.Contains(lowerContent, "kind 20") {
			tool.Endpoint = "NIP-68 · Kind 20"
		} else if strings.Contains(lowerFile, "post") || strings.Contains(lowerContent, "kind 1") {
			tool.Endpoint = "NIP-01 · Kind 1"
		} else if strings.Contains(lowerContent, "/upload") {
			tool.Endpoint = "POST /upload"
		} else {
			tool.Endpoint = "TOOL"
		}
	}

	// Badge class inference
	if strings.HasPrefix(tool.Endpoint, "POST") {
		tool.Badge = "pill-post"
	} else {
		tool.Badge = "pill-get"
	}

	return tool
}

// GetExamplesFS returns a live disk FS if ./examples exists on disk, otherwise the provided fallbackFS.
func GetExamplesFS(fallbackFS fs.FS) fs.FS {
	if fi, err := os.Stat("examples"); err == nil && fi.IsDir() {
		return os.DirFS("examples")
	}
	return fallbackFS
}
