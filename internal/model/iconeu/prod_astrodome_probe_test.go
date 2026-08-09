package iconeu

// TEMPORARY PRODUCTION DIAGNOSTIC. DO NOT COMMIT.
//
// This opt-in test exercises the exact Astrodome refraction and science path
// against one immutable, already-published ICON-EU volume. Ordinary test runs
// always skip it. It neither submits a job nor writes a user dataset.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"bot_astrosferum/internal/app/directional"
	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
)

const (
	prodAstrodomeProbeGateValue    = "embedded-v28-v22"
	prodAstrodomeProbeDefaultLimit = uint64(16 << 30)
)

type prodAstrodomeProbeRecord struct {
	Type             string         `json:"type"`
	RunID            string         `json:"run_id,omitempty"`
	ManifestSHA256   string         `json:"manifest_sha256,omitempty"`
	GridProfile      string         `json:"grid_profile,omitempty"`
	ScienceVersion   string         `json:"science_version,omitempty"`
	PathVersion      string         `json:"path_version,omitempty"`
	ValidAt          string         `json:"valid_at,omitempty"`
	ForecastHour     int            `json:"forecast_hour,omitempty"`
	NodeIndex        int            `json:"node_index,omitempty"`
	ElevationDegrees float64        `json:"elevation_deg,omitempty"`
	AzimuthDegrees   *float64       `json:"azimuth_deg,omitempty"`
	Stage            string         `json:"stage,omitempty"`
	Status           string         `json:"status,omitempty"`
	Category         string         `json:"category,omitempty"`
	Error            string         `json:"error,omitempty"`
	DurationMS       int64          `json:"duration_ms,omitempty"`
	Overall          *float64       `json:"overall,omitempty"`
	Seeing500Arcsec  *float64       `json:"seeing_500nm_arcsec,omitempty"`
	Quality          string         `json:"quality,omitempty"`
	SelectedNodes    []int          `json:"selected_nodes,omitempty"`
	SelectedHours    []int          `json:"selected_hours,omitempty"`
	UniqueColumns    int            `json:"unique_columns,omitempty"`
	ProjectedBytes   uint64         `json:"projected_resident_bytes,omitempty"`
	Counts           map[string]int `json:"counts,omitempty"`
}

type prodAstrodomeProbeCase struct {
	validAt      time.Time
	forecastHour int
	node         forecast.AstrodomeGridNode
}

