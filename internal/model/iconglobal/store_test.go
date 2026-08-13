package iconglobal

import (
	"compress/gzip"
	"encoding/gob"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
)

func TestPointCacheVersionRejectsFormerHeuristicContract(t *testing.T) {
	root := t.TempDir()
	store := &Store{dataRoot: root}
	const runID = "2026071400"
	const cellID = "lat+60.000-lon+090.000"
	legacy := pointBundle{
		Schema: "point-v2-native-cloud", RunID: runID, CellID: cellID,
		Created:  time.Now().UTC(),
		Vertical: forecast.VerticalSeries{Frames: make([]forecast.VerticalFrame, 25)},
		Surface:  forecast.SurfaceSeries{Frames: make([]forecast.SurfaceFrame, 79)},
		Cloud:    forecast.CloudSeries{Frames: make([]forecast.CloudFrame, 79)},
	}
	writeGlobalTestBundle(t, store.cachePath(runID, cellID), legacy)
	if _, err := store.readDisk(runID, cellID, true); err == nil {
		t.Fatal("former point-cache schema was accepted")
	}
	formerPath := filepath.Join(root, "cache", "points", "icon-global", legacy.Schema, runID, cellID+".gob.gz")
	if formerPath == store.cachePath(runID, cellID) {
		t.Fatal("former and current point-cache versions share a directory")
	}
}

func TestPointCachePreservesExplicitHeuristicFields(t *testing.T) {
	root := t.TempDir()
	store := &Store{dataRoot: root}
	const runID = "2026071400"
	const cellID = "lat+60.000-lon+090.000"
	verticalFrames := make([]forecast.VerticalFrame, 25)
	for index := range verticalFrames {
		verticalFrames[index].LeadTimeQualityHeuristic = 0.91
	}
	surfaceFrames := make([]forecast.SurfaceFrame, 79)
	for index := range surfaceFrames {
		surfaceFrames[index].FogHeuristicAvailable = true
		surfaceFrames[index].TransparencyHeuristicAvailable = true
	}
	bundle := pointBundle{
		Schema: pointCacheVersion, RunID: runID, CellID: cellID, Created: time.Now().UTC(),
		Vertical: forecast.VerticalSeries{Frames: verticalFrames},
		Surface:  forecast.SurfaceSeries{Frames: surfaceFrames},
		Cloud:    forecast.CloudSeries{Frames: make([]forecast.CloudFrame, 79)},
	}
	if err := store.writeDisk(bundle); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.readDisk(runID, cellID, true)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Vertical.Frames[0].LeadTimeQualityHeuristic != 0.91 ||
		!loaded.Surface.Frames[0].FogHeuristicAvailable ||
		!loaded.Surface.Frames[0].TransparencyHeuristicAvailable {
		t.Fatal("current point cache lost explicit heuristic fields")
	}
}

func writeGlobalTestBundle(t *testing.T, path string, bundle pointBundle) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(file)
	if err := gob.NewEncoder(compressed).Encode(bundle); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
