package modules

import (
	"testing"
	"testing/fstest"
)

func TestScanExamples(t *testing.T) {
	mockFS := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte(`<!DOCTYPE html><html><head><title>Originless Examples</title></head><body>Index</body></html>`),
		},
		"upload-file.html": &fstest.MapFile{
			Data: []byte(`<!DOCTYPE html><html><head>
				<title>Single File Uploader — Originless Examples</title>
				<meta name="description" content="Upload and pin single files to IPFS" />
				<meta name="category" content="Storage & Publishing Tools" />
				<meta name="endpoint" content="POST /upload" />
				<meta name="order" content="1" />
			</head><body></body></html>`),
		},
		"upload-folder.html": &fstest.MapFile{
			Data: []byte(`<!DOCTYPE html><html><head>
				<title>Folder Uploader — Originless Examples</title>
				<meta name="description" content="Upload directory tree" />
			</head><body>curl -X POST /uploadfolder</body></html>`),
		},
		"custom.html": &fstest.MapFile{
			Data: []byte(`<!DOCTYPE html><html><head>
				<title>Custom Tool</title>
				<meta name="description" content="A custom tool" />
			</head><body></body></html>`),
		},
		"picture.html": &fstest.MapFile{
			Data: []byte(`<!DOCTYPE html><html><head>
				<title>Kind 20 Picture Post Generator — Originless</title>
				<meta name="description" content="Generate Nostr Kind 20 picture posts" />
			</head><body></body></html>`),
		},
		"article.html": &fstest.MapFile{
			Data: []byte(`<!DOCTYPE html><html><head>
				<title>Kind 30023 Long-Form Article Writer — Originless</title>
				<meta name="description" content="Write NIP-23 long-form articles" />
			</head><body></body></html>`),
		},
		"other.txt": &fstest.MapFile{
			Data: []byte(`not an html file`),
		},
	}

	tools, err := ScanExamples(mockFS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tools) != 5 {
		t.Fatalf("expected 5 tools, got %d", len(tools))
	}

	// First tool should be upload-file.html due to order 1
	if tools[0].File != "upload-file.html" {
		t.Errorf("expected tools[0].File to be upload-file.html, got %s", tools[0].File)
	}
	if tools[0].Title != "Single File Uploader" {
		t.Errorf("expected title 'Single File Uploader', got %q", tools[0].Title)
	}
	if tools[0].Endpoint != "POST /upload" {
		t.Errorf("expected endpoint 'POST /upload', got %q", tools[0].Endpoint)
	}
	if tools[0].Badge != "pill-post" {
		t.Errorf("expected badge 'pill-post', got %q", tools[0].Badge)
	}

	var folderTool *ExampleTool
	for _, tool := range tools {
		if tool.File == "upload-folder.html" {
			folderTool = &tool
			break
		}
	}
	if folderTool == nil {
		t.Fatal("upload-folder.html not found in tools")
	}
	if folderTool.Title != "Folder Uploader" {
		t.Errorf("expected title 'Folder Uploader', got %q", folderTool.Title)
	}
	if folderTool.Endpoint != "POST /uploadfolder" {
		t.Errorf("expected endpoint 'POST /uploadfolder', got %q", folderTool.Endpoint)
	}
	if folderTool.Badge != "pill-post" {
		t.Errorf("expected badge 'pill-post', got %q", folderTool.Badge)
	}

	// Check picture.html category inference
	var pictureTool *ExampleTool
	for _, tool := range tools {
		if tool.File == "picture.html" {
			pictureTool = &tool
			break
		}
	}
	if pictureTool == nil {
		t.Fatal("picture.html not found in tools")
	}
	if pictureTool.Category != "Nostr Event Generators" {
		t.Errorf("expected category 'Nostr Event Generators', got %q", pictureTool.Category)
	}
	if pictureTool.Endpoint != "NIP-68 · Kind 20" {
		t.Errorf("expected endpoint 'NIP-68 · Kind 20', got %q", pictureTool.Endpoint)
	}

	var articleTool *ExampleTool
	for _, tool := range tools {
		if tool.File == "article.html" {
			articleTool = &tool
			break
		}
	}
	if articleTool == nil {
		t.Fatal("article.html not found in tools")
	}
	if articleTool.Category != "Nostr Event Generators" {
		t.Errorf("expected category 'Nostr Event Generators', got %q", articleTool.Category)
	}
	if articleTool.Endpoint != "NIP-23 · Kind 30023" {
		t.Errorf("expected endpoint 'NIP-23 · Kind 30023', got %q", articleTool.Endpoint)
	}
}