func TestProductionAstrodomeScienceProbe(t *testing.T) {
	if os.Getenv("ASTRO_ASTRODOME_PROBE") != prodAstrodomeProbeGateValue {
		t.Skip("temporary production Astrodome probe is disabled")
	}

	dataRoot := prodAstrodomeProbeRequiredAbsoluteDir(t, "ASTRO_PROBE_DATA_ROOT")
	tempRoot := prodAstrodomeProbeRequiredAbsoluteDir(t, "ASTRO_PROBE_TEMP_ROOT")
	if err := os.MkdirAll(tempRoot, 0o700); err != nil {
		t.Fatalf("create isolated probe directory: %v", err)
	}
	resultPath := filepath.Join(tempRoot, "probe-results.jsonl")
	resultFile, err := os.OpenFile(resultPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("create isolated probe report: %v", err)
	}
	defer func() {
		if closeErr := resultFile.Close(); closeErr != nil {
			t.Errorf("close probe report: %v", closeErr)
		}
	}()
	reporter := json.NewEncoder(io.MultiWriter(os.Stdout, resultFile))

	timeout := prodAstrodomeProbeDuration(t, "ASTRO_PROBE_TIMEOUT", 90*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	lockRoot := filepath.Join(dataRoot, "state", "run-leases")
	gate, err := directional.NewExecutionGate(lockRoot,
		prodAstrodomeProbePositiveInt(t, "ASTRO_PROBE_DIRECTIONAL_CONCURRENCY", 1, directional.MaxConcurrency))
	if err != nil {
		t.Fatalf("initialize shared directional gate: %v", err)
	}
	gateLease, err := gate.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire shared directional slot: %v", err)
	}
	defer func() {
		if closeErr := gateLease.Close(); closeErr != nil {
			t.Errorf("release shared directional slot: %v", closeErr)
		}
	}()

	loaded, err := LoadCurrentDomeManifest(dataRoot)
	if err != nil {
		t.Fatalf("load current ready ICON-EU Astrodome manifest: %v", err)
	}
	leaseManager, err := model.NewRunLeaseManager(lockRoot)
	if err != nil {
		t.Fatalf("initialize run-retention leases: %v", err)
	}
	runLease, err := leaseManager.AcquireShared(ctx, "icon-eu", loaded.RunID, model.RunRetentionLeaseDigest)
	if err != nil {
		t.Fatalf("acquire immutable run-retention lease: %v", err)
	}
	defer func() {
		if closeErr := runLease.Close(); closeErr != nil {
			t.Errorf("release run-retention lease: %v", closeErr)
		}
	}()

	location := forecast.Location{
		Latitude:  prodAstrodomeProbeFloat(t, "ASTRO_PROBE_LAT", -90, 90),
		Longitude: prodAstrodomeProbeFloat(t, "ASTRO_PROBE_LON", -180, 180),
		TimeZone:  strings.TrimSpace(os.Getenv("ASTRO_PROBE_TIMEZONE")),
	}
	if location.TimeZone == "" {
		location.TimeZone = "UTC"
	}
	if _, err := time.LoadLocation(location.TimeZone); err != nil {
		t.Fatalf("ASTRO_PROBE_TIMEZONE: %v", err)
	}
	if !Coverage().Contains(location) {
		t.Fatal("probe location is outside the current ICON-EU coverage")
	}

	profile, err := prodAstrodomeProbeProfile(loaded.GridProfile)
	if err != nil {
		t.Fatal(err)
	}
	allNodes, err := profile.Nodes()
	if err != nil {
		t.Fatalf("construct production grid nodes: %v", err)
	}
	nodes := prodAstrodomeProbeNodes(t, profile, allNodes)
	hours := prodAstrodomeProbeHours(t, loaded)
	selectedNodeIndexes := make([]int, len(nodes))
	for index := range nodes {
		selectedNodeIndexes[index] = nodes[index].Index
	}

	if err := reporter.Encode(prodAstrodomeProbeRecord{
		Type: "metadata", RunID: loaded.RunID, ManifestSHA256: loaded.ManifestSHA256,
		GridProfile: string(profile.ID), ScienceVersion: forecast.AstrodomeScienceVersion,
		PathVersion:   forecast.AstrodomeSciencePathContractVersion,
		SelectedNodes: selectedNodeIndexes, SelectedHours: hours,
	}); err != nil {
		t.Fatalf("write probe metadata: %v", err)
	}

	volume, err := NewDomeVolume(dataRoot, filepath.Join(tempRoot, "cdo"), loaded,
		prodAstrodomeProbePositiveInt(t, "ASTRO_PROBE_ECCODES_WORKERS", 4, 64))
	if err != nil {
		t.Fatalf("open immutable ICON-EU Astrodome volume: %v", err)
	}
	refractionCalibration := forecast.DefaultAstrodomeRefractionCalibration()
	preloadStarted := time.Now()
	footprint, err := prodAstrodomeProbePreload(ctx, volume, location, nodes, refractionCalibration,
		prodAstrodomeProbeUint64(t, "ASTRO_PROBE_RESIDENT_LIMIT_BYTES", prodAstrodomeProbeDefaultLimit))
	if err != nil {
		t.Fatalf("preload selected immutable footprint: %v", err)
	}
	defer func() {
		if closeErr := footprint.Close(); closeErr != nil {
			t.Errorf("close selected footprint: %v", closeErr)
		}
	}()
	report := footprint.Report()
	if err := reporter.Encode(prodAstrodomeProbeRecord{
		Type: "preload", RunID: loaded.RunID, Status: "ok", DurationMS: time.Since(preloadStarted).Milliseconds(),
		UniqueColumns: report.UniqueColumns, ProjectedBytes: report.ProjectedResidentBytes,
	}); err != nil {
		t.Fatalf("write preload report: %v", err)
	}

	reconstructor, err := forecast.NewAstrodomePrimitiveReconstructor(footprint)
	if err != nil {
		t.Fatalf("initialize primitive reconstructor: %v", err)
	}
	surfaceHeightM, err := footprint.AstrodomeSurfaceHeightAt(ctx, location)
	if err != nil {
		t.Fatalf("resolve observer surface: %v", err)
	}
	observerHeightM := surfaceHeightM + forecast.AstrodomeRefractionApertureHeightAGLM
	scienceCalibration := forecast.DefaultAstrodomeScienceCalibration()
	refractivityCalibration := forecast.DefaultAstrodomeRefractivityCalibration()
	identity := volume.Identity()
	nodeWorkers := prodAstrodomeProbePositiveInt(t, "ASTRO_PROBE_NODE_WORKERS", 8, 64)
	counts := make(map[string]int)
	unexpected := 0

	for _, forecastHour := range hours {
		validAt := loaded.BaseTime.Add(time.Duration(forecastHour) * time.Hour)
		site, siteErr := footprint.AstrodomeScienceSiteAt(ctx, validAt, location)
		if siteErr != nil {
			record := prodAstrodomeProbeFailure(loaded.RunID, validAt, forecastHour,
				forecast.AstrodomeGridNode{}, "site", time.Time{}, siteErr)
			if err := reporter.Encode(record); err != nil {
				t.Fatalf("write site failure: %v", err)
			}
			counts[record.Category]++
			unexpected++
			continue
		}
		field, fieldErr := forecast.NewAstrodomeReconstructedRefractivityField(
			reconstructor, validAt, refractivityCalibration)
		if fieldErr != nil {
			record := prodAstrodomeProbeFailure(loaded.RunID, validAt, forecastHour,
				forecast.AstrodomeGridNode{}, "refractivity_field", time.Time{}, fieldErr)
			if err := reporter.Encode(record); err != nil {
				t.Fatalf("write refractivity-field failure: %v", err)
			}
			counts[record.Category]++
			unexpected++
			continue
		}

		cases := make([]prodAstrodomeProbeCase, len(nodes))
		for index, node := range nodes {
			cases[index] = prodAstrodomeProbeCase{validAt: validAt, forecastHour: forecastHour, node: node}
		}
		records := prodAstrodomeProbeRunCases(ctx, min(nodeWorkers, len(cases)), cases, func(probeCase prodAstrodomeProbeCase) prodAstrodomeProbeRecord {
			return prodAstrodomeProbeRunNode(ctx, footprint, reconstructor, field, location, observerHeightM,
				identity, site, refractionCalibration, scienceCalibration, probeCase)
		})
		for _, record := range records {
			if err := reporter.Encode(record); err != nil {
				t.Fatalf("write node result: %v", err)
			}
			counts[record.Category]++
			if record.Status == "error" {
				unexpected++
			}
		}
	}
	if err := reporter.Encode(prodAstrodomeProbeRecord{
		Type: "summary", RunID: loaded.RunID, ScienceVersion: forecast.AstrodomeScienceVersion,
		PathVersion: forecast.AstrodomeSciencePathContractVersion, Status: map[bool]string{true: "failed", false: "passed"}[unexpected > 0],
		Counts: counts,
	}); err != nil {
		t.Fatalf("write probe summary: %v", err)
	}
	if err := resultFile.Sync(); err != nil {
		t.Errorf("sync probe report: %v", err)
	}
	if unexpected > 0 {
		t.Errorf("strict Astrodome production probe found %d unexpected numerical or data failures; see %s", unexpected, resultPath)
	}
}

