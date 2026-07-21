package lightpollution

import (
	"bytes"
	"compress/gzip"
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAtlasUsesConfiguredAnnualTile(t *testing.T) {
	payload := make([]byte, tilePayloadBytes)
	// A constant compressed value of 195 in Lorenz's signed base-128 format.
	payload[0], payload[1] = 1, 67
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || len(request.URL.Path) < 6 || request.URL.Path[1:6] != "2024/" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/gzip")
		_, _ = response.Write(compressed.Bytes())
	}))
	defer server.Close()

	atlas, err := NewAtlas(t.TempDir(), Options{
		BaseURL: server.URL, AtlasYear: 2024, HTTPClient: server.Client(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	estimate, err := atlas.At(context.Background(), 55.7558, 37.6173)
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Year != 2024 {
		t.Fatalf("year = %d, want 2024", estimate.Year)
	}
	wantLPI := compressedToLPI(195)
	if math.Abs(estimate.LPI-wantLPI) > 1e-12 {
		t.Fatalf("LPI = %.12f, want %.12f", estimate.LPI, wantLPI)
	}
	wantSQM := 22 - 2.5*math.Log10(1+wantLPI)
	if math.Abs(estimate.SQM-wantSQM) > 1e-12 {
		t.Fatalf("SQM = %.12f, want %.12f", estimate.SQM, wantSQM)
	}
}

func TestApproximateBortleDoesNotInventBrightestClass(t *testing.T) {
	tests := []struct {
		sqm  float64
		want string
	}{{22, "1"}, {21.9, "2"}, {21.8, "3"}, {21, "4"}, {20, "5"}, {19.2, "6"}, {18.5, "7"}, {17, "8–9"}}
	for _, test := range tests {
		if got := approximateBortle(test.sqm); got != test.want {
			t.Errorf("approximateBortle(%v) = %q, want %q", test.sqm, got, test.want)
		}
	}
}

func TestDecodeCompressedAtAppliesRowAndColumnDeltas(t *testing.T) {
	payload := make([]int8, tilePayloadBytes)
	payload[0], payload[1] = 1, 2 // 130
	payload[tileCells+1] = 3      // first value of row 1
	payload[tileCells+2] = 4      // second value of row 1
	got, err := decodeCompressedAt(payload, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != 137 {
		t.Fatalf("decoded value = %d, want 137", got)
	}
}

func TestAtlasCacheKeepsOnlyConfiguredAndPreviousYear(t *testing.T) {
	root := t.TempDir()
	for _, year := range []string{"2022", "2023", "2024", "2025"} {
		if err := os.Mkdir(filepath.Join(root, year), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewAtlas(root, Options{AtlasYear: 2024}, nil); err != nil {
		t.Fatal(err)
	}
	for _, year := range []string{"2023", "2024"} {
		if _, err := os.Stat(filepath.Join(root, year)); err != nil {
			t.Fatalf("retained year %s: %v", year, err)
		}
	}
	for _, year := range []string{"2022", "2025"} {
		if _, err := os.Stat(filepath.Join(root, year)); !os.IsNotExist(err) {
			t.Fatalf("obsolete year %s was not removed", year)
		}
	}
}
