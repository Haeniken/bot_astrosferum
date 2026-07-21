package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneICONRunsKeepsCurrentAndNewest(t *testing.T) {
	dataRoot := t.TempDir()
	runs := filepath.Join(dataRoot, "models", "icon-eu", "runs")
	for _, run := range []string{"2026071800", "2026071812", "2026071900", "notes"} {
		if err := os.MkdirAll(filepath.Join(runs, run), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneICONRuns(dataRoot, 2, "2026071812"); err != nil {
		t.Fatal(err)
	}
	for _, run := range []string{"2026071812", "2026071900", "notes"} {
		if _, err := os.Stat(filepath.Join(runs, run)); err != nil {
			t.Fatalf("expected %s to remain: %v", run, err)
		}
	}
	if _, err := os.Stat(filepath.Join(runs, "2026071800")); !os.IsNotExist(err) {
		t.Fatalf("old run was not removed: %v", err)
	}
}

func TestCleanupStaleICONArtifacts(t *testing.T) {
	dataRoot := t.TempDir()
	now := time.Now()
	old := now.Add(-25 * time.Hour)
	paths := []string{
		filepath.Join(dataRoot, "models", "icon-eu", "incoming", "old-download"),
		filepath.Join(dataRoot, "models", "icon-eu", "runs", "2026072012", ".cloud-hourly-dead"),
		filepath.Join(dataRoot, "models", "icon-eu", "runs", "2026072012", ".surface-hourly-dead"),
		filepath.Join(dataRoot, "models", "icon-eu", "runs", "2026072012", ".steps-v3-dead"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(dataRoot, "models", "icon-eu", "runs", "2026072012", "surface-hourly-v14")
	if err := os.MkdirAll(keep, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := cleanupStaleICONArtifacts(dataRoot, 24*time.Hour, now); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("stale artifact remains at %s: %v", path, err)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("published data was removed: %v", err)
	}
}