func prodAstrodomeProbeRunCases(
	ctx context.Context,
	workers int,
	cases []prodAstrodomeProbeCase,
	run func(prodAstrodomeProbeCase) prodAstrodomeProbeRecord,
) []prodAstrodomeProbeRecord {
	records := make([]prodAstrodomeProbeRecord, len(cases))
	tasks := make(chan int)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range tasks {
				if ctx.Err() != nil {
					records[index] = prodAstrodomeProbeFailure("", cases[index].validAt,
						cases[index].forecastHour, cases[index].node, "context", time.Time{}, ctx.Err())
					continue
				}
				records[index] = run(cases[index])
			}
		}()
	}
	for index := range cases {
		tasks <- index
	}
	close(tasks)
	wait.Wait()
	return records
}

func prodAstrodomeProbeRunNode(
	ctx context.Context,
	footprint *DomeAstrodomeFootprint,
	reconstructor *forecast.AstrodomePrimitiveReconstructor,
	field forecast.AstrodomeRefractionField,
	observer forecast.Location,
	observerHeightM float64,
	identity forecast.AstrodomePrimitiveVolumeIdentity,
	site forecast.AstrodomeScienceSiteInputs,
	refractionCalibration forecast.AstrodomeRefractionCalibration,
	scienceCalibration forecast.AstrodomeScienceCalibration,
	probeCase prodAstrodomeProbeCase,
) prodAstrodomeProbeRecord {
	startedAt := time.Now()
	node := probeCase.node
	initial, err := forecast.NewAstrodomeRay(observer, observerHeightM, node.ElevationDegrees, node.AzimuthDegrees)
	if err != nil {
		return prodAstrodomeProbeFailure(identity.RunID, probeCase.validAt, probeCase.forecastHour,
			node, "ray", startedAt, err)
	}
	traced, err := forecast.TraceAstrodomeRefractedRay(ctx, field, initial, refractionCalibration)
	if errors.Is(err, forecast.ErrAstrodomeRefractionTerrain) {
		return prodAstrodomeProbeRecord{
			Type: "node", RunID: identity.RunID, ValidAt: probeCase.validAt.Format(time.RFC3339),
			ForecastHour: probeCase.forecastHour, NodeIndex: node.Index, ElevationDegrees: node.ElevationDegrees,
			AzimuthDegrees: node.AzimuthDegrees, Stage: "refraction", Status: "physical",
			Category: "terrain_blocked", Error: err.Error(), DurationMS: time.Since(startedAt).Milliseconds(),
		}
	}
	if err != nil {
		return prodAstrodomeProbeFailure(identity.RunID, probeCase.validAt, probeCase.forecastHour,
			node, "refraction", startedAt, err)
	}
	path, err := footprint.BuildAstrodomeSciencePath(ctx, traced, probeCase.validAt, scienceCalibration)
	if err != nil {
		return prodAstrodomeProbeFailure(identity.RunID, probeCase.validAt, probeCase.forecastHour,
			node, "science_path", startedAt, err)
	}
	result, err := forecast.ComputeAstrodomeScienceNodeRefracted(
		ctx, reconstructor, traced, probeCase.validAt, path, site, scienceCalibration)
	if err != nil {
		return prodAstrodomeProbeFailure(identity.RunID, probeCase.validAt, probeCase.forecastHour,
			node, "science_integral", startedAt, err)
	}
	return prodAstrodomeProbeRecord{
		Type: "node", RunID: identity.RunID, ValidAt: probeCase.validAt.Format(time.RFC3339),
		ForecastHour: probeCase.forecastHour, NodeIndex: node.Index, ElevationDegrees: node.ElevationDegrees,
		AzimuthDegrees: node.AzimuthDegrees, Stage: "complete", Status: "ok", Category: "available",
		DurationMS: time.Since(startedAt).Milliseconds(), Overall: result.Overall,
		Seeing500Arcsec: result.Seeing500Arcsec, Quality: string(result.Quality.Category),
	}
}

