package p2p

import (
	"os"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	orig := os.Getenv("NETWORK_ID")
	defer os.Setenv("NETWORK_ID", orig)

	// Unset NETWORK_ID should default to "originless"
	os.Unsetenv("NETWORK_ID")
	os.Unsetenv("NETWORKID")
	cfg := LoadConfig()
	if !cfg.Enabled {
		t.Fatalf("expected Enabled=true by default")
	}
	if cfg.NetworkID != "originless" {
		t.Fatalf("expected NetworkID='originless', got '%s'", cfg.NetworkID)
	}

	// Custom NETWORK_ID
	os.Setenv("NETWORK_ID", "custom-mesh")
	cfg = LoadConfig()
	if !cfg.Enabled || cfg.NetworkID != "custom-mesh" {
		t.Fatalf("expected Enabled=true and NetworkID='custom-mesh', got '%s'", cfg.NetworkID)
	}

	// Disabled options
	for _, dis := range []string{"off", "none", "disabled", "false", "0"} {
		os.Setenv("NETWORK_ID", dis)
		cfg = LoadConfig()
		if cfg.Enabled {
			t.Fatalf("expected Enabled=false for NETWORK_ID='%s'", dis)
		}
	}
}
