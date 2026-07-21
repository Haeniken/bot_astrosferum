package eccodes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type fakeRunner struct{}

func (fakeRunner) CombinedOutput(_ context.Context, name string, _ ...string) ([]byte, error) {
	switch name {
	case "grib_ls":
		return []byte("Grid Point chosen #1 index=671460 latitude=59.94 longitude=30.31 distance=0.15 (Km)\n"), nil
	case "grib_get":
		return []byte("2t heightAboveGround 2 180 m 180 K 295.755\n"), nil
	default:
		return nil, fmt.Errorf("unexpected command %q", name)
	}
}

func TestExtractor(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "sample.grib2")
	if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	extractor := Extractor{Runner: fakeRunner{}}
	cell, sample, err := extractor.Extract(context.Background(), file, 59.9386, 30.3141)
	if err != nil {
		t.Fatal(err)
	}
	if cell.Index != 671460 || cell.DistanceKM != 0.15 {
		t.Fatalf("unexpected cell: %+v", cell)
	}
	if sample.ShortName != "2t" || sample.Step != 180 || sample.StepUnits != "m" || sample.Units != "K" || sample.Value != 295.755 {
		t.Fatalf("unexpected sample: %+v", sample)
	}
}

func TestParseSampleWithCompoundUnits(t *testing.T) {
	sample, err := parseSample("wind.grib2", "u generalVerticalLayer 20 180 m 180 m s**-1 12.75")
	if err != nil {
		t.Fatal(err)
	}
	if sample.Units != "m s**-1" || sample.Value != 12.75 {
		t.Fatalf("unexpected sample: %+v", sample)
	}
}

func TestParseCellRejectsMissingChoice(t *testing.T) {
	if _, err := parseCell("Nearest neighbour is unavailable"); err == nil {
		t.Fatal("expected missing chosen-cell line to fail")
	}
}
