package iconeu

// TEMPORARY PRODUCTION RELEASE DIAGNOSTIC. DO NOT COMMIT.
//
// This opt-in test measures the piecewise-constant production-v2 sky
// representation against two independently denser node sets. It calculates
// every physical node through the production refraction and science kernels;
// no seeing, tau0, cloud transmission, PWV, Overall, or quality value is
// interpolated. Ordinary test runs always skip it and it never publishes a
// user dataset.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"bot_astrosferum/internal/app/directional"
	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
)

const prodAstrodomeConvergenceGateValue = "production-v2-dense-v1-uniform32-v1"

type prodAstrodomeConvergenceNodeResult struct {
	node  forecast.AstrodomeScienceNode
	stage string
	err   error
}

type prodAstrodomeConvergenceMetric struct {
	ComparableAreaFraction       float64 `json:"comparable_area_fraction"`
	MaximumAbsoluteDelta         float64 `json:"maximum_absolute_delta"`
	P95AbsoluteDelta             float64 `json:"p95_absolute_delta"`
	MaximumSample                int     `json:"maximum_sample_node"`
	MaximumSupport               int     `json:"maximum_support_node"`
	CentresBelow20ComparableArea float64 `json:"reference_centres_below_20_comparable_area_fraction"`
	CentresBelow20MaximumDelta   float64 `json:"reference_centres_below_20_maximum_absolute_delta"`
	CentresBelow20P95Delta       float64 `json:"reference_centres_below_20_p95_absolute_delta"`
}

type prodAstrodomeConvergenceExtrema struct {
	FineNodeIndex     int     `json:"fine_node_index"`
	SupportNodeIndex  int     `json:"support_node_index"`
	FineValue         float64 `json:"fine_value"`
	SupportValue      float64 `json:"support_value"`
	AngularSeparation float64 `json:"angular_separation_deg"`
}

type prodAstrodomeConvergenceComparison struct {
	SupportGrid                      string                                    `json:"support_grid"`
	SupportNodeCount                 int                                       `json:"support_node_count"`
	ReferenceGrid                    string                                    `json:"reference_grid"`
	ReferenceNodeCount               int                                       `json:"reference_node_count"`
	AvailabilityMismatchAreaFraction float64                                   `json:"availability_mismatch_area_fraction"`
	Tau0StateMismatchAreaFraction    float64                                   `json:"tau0_state_mismatch_area_fraction"`
	PWVStateMismatchAreaFraction     float64                                   `json:"pwv_state_mismatch_area_fraction"`
	OverallAboveQuarterAreaFraction  float64                                   `json:"overall_delta_gt_0_25_area_fraction"`
	OverallAboveHalfAreaFraction     float64                                   `json:"overall_delta_gt_0_5_area_fraction"`
	LimiterMismatchAreaFraction      float64                                   `json:"limiter_mismatch_area_fraction"`
	LimiterConfusionAreaFraction     map[string]float64                        `json:"limiter_confusion_area_fraction"`
	Metrics                          map[string]prodAstrodomeConvergenceMetric `json:"metrics"`
	OverallMinimum                   prodAstrodomeConvergenceExtrema           `json:"overall_minimum_direction"`
	OverallMaximum                   prodAstrodomeConvergenceExtrema           `json:"overall_maximum_direction"`
}

type prodAstrodomeConvergenceHourReport struct {
	ValidAt      string                             `json:"valid_at"`
	ForecastHour int                                `json:"forecast_hour"`
	Production   prodAstrodomeConvergenceComparison `json:"production_v2"`
	Dense        prodAstrodomeConvergenceComparison `json:"dense_v1"`
}

