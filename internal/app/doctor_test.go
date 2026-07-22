package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"bot_astrosferum/internal/config"
)

func TestCheckSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("not-a-real-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if check := checkSecret("token", path); !check.OK {
		t.Fatalf("expected restricted file to pass: %+v", check)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if check := checkSecret("token", path); check.OK {
		t.Fatalf("expected broad permissions to fail: %+v", check)
	}
}

func TestDoctorChecksCDOWhenHorizonAnalysisEnabled(t *testing.T) {
	cfg := config.Defaults()
	cfg.Paths.Data = t.TempDir()
	cfg.Paths.Temp = t.TempDir()
	cfg.Sync.MinFreeSpace = 0
	cfg.Platforms.Telegram.Enabled = false
	cfg.Platforms.VK.Enabled = false
	cfg.Providers.ICONGlobal.Enabled = false
	cfg.HorizonAnalysis.Enabled = true

	checks := Doctor(context.Background(), cfg)
	found := false
	for _, check := range checks {
		if check.Name == "cdo" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected cdo doctor check for enabled horizon analysis")
	}
}

func TestCheckDirectory(t *testing.T) {
	if check := checkDirectory("temporary", t.TempDir(), true); !check.OK {
		t.Fatalf("writable directory failed: %+v", check)
	}
}
