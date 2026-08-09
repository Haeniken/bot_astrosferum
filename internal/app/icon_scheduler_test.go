package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bot_astrosferum/internal/model"
	"bot_astrosferum/internal/model/iconeu"
)

func TestPruneICONRunsKeepsCurrentAndNewest(t *testing.T) {
	dataRoot := t.TempDir()
	runs := filepath.Join(dataRoot, "models", "icon-eu", "runs")
	domeRuns := filepath.Join(dataRoot, "models", "icon-eu", "dome-runs")
	for _, run := range []string{"2026071800", "2026071812", "2026071900", "notes"} {
		if err := os.MkdirAll(filepath.Join(runs, run), 0o750); err != nil {
			t.Fatal(err)
		}
		if run != "notes" {
			if err := os.MkdirAll(filepath.Join(domeRuns, run, "contract"), 0o750); err != nil {
				t.Fatal(err)
			}
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
	if _, err := os.Stat(filepath.Join(domeRuns, "2026071800")); !os.IsNotExist(err) {
		t.Fatalf("old dome run was not removed: %v", err)
	}
}

func TestPruneICONRunsSkipsActivelyLeasedCalculation(t *testing.T) {
	dataRoot := t.TempDir()
	runID := "2026071800"
	for _, root := range []string{"runs", "dome-runs"} {
		if err := os.MkdirAll(filepath.Join(dataRoot, "models", "icon-eu", root, runID), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dataRoot, "models", "icon-eu", "runs", "2026071900"), 0o750); err != nil {
		t.Fatal(err)
	}
	manager, err := model.NewRunLeaseManager(filepath.Join(dataRoot, "state", "run-leases"))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.AcquireShared(context.Background(), "icon-eu", runID, model.RunRetentionLeaseDigest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close() }()
	if err := pruneICONRuns(dataRoot, 1, "2026071900"); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"runs", "dome-runs"} {
		if _, err := os.Stat(filepath.Join(dataRoot, "models", "icon-eu", root, runID)); err != nil {
			t.Fatalf("leased %s run was removed: %v", root, err)
		}
	}
}

func TestPruneICONRunsReclaimsOrphanDomeRun(t *testing.T) {
	dataRoot := t.TempDir()
	current := "2026071900"
	if err := os.MkdirAll(filepath.Join(dataRoot, "models", "icon-eu", "runs", current), 0o750); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(dataRoot, "models", "icon-eu", "dome-runs", "2026071800")
	if err := os.MkdirAll(orphan, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := pruneICONRuns(dataRoot, 1, current); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan dome run remains: %v", err)
	}
}

func TestEnqueueLatestDomeNeverBlocksAndKeepsNewestPendingRun(t *testing.T) {
	requests := make(chan iconeu.LoadedManifest, 1)
	enqueueLatestDome(requests, iconeu.LoadedManifest{Manifest: iconeu.Manifest{RunID: "2026071800"}})
	enqueueLatestDome(requests, iconeu.LoadedManifest{Manifest: iconeu.Manifest{RunID: "2026071806"}})
	select {
	case got := <-requests:
		if got.RunID != "2026071806" {
			t.Fatalf("pending dome run = %s", got.RunID)
		}
	default:
		t.Fatal("latest dome request was dropped")
	}
	// A disabled nil queue is deliberately a no-op.
	enqueueLatestDome(nil, iconeu.LoadedManifest{})
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