type prodAstrodomeConvergenceReport struct {
	Method             string                               `json:"method"`
	RunID              string                               `json:"run_id"`
	ManifestSHA256     string                               `json:"manifest_sha256"`
	ScienceVersion     string                               `json:"science_version"`
	PathVersion        string                               `json:"path_version"`
	Latitude           float64                              `json:"latitude"`
	Longitude          float64                              `json:"longitude"`
	AreaSteradians     float64                              `json:"area_steradians"`
	ProductionNodes    int                                  `json:"production_nodes"`
	DenseNodes         int                                  `json:"dense_nodes"`
	ReferenceNodes     int                                  `json:"reference_nodes"`
	UnionNodes         int                                  `json:"union_nodes"`
	UniqueColumns      int                                  `json:"unique_columns"`
	ProjectedBytes     uint64                               `json:"projected_resident_bytes"`
	PreloadDurationMS  int64                                `json:"preload_duration_ms"`
	CalculationMS      int64                                `json:"calculation_duration_ms"`
	UnexpectedFailures int                                  `json:"unexpected_failures"`
	Hours              []prodAstrodomeConvergenceHourReport `json:"hours"`
}

type prodAstrodomeConvergenceGrid struct {
	name       string
	nodes      []forecast.AstrodomeGridNode
	unionIndex []int
}

type prodAstrodomeDirectionKey struct {
	elevation uint64
	azimuth   uint64
	zenith    bool
}

type prodAstrodomeWeightedDelta struct {
	delta        float64
	weight       float64
	sampleIndex  int
	supportIndex int
}

func TestProdAstrodomeConvergenceReferenceAreaAndNestedUnion(t *testing.T) {
	t.Parallel()
	productionProfile, err := forecast.NewAstrodomeGridProfile(forecast.AstrodomeGridProductionV2)
	if err != nil {
		t.Fatal(err)
	}
	denseProfile, err := forecast.NewAstrodomeGridProfile(forecast.AstrodomeGridDenseV1)
	if err != nil {
		t.Fatal(err)
	}
	production, _ := productionProfile.Nodes()
	dense, _ := denseProfile.Nodes()
	reference := prodAstrodomeUniformReferenceNodes()
	weights, area := prodAstrodomeReferenceAreaWeights(reference)
	if len(reference) != 513 || len(weights) != len(reference) ||
		math.Abs(area-2*math.Pi*(1-math.Sin(10*math.Pi/180))) > 2e-14 {
		t.Fatalf("invalid reference area contract: nodes=%d weights=%d area=%.17g", len(reference), len(weights), area)
	}
	union, grids := prodAstrodomeConvergenceUnion([]prodAstrodomeConvergenceGrid{
		{name: "production", nodes: production}, {name: "dense", nodes: dense}, {name: "reference", nodes: reference},
	})
	if len(union) != 789 {
		t.Fatalf("convergence union has %d distinct directions, want 789", len(union))
	}
	centresBelow20Area := 0.0
	for index, node := range reference {
		if node.ElevationDegrees < 20 {
			centresBelow20Area += weights[index]
		}
	}
	wantCentresBelow20Area := 2 * math.Pi * (math.Sin(17.5*math.Pi/180) - math.Sin(10*math.Pi/180))
	if math.Abs(centresBelow20Area-wantCentresBelow20Area) > 2e-14 {
		t.Fatalf("reference centres below 20 degrees cover %.17g sr, want %.17g sr", centresBelow20Area, wantCentresBelow20Area)
	}
	for gridIndex := range grids {
		for nodeIndex, unionIndex := range grids[gridIndex].unionIndex {
			if prodAstrodomeNodeDirectionKey(grids[gridIndex].nodes[nodeIndex]) !=
				prodAstrodomeNodeDirectionKey(union[unionIndex]) {
				t.Fatalf("grid %d node %d maps to a different union direction", gridIndex, nodeIndex)
			}
		}
	}
}

