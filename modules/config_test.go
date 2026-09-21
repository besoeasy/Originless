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
