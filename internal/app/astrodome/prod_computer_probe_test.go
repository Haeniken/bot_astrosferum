package astrodome

// Opt-in production performance/release diagnostic. Ordinary test runs skip
// it. The probe reads one immutable already-published ICON-EU volume under a
// retention lease, executes the real Computer scheduler, validates the full
// dataset in memory, and writes only an isolated JSON report. It never submits
// a user job, publishes an archive, or sends a platform message.

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"bot_astrosferum/internal/app/directional"
	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model/iconeu"
)

const productionComputerProbeGate = "full-computer-v1"

type productionComputerProbeTimeZone string

func (zone productionComputerProbeTimeZone) Resolve(float64, float64) string { return string(zone) }

type productionComputerProbeReport struct {
	RunID                  string         `json:"run_id"`
	ManifestSHA256         string         `json:"manifest_sha256"`
	GridProfile            string         `json:"grid_profile"`
	RefractionVersion      string         `json:"refraction_version"`
	ScienceVersion         string         `json:"science_version"`
	SciencePathVersion     string         `json:"science_path_version"`
	GoVersion              string         `json:"go_version"`
	NodeWorkers            int            `json:"node_workers"`
	ECCodesWorkers         int            `json:"eccodes_workers"`
	FrameCount             int            `json:"frame_count"`
	NodesPerFrame          int            `json:"nodes_per_frame"`
	NodeHours              int            `json:"node_hours"`
	Calculated             int            `json:"calculated"`
	Valid                  int            `json:"valid"`
	PrecipitationVeto      int            `json:"precipitation_veto"`
	UnavailableByState     map[string]int `json:"unavailable_by_state"`
	CalculationDurationMS  int64          `json:"calculation_duration_ms"`
	HeapAllocBeforeBytes   uint64         `json:"heap_alloc_before_bytes"`
	HeapAllocAfterBytes    uint64         `json:"heap_alloc_after_bytes"`
	HeapInUseAfterBytes    uint64         `json:"heap_in_use_after_bytes"`
	TotalAllocDeltaBytes   uint64         `json:"total_alloc_delta_bytes"`
	GarbageCollectionDelta uint32         `json:"garbage_collection_delta"`
}