func TestProductionAstrodomeGridConvergence(t *testing.T) {
	if os.Getenv("ASTRO_ASTRODOME_GRID_CONVERGENCE") != prodAstrodomeConvergenceGateValue {
		t.Skip("temporary production Astrodome grid-convergence diagnostic is disabled")
	}
	dataRoot := prodAstrodomeProbeRequiredAbsoluteDir(t, "ASTRO_PROBE_DATA_ROOT")
	tempRoot := prodAstrodomeProbeRequiredAbsoluteDir(t, "ASTRO_PROBE_TEMP_ROOT")
	if err := os.MkdirAll(tempRoot, 0o700); err != nil {
		t.Fatalf("create isolated convergence directory: %v", err)
	}
	resultPath := filepath.Join(tempRoot, "grid-convergence.json")
	if _, err := os.Stat(resultPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("isolated convergence report already exists: %s", resultPath)
	}

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
	defer func() { _ = gateLease.Close() }()

	loaded, err := LoadCurrentDomeManifest(dataRoot)
	if err != nil {
		t.Fatalf("load current ready ICON-EU Astrodome manifest: %v", err)
	}
	if loaded.GridProfile != model.StorageProfileDense {
		t.Fatalf("grid convergence requires the complete dense volume, got %q", loaded.GridProfile)
	}
	leaseManager, err := model.NewRunLeaseManager(lockRoot)
	if err != nil {
		t.Fatalf("initialize run-retention leases: %v", err)
	}
	runLease, err := leaseManager.AcquireShared(ctx, "icon-eu", loaded.RunID, model.RunRetentionLeaseDigest)
	if err != nil {
		t.Fatalf("acquire immutable run-retention lease: %v", err)
	}
	defer func() { _ = runLease.Close() }()

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
		t.Fatal("convergence location is outside the current ICON-EU coverage")
	}

	productionProfile, _ := forecast.NewAstrodomeGridProfile(forecast.AstrodomeGridProductionV2)
	denseProfile, _ := forecast.NewAstrodomeGridProfile(forecast.AstrodomeGridDenseV1)
	productionNodes, _ := productionProfile.Nodes()
	denseNodes, _ := denseProfile.Nodes()
	referenceNodes := prodAstrodomeUniformReferenceNodes()
	unionNodes, grids := prodAstrodomeConvergenceUnion([]prodAstrodomeConvergenceGrid{
		{name: string(forecast.AstrodomeGridProductionV2), nodes: productionNodes},
		{name: string(forecast.AstrodomeGridDenseV1), nodes: denseNodes},
		{name: "verification-uniform32-v1", nodes: referenceNodes},
	})
	hours := prodAstrodomeProbeHours(t, loaded)

	volume, err := NewDomeVolume(dataRoot, filepath.Join(tempRoot, "cdo"), loaded,
		prodAstrodomeProbePositiveInt(t, "ASTRO_PROBE_ECCODES_WORKERS", 4, 64))
	if err != nil {
		t.Fatalf("open immutable ICON-EU Astrodome volume: %v", err)
	}
	refractionCalibration := forecast.DefaultAstrodomeRefractionCalibration()
	preloadStarted := time.Now()
	footprint, err := prodAstrodomeProbePreload(ctx, volume, location, unionNodes, refractionCalibration,
		prodAstrodomeProbeUint64(t, "ASTRO_PROBE_RESIDENT_LIMIT_BYTES", prodAstrodomeProbeDefaultLimit))
	if err != nil {
		t.Fatalf("preload convergence footprint: %v", err)
	}
	defer func() { _ = footprint.Close() }()
	preloadDuration := time.Since(preloadStarted)
	reconstructor, err := forecast.NewAstrodomePrimitiveReconstructor(footprint)
	if err != nil {
		t.Fatalf("initialize primitive reconstructor: %v", err)
	}
	surfaceHeightM, err := footprint.AstrodomeSurfaceHeightAt(ctx, location)
	if err != nil {
		t.Fatalf("resolve observer surface: %v", err)
	}
	observerHeightM := surfaceHeightM + forecast.AstrodomeRefractionApertureHeightAGLM
	identity := volume.Identity()
	scienceCalibration := forecast.DefaultAstrodomeScienceCalibration()
	refractivityCalibration := forecast.DefaultAstrodomeRefractivityCalibration()
	nodeWorkers := prodAstrodomeProbePositiveInt(t, "ASTRO_PROBE_NODE_WORKERS", 8, 64)
	areaWeights, capArea := prodAstrodomeReferenceAreaWeights(referenceNodes)

	report := prodAstrodomeConvergenceReport{
		Method: "nearest-support/no-derived-interpolation/spherical-ring-midpoint-area-v1",
		RunID:  loaded.RunID, ManifestSHA256: loaded.ManifestSHA256,
		ScienceVersion: forecast.AstrodomeScienceVersion,
		PathVersion:    forecast.AstrodomeSciencePathContractVersion,
		Latitude:       location.Latitude, Longitude: location.Longitude,
		AreaSteradians: capArea, ProductionNodes: len(productionNodes), DenseNodes: len(denseNodes),
		ReferenceNodes: len(referenceNodes), UnionNodes: len(unionNodes),
		UniqueColumns:     footprint.Report().UniqueColumns,
		ProjectedBytes:    footprint.Report().ProjectedResidentBytes,
		PreloadDurationMS: preloadDuration.Milliseconds(),
		Hours:             make([]prodAstrodomeConvergenceHourReport, 0, len(hours)),
	}
	calculationStarted := time.Now()
	for _, forecastHour := range hours {
		validAt := loaded.BaseTime.Add(time.Duration(forecastHour) * time.Hour)
		site, siteErr := footprint.AstrodomeScienceSiteAt(ctx, validAt, location)
		if siteErr != nil {
			t.Fatalf("resolve convergence site f%03d: %v", forecastHour, siteErr)
		}
		field, fieldErr := forecast.NewAstrodomeReconstructedRefractivityField(
			reconstructor, validAt, refractivityCalibration)
		if fieldErr != nil {
			t.Fatalf("construct convergence refractivity f%03d: %v", forecastHour, fieldErr)
		}
		results := prodAstrodomeRunConvergenceNodes(ctx, nodeWorkers, unionNodes, func(node forecast.AstrodomeGridNode) prodAstrodomeConvergenceNodeResult {
			return prodAstrodomeCalculateConvergenceNode(ctx, footprint, reconstructor, field, location,
				observerHeightM, identity, site, refractionCalibration, scienceCalibration, validAt, node)
		})
		for _, result := range results {
			if result.err != nil {
				report.UnexpectedFailures++
				t.Logf("f%03d convergence node failed at %s: %v", forecastHour, result.stage, result.err)
			}
		}
		referenceResults := prodAstrodomeConvergenceSelect(results, grids[2].unionIndex)
		report.Hours = append(report.Hours, prodAstrodomeConvergenceHourReport{
			ValidAt: validAt.Format(time.RFC3339), ForecastHour: forecastHour,
			Production: prodAstrodomeCompareGrid(grids[0], results, grids[2], referenceResults, areaWeights, capArea),
			Dense:      prodAstrodomeCompareGrid(grids[1], results, grids[2], referenceResults, areaWeights, capArea),
		})
	}
	report.CalculationMS = time.Since(calculationStarted).Milliseconds()
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal convergence report: %v", err)
	}
	if err := os.WriteFile(resultPath, append(payload, '\n'), 0o600); err != nil {
		t.Fatalf("write convergence report: %v", err)
	}
	t.Logf("Astrodome grid convergence report: %s", resultPath)
	if report.UnexpectedFailures != 0 {
		t.Fatalf("grid-convergence calculation had %d unexpected physical-kernel failures", report.UnexpectedFailures)
	}
}

