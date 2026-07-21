package app

import (
	"os"
	"path/filepath"
	"testing"
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

func TestCheckDirectory(t *testing.T) {
	if check := checkDirectory("temporary", t.TempDir(), true); !check.OK {
		t.Fatalf("writable directory failed: %+v", check)
	}
}