func prodAstrodomeProbeFailure(
	runID string,
	validAt time.Time,
	forecastHour int,
	node forecast.AstrodomeGridNode,
	stage string,
	startedAt time.Time,
	err error,
) prodAstrodomeProbeRecord {
	duration := int64(0)
	if !startedAt.IsZero() {
		duration = time.Since(startedAt).Milliseconds()
	}
	reason := "unknown error"
	if err != nil {
		reason = err.Error()
	}
	return prodAstrodomeProbeRecord{
		Type: "node", RunID: runID, ValidAt: validAt.Format(time.RFC3339), ForecastHour: forecastHour,
		NodeIndex: node.Index, ElevationDegrees: node.ElevationDegrees, AzimuthDegrees: node.AzimuthDegrees,
		Stage: stage, Status: "error", Category: prodAstrodomeProbeCategory(stage, err), Error: reason,
		DurationMS: duration,
	}
}

func prodAstrodomeProbeCategory(stage string, err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, forecast.ErrAstrodomeRefractionTerrain):
		return "terrain_blocked"
	case errors.Is(err, forecast.ErrAstrodomeRefractionNonConvergence):
		return "refraction_nonconvergence"
	case errors.Is(err, forecast.ErrAstrodomeScienceTerrainBlocked):
		return "terrain_blocked"
	case errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition):
		return prodAstrodomeProbePartitionCategory(err)
	case errors.Is(err, forecast.ErrAstrodomeScienceNonConvergence):
		return "integration_nonconvergence"
	default:
		return stage + "_other"
	}
}