func prodAstrodomeUniformReferenceNodes() []forecast.AstrodomeGridNode {
	const azimuthCount = 32
	nodes := make([]forecast.AstrodomeGridNode, 0, 16*azimuthCount+1)
	for elevation := 10.0; elevation <= 85; elevation += 5 {
		for azimuthIndex := 0; azimuthIndex < azimuthCount; azimuthIndex++ {
			azimuth := 360 * float64(azimuthIndex) / azimuthCount
			nodes = append(nodes, forecast.AstrodomeGridNode{
				Index: len(nodes), RingIndex: int((elevation - 10) / 5), AzimuthIndex: azimuthIndex,
				ElevationDegrees: elevation, AzimuthDegrees: &azimuth,
			})
		}
	}
	nodes = append(nodes, forecast.AstrodomeGridNode{
		Index: len(nodes), RingIndex: -1, AzimuthIndex: -1,
		ElevationDegrees: forecast.AstrodomeZenithElevationDegrees,
	})
	return nodes
}

func prodAstrodomeConvergenceUnion(grids []prodAstrodomeConvergenceGrid) ([]forecast.AstrodomeGridNode, []prodAstrodomeConvergenceGrid) {
	union := make([]forecast.AstrodomeGridNode, 0)
	indexes := make(map[prodAstrodomeDirectionKey]int)
	for gridIndex := range grids {
		grids[gridIndex].unionIndex = make([]int, len(grids[gridIndex].nodes))
		for nodeIndex, node := range grids[gridIndex].nodes {
			key := prodAstrodomeNodeDirectionKey(node)
			index, exists := indexes[key]
			if !exists {
				index = len(union)
				copy := node
				copy.Index = index
				union = append(union, copy)
				indexes[key] = index
			}
			grids[gridIndex].unionIndex[nodeIndex] = index
		}
	}
	return union, grids
}

