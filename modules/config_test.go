package modules

import (
	"os"
	"testing"
)

func TestEnvOrDefaultBool(t *testing.T) {
	const key = "TEST_BOOL_FLAG"
	t.Cleanup(func() { os.Unsetenv(key) })

	tests := []struct {
		value    string
		set      bool
		fallback bool
		want     bool
	}{
		{set: false, fallback: true, want: true},
		{set: false, fallback: false, want: false},
		{value: "true", set: true, fallback: false, want: true},
		{value: "TRUE", set: true, fallback: false, want: true},
		{value: "1", set: true, fallback: false, want: true},
		{value: "yes", set: true, fallback: false, want: true},
		{value: "on", set: true, fallback: false, want: true},
		{value: "false", set: true, fallback: true, want: false},
		{value: "0", set: true, fallback: true, want: false},
		{value: "no", set: true, fallback: true, want: false},
		{value: "off", set: true, fallback: true, want: false},
		{value: "maybe", set: true, fallback: true, want: true},
		{value: "  false  ", set: true, fallback: true, want: false},
	}

	for _, tt := range tests {
		os.Unsetenv(key)
		if tt.set {
			os.Setenv(key, tt.value)
		}
		got := envOrDefaultBool(key, tt.fallback)
		if got != tt.want {
			t.Errorf("envOrDefaultBool(%q, fallback=%v) = %v, want %v", tt.value, tt.fallback, got, tt.want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 Bytes"},
		{1024, "1.00 KB"},
		{1048576, "1.00 MB"},
		{1073741824, "1.00 GB"},
	}
	for _, tt := range tests {
		got := FormatBytes(tt.input)
		if got != tt.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseBytes(t *testing.T) {
	fallback := int64(1 << 30) // 1 GiB
	tests := []struct {
		input    string
		fallback int64
		want     int64
	}{
		{"", fallback, fallback},
		{"   ", fallback, fallback},
		{"invalid", fallback, fallback},
		{"-1", fallback, fallback},
		{"-500MB", fallback, fallback},
		{"0", fallback, 0},
		{"1024", fallback, 1024},
		{"1073741824", fallback, 1073741824},
		{"512B", fallback, 512},
		{"512b", fallback, 512},
		{"10K", fallback, 10 * 1024},
		{"10KB", fallback, 10 * 1024},
		{"10KiB", fallback, 10 * 1024},
		{"500M", fallback, 500 * 1024 * 1024},
		{"500MB", fallback, 500 * 1024 * 1024},
		{"500MiB", fallback, 500 * 1024 * 1024},
		{"  500 mb  ", fallback, 500 * 1024 * 1024},
		{"1G", fallback, 1 * 1024 * 1024 * 1024},
		{"1GB", fallback, 1 * 1024 * 1024 * 1024},
		{"1GiB", fallback, 1 * 1024 * 1024 * 1024},
		{"2GB", fallback, 2 * 1024 * 1024 * 1024},
		{"1.5GB", fallback, int64(1.5 * 1024 * 1024 * 1024)},
		{"1TB", fallback, 1024 * 1024 * 1024 * 1024},
		{"1TiB", fallback, 1024 * 1024 * 1024 * 1024},
	}

	for _, tt := range tests {
		got := ParseBytes(tt.input, tt.fallback)
		if got != tt.want {
			t.Errorf("ParseBytes(%q, fallback=%d) = %d, want %d", tt.input, tt.fallback, got, tt.want)
		}
	}
}

func TestEnvOrDefaultBytes(t *testing.T) {
	const key = "TEST_MAX_BLOB_BYTES"
	fallback := int64(1 << 30)
	t.Cleanup(func() { os.Unsetenv(key) })

	os.Unsetenv(key)
	if got := envOrDefaultBytes(key, fallback); got != fallback {
		t.Fatalf("expected fallback %d when unset, got %d", fallback, got)
	}

	os.Setenv(key, "500MB")
	if got := envOrDefaultBytes(key, fallback); got != 500*1024*1024 {
		t.Fatalf("expected 524288000 for 500MB, got %d", got)
	}

	os.Setenv(key, "2GB")
	if got := envOrDefaultBytes(key, fallback); got != 2*1024*1024*1024 {
		t.Fatalf("expected 2147483648 for 2GB, got %d", got)
	}
}