func prodAstrodomeProbePartitionCategory(err error) string {
	message := err.Error()
	switch {
	case strings.Contains(message, "endpoint sliver"):
		return "partition_endpoint_sliver"
	case strings.Contains(message, "physical-root gap"):
		return "partition_physical_root_gap"
	case strings.Contains(message, "monotone root uncertainty"):
		return "partition_root_uncertainty"
	case strings.Contains(message, "WMO transition"):
		return "partition_wmo_transition"
	case strings.Contains(message, "distinct ICON-EU physical events"):
		return "partition_compound_event_collision"
	case strings.Contains(message, " differs from "):
		return "partition_branch_mismatch"
	default:
		return "incomplete_partition"
	}
}

func prodAstrodomeProbePreload(
	ctx context.Context,
	volume *DomeVolume,
	observer forecast.Location,
	nodes []forecast.AstrodomeGridNode,
	refraction forecast.AstrodomeRefractionCalibration,
	residentLimit uint64,
) (*DomeAstrodomeFootprint, error) {
	maximumTopM, err := volume.domeAstrodomeMaximumTopHeight(ctx)
	if err != nil {
		return nil, err
	}
	rays, envelopePathM, err := domeAstrodomeCanonicalEnvelopeRays(
		observer, nodes, maximumTopM, refraction.MaximumPathLengthM)
	if err != nil {
		return nil, err
	}
	addresses, err := volume.domeAstrodomeCorridorAddresses(ctx, observer, rays, false)
	if err != nil {
		return nil, err
	}
	projection := domeAstrodomeProjectedResidentBytes(len(addresses), len(volume.manifest.ModelSteps))
	if projection > residentLimit {
		return nil, fmt.Errorf("selected footprint projects %d bytes for %d columns, above %d-byte probe limit",
			projection, len(addresses), residentLimit)
	}
	volume.mu.Lock()
	previousLimit := volume.cacheLimit
	if volume.cacheLimit < len(addresses) {
		volume.cacheLimit = len(addresses)
	}
	volume.mu.Unlock()
	rollback := func() {
		volume.mu.Lock()
		volume.cacheLimit = previousLimit
		volume.pruneDomeColumnCacheLocked()
		volume.mu.Unlock()
	}
	for offset := 0; offset < len(addresses); offset += DomeAstrodomePreloadBatchColumns {
		if err := ctx.Err(); err != nil {
			rollback()
			return nil, err
		}
		end := min(offset+DomeAstrodomePreloadBatchColumns, len(addresses))
		columns, extractErr := volume.extractDomeAstrodomeBatch(ctx, addresses[offset:end])
		if extractErr != nil {
			rollback()
			return nil, extractErr
		}
		volume.mu.Lock()
		for id, column := range columns {
			volume.clock++
			volume.cache[id] = domeVolumeCacheEntry{column: column, used: volume.clock}
		}
		volume.mu.Unlock()
	}
	ids := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		ids[address.id] = struct{}{}
	}
	sourceColumnPlanDigest, err := domeAstrodomeSourceColumnPlanDigest(addresses, volume.manifest.Grid)
	if err != nil {
		rollback()
		return nil, err
	}
	return &DomeAstrodomeFootprint{
		volume: volume, columnIDs: ids, previousLimit: previousLimit,
		report: DomeAstrodomePreloadReport{
			UniqueColumns: len(addresses), ProjectedResidentBytes: projection,
			ExtractionBatches: (len(addresses) + DomeAstrodomePreloadBatchColumns - 1) / DomeAstrodomePreloadBatchColumns,
			GridMarginColumns: DomeAstrodomeRefractionGridMargin, EnvelopePathM: envelopePathM,
			SourceColumnPlanDigest: sourceColumnPlanDigest,
		},
	}, nil
}