func prodAstrodomeNodeDirectionKey(node forecast.AstrodomeGridNode) prodAstrodomeDirectionKey {
	if node.AzimuthDegrees == nil {
		return prodAstrodomeDirectionKey{elevation: math.Float64bits(node.ElevationDegrees), zenith: true}
	}
	return prodAstrodomeDirectionKey{
		elevation: math.Float64bits(node.ElevationDegrees),
		azimuth:   math.Float64bits(*node.AzimuthDegrees),
	}
}

func prodAstrodomeReferenceAreaWeights(nodes []forecast.AstrodomeGridNode) ([]float64, float64) {
	weights := make([]float64, len(nodes))
	const azimuthCount = 32
	for index, node := range nodes {
		if node.AzimuthDegrees == nil {
			weights[index] = 2 * math.Pi * (1 - math.Sin(87.5*math.Pi/180))
			continue
		}
		lower := math.Max(10, node.ElevationDegrees-2.5)
		upper := math.Min(87.5, node.ElevationDegrees+2.5)
		weights[index] = 2 * math.Pi * (math.Sin(upper*math.Pi/180) - math.Sin(lower*math.Pi/180)) / azimuthCount
	}
	total := 0.0
	for _, weight := range weights {
		total += weight
	}
	expected := 2 * math.Pi * (1 - math.Sin(10*math.Pi/180))
	if math.Abs(total-expected) > 2e-14 {
		panic(fmt.Sprintf("Astrodome reference sky area %.17g differs from %.17g", total, expected))
	}
	return weights, expected
}

func prodAstrodomeRunConvergenceNodes(
	ctx context.Context,
	workers int,
	nodes []forecast.AstrodomeGridNode,
	run func(forecast.AstrodomeGridNode) prodAstrodomeConvergenceNodeResult,
) []prodAstrodomeConvergenceNodeResult {
	workers = max(1, min(workers, len(nodes)))
	results := make([]prodAstrodomeConvergenceNodeResult, len(nodes))
	tasks := make(chan int)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range tasks {
				if err := ctx.Err(); err != nil {
					results[index] = prodAstrodomeConvergenceNodeResult{stage: "context", err: err}
					continue
				}
				results[index] = run(nodes[index])
			}
		}()
	}
	for index := range nodes {
		tasks <- index
	}
	close(tasks)
	wait.Wait()
	return results
}

