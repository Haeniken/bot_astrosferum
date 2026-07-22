package iconeu

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type batchRunner struct {
	t        *testing.T
	points   []batchPoint
	messages int
	gridSeen bool
}

func (runner *batchRunner) CombinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	runner.t.Helper()
	if name != "cdo" || len(args) != 6 || !strings.HasPrefix(args[4], "-remapnn,") {
		runner.t.Fatalf("unexpected batch command: %s %q", name, args)
	}
	gridPath := strings.TrimPrefix(args[4], "-remapnn,")
	grid, err := os.ReadFile(gridPath)
	if err != nil {
		runner.t.Fatal(err)
	}
	if !strings.Contains(string(grid), fmt.Sprintf("gridsize = %d", len(runner.points))) {
		runner.t.Fatalf("target grid = %q", grid)
	}
	runner.gridSeen = true
	var output strings.Builder
	output.WriteString("# name lev lon lat value\n")
	for message := range runner.messages {
		for point, coordinate := range runner.points {
			fmt.Fprintf(&output, "field%d %d %.8f %.8f %.8f\n", message, message, coordinate.Longitude, coordinate.Latitude, float64(message*100+point))
		}
	}
	return []byte(output.String()), nil
}

func TestBatchExtractorReadsEverySourceMessageOnceForAllPoints(t *testing.T) {
	points := []batchPoint{{Latitude: 59.93, Longitude: 30.31}, {Latitude: 60.02, Longitude: 30.45}}
	runner := &batchRunner{t: t, points: points, messages: 2}
	tempRoot := t.TempDir()
	values, err := (batchExtractor{runner: runner, tempRoot: tempRoot}).extract(context.Background(), "/models/cloud.grib2", points, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !runner.gridSeen || len(values) != len(points) || values[1][batchValueKey{name: "field1", level: 1}] != 101 {
		t.Fatalf("batch values = %#v", values)
	}
	entries, err := os.ReadDir(tempRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary entries remain: %v", entries)
	}
}

func TestParseBatchOutputRejectsMissingAndUnknownTargets(t *testing.T) {
	points := []batchPoint{{Latitude: 10, Longitude: 179.9}, {Latitude: 10, Longitude: -179.9}}
	missing := []byte("x 0 179.9 10 1\n")
	if _, err := parseBatchOutput(missing, points, 1); err == nil {
		t.Fatal("missing target was accepted")
	}
	unknown := []byte("x 0 179.9 10 1\nx 0 0 0 2\n")
	if _, err := parseBatchOutput(unknown, points, 1); err == nil {
		t.Fatal("unknown target was accepted")
	}
}

func TestParseBatchOutputRejectsFiniteMissingValueSentinel(t *testing.T) {
	points := []batchPoint{{Latitude: 10, Longitude: 20}}
	if _, err := parseBatchOutput([]byte("x 0 20 10 -9e33\n"), points, 1); err == nil {
		t.Fatal("finite missing-value sentinel was accepted")
	}
}

type failedBatchRunner struct {
	output []byte
	err    error
}

func (runner failedBatchRunner) CombinedOutput(context.Context, string, ...string) ([]byte, error) {
	return runner.output, runner.err
}

func TestBatchExtractorRedactsFailedCDOOutputAndPropagatesCancellation(t *testing.T) {
	points := []batchPoint{{Latitude: 59.9386, Longitude: 30.3141}}
	raw := "field 0 30.3141 59.9386 private-output"
	_, err := (batchExtractor{
		runner:   failedBatchRunner{output: []byte(raw), err: errors.New("cdo failed")},
		tempRoot: t.TempDir(),
	}).extract(context.Background(), "/models/cloud.grib2", points, 1)
	if err == nil || strings.Contains(err.Error(), raw) || strings.Contains(err.Error(), "59.9386") || strings.Contains(err.Error(), "30.3141") {
		t.Fatalf("failed CDO error leaked raw coordinates: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = (batchExtractor{
		runner: failedBatchRunner{err: context.Canceled}, tempRoot: t.TempDir(),
	}).extract(ctx, "/models/cloud.grib2", points, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled CDO extraction error = %v, want context.Canceled", err)
	}
}

func TestWriteBatchGridRejectsDuplicateQuantizedTargets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "points.grid")
	err := writeBatchGrid(path, []batchPoint{{Latitude: 1, Longitude: 2}, {Latitude: 1.0000001, Longitude: 2.0000001}})
	if err == nil {
		t.Fatal("duplicate target was accepted")
	}
}