func prodAstrodomeProbeProfile(storage model.StorageProfile) (forecast.AstrodomeGridProfile, error) {
	switch storage {
	case model.StorageProfileDense:
		return forecast.NewAstrodomeGridProfile(forecast.AstrodomeGridProductionV2)
	case model.StorageProfileSparse:
		return forecast.NewAstrodomeGridProfile(forecast.AstrodomeGridSparseStorageV1)
	default:
		return forecast.AstrodomeGridProfile{}, fmt.Errorf("ready manifest has unsupported storage profile %q", storage)
	}
}

func prodAstrodomeProbeNodes(
	t *testing.T,
	profile forecast.AstrodomeGridProfile,
	all []forecast.AstrodomeGridNode,
) []forecast.AstrodomeGridNode {
	t.Helper()
	spec := strings.TrimSpace(os.Getenv("ASTRO_PROBE_NODES"))
	if strings.EqualFold(spec, "all") {
		return append([]forecast.AstrodomeGridNode(nil), all...)
	}
	selected := make(map[int]struct{})
	if spec != "" && !strings.EqualFold(spec, "stratified") {
		for _, index := range prodAstrodomeProbeIntList(t, "ASTRO_PROBE_NODES", spec) {
			if index < 0 || index >= len(all) {
				t.Fatalf("ASTRO_PROBE_NODES index %d is outside [0,%d)", index, len(all))
			}
			selected[index] = struct{}{}
		}
	} else {
		// The complete outer 10-degree ring exercises the longest and most
		// topographically sensitive paths. Four cardinal samples on each
		// remaining ring plus the one zenith node span the rest of the dome.
		for _, node := range all {
			if node.RingIndex == 0 || node.RingIndex == -1 {
				selected[node.Index] = struct{}{}
			}
		}
		offset := 0
		for _, ring := range profile.Rings {
			for _, azimuthIndex := range []int{0, ring.AzimuthCount / 4, ring.AzimuthCount / 2, 3 * ring.AzimuthCount / 4} {
				selected[offset+azimuthIndex] = struct{}{}
			}
			offset += ring.AzimuthCount
		}
		if len(all) > 153 {
			selected[153] = struct{}{}
		}
	}
	if len(selected) == 0 {
		t.Fatal("ASTRO_PROBE_NODES selected no nodes")
	}
	indexes := make([]int, 0, len(selected))
	for index := range selected {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	result := make([]forecast.AstrodomeGridNode, len(indexes))
	for position, index := range indexes {
		result[position] = all[index]
	}
	return result
}

func prodAstrodomeProbeHours(t *testing.T, loaded LoadedDomeManifest) []int {
	t.Helper()
	spec := strings.TrimSpace(os.Getenv("ASTRO_PROBE_HOURS"))
	available := make(map[int]struct{}, len(loaded.ModelSteps))
	for _, step := range loaded.ModelSteps {
		available[step.ForecastHour] = struct{}{}
	}
	if spec != "" {
		hours := prodAstrodomeProbeIntList(t, "ASTRO_PROBE_HOURS", spec)
		for _, hour := range hours {
			if hour <= 0 {
				t.Fatalf("ASTRO_PROBE_HOURS needs positive forecast hours, got %d", hour)
			}
			if _, ok := available[hour]; !ok {
				t.Fatalf("ASTRO_PROBE_HOURS f%03d is absent from the ready volume", hour)
			}
			if _, ok := available[hour-1]; !ok {
				t.Fatalf("ASTRO_PROBE_HOURS f%03d has no preceding native precipitation term", hour)
			}
		}
		return hours
	}
	now := time.Now().UTC()
	if value := strings.TrimSpace(os.Getenv("ASTRO_PROBE_NOW")); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			t.Fatalf("ASTRO_PROBE_NOW: %v", err)
		}
		now = parsed.UTC()
	}
	first := now.Truncate(time.Hour)
	if !now.Equal(first) {
		first = first.Add(time.Hour)
	}
	if !first.After(loaded.BaseTime) {
		first = loaded.BaseTime.Add(time.Hour)
	}
	window := make([]int, 0, forecast.AstrodomeFrameCount)
	for validAt := first; len(window) < forecast.AstrodomeFrameCount; validAt = validAt.Add(time.Hour) {
		hour := int(validAt.Sub(loaded.BaseTime) / time.Hour)
		if _, ok := available[hour]; !ok {
			break
		}
		if _, ok := available[hour-1]; !ok {
			break
		}
		window = append(window, hour)
	}
	if len(window) == 0 {
		t.Fatal("current ready volume has no user-facing consecutive hourly window")
	}
	positions := []int{0, len(window) / 4, len(window) / 2, 3 * len(window) / 4, len(window) - 1}
	selected := make(map[int]struct{}, len(positions))
	for _, position := range positions {
		selected[window[position]] = struct{}{}
	}
	hours := make([]int, 0, len(selected))
	for hour := range selected {
		hours = append(hours, hour)
	}
	sort.Ints(hours)
	return hours
}