func prodAstrodomeCalculateConvergenceNode(
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
	validAt time.Time,
	definition forecast.AstrodomeGridNode,
) prodAstrodomeConvergenceNodeResult {
	initial, err := forecast.NewAstrodomeRay(observer, observerHeightM, definition.ElevationDegrees, definition.AzimuthDegrees)
	if err != nil {
		return prodAstrodomeConvergenceNodeResult{stage: "ray", err: err}
	}
	traced, err := forecast.TraceAstrodomeRefractedRay(ctx, field, initial, refractionCalibration)
	if errors.Is(err, forecast.ErrAstrodomeRefractionTerrain) {
		return prodAstrodomeConvergenceNodeResult{stage: "refraction", node: forecast.AstrodomeScienceNode{
			State: forecast.AstrodomeScienceNodeTerrainBlocked, ElevationDegrees: definition.ElevationDegrees,
			AzimuthDegrees: definition.AzimuthDegrees, SourceIdentity: identity, ValidAt: validAt,
		}}
	}
	if err != nil {
		return prodAstrodomeConvergenceNodeResult{stage: "refraction", err: err}
	}
	path, err := footprint.BuildAstrodomeSciencePath(ctx, traced, validAt, scienceCalibration)
	if err != nil {
		return prodAstrodomeConvergenceNodeResult{stage: "science_path", err: err}
	}
	result, err := forecast.ComputeAstrodomeScienceNodeRefracted(
		ctx, reconstructor, traced, validAt, path, site, scienceCalibration)
	if err != nil {
		return prodAstrodomeConvergenceNodeResult{stage: "science_integral", err: err}
	}
	return prodAstrodomeConvergenceNodeResult{stage: "complete", node: result}
}

func prodAstrodomeConvergenceSelect(
	results []prodAstrodomeConvergenceNodeResult,
	indexes []int,
) []prodAstrodomeConvergenceNodeResult {
	selected := make([]prodAstrodomeConvergenceNodeResult, len(indexes))
	for index, unionIndex := range indexes {
		selected[index] = results[unionIndex]
	}
	return selected
}

func prodAstrodomeCompareGrid(
	supportGrid prodAstrodomeConvergenceGrid,
	allResults []prodAstrodomeConvergenceNodeResult,
	referenceGrid prodAstrodomeConvergenceGrid,
	referenceResults []prodAstrodomeConvergenceNodeResult,
	weights []float64,
	capArea float64,
) prodAstrodomeConvergenceComparison {
	supportResults := prodAstrodomeConvergenceSelect(allResults, supportGrid.unionIndex)
	comparison := prodAstrodomeConvergenceComparison{
		SupportGrid: supportGrid.name, SupportNodeCount: len(supportGrid.nodes),
		ReferenceGrid: referenceGrid.name, ReferenceNodeCount: len(referenceGrid.nodes),
		LimiterConfusionAreaFraction: make(map[string]float64),
		Metrics:                      make(map[string]prodAstrodomeConvergenceMetric),
	}
	metricNames := []string{"overall", "seeing_500nm_arcsec", "tau0_500nm_ms", "slant_pwv_kg_m2", "cloud_transmission_conservative"}
	deltas := make(map[string][]prodAstrodomeWeightedDelta, len(metricNames))
	centresBelow20Deltas := make(map[string][]prodAstrodomeWeightedDelta, len(metricNames))
	availabilityMismatchArea := 0.0
	tauMismatchArea := 0.0
	pwvMismatchArea := 0.0
	quarterArea := 0.0
	halfArea := 0.0
	limiterMismatchArea := 0.0
	for referenceIndex, definition := range referenceGrid.nodes {
		weight := weights[referenceIndex]
		supportIndex := prodAstrodomeNearestNode(definition, supportGrid.nodes)
		fine := referenceResults[referenceIndex]
		coarse := supportResults[supportIndex]
		if prodAstrodomeConvergenceState(fine) != prodAstrodomeConvergenceState(coarse) {
			availabilityMismatchArea += weight
		}
		for _, metricName := range metricNames {
			fineValue, fineState := prodAstrodomeConvergenceValue(fine, metricName)
			coarseValue, coarseState := prodAstrodomeConvergenceValue(coarse, metricName)
			if fineState != coarseState {
				if metricName == "tau0_500nm_ms" {
					tauMismatchArea += weight
				}
				if metricName == "slant_pwv_kg_m2" {
					pwvMismatchArea += weight
				}
				continue
			}
			if fineState != "finite" {
				continue
			}
			delta := math.Abs(fineValue - coarseValue)
			sample := prodAstrodomeWeightedDelta{delta: delta, weight: weight, sampleIndex: referenceIndex, supportIndex: supportIndex}
			deltas[metricName] = append(deltas[metricName], sample)
			if definition.ElevationDegrees < 20 {
				centresBelow20Deltas[metricName] = append(centresBelow20Deltas[metricName], sample)
			}
			if metricName == "overall" {
				if delta > 0.25 {
					quarterArea += weight
				}
				if delta > 0.5 {
					halfArea += weight
				}
			}
		}
		if fine.node.Available && coarse.node.Available {
			fineLimiter := prodAstrodomeLimiter(fine.node.PenaltyContributions)
			coarseLimiter := prodAstrodomeLimiter(coarse.node.PenaltyContributions)
			comparison.LimiterConfusionAreaFraction[fineLimiter+"->"+coarseLimiter] += weight / capArea
			if fineLimiter != coarseLimiter {
				limiterMismatchArea += weight
			}
		}
	}
	comparison.AvailabilityMismatchAreaFraction = availabilityMismatchArea / capArea
	comparison.Tau0StateMismatchAreaFraction = tauMismatchArea / capArea
	comparison.PWVStateMismatchAreaFraction = pwvMismatchArea / capArea
	comparison.OverallAboveQuarterAreaFraction = quarterArea / capArea
	comparison.OverallAboveHalfAreaFraction = halfArea / capArea
	comparison.LimiterMismatchAreaFraction = limiterMismatchArea / capArea
	for _, metricName := range metricNames {
		comparison.Metrics[metricName] = prodAstrodomeSummarizeMetric(deltas[metricName], centresBelow20Deltas[metricName], capArea)
	}
	comparison.OverallMinimum = prodAstrodomeConvergenceExtremaFor(false, supportGrid.nodes, supportResults, referenceGrid.nodes, referenceResults)
	comparison.OverallMaximum = prodAstrodomeConvergenceExtremaFor(true, supportGrid.nodes, supportResults, referenceGrid.nodes, referenceResults)
	return comparison
}