func TestProductionAstrodomeComputerProbe(t *testing.T) {
	if os.Getenv("ASTRO_ASTRODOME_COMPUTER_PROBE") != productionComputerProbeGate {
		t.Skip("production Astrodome Computer probe is disabled")
	}
	dataRoot := productionProbeAbsoluteDir(t, "ASTRO_PROBE_DATA_ROOT")
	tempRoot := productionProbeAbsoluteDir(t, "ASTRO_PROBE_TEMP_ROOT")
	if err := os.MkdirAll(tempRoot, 0o700); err != nil {
		t.Fatalf("create isolated Computer probe root: %v", err)
	}
	reportPath := filepath.Join(tempRoot, "computer-probe.json")
	if _, err := os.Stat(reportPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("isolated Computer report already exists: %s", reportPath)
	}

	timeout := productionProbeDuration(t, "ASTRO_PROBE_TIMEOUT", 90*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	loaded, err := iconeu.LoadCurrentDomeManifest(dataRoot)
	if err != nil {
		t.Fatalf("load current ICON-EU Astrodome manifest: %v", err)
	}
	zone := strings.TrimSpace(os.Getenv("ASTRO_PROBE_TIMEZONE"))
	if zone == "" {
		zone = "UTC"
	}
	if _, err := time.LoadLocation(zone); err != nil {
		t.Fatalf("ASTRO_PROBE_TIMEZONE: %v", err)
	}
	latitude := productionProbeFloat(t, "ASTRO_PROBE_LAT", -90, 90)
	longitude := productionProbeFloat(t, "ASTRO_PROBE_LON", -180, 180)
	calibration := forecast.DefaultAstrodomeScienceCalibration()
	backend, err := NewBackend(Config{
		Enabled: true, DataRoot: dataRoot, MaxStaleAge: 365 * 24 * time.Hour,
		TimeZones:   productionComputerProbeTimeZone(zone),
		Now:         func() time.Time { return loaded.BaseTime.Add(6 * time.Hour) },
		Calibration: calibration,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := backend.Prepare(ctx, directional.AstrodomeAdmission{
		TelegramUserID: 1,
		Point:          directional.SavedPoint{Latitude: latitude, Longitude: longitude},
	})
	if err != nil {
		t.Fatalf("prepare Computer request: %v", err)
	}
	nodeWorkers := productionProbePositiveInt(t, "ASTRO_PROBE_NODE_WORKERS", 10, 64)
	ecCodesWorkers := productionProbePositiveInt(t, "ASTRO_PROBE_ECCODES_WORKERS", 8, 16)
	computer, err := NewComputer(ComputerConfig{
		DataRoot: dataRoot, TempRoot: filepath.Join(tempRoot, "cdo"),
		ECCodesWorkers: ecCodesWorkers, NodeWorkers: nodeWorkers,
		ResidentLimitBytes: productionProbeUint64(t, "ASTRO_PROBE_RESIDENT_LIMIT_BYTES", 16<<30),
		ScienceCalibration: calibration,
		Logf:               func(format string, args ...any) { t.Logf(format, args...) },
	})
	if err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	startedAt := time.Now()
	input, err := computer.ComputeAstrodomeDataset(ctx, prepared.Source, prepared.Payload)
	duration := time.Since(startedAt)
	if err != nil {
		t.Fatalf("compute full Astrodome dataset: %v", err)
	}
	dataset, err := directional.BuildAstrodomeDataset(input)
	if err != nil {
		t.Fatalf("validate full Astrodome dataset: %v", err)
	}
	runtime.ReadMemStats(&after)
	profile, err := forecast.NewAstrodomeGridProfile(forecast.AstrodomeGridProductionV2)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := profile.Nodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(dataset.Frames) != 72 || len(nodes) != 129 {
		t.Fatalf("full Computer product shape = %d x %d; want 72 x 129", len(dataset.Frames), len(nodes))
	}
	report := productionComputerProbeReport{
		RunID: loaded.RunID, ManifestSHA256: loaded.ManifestSHA256,
		GridProfile: string(profile.ID), RefractionVersion: forecast.AstrodomeRefractionIntegratorVersion,
		ScienceVersion:     forecast.AstrodomeScienceVersion,
		SciencePathVersion: forecast.AstrodomeSciencePathContractVersion,
		GoVersion:          runtime.Version(), NodeWorkers: nodeWorkers, ECCodesWorkers: ecCodesWorkers,
		FrameCount: len(dataset.Frames), NodesPerFrame: len(nodes), NodeHours: len(dataset.Frames) * len(nodes),
		UnavailableByState: make(map[string]int), CalculationDurationMS: duration.Milliseconds(),
		HeapAllocBeforeBytes: before.HeapAlloc, HeapAllocAfterBytes: after.HeapAlloc,
		HeapInUseAfterBytes: after.HeapInuse, TotalAllocDeltaBytes: after.TotalAlloc - before.TotalAlloc,
		GarbageCollectionDelta: after.NumGC - before.NumGC,
	}
	for _, frame := range dataset.Frames {
		if len(frame.Nodes) != len(nodes) {
			t.Fatalf("frame %s has %d nodes; want %d", frame.ValidAt.Format(time.RFC3339), len(frame.Nodes), len(nodes))
		}
		for _, node := range frame.Nodes {
			switch node.State {
			case directional.AstrodomeDatasetStateValid:
				report.Calculated++
				report.Valid++
			case directional.AstrodomeDatasetStatePrecipitationVeto:
				// A precipitation veto is a calculated physical result whose
				// operational status forbids telescope use. It is not missing data.
				report.Calculated++
				report.PrecipitationVeto++
			default:
				key := node.State + "/" + strings.TrimSpace(node.LimitingFactor)
				report.UnavailableByState[key]++
			}
		}
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(reportPath, encoded, 0o600); err != nil {
		t.Fatalf("write Computer probe report: %v", err)
	}
	t.Logf("full Computer report: %s", reportPath)
}

func productionProbeAbsoluteDir(t *testing.T, name string) string {
	t.Helper()
	value := filepath.Clean(strings.TrimSpace(os.Getenv(name)))
	if !filepath.IsAbs(value) || value == string(filepath.Separator) || value == "." {
		t.Fatalf("%s must be a narrow absolute directory", name)
	}
	return value
}

func productionProbePositiveInt(t *testing.T, name string, fallback, maximum int) int {
	t.Helper()
	value := fallback
	if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		value = parsed
	}
	if value < 1 || value > maximum {
		t.Fatalf("%s must be in [1,%d]", name, maximum)
	}
	return value
}

func productionProbeUint64(t *testing.T, name string, fallback uint64) uint64 {
	t.Helper()
	value := fallback
	if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		value = parsed
	}
	if value == 0 {
		t.Fatalf("%s must be positive", name)
	}
	return value
}

func productionProbeFloat(t *testing.T, name string, minimum, maximum float64) float64 {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(name))
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < minimum || value > maximum {
		t.Fatalf("%s must be finite and in [%g,%g]", name, minimum, maximum)
	}
	return value
}

func productionProbeDuration(t *testing.T, name string, fallback time.Duration) time.Duration {
	t.Helper()
	value := fallback
	if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		value = parsed
	}
	if value <= 0 {
		t.Fatalf("%s must be positive", name)
	}
	return value
}