func prodAstrodomeProbeRequiredAbsoluteDir(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" || !filepath.IsAbs(value) {
		t.Fatalf("%s must be an absolute path", name)
	}
	return filepath.Clean(value)
}

func prodAstrodomeProbeFloat(t *testing.T, name string, minimum, maximum float64) float64 {
	t.Helper()
	value, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < minimum || value > maximum {
		t.Fatalf("%s must be finite and within [%g,%g]", name, minimum, maximum)
	}
	return value
}

func prodAstrodomeProbePositiveInt(t *testing.T, name string, fallback, maximum int) int {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > maximum {
		t.Fatalf("%s must be within [1,%d]", name, maximum)
	}
	return parsed
}

func prodAstrodomeProbeUint64(t *testing.T, name string, fallback uint64) uint64 {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		t.Fatalf("%s must be a positive byte count", name)
	}
	return parsed
}

func prodAstrodomeProbeDuration(t *testing.T, name string, fallback time.Duration) time.Duration {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		t.Fatalf("%s must be a positive Go duration", name)
	}
	return parsed
}

func prodAstrodomeProbeIntList(t *testing.T, name, value string) []int {
	t.Helper()
	seen := make(map[int]struct{})
	result := make([]int, 0)
	for _, raw := range strings.Split(value, ",") {
		parsed, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			t.Fatalf("%s contains invalid integer %q", name, raw)
		}
		if _, exists := seen[parsed]; exists {
			continue
		}
		seen[parsed] = struct{}{}
		result = append(result, parsed)
	}
	if len(result) == 0 {
		t.Fatalf("%s must select at least one value", name)
	}
	sort.Ints(result)
	return result
}