func prodAstrodomeSummarizeMetric(
	values, centresBelow20 []prodAstrodomeWeightedDelta,
	capArea float64,
) prodAstrodomeConvergenceMetric {
	result := prodAstrodomeConvergenceMetric{MaximumSample: -1, MaximumSupport: -1}
	weight := 0.0
	for _, value := range values {
		weight += value.weight
		if value.delta > result.MaximumAbsoluteDelta || result.MaximumSample < 0 {
			result.MaximumAbsoluteDelta = value.delta
			result.MaximumSample = value.sampleIndex
			result.MaximumSupport = value.supportIndex
		}
	}
	outerWeight := 0.0
	for _, value := range centresBelow20 {
		outerWeight += value.weight
		result.CentresBelow20MaximumDelta = math.Max(result.CentresBelow20MaximumDelta, value.delta)
	}
	result.ComparableAreaFraction = weight / capArea
	result.CentresBelow20ComparableArea = outerWeight / capArea
	result.P95AbsoluteDelta = prodAstrodomeWeightedPercentile(values, 0.95)
	result.CentresBelow20P95Delta = prodAstrodomeWeightedPercentile(centresBelow20, 0.95)
	return result
}

func prodAstrodomeWeightedPercentile(values []prodAstrodomeWeightedDelta, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]prodAstrodomeWeightedDelta(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].delta < ordered[j].delta })
	total := 0.0
	for _, value := range ordered {
		total += value.weight
	}
	target := quantile * total
	cumulative := 0.0
	for _, value := range ordered {
		cumulative += value.weight
		if cumulative >= target {
			return value.delta
		}
	}
	return ordered[len(ordered)-1].delta
}

func prodAstrodomeConvergenceState(result prodAstrodomeConvergenceNodeResult) string {
	if result.err != nil {
		return "error"
	}
	if result.node.Available {
		return "available"
	}
	return string(result.node.State)
}

func prodAstrodomeConvergenceValue(result prodAstrodomeConvergenceNodeResult, metric string) (float64, string) {
	if result.err != nil || !result.node.Available {
		return 0, prodAstrodomeConvergenceState(result)
	}
	var value *float64
	switch metric {
	case "overall":
		value = result.node.Overall
	case "seeing_500nm_arcsec":
		value = result.node.Seeing500Arcsec
	case "tau0_500nm_ms":
		if result.node.Tau0UnboundedAbove {
			return 0, "unbounded"
		}
		value = result.node.Tau0500MS
	case "slant_pwv_kg_m2":
		if result.node.SlantWaterKgM2 != nil {
			copy := result.node.SlantWaterKgM2.Value
			value = &copy
		}
	case "cloud_transmission_conservative":
		value = result.node.CloudTransmissionConservative
	default:
		return 0, "unknown"
	}
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return 0, "undefined"
	}
	return *value, "finite"
}

func prodAstrodomeLimiter(contributions []forecast.OverallPenaltyContribution) string {
	limiter := "none"
	maximum := 0.0
	for _, contribution := range contributions {
		if contribution.LossFraction > maximum {
			maximum = contribution.LossFraction
			limiter = contribution.Key
		}
	}
	return limiter
}

func prodAstrodomeNearestNode(sample forecast.AstrodomeGridNode, supports []forecast.AstrodomeGridNode) int {
	sampleVector := prodAstrodomeNodeVector(sample)
	best, bestDot := 0, math.Inf(-1)
	for index, support := range supports {
		dot := sampleVector.dot(prodAstrodomeNodeVector(support))
		if dot > bestDot {
			best, bestDot = index, dot
		}
	}
	return best
}

type prodAstrodomeDirectionVector struct{ x, y, z float64 }

func (vector prodAstrodomeDirectionVector) dot(other prodAstrodomeDirectionVector) float64 {
	return vector.x*other.x + vector.y*other.y + vector.z*other.z
}

func prodAstrodomeNodeVector(node forecast.AstrodomeGridNode) prodAstrodomeDirectionVector {
	elevation := node.ElevationDegrees * math.Pi / 180
	azimuth := 0.0
	if node.AzimuthDegrees != nil {
		azimuth = *node.AzimuthDegrees * math.Pi / 180
	}
	cosElevation := math.Cos(elevation)
	return prodAstrodomeDirectionVector{
		x: cosElevation * math.Sin(azimuth),
		y: cosElevation * math.Cos(azimuth),
		z: math.Sin(elevation),
	}
}

func prodAstrodomeAngularSeparation(left, right forecast.AstrodomeGridNode) float64 {
	dot := prodAstrodomeNodeVector(left).dot(prodAstrodomeNodeVector(right))
	return math.Acos(math.Max(-1, math.Min(1, dot))) * 180 / math.Pi
}

func prodAstrodomeConvergenceExtremaFor(
	maximum bool,
	supportNodes []forecast.AstrodomeGridNode,
	supportResults []prodAstrodomeConvergenceNodeResult,
	referenceNodes []forecast.AstrodomeGridNode,
	referenceResults []prodAstrodomeConvergenceNodeResult,
) prodAstrodomeConvergenceExtrema {
	selectIndex := func(results []prodAstrodomeConvergenceNodeResult) int {
		selected := -1
		selectedValue := 0.0
		for index, result := range results {
			value, state := prodAstrodomeConvergenceValue(result, "overall")
			if state != "finite" {
				continue
			}
			if selected < 0 || (maximum && value > selectedValue) || (!maximum && value < selectedValue) {
				selected, selectedValue = index, value
			}
		}
		return selected
	}
	fineIndex := selectIndex(referenceResults)
	supportIndex := selectIndex(supportResults)
	result := prodAstrodomeConvergenceExtrema{FineNodeIndex: fineIndex, SupportNodeIndex: supportIndex}
	if fineIndex < 0 || supportIndex < 0 {
		return result
	}
	result.FineValue, _ = prodAstrodomeConvergenceValue(referenceResults[fineIndex], "overall")
	result.SupportValue, _ = prodAstrodomeConvergenceValue(supportResults[supportIndex], "overall")
	result.AngularSeparation = prodAstrodomeAngularSeparation(referenceNodes[fineIndex], supportNodes[supportIndex])
	return result
}
