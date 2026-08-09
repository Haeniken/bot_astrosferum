package iconeu

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
)

func TestDomeAstrodomeProviderResolvesNativeDomainAndSiteInputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	loaded := writeDomeVolumePublication(t, root)
	volume, err := newDomeVolume(root, filepath.Join(root, "tmp"), loaded, &domeVolumeTestRunner{}, 8)
	if err != nil {
		t.Fatal(err)
	}
	grid := Coverage()
	location := forecast.Location{
		Latitude: grid.MinLat + 8.25*grid.Increment, Longitude: grid.MinLon + 9.75*grid.Increment,
		TimeZone: "UTC",
	}
	surfaceHeightM, err := volume.AstrodomeSurfaceHeightAt(context.Background(), location)
	if err != nil {
		t.Fatal(err)
	}
	if !finiteDomeVolume(surfaceHeightM) {
		t.Fatalf("surface height = %g", surfaceHeightM)
	}
	stencil, err := volume.HorizontalStencil(context.Background(), location)
	if err != nil {
		t.Fatal(err)
	}
	validAt := loaded.BaseTime.Add(2 * time.Hour)
	domain, err := volume.ResolveAstrodomeRefractionDomain(context.Background(), validAt,
		forecast.AstrodomeRayPoint{Location: location, HeightM: 5_000}, stencil)
	if err != nil {
		t.Fatal(err)
	}
	if domain.SurfaceHeightM != surfaceHeightM || domain.ModelTopHeightM <= domain.SurfaceHeightM ||
		!strings.Contains(domain.PartitionID, "cell-lat0008-lon0009") ||
		!strings.Contains(domain.PartitionID, "full-") {
		t.Fatalf("native refraction domain = %+v", domain)
	}

	site, err := volume.AstrodomeScienceSiteAt(context.Background(), validAt, location)
	if err != nil {
		t.Fatal(err)
	}
	if site.SourceIdentity != volume.Identity() || !site.ValidAt.Equal(validAt) ||
		math.Abs(site.WindSpeed10MMS-5) > 1e-12 || site.WindGust10MMS != 7 ||
		math.Abs(site.PrecipitationRateMMPerHour-0.1) > 1e-12 ||
		site.FogState != forecast.AstrodomeScienceFogNone || site.ForecastLeadHours != 2 ||
		!site.PrecipitationIntervalStart.Equal(validAt.Add(-time.Hour)) ||
		!site.PrecipitationIntervalEnd.Equal(validAt) {
		t.Fatalf("native site inputs = %+v", site)
	}
}

func TestDomeHourlyPrecipitationUsesGRIBPackingErrorEnclosure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                                           string
		current, previous, currentError, previousError float64
		want                                           float64
		wantError                                      bool
	}{
		{
			name:    "observed quantization decrease",
			current: 2.10546875, previous: 2.107421875,
			currentError: 0.001953125, previousError: 0.0009765625,
		},
		{
			name:    "ambiguous positive drizzle",
			current: 2.109375, previous: 2.107421875,
			currentError: 0.001953125, previousError: 0.0009765625,
		},
		{
			name:    "resolved positive accumulation",
			current: 2.207421875, previous: 2.107421875,
			currentError: 0.001953125, previousError: 0.0009765625,
			want: 0.1,
		},
		{
			name:    "material decrease",
			current: 2.0, previous: 2.1,
			currentError: 0.001953125, previousError: 0.0009765625,
			wantError: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := domeHourlyPrecipitationFromPackedAccumulations(
				test.current, test.previous, test.currentError, test.previousError,
			)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError %v", err, test.wantError)
			}
			if math.Abs(got-test.want) > 1e-12 {
				t.Fatalf("precipitation = %.12g, want %.12g", got, test.want)
			}
		})
	}
}

func TestDomeAstrodomeDomainAndPathUseBilinearLocalHHLGeometry(t *testing.T) {
	t.Parallel()

	grid := Coverage()
	volume := &DomeVolume{
		manifest:   LoadedDomeManifest{DomeManifest: DomeManifest{Grid: grid}},
		cache:      make(map[string]domeVolumeCacheEntry, 4),
		groups:     make(map[string][]domeColumnAddress),
		flights:    make(map[string]*domeVolumeFlight),
		cacheLimit: 4,
	}
	location := forecast.Location{
		Latitude:  grid.MinLat + 10.25*grid.Increment,
		Longitude: grid.MinLon + 20.75*grid.Increment,
		TimeZone:  "UTC",
	}
	indices := [4][2]int{{10, 20}, {10, 21}, {11, 20}, {11, 21}}
	terrainOffsets := [4]float64{0, 40, 100, 220}
	columns := [4]forecast.AstrodomePrimitiveColumn{}
	for columnIndex, index := range indices {
		address := volume.domeColumnAddress(index[0], index[1])
		geometry := make([]forecast.AstrodomeHalfLevelGeometry, domeHalfLevelCount)
		for levelIndex := range geometry {
			terrainFraction := float64(levelIndex) / float64(domeHalfLevelCount-1)
			geometry[levelIndex] = forecast.AstrodomeHalfLevelGeometry{
				ModelHalfLevel: levelIndex + 1,
				HeightM:        30_000 - 400*float64(levelIndex) + terrainOffsets[columnIndex]*terrainFraction,
			}
		}
		columns[columnIndex] = forecast.AstrodomePrimitiveColumn{
			ColumnID: address.id, Location: address.location,
			HSURFHeightM: geometry[len(geometry)-1].HeightM, HalfLevelGeometry: geometry,
		}
		volume.cache[address.id] = domeVolumeCacheEntry{column: columns[columnIndex]}
	}

	stencil, err := volume.HorizontalStencil(context.Background(), location)
	if err != nil {
		t.Fatal(err)
	}
	weights := domeStencilWeights(stencil)
	localHHL, err := domeInterpolatedHHLColumn(columns, weights)
	if err != nil {
		t.Fatal(err)
	}
	const levelIndex = 37
	wantHHL := 0.0
	wantFull := 0.0
	for columnIndex := range columns {
		wantHHL += weights[columnIndex] * columns[columnIndex].HalfLevelGeometry[levelIndex].HeightM
		wantFull += weights[columnIndex] * (columns[columnIndex].HalfLevelGeometry[levelIndex].HeightM +
			columns[columnIndex].HalfLevelGeometry[levelIndex+1].HeightM) / 2
	}
	hhlHeight, err := domeAstrodomeNativeBoundaryHeight(columns, weights, domeAstrodomeBoundary{
		id: "hhl/038", kind: domeAstrodomeBoundaryHHL, levelIndex: levelIndex,
	})
	if err != nil || math.Abs(hhlHeight-wantHHL) > 1e-12 || hhlHeight != localHHL[levelIndex].HeightM {
		t.Fatalf("local HHL boundary = %.12g, %v; want %.12g", hhlHeight, err, wantHHL)
	}
	fullHeight, err := domeAstrodomeNativeBoundaryHeight(columns, weights, domeAstrodomeBoundary{
		id: "full-mid/038", kind: domeAstrodomeBoundaryFull, levelIndex: levelIndex,
	})
	if err != nil || math.Abs(fullHeight-wantFull) > 1e-12 {
		t.Fatalf("local full-level boundary = %.12g, %v; want %.12g", fullHeight, err, wantFull)
	}
	for _, column := range columns {
		if hhlHeight == column.HalfLevelGeometry[levelIndex].HeightM ||
			fullHeight == (column.HalfLevelGeometry[levelIndex].HeightM+column.HalfLevelGeometry[levelIndex+1].HeightM)/2 {
			t.Fatal("bilinear native boundary collapsed to one support-column constant")
		}
	}

	pointHeight := fullHeight + 10
	wantSurface := localHHL[len(localHHL)-1].HeightM
	wantBranch, err := domeThermodynamicBranch(&localHHL, wantSurface, pointHeight)
	if err != nil {
		t.Fatal(err)
	}
	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	domain, err := volume.ResolveAstrodomeRefractionDomain(context.Background(), validAt,
		forecast.AstrodomeRayPoint{Location: location, HeightM: pointHeight}, stencil)
	if err != nil {
		t.Fatal(err)
	}
	wantCell := "cell-lat0010-lon0020"
	if domain.PartitionID != wantCell+"|"+wantBranch ||
		math.Abs(domain.ModelTopHeightM-localHHL[0].HeightM) > 1e-12 ||
		math.Abs(domain.SurfaceHeightM-localHHL[len(localHHL)-1].HeightM) > 1e-12 {
		t.Fatalf("local refraction domain = %+v; want cell %q branch %q", domain, wantCell, wantBranch)
	}
	guardedDomain, err := volume.ResolveAstrodomeRefractionDomain(context.Background(), validAt,
		forecast.AstrodomeRayPoint{
			Location: location,
			HeightM:  wantSurface - forecast.AstrodomeRefractionPrimitiveEventGuardM/2,
		}, stencil)
	if err != nil || !strings.HasSuffix(guardedDomain.PartitionID, "|lower-boundary") {
		t.Fatalf("refraction domain rejected the below-surface event guard: %+v, %v", guardedDomain, err)
	}
	if _, err := volume.ResolveAstrodomeRefractionDomain(context.Background(), validAt,
		forecast.AstrodomeRayPoint{
			Location: location,
			HeightM:  wantSurface - 2*forecast.AstrodomeRefractionPrimitiveEventGuardM,
		}, stencil); err == nil {
		t.Fatal("refraction domain accepted a point below the bilinear surface event guard")
	}
}

func TestDomeInterpolatedHHLColumnDoesNotAllocate(t *testing.T) {
	columns := [4]forecast.AstrodomePrimitiveColumn{}
	for columnIndex := range columns {
		geometry := make([]forecast.AstrodomeHalfLevelGeometry, domeHalfLevelCount)
		for levelIndex := range geometry {
			geometry[levelIndex] = forecast.AstrodomeHalfLevelGeometry{
				ModelHalfLevel: levelIndex + 1,
				HeightM:        30_000 - 400*float64(levelIndex) + float64(columnIndex),
			}
		}
		columns[columnIndex] = forecast.AstrodomePrimitiveColumn{
			ColumnID: "allocation-probe", HalfLevelGeometry: geometry,
		}
	}
	weights := [4]float64{0.125, 0.375, 0.125, 0.375}
	allocations := testing.AllocsPerRun(1_000, func() {
		if _, err := domeInterpolatedHHLColumn(columns, weights); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("domeInterpolatedHHLColumn allocations/run = %.3f, want 0", allocations)
	}
}

func TestDomeBisectionPathRootReturnsExactEndpointAndInteriorRoots(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		left, right float64
		root        float64
	}{
		{name: "left endpoint", left: 2, right: 8, root: 2},
		{name: "right endpoint", left: 2, right: 8, root: 8},
		{name: "interior", left: 2, right: 8, root: 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := domeBisectionPathRoot(test.left, test.right, 1e-9, func(pathM float64) (float64, error) {
				return pathM - test.root, nil
			})
			if err != nil || math.Abs(got-test.root) > 1e-9 {
				t.Fatalf("root = %.12g, %v; want %.12g", got, err, test.root)
			}
		})
	}
}

func TestDomeAstrodomePhysicalProbePathsUseMetricCellInterior(t *testing.T) {
	t.Parallel()

	paths, err := domeAstrodomePhysicalProbePaths(domeAstrodomeCellInterval{startM: 100, endM: 110})
	if err != nil {
		t.Fatal(err)
	}
	leftM := 100 + forecast.AstrodomeScienceCompoundRootSideGuardM
	for leftM-100 <= forecast.AstrodomeScienceCompoundRootSideGuardM {
		leftM = math.Nextafter(leftM, math.Inf(1))
	}
	rightM := 110 - forecast.AstrodomeScienceCompoundRootSideGuardM
	for 110-rightM <= forecast.AstrodomeScienceCompoundRootSideGuardM {
		rightM = math.Nextafter(rightM, math.Inf(-1))
	}
	want := [5]float64{
		leftM,
		leftM + (rightM-leftM)/4,
		leftM + (rightM-leftM)/2,
		leftM + 3*(rightM-leftM)/4,
		rightM,
	}
	for index := range paths {
		if math.Abs(paths[index]-want[index]) > 1e-12 {
			t.Fatalf("physical probe %d = %.12g; want %.12g", index, paths[index], want[index])
		}
	}
	if paths[0]-100 <= forecast.AstrodomeScienceCompoundRootSideGuardM ||
		110-paths[4] <= forecast.AstrodomeScienceCompoundRootSideGuardM {
		t.Fatalf("physical probes do not clear compound-root envelope: %v", paths)
	}
}

func TestDomeAstrodomeProbeEndpointSliverRequiresLipschitzClearance(t *testing.T) {
	t.Parallel()

	interval := domeAstrodomeCellInterval{startM: 0, endM: 10, cellID: "endpoint-sliver"}
	probes, err := domeAstrodomePhysicalProbePaths(interval)
	if err != nil {
		t.Fatal(err)
	}
	clear := func(pathM float64) (float64, error) { return 1 + pathM, nil }
	if _, err := domeAstrodomeIsolateFieldAcrossProbes(interval, probes, 1, 10, clear); err != nil {
		t.Fatalf("certified clear endpoint slivers failed: %v", err)
	}
	rootInLeftSliver := probes[0] / 2
	unsafe := func(pathM float64) (float64, error) { return pathM - rootInLeftSliver, nil }
	if _, err := domeAstrodomeIsolateFieldAcrossProbes(interval, probes, 1, 10, unsafe); !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("uncertified endpoint sliver error = %v", err)
	}
	values := [5]domeAstrodomeResidualSample{}
	for index, pathM := range probes {
		value, valueErr := unsafe(pathM)
		if valueErr != nil {
			t.Fatal(valueErr)
		}
		values[index] = domeAstrodomeResidualSample{value: value}
	}
	approximation := &domeAstrodomePathApproximation{}
	if err := domeAstrodomeCertifyProbeEndpointSliversResidualsWithApproximation(
		interval, probes, values, 1, 10, nil, approximation,
	); err != nil {
		t.Fatalf("limited endpoint sliver = %v", err)
	}
	if math.Abs(approximation.lengthM()-probes[0]) > 1e-15 {
		t.Fatalf("limited endpoint-sliver union = %.12g m; want %.12g m", approximation.lengthM(), probes[0])
	}
}

func TestDomeAstrodomeApproximationUnionHasOneMetreCeiling(t *testing.T) {
	t.Parallel()

	approximation := &domeAstrodomePathApproximation{}
	if err := approximation.add(10, 10.6); err != nil {
		t.Fatal(err)
	}
	if err := approximation.add(10.5, 11); err != nil {
		t.Fatal(err)
	}
	if approximation.lengthM() != forecast.AstrodomeScienceMaximumApproximatePathLengthM {
		t.Fatalf("overlapping approximation union = %.12g m", approximation.lengthM())
	}
	if err := approximation.add(20, math.Nextafter(20, math.Inf(1))); !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("above-ceiling approximation error = %v", err)
	}
}

func TestDomeAstrodomeProbeEndpointSliverUsesCompleteOneSidedDerivativeRange(t *testing.T) {
	t.Parallel()

	interval := domeAstrodomeCellInterval{startM: 0, endM: 10, cellID: "one-sided-sliver"}
	probes, err := domeAstrodomePhysicalProbePaths(interval)
	if err != nil {
		t.Fatal(err)
	}
	valuesFor := func(value func(float64) float64) [5]domeAstrodomeResidualSample {
		values := [5]domeAstrodomeResidualSample{}
		for index, pathM := range probes {
			values[index] = domeAstrodomeResidualSample{value: value(pathM)}
		}
		return values
	}
	slope := func(value float64) domeAstrodomeEndpointSlopeBounds {
		return func(float64, float64) (float64, float64, error) {
			return value, value, nil
		}
	}

	// Both roots are outside the endpoint sliver under test. Symmetric
	// Lipschitz clearance alone cannot prove the near endpoint clear, whereas
	// the strict derivative direction proves that |f| grows toward it.
	startAway := valuesFor(func(pathM float64) float64 { return 0.03 - pathM })
	if err := domeAstrodomeCertifyProbeEndpointSliversResiduals(
		interval, probes, startAway, 1, 10, slope(-1),
	); err != nil {
		t.Fatalf("start one-sided monotonic clearance: %v", err)
	}
	endAway := valuesFor(func(pathM float64) float64 { return pathM - 9.97 })
	if err := domeAstrodomeCertifyProbeEndpointSliversResiduals(
		interval, probes, endAway, 1, 10, slope(1),
	); err != nil {
		t.Fatalf("end one-sided monotonic clearance: %v", err)
	}

	// Motion toward zero is also safe when the complete signed derivative
	// enclosure proves that the residual cannot reach zero over the omitted
	// width. The broad |f'| <= 1 Lipschitz test is intentionally inconclusive
	// for these values, so acceptance must come from the directed integral.
	startTowardButClear := valuesFor(func(pathM float64) float64 { return 0.0001 + 0.25*pathM })
	if err := domeAstrodomeCertifyProbeEndpointSliversResiduals(
		interval, probes, startTowardButClear, 1, 10, slope(0.25),
	); err != nil {
		t.Fatalf("start directed toward-zero clearance: %v", err)
	}
	endTowardButClear := valuesFor(func(pathM float64) float64 { return 0.0001 + 0.25*(interval.endM-pathM) })
	if err := domeAstrodomeCertifyProbeEndpointSliversResiduals(
		interval, probes, endTowardButClear, 1, 10, slope(-0.25),
	); err != nil {
		t.Fatalf("end directed toward-zero clearance: %v", err)
	}

	// A derivative directed toward zero is not a clearance proof. This keeps a
	// real root inside either sliver unavailable rather than silently folding it
	// into the horizontal-cell boundary.
	startRoot := probes[0] / 2
	insideStart := valuesFor(func(pathM float64) float64 { return pathM - startRoot })
	if err := domeAstrodomeCertifyProbeEndpointSliversResiduals(
		interval, probes, insideStart, 1, 10, slope(1),
	); !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("start-sliver physical root error = %v", err)
	}
	endRoot := (probes[4] + interval.endM) / 2
	insideEnd := valuesFor(func(pathM float64) float64 { return pathM - endRoot })
	if err := domeAstrodomeCertifyProbeEndpointSliversResiduals(
		interval, probes, insideEnd, 1, 10, slope(1),
	); !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("end-sliver physical root error = %v", err)
	}

	// A derivative enclosure containing zero does not supply a directed proof.
	// The same deliberately small residual must therefore remain fail-closed.
	indeterminate := valuesFor(func(float64) float64 { return 0.0001 })
	if err := domeAstrodomeCertifyProbeEndpointSliversResiduals(
		interval, probes, indeterminate, 1, 10, func(float64, float64) (float64, float64, error) {
			return -0.25, 0.25, nil
		},
	); !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("indeterminate one-sided derivative error = %v", err)
	}
}

func TestDomeAstrodomeProbeEndpointSliverDoesNotInventExactBoundaryAlias(t *testing.T) {
	t.Parallel()

	interval := domeAstrodomeCellInterval{startM: 0, endM: 10, cellID: "endpoint-root"}
	probes, err := domeAstrodomePhysicalProbePaths(interval)
	if err != nil {
		t.Fatal(err)
	}
	values := [5]domeAstrodomeResidualSample{}
	for index, pathM := range probes {
		values[index] = domeAstrodomeResidualSample{value: pathM}
	}
	positiveSlope := func(float64, float64) (float64, float64, error) { return 1, 1, nil }
	if err := domeAstrodomeCertifyProbeEndpointSliversResiduals(
		interval, probes, values, 1, 10, positiveSlope,
	); !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("unproved exact endpoint root error = %v", err)
	}
}

func TestDomeAstrodomePhysicalEndpointSlopeWiringByBoundaryFamily(t *testing.T) {
	t.Parallel()

	grid := Coverage()
	observer := forecast.Location{
		Latitude: grid.MinLat + 10.5*grid.Increment, Longitude: grid.MinLon + 10.5*grid.Increment,
		TimeZone: "UTC",
	}
	ray := traceDomeAstrodomeGridTestRay(t, observer, 0)
	interval := domeAstrodomeCellInterval{startM: 100, endM: 110, cellID: "physical-wiring"}
	probes, err := domeAstrodomePhysicalProbePaths(interval)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		kind string
	}{
		{name: "HHL", kind: domeAstrodomeBoundaryHHL},
		{name: "full level", kind: domeAstrodomeBoundaryFull},
		{name: "cloud tier", kind: domeAstrodomeBoundaryLowTop},
		{name: "PBL branch", kind: domeAstrodomeBoundaryPBL},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			endpointSlope := domeAstrodomePhysicalEndpointSlopeBounds(test.kind, ray, 0)
			lowerSlope, upperSlope, slopeErr := endpointSlope(probes[4], interval.endM)
			if slopeErr != nil || lowerSlope <= 0 || upperSlope < lowerSlope {
				t.Fatalf("physical endpoint slope = [%.12g, %.12g], %v", lowerSlope, upperSlope, slopeErr)
			}
			slope := (lowerSlope + upperSlope) / 2
			rootM := probes[4] - (interval.endM-probes[4])/2
			valueAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
				return domeAstrodomeResidualSample{value: slope * (pathM - rootM)}, nil
			}
			residualSlope := func(
				float64, float64, domeAstrodomeResidualSample, domeAstrodomeResidualSample,
			) (float64, float64, error) {
				return slope, slope, nil
			}

			if _, err := domeAstrodomeIsolateFieldAcrossProbesWithEndpointSlopeBoundsResiduals(
				interval, probes, 2, domeAstrodomePhysicalHeightOperandScaleM(), residualSlope, nil, valueAt,
			); !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
				t.Fatalf("fixture without %s endpoint wiring error = %v", test.name, err)
			}
			roots, err := domeAstrodomeIsolateFieldAcrossProbesWithEndpointSlopeBoundsResiduals(
				interval, probes, 2, domeAstrodomePhysicalHeightOperandScaleM(), residualSlope, endpointSlope, valueAt,
			)
			if err != nil || len(roots) != 1 || math.Abs(roots[0]-rootM) > domeAstrodomePhysicalRootToleranceM {
				t.Fatalf("%s endpoint-wired roots = %v, %v; want %.12g", test.name, roots, err, rootM)
			}
		})
	}
}

func TestDomeAstrodomePhysicalEndpointSliverUsesInteriorDerivativeCertificate(t *testing.T) {
	t.Parallel()

	grid := Coverage()
	observer := forecast.Location{
		Latitude: grid.MinLat + 10.5*grid.Increment, Longitude: grid.MinLon + 10.5*grid.Increment,
		TimeZone: "UTC",
	}
	ray := traceDomeAstrodomeGridTestRay(t, observer, 0)
	interval := domeAstrodomeCellInterval{startM: 100, endM: 110, cellID: "physical-local-endpoint"}
	probes, err := domeAstrodomePhysicalProbePaths(interval)
	if err != nil {
		t.Fatal(err)
	}
	valuesFor := func(value func(float64) float64) [5]domeAstrodomeResidualSample {
		values := [5]domeAstrodomeResidualSample{}
		for index, pathM := range probes {
			values[index] = domeAstrodomeResidualSample{value: value(pathM)}
		}
		return values
	}
	endpointBounds := func(values [5]domeAstrodomeResidualSample) domeAstrodomeEndpointSlopeBounds {
		bounds, boundsErr := domeAstrodomePhysicalEndpointSlopeBoundsFromInterior(
			domeAstrodomeBoundaryFull, ray, 10, probes, values, 0,
		)
		if boundsErr != nil {
			t.Fatal(boundsErr)
		}
		return bounds
	}

	// The independent global enclosure deliberately contains zero. The local
	// secant/curvature certificate nevertheless proves that the residual grows
	// away from zero toward the start endpoint, so the omitted sliver is clear.
	clearRootM := interval.startM + 0.0016
	clear := valuesFor(func(pathM float64) float64 { return clearRootM - pathM })
	globalOnly := domeAstrodomePhysicalEndpointSlopeBounds(domeAstrodomeBoundaryFull, ray, 10)
	if err := domeAstrodomeCertifyProbeEndpointSliversResiduals(
		interval, probes, clear, 10, domeAstrodomePhysicalHeightOperandScaleM(), globalOnly,
	); !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("broad global endpoint enclosure unexpectedly certified the sliver: %v", err)
	}
	if err := domeAstrodomeCertifyProbeEndpointSliversResiduals(
		interval, probes, clear, 10, domeAstrodomePhysicalHeightOperandScaleM(), endpointBounds(clear),
	); err != nil {
		t.Fatalf("local physical endpoint certificate rejected a clear sliver: %v", err)
	}

	// A real root inside the omitted start sliver makes the local derivative
	// point toward zero and must remain fail-closed.
	insideRootM := interval.startM + (probes[0]-interval.startM)/2
	inside := valuesFor(func(pathM float64) float64 { return pathM - insideRootM })
	if err := domeAstrodomeCertifyProbeEndpointSliversResiduals(
		interval, probes, inside, 10, domeAstrodomePhysicalHeightOperandScaleM(), endpointBounds(inside),
	); !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("local physical endpoint certificate hid a start-sliver root: %v", err)
	}
}

func TestDomeAstrodomeRawPBLDecisionUsesInteriorDerivativeForEndpointSlivers(t *testing.T) {
	t.Parallel()

	grid := Coverage()
	observer := forecast.Location{
		Latitude: grid.MinLat + 10.5*grid.Increment, Longitude: grid.MinLon + 10.5*grid.Increment,
		TimeZone: "UTC",
	}
	ray := traceDomeAstrodomePBLTestRay(t, observer, 10, 90)
	volume := newDomeAstrodomeGridTestVolume(grid)
	interval := domeAstrodomePBLTestInterval(t, volume, ray, 400, 1400)
	probes, err := domeAstrodomePhysicalProbePaths(interval)
	if err != nil {
		t.Fatal(err)
	}
	metric, err := volume.domeAstrodomeMetricBoundsForInterval(ray, interval)
	if err != nil {
		t.Fatal(err)
	}
	sampler := domeAstrodomePhysicalSampler{
		ctx: context.Background(), volume: volume, ray: ray,
		cellID: interval.cellID, cache: make(map[uint64]*domeAstrodomePhysicalSample),
	}

	for _, test := range []struct {
		name  string
		rootM float64
	}{
		{name: "start", rootM: probes[0] + (probes[0]-interval.startM)/2},
		{name: "end", rootM: probes[4] - (interval.endM-probes[4])/2},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			rootPoint, pointErr := ray.PointAtPathLength(test.rootM)
			if pointErr != nil {
				t.Fatal(pointErr)
			}
			rootStencil, stencilErr := volume.HorizontalStencil(context.Background(), rootPoint.Location)
			if stencilErr != nil {
				t.Fatal(stencilErr)
			}
			rootEastFraction := rootStencil.Supports[1].Weight + rootStencil.Supports[3].Weight
			const (
				thresholdM = 1000.0
				spanM      = 1000.0
			)
			westM := thresholdM - spanM*rootEastFraction
			mixedLayerDepths := [4]float64{westM, westM + spanM, westM, westM + spanM}
			field := domeAstrodomePBLDecisionField(
				"pbl-clamp/minimum", mixedLayerDepths, thresholdM,
			)
			lipschitz, lipschitzErr := domeAstrodomeScalarFieldLipschitz(
				metric, field.corners, field.roundoffOperandScale,
			)
			if lipschitzErr != nil {
				t.Fatal(lipschitzErr)
			}
			probeValues := [5]domeAstrodomeResidualSample{}
			for index, pathM := range probes {
				sample, sampleErr := sampler.sample(pathM, false)
				if sampleErr != nil {
					t.Fatal(sampleErr)
				}
				probeValues[index], sampleErr = domeAstrodomeScalarFieldResidualAtSample(sample, field)
				if sampleErr != nil {
					t.Fatal(sampleErr)
				}
			}
			if err := domeAstrodomeCertifyProbeEndpointSliversResiduals(
				interval, probes, probeValues, lipschitz, field.roundoffOperandScale, nil,
			); !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
				t.Fatalf("fixture without raw-PBL endpoint derivative error = %v", err)
			}

			roots, rootsErr := domeAstrodomePBLDecisionFieldRoots(
				ray, interval, probes, metric, &sampler, field,
			)
			if rootsErr != nil || len(roots) != 1 ||
				math.Abs(roots[0].evidence.pathM-test.rootM) > domeAstrodomePhysicalRootToleranceM {
				t.Fatalf("raw PBL %s endpoint roots = %+v, %v; want %.12g",
					test.name, roots, rootsErr, test.rootM)
			}
		})
	}
}

func TestDomeAstrodomeIsolatePathRootsFailsClosedAroundUnprovedPairedCrossing(t *testing.T) {
	t.Parallel()

	// Both roots lie between the old one-sided/quarter probes at 0.01 and
	// 1.0 m. Those endpoint values have the same sign, so the former scan
	// silently skipped both crossings.
	value := func(pathM float64) (float64, error) {
		return (pathM - 0.375) * (pathM - 0.625), nil
	}
	leftM, rightM := 0.0, 1.0
	leftValue, _ := value(leftM)
	rightValue, _ := value(rightM)
	remaining := 8192
	_, err := domeAstrodomeIsolatePathRoots(
		leftM, rightM, leftValue, rightValue, 1.2, 1, &remaining, value,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("unproved paired-root neighbourhood error = %v", err)
	}
}

func TestDomeAstrodomeIsolatePathRootsPrunesCertifiedClearance(t *testing.T) {
	t.Parallel()

	evaluations := 0
	value := func(pathM float64) (float64, error) {
		evaluations++
		return 10 + pathM, nil
	}
	remaining := 16
	roots, err := domeAstrodomeIsolatePathRoots(0, 1, 10, 11, 1, 11, &remaining, value)
	if err != nil || len(roots) != 0 {
		t.Fatalf("certified root-free interval = %v, %v", roots, err)
	}
	if evaluations != 1 || remaining != 15 {
		t.Fatalf("root-free isolation used %d evaluations and %d budget; want 1 and 15", evaluations, remaining)
	}
}

func TestDomeValidatePhysicalBreakpointsAcceptsDistinctCertifiedPair(t *testing.T) {
	t.Parallel()

	distinctGapM := 1.1 * forecast.AstrodomeScienceMinimumEventIntervalLengthM
	err := domeValidatePhysicalBreakpoints(
		domeAstrodomeCellInterval{startM: 0, endM: 10, cellID: "test-cell"},
		[]domeAstrodomePhysicalRootCandidate{
			{pathM: 4, eventID: "first"},
			{pathM: 4 + distinctGapM, eventID: "second"},
		},
	)
	if err != nil {
		t.Fatalf("distinct certified root pair error = %v", err)
	}
	err = domeValidatePhysicalBreakpoints(
		domeAstrodomeCellInterval{startM: 0, endM: 10, cellID: "test-cell"},
		[]domeAstrodomePhysicalRootCandidate{
			{pathM: 4, eventID: "first"},
			{pathM: 4 + forecast.AstrodomeScienceRootMergeToleranceM, eventID: "second"},
		},
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("numerically inseparable root pair error = %v", err)
	}
}

func TestCompactDomePhysicalRootCandidatesNeverCompoundsOverlappingDistinctEvidence(t *testing.T) {
	t.Parallel()

	firstM := 10.0
	secondM := firstM + 0.5*domeAstrodomePhysicalMergeToleranceM
	values := []domeAstrodomePhysicalRootCandidate{
		{
			evidence: domeAstrodomeRootEvidence{
				pathM:  firstM,
				leftM:  firstM - domeAstrodomePhysicalRootToleranceM,
				rightM: firstM + domeAstrodomePhysicalRootToleranceM,
			},
			eventID: "boundary/hhl/010",
		},
		{
			evidence: domeAstrodomeRootEvidence{
				pathM:  secondM,
				leftM:  secondM - domeAstrodomePhysicalRootToleranceM,
				rightM: secondM + domeAstrodomePhysicalRootToleranceM,
			},
			eventID: "boundary/full-mid/010",
		},
	}
	_, err := compactDomePhysicalRootCandidates(values)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("overlapping bit-distinct physical evidence error = %v", err)
	}
}

func TestDomeAstrodomeShortPanelResidualVerifierFailsClosedWhenIndeterminate(t *testing.T) {
	t.Parallel()

	checks := []domeAstrodomeShortPanelResidualCheck{{
		eventID:       "boundary/hhl/010",
		roundoffScale: domeAstrodomePhysicalHeightOperandScaleM(),
		expectedSign:  1,
		valueAt: func(float64) (domeAstrodomeResidualSample, error) {
			return domeAstrodomeResidualWithEvaluationError(0, 1e-6)
		},
	}}
	err := domeVerifyAstrodomeShortPanelResidualChecks(22409.8232865, checks)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) &&
		!strings.Contains(err.Error(), "indeterminate") {
		t.Fatalf("indeterminate short-panel node error = %v", err)
	}
}

func TestDomeAstrodomeShortPanelRejectsPhysicalEndpointWithoutResidualProof(t *testing.T) {
	t.Parallel()

	start := domeAstrodomePathEvent{
		evidence: domeAstrodomeRootEvidence{
			pathM: 10, leftM: 10, rightM: 10, exact: true,
		},
		eventID: "boundary/hhl/010", kind: "physical",
	}
	end := domeAstrodomePathEvent{
		evidence: domeAstrodomeRootEvidence{
			pathM: 10.00075, leftM: 10.00075, rightM: 10.00075, exact: true,
		},
		eventID: "longitude/976", kind: "horizontal",
	}
	_, err := domeBuildAstrodomeShortPanelCertificate(
		domeAstrodomeCellInterval{startM: 0, endM: 20, cellID: "cell"},
		start, end,
		&domeAstrodomePhysicalSampler{},
		&domeAstrodomeShortPanelVerifier{volume: &DomeVolume{}},
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) ||
		!strings.Contains(err.Error(), "no residual-sign proof") {
		t.Fatalf("physical endpoint without residual proof error = %v", err)
	}
}

func TestDomeAstrodomeIsolatePathRootsFailsClosedAroundSubCentimetrePair(t *testing.T) {
	t.Parallel()

	value := func(pathM float64) (float64, error) {
		return (pathM - 0.005) * (pathM - 0.015), nil
	}
	leftValue, _ := value(0)
	rightValue, _ := value(0.02)
	remaining := 8192
	_, err := domeAstrodomeIsolatePathRoots(0, 0.02, leftValue, rightValue, 0.03, 1, &remaining, value)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("unproved sub-centimetre root neighbourhood error = %v", err)
	}
}

func TestDomeAstrodomeIsolatePathRootsFailsClosedWithoutClearanceProof(t *testing.T) {
	t.Parallel()

	value := func(float64) (float64, error) { return 1, nil }
	remaining := 8
	_, err := domeAstrodomeIsolatePathRoots(
		0, domeAstrodomePhysicalRootToleranceM, 1, 1, 1e12, 1, &remaining, value,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("unproved terminal interval error = %v", err)
	}
}

func TestDomeAstrodomeResolveRootIsolationDoesNotUseNearbyRootAsProvenance(t *testing.T) {
	t.Parallel()

	root := domeAstrodomeRootEvidence{
		pathM: 1, leftM: 1, rightM: 1, exact: true,
	}
	nearbyButDisjoint := domeAstrodomeRootIsolationInterval{
		leftM:  1 + 0.25*domeAstrodomePhysicalMergeToleranceM,
		rightM: 1 + 0.50*domeAstrodomePhysicalMergeToleranceM,
	}
	_, err := domeAstrodomeResolveRootIsolation(domeAstrodomeRootIsolationResult{
		roots: []domeAstrodomeRootEvidence{root},
		unresolved: []domeAstrodomeRootIsolationInterval{
			nearbyButDisjoint,
		},
	}, 2)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("nearby but disjoint unresolved interval error = %v", err)
	}

	sharedEndpoint := domeAstrodomeRootIsolationInterval{leftM: 0.99, rightM: 1}
	_, err = domeAstrodomeResolveRootIsolation(domeAstrodomeRootIsolationResult{
		roots:      []domeAstrodomeRootEvidence{root},
		unresolved: []domeAstrodomeRootIsolationInterval{sharedEndpoint},
	}, 2)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("shared-endpoint unresolved interval error = %v", err)
	}

	_, err = domeAstrodomeResolveRootIsolation(domeAstrodomeRootIsolationResult{
		roots: []domeAstrodomeRootEvidence{{
			pathM: 1, leftM: 0.999, rightM: 1.001,
		}},
		unresolved: []domeAstrodomeRootIsolationInterval{{leftM: 1, rightM: 1.001}},
	}, 2)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("overlapping unresolved interval error = %v", err)
	}
}

func TestDomeAstrodomeResolveRootIsolationRejectsConnectedProofLeaf(t *testing.T) {
	t.Parallel()

	width := domeAstrodomeRootProofToleranceM / 4
	_, err := domeAstrodomeResolveRootIsolation(domeAstrodomeRootIsolationResult{
		roots: []domeAstrodomeRootEvidence{{
			pathM: 1, leftM: 1, rightM: 1, exact: true,
		}},
		unresolved: []domeAstrodomeRootIsolationInterval{
			{leftM: 1 - width, rightM: 1},
			{leftM: 1, rightM: 1 + width},
		},
	}, 2)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("connected proof-leaf cluster error = %v", err)
	}
}

func TestDomeAstrodomeIsolatePathRootsRejectsSubClusterTripleCrossing(t *testing.T) {
	t.Parallel()

	const (
		leftRoot   = 0.499998
		middleRoot = 0.5
		rightRoot  = 0.500002
	)
	value := func(pathM float64) (float64, error) {
		return (pathM - leftRoot) * (pathM - middleRoot) * (pathM - rightRoot), nil
	}
	leftValue, _ := value(0)
	rightValue, _ := value(1)
	remaining := domeAstrodomeRootIsolationBudgetPerField
	_, err := domeAstrodomeIsolatePathRoots(
		0, 1, leftValue, rightValue, 1, 1, &remaining, value,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("three same-event roots inside merge cluster error = %v", err)
	}
}

func TestDomeAstrodomeIsolatePathRootsRejectsAsymmetricMicrometreTriple(t *testing.T) {
	t.Parallel()

	value := func(pathM float64) (float64, error) {
		return (pathM - 0.5000005) * (pathM - 0.5000010) * (pathM - 0.5000015), nil
	}
	leftValue, _ := value(0)
	rightValue, _ := value(1)
	remaining := domeAstrodomeRootIsolationBudgetPerField
	_, err := domeAstrodomeIsolatePathRoots(
		0, 1, leftValue, rightValue, 2, 1, &remaining, value,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("asymmetric micrometre triple error = %v", err)
	}
}

func TestDomeAstrodomeProofLeafClassifiesBothHalves(t *testing.T) {
	t.Parallel()

	width := domeAstrodomeRootProofToleranceM
	value := func(pathM float64) (float64, error) {
		return (pathM - 0.1*width) * (pathM - 0.6*width) * (pathM - 0.8*width), nil
	}
	leftValue, _ := value(0)
	rightValue, _ := value(width)
	// For f=product_i(s-r_i) on [0,w], |f'| is bounded by 3*w^2.
	conservativeLipschitz := 3 * width * width
	remaining := 3
	_, err := domeAstrodomeIsolatePathRoots(
		0, width, leftValue, rightValue, conservativeLipschitz, 1, &remaining, value,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("proof leaf with one left and two right roots error = %v", err)
	}
}

func TestDomeAstrodomeProofLeafFailsClosedAroundExactLinearRoot(t *testing.T) {
	t.Parallel()

	width := domeAstrodomeRootProofToleranceM
	root := width / 2
	value := func(pathM float64) (float64, error) { return pathM - root, nil }
	leftValue, _ := value(0)
	rightValue, _ := value(width)
	remaining := 3
	_, err := domeAstrodomeIsolatePathRoots(
		0, width, leftValue, rightValue, 1, 1, &remaining, value,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("exact linear proof-leaf error = %v", err)
	}
}

func TestDomeAstrodomeCertifiedMonotoneRootResolvesHeightRoundoffLeaf(t *testing.T) {
	t.Parallel()

	const slope = 0.2
	width := 1.6 * domeAstrodomePhysicalRootToleranceM
	root := width / 2
	value := func(pathM float64) (float64, error) { return slope * (pathM - root), nil }
	leftValue, _ := value(0)
	rightValue, _ := value(width)
	slopeBounds := func(float64, float64, float64, float64) (float64, float64, error) {
		return math.Nextafter(slope, math.Inf(-1)), math.Nextafter(slope, math.Inf(1)), nil
	}
	remaining := 3
	roots, err := domeAstrodomeIsolatePathRootsWithSlopeBounds(
		0, width, leftValue, rightValue, slope,
		domeAstrodomePhysicalHeightOperandScaleM(), slopeBounds, &remaining, value,
	)
	if err != nil {
		t.Fatalf("certified monotone root: %v", err)
	}
	if len(roots) != 1 || math.Abs(roots[0]-root) > domeAstrodomePhysicalRootToleranceM {
		t.Fatalf("certified monotone roots = %v; want %.12g within %.9g m", roots, root,
			domeAstrodomePhysicalRootToleranceM)
	}
}

func TestDomeAstrodomeCertifiedMonotoneRootKeepsAncestorExistenceProof(t *testing.T) {
	t.Parallel()

	// At this width both ancestor endpoint signs clear the native-height
	// roundoff envelope, while the exact midpoint root necessarily has an
	// indeterminate residual sign. The unique-root proof must survive that
	// midpoint instead of descending into nanometre leaves and failing closed.
	const (
		slope = 0.2
		width = 100e-6
	)
	root := width / 2
	value := func(pathM float64) (float64, error) { return slope * (pathM - root), nil }
	leftValue, _ := value(0)
	rightValue, _ := value(width)
	slopeBounds := func(float64, float64, float64, float64) (float64, float64, error) {
		return math.Nextafter(slope, math.Inf(-1)), math.Nextafter(slope, math.Inf(1)), nil
	}
	remaining := 32
	roots, err := domeAstrodomeIsolatePathRootsWithSlopeBounds(
		0, width, leftValue, rightValue, slope,
		domeAstrodomePhysicalHeightOperandScaleM(), slopeBounds, &remaining, value,
	)
	if err != nil {
		t.Fatalf("ancestor-certified monotone root: %v", err)
	}
	if len(roots) != 1 || math.Abs(roots[0]-root) > domeAstrodomePhysicalRootToleranceM {
		t.Fatalf("ancestor-certified roots = %v; want %.12g within %.9g m", roots, root,
			domeAstrodomePhysicalRootToleranceM)
	}
}

func TestDomeAstrodomeCertifiedMonotoneRootContractsAsymmetricResidualInterval(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name                   string
		direction              float64
		lowerSlope, upperSlope float64
	}{
		{name: "increasing", direction: 1, lowerSlope: 1, upperSlope: 1},
		{name: "decreasing", direction: -1, lowerSlope: -1, upperSlope: -1},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			const (
				leftM         = 0.0
				rightM        = 1e-3
				rootM         = 0.5e-3
				endpointError = 0.49e-3
			)
			valueAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
				sample := domeAstrodomeResidualSample{value: test.direction * (pathM - rootM)}
				switch pathM {
				case leftM, rightM:
					sample.evaluationError = endpointError
				case rootM:
					// The interval [-0.05,0.25] mm contains the exact zero. The former
					// symmetric max|F|/min|f'| enclosure was 0.5 mm wide, while the
					// interval-Newton image is only 0.3 mm wide.
					sample.value = 0.1e-3
					sample.evaluationError = 0.15e-3
				}
				return sample, nil
			}
			leftSample, _ := valueAt(leftM)
			rightSample, _ := valueAt(rightM)
			remaining := 32
			result, err := domeAstrodomeLocalizeCertifiedMonotoneRootResiduals(
				leftM, rightM, leftSample, rightSample,
				test.lowerSlope, test.upperSlope, 1, domeAstrodomePhysicalRootToleranceM,
				&remaining, valueAt,
			)
			if err != nil {
				t.Fatalf("asymmetric interval-Newton root: %v", err)
			}
			if len(result.roots) != 1 {
				t.Fatalf("asymmetric interval-Newton roots = %+v, want one", result.roots)
			}
			evidence := result.roots[0]
			if evidence.leftM > rootM || evidence.rightM < rootM ||
				evidence.rightM-evidence.leftM > 2*domeAstrodomePhysicalRootToleranceM {
				t.Fatalf("asymmetric interval-Newton evidence = %+v, want root %.12g inside %.9g m diameter",
					evidence, rootM, 2*domeAstrodomePhysicalRootToleranceM)
			}
		})
	}
}

func TestDomeAstrodomeCertifiedMonotoneRootUsesPairedProbesAfterMidpointFixedPoint(t *testing.T) {
	t.Parallel()

	const (
		leftM         = 0.0
		rightM        = 1e-3
		rootM         = 0.5e-3
		endpointError = 0.49e-3
	)
	valueAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
		sample := domeAstrodomeResidualSample{value: pathM - rootM}
		switch pathM {
		case leftM, rightM:
			sample.evaluationError = endpointError
		case rootM:
			// This symmetric midpoint image is wider than the target and does
			// not choose a bisection side. The off-centre samples are exact.
			sample.evaluationError = 0.4e-3
		}
		return sample, nil
	}
	leftSample, _ := valueAt(leftM)
	rightSample, _ := valueAt(rightM)
	remaining := 32
	result, err := domeAstrodomeLocalizeCertifiedMonotoneRootResiduals(
		leftM, rightM, leftSample, rightSample,
		0.5, 1.5, 1, domeAstrodomePhysicalRootToleranceM,
		&remaining, valueAt,
	)
	if err != nil {
		t.Fatalf("paired-probe monotone root: %v", err)
	}
	if len(result.roots) != 1 {
		t.Fatalf("paired-probe roots = %+v, want one", result.roots)
	}
	evidence := result.roots[0]
	if evidence.leftM > rootM || evidence.rightM < rootM ||
		evidence.rightM-evidence.leftM > 2*domeAstrodomePhysicalRootToleranceM {
		t.Fatalf("paired-probe evidence = %+v, want root %.12g inside %.9g m diameter",
			evidence, rootM, 2*domeAstrodomePhysicalRootToleranceM)
	}
}

func TestDomeAstrodomeCertifiedMonotoneRootDoesNotPostponePairedProbesAfterTinyContraction(t *testing.T) {
	t.Parallel()

	const (
		leftM  = 0.0
		rightM = 1.0
		rootM  = 0.5
	)
	leftSample := domeAstrodomeResidualSample{value: leftM - rootM, evaluationError: 0.49}
	rightSample := domeAstrodomeResidualSample{value: rightM - rootM, evaluationError: 0.49}
	evaluations := 0
	offCentreEvaluated := false
	valueAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
		evaluations++
		if pathM > 0.49 && pathM < 0.51 {
			// N=[x-0.489999,x+0.490001] trims only a diminishing amount
			// from the left. Repeating shifted midpoints would make nominal
			// progress while needlessly postponing decisive side probes.
			return domeAstrodomeResidualSample{value: -1e-6, evaluationError: 0.49}, nil
		}
		offCentreEvaluated = true
		return domeAstrodomeResidualSample{value: pathM - rootM}, nil
	}
	remaining := 32
	result, err := domeAstrodomeLocalizeCertifiedMonotoneRootResiduals(
		leftM, rightM, leftSample, rightSample,
		1, 1, 1, 0.1, &remaining, valueAt,
	)
	if err != nil {
		t.Fatalf("tiny-contraction paired refinement: %v", err)
	}
	if !offCentreEvaluated || evaluations > 3 {
		t.Fatalf("tiny-contraction refinement used %d evaluations, off-centre=%t; paired probes were postponed",
			evaluations, offCentreEvaluated)
	}
	if len(result.roots) != 1 || result.roots[0].leftM > rootM || result.roots[0].rightM < rootM {
		t.Fatalf("tiny-contraction evidence = %+v, want root %.12g", result.roots, rootM)
	}
}

func TestDomeAstrodomeCertifiedMonotoneRootFailsAtEvaluationInformationFloor(t *testing.T) {
	t.Parallel()

	const (
		leftM  = 0.0
		rightM = 1e-3
		rootM  = 0.5e-3
	)
	valueAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
		return domeAstrodomeResidualSample{
			value: pathM - rootM, evaluationError: 0.3e-3,
		}, nil
	}
	leftSample, _ := valueAt(leftM)
	rightSample, _ := valueAt(rightM)
	remaining := 32
	_, err := domeAstrodomeLocalizeCertifiedMonotoneRootResiduals(
		leftM, rightM, leftSample, rightSample,
		1, 1, 1, domeAstrodomePhysicalRootToleranceM,
		&remaining, valueAt,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) ||
		!strings.Contains(err.Error(), "monotone root uncertainty") {
		t.Fatalf("evaluation-information-floor error = %v", err)
	}
	if remaining <= 0 {
		t.Fatalf("evaluation-information-floor refinement exhausted its complete budget")
	}
}

func TestDomeAstrodomeCertifiedMonotoneRootRejectsRoundedMidpointOutsideRadius(t *testing.T) {
	t.Parallel()

	leftM := 1e16
	rightM := math.Nextafter(leftM, math.Inf(1))
	spacingM := rightM - leftM
	rootToleranceM := 0.75 * spacingM
	valueAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
		switch pathM {
		case leftM:
			return domeAstrodomeResidualSample{value: -spacingM / 2}, nil
		case rightM:
			return domeAstrodomeResidualSample{value: spacingM / 2}, nil
		default:
			return domeAstrodomeResidualSample{}, fmt.Errorf("unexpected non-adjacent sample %.17g", pathM)
		}
	}
	leftSample, _ := valueAt(leftM)
	rightSample, _ := valueAt(rightM)
	remaining := 8
	_, err := domeAstrodomeLocalizeCertifiedMonotoneRootResiduals(
		leftM, rightM, leftSample, rightSample,
		1, 1, 1, rootToleranceM, &remaining, valueAt,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) ||
		!strings.Contains(err.Error(), "monotone root uncertainty") {
		t.Fatalf("large-offset rounded-midpoint error = %v", err)
	}
}

func TestDomeAstrodomeCertifiedMonotoneRootRequiresBudgetPointerForEndpointSuccess(t *testing.T) {
	t.Parallel()

	valueAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
		return domeAstrodomeResidualSample{value: pathM - 0.5}, nil
	}
	leftSample, _ := valueAt(0)
	rightSample, _ := valueAt(1)
	_, err := domeAstrodomeLocalizeCertifiedMonotoneRootResiduals(
		0, 1, leftSample, rightSample,
		1, 1, 1, 1, nil, valueAt,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) ||
		!strings.Contains(err.Error(), "invalid inherited monotone root certificate") {
		t.Fatalf("nil endpoint-success budget error = %v", err)
	}
}

func TestDomeAstrodomeCertifiedMonotoneRootRejectsNonFiniteLocalizationDiameter(t *testing.T) {
	t.Parallel()

	valueAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
		return domeAstrodomeResidualSample{value: pathM - 0.5}, nil
	}
	leftSample, _ := valueAt(0)
	rightSample, _ := valueAt(1)
	remaining := 0
	_, err := domeAstrodomeLocalizeCertifiedMonotoneRootResiduals(
		0, 1, leftSample, rightSample,
		1, 1, 1, math.MaxFloat64, &remaining, valueAt,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) ||
		!strings.Contains(err.Error(), "invalid inherited monotone root certificate") {
		t.Fatalf("non-finite localization-diameter error = %v", err)
	}
}

func TestDomeAstrodomeCertifiedMonotoneWMORootAcceptsSubMillimetreResidualEnvelope(t *testing.T) {
	t.Parallel()

	// A shallow but strictly monotone WMO predicate can be closer to zero than
	// the conservative binary64 residual enclosure. The resulting certified
	// root bracket is about 60 um wide: far below model resolution, but wider
	// than the former 10-um diameter and therefore a production regression for
	// the root-localisation contract introduced in v13 and retained by v14.
	const operandScale = 150_000.0
	uncertainty := domeAstrodomeClearanceRoundoff(0, 0, operandScale)
	slope := uncertainty / 30e-6
	root := 0.5
	value := func(pathM float64) (float64, error) { return slope * (pathM - root), nil }
	leftValue, _ := value(0)
	rightValue, _ := value(1)
	remaining := 64
	result, err := domeAstrodomeLocalizeCertifiedMonotoneRoot(
		0, 1, leftValue, rightValue,
		math.Nextafter(slope, math.Inf(-1)), math.Nextafter(slope, math.Inf(1)),
		operandScale, &remaining, value,
	)
	if err != nil {
		t.Fatalf("certified shallow WMO root: %v", err)
	}
	if len(result.roots) != 1 {
		t.Fatalf("certified shallow WMO roots = %+v, want one", result.roots)
	}
	evidence := result.roots[0]
	width := evidence.rightM - evidence.leftM
	if width <= 10e-6 || width > 2*domeAstrodomePhysicalRootToleranceM {
		t.Fatalf("certified shallow WMO bracket width = %.12g m", width)
	}
	if math.Abs(evidence.pathM-root) > domeAstrodomePhysicalRootToleranceM {
		t.Fatalf("certified shallow WMO root = %.12g, want %.12g", evidence.pathM, root)
	}
}

func TestDomeAstrodomeSecantCertificateResolvesMonotoneEarthScaleLeaf(t *testing.T) {
	t.Parallel()

	// This reproduces the scale of the production HHL failure: the public root
	// bracket is only micrometres wide while HeightM is formed by subtracting
	// the ICON Earth radius from an ECEF norm. A global absolute terrain-slope
	// enclosure can contain zero even though the local residual is monotone.
	width := 1.6 * domeAstrodomePhysicalRootToleranceM
	root := width / 2
	const (
		slope                 = 0.2
		secondDerivativeBound = 1e-3
		directLower           = -0.05
		directUpper           = 0.25
	)
	if directLower > 0 || directUpper < 0 {
		t.Fatal("fixture's direct derivative enclosure must contain zero")
	}
	value := func(pathM float64) (float64, error) {
		return slope * (pathM - root), nil
	}
	leftValue, _ := value(0)
	rightValue, _ := value(width)
	secantLower, secantUpper, err := domeAstrodomeSecantSlopeBounds(
		0, width, leftValue, rightValue,
		domeAstrodomePhysicalHeightOperandScaleM(), secondDerivativeBound,
	)
	if err != nil {
		t.Fatal(err)
	}
	if secantLower <= 0 || secantUpper < slope {
		t.Fatalf("secant derivative enclosure = [%.12g, %.12g], want a positive enclosure containing %.12g",
			secantLower, secantUpper, slope)
	}
	certifiedLower := math.Max(directLower, secantLower)
	certifiedUpper := math.Min(directUpper, secantUpper)
	if certifiedLower <= 0 || certifiedLower > certifiedUpper {
		t.Fatalf("intersected derivative enclosure = [%.12g, %.12g], want a nonempty positive interval",
			certifiedLower, certifiedUpper)
	}

	slopeBounds := func(leftM, rightM, leftValue, rightValue float64) (float64, float64, error) {
		lower, upper, boundsErr := domeAstrodomeSecantSlopeBounds(
			leftM, rightM, leftValue, rightValue,
			domeAstrodomePhysicalHeightOperandScaleM(), secondDerivativeBound,
		)
		if boundsErr != nil {
			return 0, 0, boundsErr
		}
		return math.Max(directLower, lower), math.Min(directUpper, upper), nil
	}
	remaining := 3
	roots, err := domeAstrodomeIsolatePathRootsWithSlopeBounds(
		0, width, leftValue, rightValue, directUpper,
		domeAstrodomePhysicalHeightOperandScaleM(), slopeBounds, &remaining, value,
	)
	if err != nil {
		t.Fatalf("secant-certified monotone root: %v", err)
	}
	if len(roots) != 1 || math.Abs(roots[0]-root) > domeAstrodomePhysicalRootToleranceM {
		t.Fatalf("secant-certified roots = %v; want %.12g within %.9g m", roots, root,
			domeAstrodomePhysicalRootToleranceM)
	}
}

func TestDomeAstrodomeSecantCertificateKeepsCompoundRootsFailClosed(t *testing.T) {
	t.Parallel()

	width := 1.6 * domeAstrodomePhysicalRootToleranceM
	tests := []struct {
		name  string
		value func(float64) (float64, error)
	}{
		{
			name: "paired crossing",
			value: func(pathM float64) (float64, error) {
				return (pathM - 0.375*width) * (pathM - 0.625*width) / width, nil
			},
		},
		{
			name: "tangent",
			value: func(pathM float64) (float64, error) {
				return (pathM - 0.5*width) * (pathM - 0.5*width) / width, nil
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			secondDerivativeBound := 2 / width
			slopeBounds := func(leftM, rightM, leftValue, rightValue float64) (float64, float64, error) {
				return domeAstrodomeSecantSlopeBounds(
					leftM, rightM, leftValue, rightValue, 1, secondDerivativeBound,
				)
			}
			leftValue, _ := test.value(0)
			rightValue, _ := test.value(width)
			lower, upper, err := slopeBounds(0, width, leftValue, rightValue)
			if err != nil {
				t.Fatal(err)
			}
			if lower > 0 || upper < 0 {
				t.Fatalf("compound-root secant enclosure = [%.12g, %.12g], want zero retained", lower, upper)
			}
			remaining := 8192
			_, err = domeAstrodomeIsolatePathRootsWithSlopeBounds(
				0, width, leftValue, rightValue, 1, 1, slopeBounds, &remaining, test.value,
			)
			if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
				t.Fatalf("compound-root isolation error = %v", err)
			}
		})
	}
}

func TestDomeAstrodomePhysicalBoundarySecondDerivativeBoundCoversSampledRay(t *testing.T) {
	t.Parallel()

	grid := Coverage()
	observer := forecast.Location{
		Latitude:  grid.MinLat + 10.5*grid.Increment,
		Longitude: grid.MinLon + 10.5*grid.Increment,
		TimeZone:  "UTC",
	}
	ray := traceDomeAstrodomeGridTestRay(t, observer, 45)
	volume := newDomeAstrodomeGridTestVolume(grid)
	const (
		startM = 100.0
		endM   = 1000.0
		stepM  = 2.0
	)
	middle, err := ray.PointAtPathLength((startM + endM) / 2)
	if err != nil {
		t.Fatal(err)
	}
	stencil, err := volume.HorizontalStencil(context.Background(), middle.Location)
	if err != nil {
		t.Fatal(err)
	}
	cellID, err := volume.HorizontalCellID(stencil)
	if err != nil {
		t.Fatal(err)
	}
	interval := domeAstrodomeCellInterval{
		startM: startM, endM: endM, cellID: cellID, stencil: stencil,
	}
	metric, err := volume.domeAstrodomeMetricBoundsForInterval(ray, interval)
	if err != nil {
		t.Fatal(err)
	}
	fields := [][4]float64{{100, 160, 220, 430}}
	bound, err := domeAstrodomePhysicalBoundarySecondDerivativeBound(
		ray, metric, fields, startM, endM,
	)
	if err != nil {
		t.Fatal(err)
	}
	valueAt := func(pathM float64) float64 {
		point, pointErr := ray.PointAtPathLength(pathM)
		if pointErr != nil {
			t.Fatal(pointErr)
		}
		pointStencil, stencilErr := volume.HorizontalStencil(context.Background(), point.Location)
		if stencilErr != nil {
			t.Fatal(stencilErr)
		}
		pointCellID, cellErr := volume.HorizontalCellID(pointStencil)
		if cellErr != nil {
			t.Fatal(cellErr)
		}
		if pointCellID != cellID {
			t.Fatalf("sample at %.3f m escaped %q into %q", pathM, cellID, pointCellID)
		}
		return point.HeightM - domeWeighted4(fields[0], domeStencilWeights(pointStencil))
	}
	maximumSample := 0.0
	for sample := 1; sample < 100; sample++ {
		pathM := startM + stepM + (endM-startM-2*stepM)*float64(sample)/100
		second := math.Abs((valueAt(pathM+stepM) - 2*valueAt(pathM) + valueAt(pathM-stepM)) / (stepM * stepM))
		maximumSample = math.Max(maximumSample, second)
	}
	if bound < maximumSample {
		t.Fatalf("physical-boundary second-derivative bound %.17g is below sampled maximum %.17g",
			bound, maximumSample)
	}
}

func TestDomeAstrodomePBLClampBranchClassification(t *testing.T) {
	t.Parallel()

	surface := [4]float64{180, 181, 179, 182}
	tests := []struct {
		name       string
		mixed      [4]float64
		wantSmooth bool
		wantFields int
	}{
		{name: "lower saturated", mixed: [4]float64{300, 420, 499, 450}, wantSmooth: true, wantFields: 1},
		{name: "production interior", mixed: [4]float64{856, 1731, 975, 1126}, wantSmooth: true, wantFields: 2},
		{name: "upper saturated", mixed: [4]float64{2100, 2400, 2200, 2300}, wantSmooth: true, wantFields: 1},
		{name: "lower kink", mixed: [4]float64{499, 501, 520, 530}, wantSmooth: false, wantFields: 2},
		{name: "upper kink", mixed: [4]float64{1900, 1999, 2001, 2100}, wantSmooth: false, wantFields: 2},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fields, smooth, err := domeAstrodomePBLFieldsForCornerRange(surface, test.mixed, 500, 2000)
			if err != nil {
				t.Fatal(err)
			}
			if smooth != test.wantSmooth || len(fields) != test.wantFields {
				t.Fatalf("PBL fields = %d, smooth=%t; want %d, %t", len(fields), smooth, test.wantFields, test.wantSmooth)
			}
		})
	}
}

func TestDomeAstrodomePBLLocalClampBranchCertificate(t *testing.T) {
	t.Parallel()

	surface := [4]float64{180, 181, 179, 182}
	mixed := [4]float64{400, 800, 450, 850}
	tests := []struct {
		name        string
		middleDepth float64
		halfWidth   float64
		wantSmooth  bool
		wantFields  int
	}{
		{name: "locally lower saturated", middleDepth: 450, halfWidth: 100, wantSmooth: true, wantFields: 1},
		{name: "locally interior", middleDepth: 700, halfWidth: 100, wantSmooth: true, wantFields: 2},
		{name: "contains lower kink", middleDepth: 500, halfWidth: 100, wantSmooth: false, wantFields: 2},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fields, smooth, err := domeAstrodomePBLFieldsAroundSample(
				surface, mixed, test.middleDepth, 0.02, 1000,
				0, test.halfWidth, 500, 2000,
			)
			if err != nil {
				t.Fatal(err)
			}
			if smooth != test.wantSmooth || len(fields) != test.wantFields {
				t.Fatalf("local PBL fields = %d, smooth=%t; want %d, %t", len(fields), smooth, test.wantFields, test.wantSmooth)
			}
		})
	}
}

func TestDomeAstrodomeBilinearCoordinateUncertaintyCoversOneULPPerturbation(t *testing.T) {
	t.Parallel()

	ray := traceDomeAstrodomeGridTestRay(
		t, forecast.Location{Latitude: 70, Longitude: 30, TimeZone: "UTC"}, 45,
	)
	pathM := ray.PathLengthM / 2
	point, err := ray.PointAtPathLength(pathM)
	if err != nil {
		t.Fatal(err)
	}
	positionErrorM, err := ray.PositionEvaluationErrorUpperBound(pathM)
	if err != nil {
		t.Fatal(err)
	}
	grid := Coverage()
	grid.MinLat = point.Location.Latitude - 10.5*grid.Increment
	grid.MaxLat = grid.MinLat + 20*grid.Increment
	grid.MinLon = point.Location.Longitude - 10.5*grid.Increment
	grid.MaxLon = grid.MinLon + 20*grid.Increment
	values := [4]float64{856, 1731, 975, 1126}
	operandScale := domeAstrodomeCornerOperandScale(values)

	valueAt := func(location forecast.Location) float64 {
		cell, cellErr := domeGridCell(grid, location)
		if cellErr != nil {
			t.Fatal(cellErr)
		}
		weights := [4]float64{
			(1 - cell.latitudeFraction) * (1 - cell.longitudeFraction),
			(1 - cell.latitudeFraction) * cell.longitudeFraction,
			cell.latitudeFraction * (1 - cell.longitudeFraction),
			cell.latitudeFraction * cell.longitudeFraction,
		}
		return domeWeighted4(values, weights)
	}
	baseValue := valueAt(point.Location)
	maximumDelta := 0.0
	for _, latitudeDirection := range []float64{math.Inf(-1), math.Inf(1)} {
		for _, longitudeDirection := range []float64{math.Inf(-1), math.Inf(1)} {
			perturbed := point.Location
			perturbed.Latitude = math.Nextafter(perturbed.Latitude, latitudeDirection)
			perturbed.Longitude = math.Nextafter(perturbed.Longitude, longitudeDirection)
			maximumDelta = math.Max(maximumDelta, math.Abs(valueAt(perturbed)-baseValue))
		}
	}
	scalarOnly := domeAstrodomeClearanceRoundoff(0, 0, operandScale)
	if maximumDelta <= scalarOnly {
		t.Fatalf("one-ULP coordinate delta %.17g m does not exercise scalar-only allowance %.17g m",
			maximumDelta, scalarOnly)
	}
	uncertainty, err := domeAstrodomeBilinearCoordinateUncertainty(
		point, positionErrorM, grid, values, operandScale,
	)
	if err != nil {
		t.Fatal(err)
	}
	if maximumDelta > uncertainty {
		t.Fatalf("one-ULP coordinate delta %.17g m exceeds coordinate uncertainty %.17g m",
			maximumDelta, uncertainty)
	}
}

func TestDomeAstrodomeBilinearCoordinateUncertaintyCoversGridSnapDiscontinuity(t *testing.T) {
	t.Parallel()

	threshold := 600.0 + domeGridLineSnapTolerance
	inside := math.Nextafter(threshold, math.Inf(-1))
	outside := math.Nextafter(threshold, math.Inf(1))
	_, insideFraction, err := domeLowerGridIndex(inside, 1000)
	if err != nil {
		t.Fatal(err)
	}
	_, outsideFraction, err := domeLowerGridIndex(outside, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if insideFraction != 0 || outsideFraction == 0 {
		t.Fatalf("one-ULP snap probes produced fractions %.17g and %.17g", insideFraction, outsideFraction)
	}
	values := [4]float64{856, 1731, 975, 1126}
	valueAt := func(longitudeFraction float64) float64 {
		const latitudeFraction = 0.5
		return domeWeighted4(values, [4]float64{
			(1 - latitudeFraction) * (1 - longitudeFraction),
			(1 - latitudeFraction) * longitudeFraction,
			latitudeFraction * (1 - longitudeFraction),
			latitudeFraction * longitudeFraction,
		})
	}
	delta := math.Abs(valueAt(outsideFraction) - valueAt(insideFraction))
	operandScale := domeAstrodomeCornerOperandScale(values)
	if delta <= domeAstrodomeClearanceRoundoff(0, 0, operandScale) {
		t.Fatalf("grid-snap delta %.17g m does not exercise the former scalar-only allowance", delta)
	}

	ray := traceDomeAstrodomeGridTestRay(
		t, forecast.Location{Latitude: 70, Longitude: 30, TimeZone: "UTC"}, 45,
	)
	pathM := ray.PathLengthM / 2
	point, err := ray.PointAtPathLength(pathM)
	if err != nil {
		t.Fatal(err)
	}
	positionErrorM, err := ray.PositionEvaluationErrorUpperBound(pathM)
	if err != nil {
		t.Fatal(err)
	}
	grid := Coverage()
	grid.MinLat = point.Location.Latitude - 10.5*grid.Increment
	grid.MaxLat = grid.MinLat + 20*grid.Increment
	grid.MinLon = point.Location.Longitude - 10.5*grid.Increment
	grid.MaxLon = grid.MinLon + 20*grid.Increment
	uncertainty, err := domeAstrodomeBilinearCoordinateUncertainty(
		point, positionErrorM, grid, values, operandScale,
	)
	if err != nil {
		t.Fatal(err)
	}
	if delta > uncertainty {
		t.Fatalf("grid-snap delta %.17g m exceeds coordinate uncertainty %.17g m", delta, uncertainty)
	}
}

func TestDomeAstrodomeBilinearCoordinateUncertaintyFailsClosedAcrossCellBoundary(t *testing.T) {
	t.Parallel()

	ray := traceDomeAstrodomeGridTestRay(
		t, forecast.Location{Latitude: 50, Longitude: 30, TimeZone: "UTC"}, 45,
	)
	pathM := ray.PathLengthM / 2
	point, err := ray.PointAtPathLength(pathM)
	if err != nil {
		t.Fatal(err)
	}
	positionErrorM, err := ray.PositionEvaluationErrorUpperBound(pathM)
	if err != nil {
		t.Fatal(err)
	}
	grid := Coverage()
	grid.MinLat = point.Location.Latitude
	grid.MaxLat = grid.MinLat + 20*grid.Increment
	grid.MinLon = point.Location.Longitude - 10.5*grid.Increment
	grid.MaxLon = grid.MinLon + 20*grid.Increment
	values := [4]float64{856, 1731, 975, 1126}
	_, err = domeAstrodomeBilinearCoordinateUncertainty(
		point, positionErrorM, grid, values, domeAstrodomeCornerOperandScale(values),
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("cell-crossing coordinate uncertainty error = %v; want fail-closed incomplete partition", err)
	}
}

func TestDomeAstrodomePBLLocalCoordinateUncertaintyFailsClosedAtClamp(t *testing.T) {
	t.Parallel()

	surface := [4]float64{180, 181, 179, 182}
	mixed := [4]float64{856, 1731, 975, 1126}
	operandScale := domeAstrodomeCornerOperandScale(mixed)
	const coordinateUncertaintyM = 1e-5
	fields, smooth, err := domeAstrodomePBLFieldsAroundSample(
		surface, mixed, 500-coordinateUncertaintyM/2, 0, operandScale,
		coordinateUncertaintyM, 0, 500, 2000,
	)
	if err != nil {
		t.Fatal(err)
	}
	if smooth || len(fields) != 2 {
		t.Fatalf("coordinate-uncertain PBL clamp returned %d fields, smooth=%t; want fail-closed kink enclosure",
			len(fields), smooth)
	}
}

func TestDomeAstrodomeBilinearWMOPredicateRetainsMonotoneRootProof(t *testing.T) {
	t.Parallel()

	grid := Coverage()
	observer := forecast.Location{
		Latitude:  grid.MinLat + 10.5*grid.Increment,
		Longitude: grid.MinLon + 10.5*grid.Increment,
		TimeZone:  "UTC",
	}
	ray := traceDomeAstrodomeGridTestRay(t, observer, 45)
	volume := newDomeAstrodomeGridTestVolume(grid)
	const (
		startM = 100.0
		endM   = 1000.0
	)
	middleM := (startM + endM) / 2
	middle, err := ray.PointAtPathLength(middleM)
	if err != nil {
		t.Fatal(err)
	}
	stencil, err := volume.HorizontalStencil(context.Background(), middle.Location)
	if err != nil {
		t.Fatal(err)
	}
	cellID, err := volume.HorizontalCellID(stencil)
	if err != nil {
		t.Fatal(err)
	}
	interval := domeAstrodomeCellInterval{
		startM: startM, endM: endM, cellID: cellID, stencil: stencil,
	}
	metric, err := volume.domeAstrodomeMetricBoundsForInterval(ray, interval)
	if err != nil {
		t.Fatal(err)
	}
	middleWeights := domeStencilWeights(stencil)
	middleLongitudeCoordinate := middleWeights[1] + middleWeights[3]
	const predicateScale = 1000.0
	desiredResidual := [4]float64{
		-predicateScale * middleLongitudeCoordinate,
		predicateScale * (1 - middleLongitudeCoordinate),
		-predicateScale * middleLongitudeCoordinate,
		predicateScale * (1 - middleLongitudeCoordinate),
	}
	lowerHeights := [4]float64{10_000, 10_000, 10_000, 10_000}
	upperHeights := [4]float64{10_200, 10_200, 10_200, 10_200}
	lowerTemperatures := [4]float64{220, 220, 220, 220}
	upperTemperatures := [4]float64{}
	cornerResiduals := [4]float64{}
	for corner := range upperTemperatures {
		upperTemperatures[corner] = lowerTemperatures[corner] +
			(desiredResidual[corner]-(upperHeights[corner]-lowerHeights[corner]))/500
		cornerResiduals[corner] = forecast.WMOThermalLapseResidual(
			lowerHeights[corner], upperHeights[corner],
			lowerTemperatures[corner], upperTemperatures[corner],
		)
	}
	field := domeAstrodomeScalarField{
		id:      "wmo/level-039/instant-lapse",
		corners: cornerResiduals,
		evaluate: func(weights [4]float64) float64 {
			return forecast.WMOThermalLapseResidual(
				domeWeighted4(lowerHeights, weights), domeWeighted4(upperHeights, weights),
				domeWeighted4(lowerTemperatures, weights), domeWeighted4(upperTemperatures, weights),
			)
		},
		// A WMO lapse residual encloses HHL and 500*T operands, rather than
		// pretending that its small cancellation residual is the input scale.
		roundoffOperandScale: 150_000,
	}
	valueAt := func(pathM float64) (float64, error) {
		point, pointErr := ray.PointAtPathLength(pathM)
		if pointErr != nil {
			return 0, pointErr
		}
		pointStencil, stencilErr := volume.HorizontalStencil(context.Background(), point.Location)
		if stencilErr != nil {
			return 0, stencilErr
		}
		pointCellID, cellErr := volume.HorizontalCellID(pointStencil)
		if cellErr != nil {
			return 0, cellErr
		}
		if pointCellID != cellID {
			return 0, fmt.Errorf("sample escaped %q into %q", cellID, pointCellID)
		}
		return field.value(domeStencilWeights(pointStencil)), nil
	}
	leftValue, err := valueAt(startM)
	if err != nil {
		t.Fatal(err)
	}
	rightValue, err := valueAt(endM)
	if err != nil {
		t.Fatal(err)
	}
	if leftValue >= 0 || rightValue <= 0 {
		t.Fatalf("fixture does not bracket its monotone WMO root: %.12g .. %.12g", leftValue, rightValue)
	}
	lipschitz, err := domeAstrodomeScalarFieldLipschitz(metric, field.corners, field.roundoffOperandScale)
	if err != nil {
		t.Fatal(err)
	}
	slopeBounds, err := domeAstrodomeBilinearScalarSlopeBounds(
		ray, metric, field, lipschitz, startM, endM,
	)
	if err != nil {
		t.Fatal(err)
	}
	probes, err := domeAstrodomePhysicalProbePaths(interval)
	if err != nil {
		t.Fatal(err)
	}
	maximumCoordinateError := 0.0
	residualValueAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
		point, valueErr := ray.PointAtPathLength(pathM)
		if valueErr != nil {
			return domeAstrodomeResidualSample{}, valueErr
		}
		positionErrorM, valueErr := ray.PositionEvaluationErrorUpperBound(pathM)
		if valueErr != nil {
			return domeAstrodomeResidualSample{}, valueErr
		}
		coordinateError, valueErr := domeAstrodomeBilinearCoordinateUncertainty(
			point, positionErrorM, grid, field.corners, field.roundoffOperandScale,
		)
		if valueErr != nil {
			return domeAstrodomeResidualSample{}, valueErr
		}
		maximumCoordinateError = math.Max(maximumCoordinateError, coordinateError)
		value, valueErr := valueAt(pathM)
		if valueErr != nil {
			return domeAstrodomeResidualSample{}, valueErr
		}
		return domeAstrodomeResidualWithEvaluationError(value, coordinateError)
	}
	roots, err := domeAstrodomeIsolateFieldAcrossProbesWithSlopeBoundsResiduals(
		interval, probes, lipschitz, field.roundoffOperandScale, slopeBounds, residualValueAt,
	)
	if err != nil {
		t.Fatalf("isolate smooth WMO predicate: %v", err)
	}
	if len(roots) != 1 || math.Abs(roots[0]-middleM) > domeAstrodomePhysicalRootToleranceM {
		t.Fatalf("smooth WMO roots = %v; want %.12g within %.9g m", roots, middleM,
			domeAstrodomePhysicalRootToleranceM)
	}
	if maximumCoordinateError <= 0 || maximumCoordinateError >= 1e-3 {
		t.Fatalf("actual WMO coordinate error = %.12g; want a positive sub-millimetre enclosure",
			maximumCoordinateError)
	}
	largeErrorValueAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
		residual, valueErr := residualValueAt(pathM)
		if valueErr != nil {
			return domeAstrodomeResidualSample{}, valueErr
		}
		residual.evaluationError = domeAstrodomePositiveAddUpper(residual.evaluationError, 10)
		return residual, nil
	}
	_, err = domeAstrodomeIsolateFieldAcrossProbesWithSlopeBoundsResiduals(
		interval, probes, lipschitz, field.roundoffOperandScale, slopeBounds, largeErrorValueAt,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("large WMO evaluation error = %v; want fail-closed incomplete partition", err)
	}
}

func TestDomeAstrodomeWholeProbeCertificateKeepsTangencyFailClosed(t *testing.T) {
	t.Parallel()

	interval := domeAstrodomeCellInterval{startM: 0, endM: 1, cellID: "test"}
	probes, err := domeAstrodomePhysicalProbePaths(interval)
	if err != nil {
		t.Fatal(err)
	}
	value := func(pathM float64) (float64, error) {
		return (pathM - 0.5) * (pathM - 0.5), nil
	}
	slopeBounds := func(leftM, rightM, _, _ float64) (float64, float64, error) {
		return math.Nextafter(2*(leftM-0.5), math.Inf(-1)),
			math.Nextafter(2*(rightM-0.5), math.Inf(1)), nil
	}
	_, err = domeAstrodomeIsolateFieldAcrossProbesWithSlopeBounds(
		interval, probes, 1, 1, slopeBounds, value,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("whole-probe tangent error = %v", err)
	}
}

func TestDomeAstrodomeSlopeCertificateContainingZeroRemainsFailClosed(t *testing.T) {
	t.Parallel()

	width := domeAstrodomeRootProofToleranceM
	root := width / 2
	value := func(pathM float64) (float64, error) { return pathM - root, nil }
	leftValue, _ := value(0)
	rightValue, _ := value(width)
	slopeBounds := func(float64, float64, float64, float64) (float64, float64, error) {
		return -1, 1, nil
	}
	remaining := 3
	_, err := domeAstrodomeIsolatePathRootsWithSlopeBounds(
		0, width, leftValue, rightValue, 1, 1, slopeBounds, &remaining, value,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("inconclusive slope certificate error = %v", err)
	}
}

func TestDomeAstrodomeRejectsInvalidSlopeCertificate(t *testing.T) {
	t.Parallel()

	width := domeAstrodomePhysicalRootToleranceM
	value := func(pathM float64) (float64, error) { return pathM - width/2, nil }
	leftValue, _ := value(0)
	rightValue, _ := value(width)
	slopeBounds := func(float64, float64, float64, float64) (float64, float64, error) {
		return 2, 1, nil
	}
	remaining := 3
	_, err := domeAstrodomeIsolatePathRootsWithSlopeBounds(
		0, width, leftValue, rightValue, 1, 1, slopeBounds, &remaining, value,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("invalid slope certificate error = %v", err)
	}
}

func TestDomeAstrodomeProofLeafExactEndpointDoesNotHidePairedRoots(t *testing.T) {
	t.Parallel()

	width := domeAstrodomeRootProofToleranceM
	value := func(pathM float64) (float64, error) {
		return pathM * (pathM - 0.1*width) * (pathM - 0.2*width), nil
	}
	leftValue, _ := value(0)
	rightValue, _ := value(width)
	// For f=product_i(s-r_i) on [0,w], |f'| is bounded by 3*w^2.
	remaining := 3
	_, err := domeAstrodomeIsolatePathRoots(
		0, width, leftValue, rightValue, 3*width*width, 1, &remaining, value,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("exact endpoint with paired roots error = %v", err)
	}
}

func TestDomeAstrodomeClearanceIncludesNativeHeightCancellationScale(t *testing.T) {
	t.Parallel()

	operandScale := domeAstrodomePhysicalHeightOperandScaleM()
	residual := domeAstrodomeClearanceRoundoff(0, 0, operandScale) / 2
	value := func(float64) (float64, error) { return residual, nil }
	remaining := 3
	_, err := domeAstrodomeIsolatePathRoots(
		0, domeAstrodomeRootProofToleranceM, residual, residual,
		1e-12, operandScale, &remaining, value,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("Earth-radius cancellation clearance error = %v", err)
	}
}

func TestDomeAstrodomeEndpointSliverIncludesNativeHeightCancellationScale(t *testing.T) {
	t.Parallel()

	operandScale := domeAstrodomePhysicalHeightOperandScaleM()
	residual := domeAstrodomeClearanceRoundoff(0, 0, operandScale) / 2
	interval := domeAstrodomeCellInterval{startM: 0, endM: 1, cellID: "roundoff-sliver"}
	probes := [5]float64{1e-6, 0.25, 0.5, 0.75, 1 - 1e-6}
	values := [5]float64{residual, residual, residual, residual, residual}
	err := domeAstrodomeCertifyProbeEndpointSlivers(
		interval, probes, values, 1e-12, operandScale,
	)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("Earth-radius endpoint-sliver cancellation error = %v", err)
	}
}

func TestDomeAstrodomePhysicalRootContractScales(t *testing.T) {
	t.Parallel()

	if domeAstrodomeHorizontalRootToleranceM != 0.2e-3 ||
		domeAstrodomePhysicalRootToleranceM != 0.2e-3 ||
		domeAstrodomePhysicalMergeToleranceM != 0.4e-3 ||
		forecast.AstrodomeScienceCompoundRootSideGuardM != 0.5e-3 ||
		domeAstrodomeRootProofToleranceM != 0.2e-3 ||
		domeAstrodomePositionCoordinateEnvelopeM != 1e-3 {
		t.Fatalf("root contract = horizontal %.9g physical %.9g merge %.9g guard %.9g proof %.9g evaluation %.9g",
			domeAstrodomeHorizontalRootToleranceM,
			domeAstrodomePhysicalRootToleranceM,
			domeAstrodomePhysicalMergeToleranceM,
			forecast.AstrodomeScienceCompoundRootSideGuardM, domeAstrodomeRootProofToleranceM,
			domeAstrodomePositionCoordinateEnvelopeM)
	}
}

func TestDomeAstrodomeResidualSignIncludesEvaluationError(t *testing.T) {
	t.Parallel()

	sign, lower, upper := domeAstrodomeResidualSignInterval(
		domeAstrodomeResidualSample{value: 0.5e-3, evaluationError: 1e-3}, 1,
	)
	if sign != 0 || lower >= 0 || upper <= 0 {
		t.Fatalf("evaluation-enclosed residual sign = %d [%.12g, %.12g]", sign, lower, upper)
	}
	sign, lower, upper = domeAstrodomeResidualSignInterval(
		domeAstrodomeResidualSample{value: 0, evaluationError: 0}, 1,
	)
	if sign != 0 || lower >= 0 || upper <= 0 {
		t.Fatalf("nominal-zero residual sign = %d [%.12g, %.12g]", sign, lower, upper)
	}
}

func TestDomeAstrodomeSecantAndSliverIncludeEvaluationError(t *testing.T) {
	t.Parallel()

	withoutLower, withoutUpper, err := domeAstrodomeSecantSlopeBoundsResiduals(
		0, 1,
		domeAstrodomeResidualSample{value: 0},
		domeAstrodomeResidualSample{value: 1},
		1, 1e-6,
	)
	if err != nil {
		t.Fatal(err)
	}
	withLower, withUpper, err := domeAstrodomeSecantSlopeBoundsResiduals(
		0, 1,
		domeAstrodomeResidualSample{value: 0, evaluationError: 1e-3},
		domeAstrodomeResidualSample{value: 1, evaluationError: 1e-3},
		1, 1e-6,
	)
	if err != nil {
		t.Fatal(err)
	}
	if withLower >= withoutLower || withUpper <= withoutUpper {
		t.Fatalf("evaluation-enclosed secant [%.12g, %.12g] did not widen [%.12g, %.12g]",
			withLower, withUpper, withoutLower, withoutUpper)
	}

	interval := domeAstrodomeCellInterval{startM: 0, endM: 1, cellID: "evaluation-sliver"}
	probes := [5]float64{0.025, 0.25, 0.5, 0.75, 0.975}
	values := [5]domeAstrodomeResidualSample{}
	for index := range values {
		values[index] = domeAstrodomeResidualSample{value: 0.026, evaluationError: 0.002}
	}
	err = domeAstrodomeCertifyProbeEndpointSliversResiduals(interval, probes, values, 1, 1, nil)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("evaluation-enclosed endpoint sliver error = %v", err)
	}
}

func TestDomeAstrodomeCoordinateEvaluationBoundMustFitEnvelope(t *testing.T) {
	t.Parallel()

	grid := Coverage()
	observer := forecast.Location{
		Latitude:  grid.MinLat + 10.5*grid.Increment,
		Longitude: grid.MinLon + 10.5*grid.Increment,
		TimeZone:  "UTC",
	}
	ray := traceDomeAstrodomeGridTestRay(t, observer, 45)
	point, err := ray.PointAtPathLength(ray.PathLengthM / 2)
	if err != nil {
		t.Fatal(err)
	}
	actualPositionError, err := ray.PositionEvaluationErrorUpperBound(ray.PathLengthM / 2)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := domeAstrodomeCoordinateEvaluationErrorBounds(point, actualPositionError, grid, true)
	if err != nil {
		t.Fatal(err)
	}
	if actual.equivalentPositionM > domeAstrodomePositionCoordinateEnvelopeM {
		t.Fatalf("actual coordinate evaluation bound %.12g m exceeds contract", actual.equivalentPositionM)
	}
	over, err := domeAstrodomeCoordinateEvaluationErrorBounds(point, 2e-3, grid, true)
	if err != nil {
		t.Fatal(err)
	}
	if over.equivalentPositionM <= domeAstrodomePositionCoordinateEnvelopeM {
		t.Fatalf("oversized coordinate evaluation bound %.12g m was accepted by contract comparison",
			over.equivalentPositionM)
	}
}

func TestDomeAstrodomeWMOPredicateFieldsPartitionRawCandidateDecisions(t *testing.T) {
	t.Parallel()

	heights := [][4]float64{
		{4900, 5100, 4900, 5100},
		{5500, 5500, 5500, 5500},
		{7490, 7510, 7490, 7510},
		{9000, 9000, 9000, 9000},
	}
	temperatures := [][4]float64{
		{275, 275, 275, 275},
		{270, 270, 270, 270},
		{265, 267, 265, 267},
		{260, 260, 260, 260},
	}
	fields, _, err := domeAstrodomeWMOPredicateFieldsFromCorners(heights, temperatures)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		seen[field.id] = true
		minimum, maximum := field.corners[0], field.corners[0]
		for _, value := range field.corners[1:] {
			minimum, maximum = math.Min(minimum, value), math.Max(maximum, value)
		}
		if !(minimum < 0 && maximum > 0) {
			t.Fatalf("retained WMO predicate %q does not cross zero: %v", field.id, field.corners)
		}
	}
	for _, id := range []string{
		"wmo/level-000/height-5000m",
		"wmo/level-001/instant-lapse",
		"wmo/level-001/top-002/span-2000m",
	} {
		if !seen[id] {
			t.Fatalf("raw WMO decision %q was not partitioned; fields=%v", id, seen)
		}
	}
}

func TestDomeAstrodomeWMOPredicateFieldsStopAfterFirstCertifiedTwoKilometreTop(t *testing.T) {
	t.Parallel()

	heights := [][4]float64{
		{6000, 6000, 6000, 6000},
		{7000, 7000, 7000, 7000},
		{8100, 8100, 8100, 8100},
		{10000, 10000, 10000, 10000},
	}
	temperatures := [][4]float64{
		{280, 280, 280, 280},
		{277, 279, 277, 279},
		{274.8, 276.8, 274.8, 276.8},
		{271, 273, 271, 273},
	}
	fields, _, err := domeAstrodomeWMOPredicateFieldsFromCorners(heights, temperatures)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		seen[field.id] = true
	}
	for _, id := range []string{
		"wmo/level-000/instant-lapse",
		"wmo/level-000/top-002/mean-lapse",
	} {
		if !seen[id] {
			t.Fatalf("reachable WMO predicate %q was omitted; fields=%v", id, seen)
		}
	}
	if seen["wmo/level-000/top-001/mean-lapse"] {
		t.Fatalf("first mean-lapse predicate duplicated the instantaneous zero set: %v", seen)
	}
	if seen["wmo/level-000/top-003/mean-lapse"] {
		t.Fatalf("unreachable WMO predicate above the first certified 2-km top was retained: %v", seen)
	}
}

func TestDomeAstrodomeWMOPredicateFieldsKeepHorizontallyVariableTwoKilometreTop(t *testing.T) {
	t.Parallel()

	heights := [][4]float64{
		{6000, 6000, 6000, 6000},
		{7000, 7000, 7000, 7000},
		{7900, 8100, 7900, 8100},
		{8500, 8500, 8500, 8500},
		{10000, 10000, 10000, 10000},
	}
	temperatures := [][4]float64{
		{280, 280, 280, 280},
		{277, 279, 277, 279},
		{275, 277, 275, 277},
		{274, 276, 274, 276},
		{271, 273, 271, 273},
	}
	fields, _, err := domeAstrodomeWMOPredicateFieldsFromCorners(heights, temperatures)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		seen[field.id] = true
	}
	for _, id := range []string{
		"wmo/level-000/top-002/span-2000m",
		"wmo/level-000/top-002/mean-lapse",
		"wmo/level-000/top-003/mean-lapse",
	} {
		if !seen[id] {
			t.Fatalf("horizontally reachable WMO predicate %q was omitted; fields=%v", id, seen)
		}
	}
	if seen["wmo/level-000/top-004/mean-lapse"] {
		t.Fatalf("predicate above the first cell-wide 2-km top was retained: %v", seen)
	}
}

func TestDomeAstrodomeWMOPredicateFieldsDoNotStopAtUncertainTwoKilometreBoundary(t *testing.T) {
	t.Parallel()

	heights := [][4]float64{
		{6000, 6000, 6000, 6000},
		{7000, 7000, 7000, 7000},
		{8000, 8000, 8000, 8000},
		{8500, 8500, 8500, 8500},
	}
	temperatures := [][4]float64{
		{280, 280, 280, 280},
		{277, 279, 277, 279},
		{275, 277, 275, 277},
		{274, 276, 274, 276},
	}
	fields, _, err := domeAstrodomeWMOPredicateFieldsFromCorners(heights, temperatures)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		seen[field.id] = true
	}
	if !seen["wmo/level-000/top-002/span-2000m"] ||
		!seen["wmo/level-000/top-003/mean-lapse"] {
		t.Fatalf("roundoff-uncertain 2-km boundary hid a reachable later predicate: %v", seen)
	}
}

func TestDomeAstrodomeWMOPredicateFieldsExcludeProductionShapedRemoteTop(t *testing.T) {
	t.Parallel()

	heights := make([][4]float64, domeFullLevelCount)
	temperatures := make([][4]float64, domeFullLevelCount)
	for level := range heights {
		heightM := 300 * float64(level)
		heights[level] = [4]float64{heightM, heightM, heightM, heightM}
		temperatureK := 285 - 0.001*heightM
		temperatures[level] = [4]float64{temperatureK, temperatureK, temperatureK, temperatureK}
	}
	temperatures[63] = [4]float64{240, 242, 240, 242}
	fields, _, err := domeAstrodomeWMOPredicateFieldsFromCorners(heights, temperatures)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range fields {
		if field.id == "wmo/level-032/top-063/mean-lapse" {
			t.Fatal("production-shaped WMO profile retained an unreachable level-032/top-063 predicate")
		}
	}
}

func TestDomeAstrodomeWMOPredicateFieldsStopAfterCellWideEarlierCandidate(t *testing.T) {
	t.Parallel()

	heights := make([][4]float64, domeFullLevelCount)
	temperatures := make([][4]float64, domeFullLevelCount)
	for level := range heights {
		heightM := 300 * float64(level)
		heights[level] = [4]float64{heightM, heightM, heightM, heightM}
		temperatureK := 285 - 0.001*heightM
		temperatures[level] = [4]float64{temperatureK, temperatureK, temperatureK, temperatureK}
	}
	// This later algebraic predicate crosses zero across the cell, but it is
	// unreachable: level 17 is already a certified WMO candidate everywhere.
	lower := 55
	deltaHeightM := heights[lower+1][0] - heights[lower][0]
	thresholdTemperature := temperatures[lower][0] - deltaHeightM/500
	temperatures[lower+1] = [4]float64{
		math.Nextafter(thresholdTemperature, math.Inf(1)),
		math.Nextafter(thresholdTemperature, math.Inf(-1)),
		math.Nextafter(thresholdTemperature, math.Inf(1)),
		math.Nextafter(thresholdTemperature, math.Inf(-1)),
	}

	fields, wmoGuaranteed, err := domeAstrodomeWMOPredicateFieldsFromCorners(heights, temperatures)
	if err != nil {
		t.Fatal(err)
	}
	if !wmoGuaranteed {
		t.Fatal("earlier cell-wide WMO candidate was not certified")
	}
	for _, field := range fields {
		if field.id == "wmo/level-055/instant-lapse" || strings.HasPrefix(field.id, "wmo/level-055/") {
			t.Fatalf("unreachable later WMO predicate was retained: %s", field.id)
		}
	}
}

func TestDomeAstrodomeWMOPredicateFieldsSkipMeansAfterCellWideInstantFailure(t *testing.T) {
	t.Parallel()

	heights := [][4]float64{
		{6000, 6000, 6000, 6000},
		{6500, 6500, 6500, 6500},
		{7000, 7000, 7000, 7000},
		{7500, 7500, 7500, 7500},
		{8100, 8100, 8100, 8100},
		{8600, 8600, 8600, 8600},
	}
	temperatures := [][4]float64{
		{280, 280, 280, 280},
		// R(0,1) = 500 + 500*(-2) = -500 m at every corner.
		{278, 278, 278, 278},
		{277.5, 277.5, 277.5, 277.5},
		{277, 277, 277, 277},
		// The unreachable lower-0 mean field would cross zero here.
		{275.7, 275.9, 275.7, 275.9},
		{275.5, 275.5, 275.5, 275.5},
	}
	fields, wmoGuaranteed, err := domeAstrodomeWMOPredicateFieldsFromCorners(heights, temperatures)
	if err != nil {
		t.Fatal(err)
	}
	if !wmoGuaranteed {
		t.Fatal("later valid WMO candidate was not evaluated after the impossible candidate")
	}
	for _, field := range fields {
		if strings.HasPrefix(field.id, "wmo/level-000/top-") {
			t.Fatalf("cell-wide instant failure retained unreachable field %q", field.id)
		}
	}
}

func TestDomeAstrodomeWMOPredicateFieldsStopAfterCellWideMeanFailure(t *testing.T) {
	t.Parallel()

	heights := [][4]float64{
		{6000, 6000, 6000, 6000},
		{6500, 6500, 6500, 6500},
		{7000, 7000, 7000, 7000},
		{7500, 7500, 7500, 7500},
		{8000, 8000, 8000, 8000},
		{8500, 8500, 8500, 8500},
		{9000, 9000, 9000, 9000},
		{9500, 9500, 9500, 9500},
	}
	temperatures := [][4]float64{
		{280, 280, 280, 280},
		// Lower level 0 passes its instantaneous test at level 1.
		{279.5, 279.5, 279.5, 279.5},
		// R(0,2) = 1000 + 500*(-3) = -500 m everywhere, so a
		// point reaching level 2 rejects candidate 0. Points whose first
		// 2-km top was earlier have already stopped and cannot read level 3.
		{277, 277, 277, 277},
		// This later, unreachable lower-0 mean field would cross zero if the
		// scan continued after the cell-wide failure at level 2.
		{math.Nextafter(277, math.Inf(1)), math.Nextafter(277, math.Inf(-1)),
			math.Nextafter(277, math.Inf(1)), math.Nextafter(277, math.Inf(-1))},
		{276, 276, 276, 276},
		{275.5, 275.5, 275.5, 275.5},
		{275, 275, 275, 275},
		{274.5, 274.5, 274.5, 274.5},
	}
	fields, wmoGuaranteed, err := domeAstrodomeWMOPredicateFieldsFromCorners(heights, temperatures)
	if err != nil {
		t.Fatal(err)
	}
	if !wmoGuaranteed {
		t.Fatal("later valid WMO candidate was not evaluated after the failed mean lapse")
	}
	for _, field := range fields {
		if strings.HasPrefix(field.id, "wmo/level-000/top-003/") {
			t.Fatalf("cell-wide mean failure retained unreachable later field %q", field.id)
		}
	}
}

func TestDomeAstrodomeWMOHeightEvaluatorUsesHorizontalHHLFirst(t *testing.T) {
	t.Parallel()

	upperHalf := [][4]float64{
		{6200.1, 6200.2, 6200.3, 6200.4},
		{6700.1, 6700.2, 6700.3, 6700.4},
		{8300.1, 8300.2, 8300.3, 8300.4},
	}
	lowerHalf := [][4]float64{
		{5799.9, 5799.8, 5799.7, 5799.6},
		{6299.9, 6299.8, 6299.7, 6299.6},
		{7899.9, 7899.8, 7899.7, 7899.6},
	}
	heights := make([][4]float64, len(upperHalf))
	for level := range heights {
		for corner := range heights[level] {
			heights[level][corner] = (upperHalf[level][corner] + lowerHalf[level][corner]) / 2
		}
	}
	temperatures := [][4]float64{
		{270, 270.1, 270.2, 270.3},
		{269, 269.1, math.Nextafter(269.2, math.Inf(1)), math.Nextafter(269.3, math.Inf(-1))},
		{267, 267.1, 267.2, 267.3},
	}
	heightAt := func(level int, weights [4]float64) float64 {
		return (domeWeighted4(upperHalf[level], weights) + domeWeighted4(lowerHalf[level], weights)) / 2
	}
	fields, _, err := domeAstrodomeWMOPredicateFieldsFromCornersWithHeightEvaluator(
		heights, temperatures, heightAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	weights := [4]float64{0.13, 0.17, 0.29, 0.41}
	for _, field := range fields {
		if field.id != "wmo/level-000/instant-lapse" {
			continue
		}
		want := forecast.WMOThermalLapseResidual(
			heightAt(0, weights), heightAt(1, weights),
			domeWeighted4(temperatures[0], weights), domeWeighted4(temperatures[1], weights),
		)
		if got := field.value(weights); got != want {
			t.Fatalf("evaluated WMO field = %.17g, horizontal-HHL-first comparator = %.17g", got, want)
		}
		return
	}
	t.Fatal("crossing instant-lapse field was not retained")
}

func TestDomeAstrodomeWMOEvaluatedFieldUsesScienceComparator(t *testing.T) {
	t.Parallel()

	heights := [][4]float64{
		{6000, 6000.25, 6000.5, 6000.75},
		{6500, 6500.25, 6500.5, 6500.75},
		{8100, 8100.25, 8100.5, 8100.75},
	}
	temperatures := [][4]float64{
		{270, 270.1, 270.2, 270.3},
		{269, 269.1, math.Nextafter(269.2, math.Inf(1)), math.Nextafter(269.3, math.Inf(-1))},
		{267, 267.1, 267.2, 267.3},
	}
	fields, _, err := domeAstrodomeWMOPredicateFieldsFromCorners(heights, temperatures)
	if err != nil {
		t.Fatal(err)
	}
	weights := [4]float64{0.13, 0.17, 0.29, 0.41}
	for _, field := range fields {
		if field.id != "wmo/level-000/instant-lapse" {
			continue
		}
		want := forecast.WMOThermalLapseResidual(
			domeWeighted4(heights[0], weights), domeWeighted4(heights[1], weights),
			domeWeighted4(temperatures[0], weights), domeWeighted4(temperatures[1], weights),
		)
		if got := field.value(weights); got != want {
			t.Fatalf("evaluated WMO field = %.17g, science comparator = %.17g", got, want)
		}
		return
	}
	t.Fatal("crossing instant-lapse field was not retained")
}

func TestDomeAppendAstrodomeCrossingFieldRetainsRoundoffUncertainty(t *testing.T) {
	t.Parallel()

	const operandScale = 30_000.0
	uncertainty := domeAstrodomeClearanceRoundoff(0, 0, operandScale)
	fields := []domeAstrodomeScalarField{}
	domeAppendAstrodomeCrossingField(&fields, "uncertain", [4]float64{
		uncertainty / 2, uncertainty / 2, uncertainty / 2, uncertainty / 2,
	}, operandScale)
	if len(fields) != 1 || fields[0].id != "uncertain" {
		t.Fatalf("roundoff-uncertain same-sign predicate was omitted: %+v", fields)
	}

	fields = fields[:0]
	domeAppendAstrodomeCrossingField(&fields, "clear", [4]float64{
		2 * uncertainty, 2 * uncertainty, 2 * uncertainty, 2 * uncertainty,
	}, operandScale)
	if len(fields) != 0 {
		t.Fatalf("roundoff-separated predicate was unnecessarily retained: %+v", fields)
	}
}

func TestDomeAstrodomeFallbackBoundaryHasFiniteCertifiedBound(t *testing.T) {
	t.Parallel()

	profile := domeAstrodomeTropopauseProfile{
		heights: [][4]float64{
			{10_000, 10_010, 10_020, 10_030},
			{12_000, 12_015, 12_025, 12_040},
		},
		pressures: [][4]float64{
			{30_000, 30_100, 30_200, 30_300},
			{10_000, 10_050, 10_100, 10_150},
		},
	}
	metric := domeAstrodomeMetricBounds{
		pathDerivativeNorm: 1.01,
		radiusMinimumM:     forecast.AstrodomeICONSphereRadiusM,
		longitudeRadiusM:   4_000_000,
		incrementRadians:   0.0625 * math.Pi / 180,
	}
	bound, err := domeAstrodomeFallbackBoundaryLipschitz(metric, profile, 0)
	if err != nil || !finiteDomeVolume(bound) || bound <= metric.pathDerivativeNorm {
		t.Fatalf("200-hPa fallback Lipschitz bound = %.12g, %v", bound, err)
	}
}

func TestDomeAstrodomeFallbackBoundaryOutwardBoundSurvivesNearCancellation(t *testing.T) {
	t.Parallel()

	profile := domeAstrodomeTropopauseProfile{
		heights: [][4]float64{
			{10_000, 10_000.001, 10_000.002, 10_000.003},
			{12_000, 12_000.001, 12_000.002, 12_000.003},
		},
		pressures: [][4]float64{
			{20_000.00001, 20_000.000011, 20_000.000012, 20_000.000013},
			{20_000, 20_000.000001, 20_000.000002, 20_000.000003},
		},
	}
	metric := domeAstrodomeMetricBounds{
		pathDerivativeNorm: 1.01,
		radiusMinimumM:     forecast.AstrodomeICONSphereRadiusM,
		longitudeRadiusM:   4_000_000,
		incrementRadians:   0.0625 * math.Pi / 180,
	}
	bound, err := domeAstrodomeFallbackBoundaryLipschitz(metric, profile, 0)
	if err != nil || !finiteDomeVolume(bound) || bound <= metric.pathDerivativeNorm {
		t.Fatalf("near-cancellation 200-hPa outward bound = %.12g, %v", bound, err)
	}
}

func TestDomeAstrodomeOutwardHelpersEncloseHighPrecisionArithmetic(t *testing.T) {
	t.Parallel()

	precision := uint(256)
	toBig := func(value float64) *big.Float {
		return new(big.Float).SetPrec(precision).SetFloat64(value)
	}
	left := math.Nextafter(forecast.AstrodomeICONSphereRadiusM+12_345.6789, math.Inf(1))
	right := math.Nextafter(9876.54321, math.Inf(1))
	scale := domeAstrodomePhysicalHeightOperandScaleM()

	exactDifference := new(big.Float).SetPrec(precision).Sub(toBig(left), toBig(right))
	lowerDifference := toBig(domeAstrodomePositiveSubLower(left, right, scale))
	if lowerDifference.Cmp(exactDifference) > 0 {
		t.Fatalf("subtraction lower bound exceeds exact value: %s > %s", lowerDifference.Text('g', 20), exactDifference.Text('g', 20))
	}

	factor := math.Nextafter(0.7312345678901234, math.Inf(1))
	exactProduct := new(big.Float).SetPrec(precision).Mul(toBig(left), toBig(factor))
	lowerProduct := toBig(domeAstrodomePositiveMulLower(left, factor, scale))
	if lowerProduct.Cmp(exactProduct) > 0 {
		t.Fatalf("product lower bound exceeds exact value: %s > %s", lowerProduct.Text('g', 20), exactProduct.Text('g', 20))
	}

	exactSum := new(big.Float).SetPrec(precision).Add(toBig(left), toBig(right))
	upperSum := toBig(domeAstrodomePositiveAddUpper(left, right))
	if upperSum.Cmp(exactSum) < 0 {
		t.Fatalf("sum upper bound is below exact value: %s < %s", upperSum.Text('g', 20), exactSum.Text('g', 20))
	}
}

func TestDomeAstrodomeFieldIsolationSharesRootEvidenceAcrossProbeBoundary(t *testing.T) {
	t.Parallel()

	const root = 1.000001
	value := func(pathM float64) (float64, error) { return pathM - root, nil }
	roots, err := domeAstrodomeIsolateFieldAcrossProbes(
		domeAstrodomeCellInterval{startM: 0, endM: 4, cellID: "probe-boundary"},
		[5]float64{0, 1, 2, 3, 4}, 1, 1, value,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || math.Abs(roots[0]-root) > domeAstrodomePhysicalRootToleranceM {
		t.Fatalf("probe-boundary root = %.12v; want %.12g", roots, root)
	}
}

func TestDomeAstrodomeIsolatePathRootsFailsClosedAroundMidpointRootSides(t *testing.T) {
	t.Parallel()

	// The exact midpoint root used to end the scan and hide the later root.
	value := func(pathM float64) (float64, error) {
		return (pathM - 1) * (pathM - 1.5), nil
	}
	remaining := 8192
	_, err := domeAstrodomeIsolatePathRoots(0, 2, 1.6, 0.4, 2.6, 2, &remaining, value)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("unproved midpoint-root neighbourhood error = %v", err)
	}
}

func TestDomeAstrodomeIsolatePathRootsFailsClosedAroundThreeRoots(t *testing.T) {
	t.Parallel()

	value := func(pathM float64) (float64, error) {
		return (pathM - 0.25) * (pathM - 0.5) * (pathM - 0.75), nil
	}
	leftValue, _ := value(0)
	rightValue, _ := value(1)
	remaining := 8192
	_, err := domeAstrodomeIsolatePathRoots(0, 1, leftValue, rightValue, 2, 1, &remaining, value)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("unproved three-root neighbourhood error = %v", err)
	}
}

func TestCompactDomePhysicalRootCandidatesUsesEventIdentity(t *testing.T) {
	t.Parallel()

	duplicates, err := compactDomePhysicalRootCandidates([]domeAstrodomePhysicalRootCandidate{
		{pathM: 50, eventID: "boundary/cloud-low-top"},
		{pathM: 50, eventID: "boundary/cloud-low-top"},
	})
	if err != nil || len(duplicates) != 1 || duplicates[0].pathM != 50 {
		t.Fatalf("same-event duplicate compaction = %+v, %v", duplicates, err)
	}

	simultaneous, err := compactDomePhysicalRootCandidates([]domeAstrodomePhysicalRootCandidate{
		{pathM: 50, eventID: "boundary/pbl"},
		{pathM: 50, eventID: "boundary/full-mid/062"},
	})
	if err != nil || len(simultaneous) != 1 || simultaneous[0].pathM != 50 ||
		simultaneous[0].eventID != "boundary/full-mid/062|boundary/pbl" {
		t.Fatalf("simultaneous distinct-event section = %+v, %v", simultaneous, err)
	}

	_, err = compactDomePhysicalRootCandidates([]domeAstrodomePhysicalRootCandidate{
		{pathM: 50, eventID: "boundary/cloud-low-top"},
		{pathM: 50 + 0.5*domeAstrodomePhysicalMergeToleranceM, eventID: "boundary/pbl"},
	})
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("distinct physical events inside root cluster error = %v", err)
	}

	_, err = compactDomePhysicalRootCandidates([]domeAstrodomePhysicalRootCandidate{
		{pathM: 50, eventID: "boundary/cloud-low-top"},
		{pathM: 50 + 0.5*domeAstrodomePhysicalMergeToleranceM, eventID: "boundary/cloud-low-top"},
	})
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("non-identical same-event roots inside root cluster error = %v", err)
	}

	for _, productionGapM := range []float64{0.0015311, 0.0019401} {
		distinct, compactErr := compactDomePhysicalRootCandidates([]domeAstrodomePhysicalRootCandidate{
			{pathM: 50, eventID: "boundary/pbl"},
			{pathM: 50 + productionGapM, eventID: "boundary/full-mid/062"},
		})
		if compactErr != nil || len(distinct) != 2 ||
			math.Abs((distinct[1].pathM-distinct[0].pathM)-productionGapM) > 1e-14 {
			t.Fatalf("production distinct-event gap %.9g m compacted as %+v, %v", productionGapM, distinct, compactErr)
		}
	}
}

func TestCompactDomeHorizontalRootCandidatesRequiresCertifiedCorner(t *testing.T) {
	t.Parallel()

	corner, err := compactDomeHorizontalRootCandidates([]domeAstrodomeHorizontalRootCandidate{
		{pathM: 50, eventID: "latitude/10", exact: true},
		{pathM: 50, eventID: "longitude/20", exact: true},
	})
	if err != nil || len(corner) != 1 || corner[0].eventID != "latitude/10|longitude/20" {
		t.Fatalf("certified exact corner = %+v, %v", corner, err)
	}

	_, err = compactDomeHorizontalRootCandidates([]domeAstrodomeHorizontalRootCandidate{
		{pathM: 50, eventID: "latitude/10", exact: false},
		{pathM: 50 + 0.5*domeAstrodomeHorizontalMergeToleranceM, eventID: "longitude/20", exact: false},
	})
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("uncertified near-corner error = %v", err)
	}

	for name, candidates := range map[string][]domeAstrodomeHorizontalRootCandidate{
		"same latitude axis": {
			{pathM: 50, eventID: "latitude/10", exact: true},
			{pathM: 50, eventID: "latitude/11", exact: true},
		},
		"arbitrary identities": {
			{pathM: 50, eventID: "grid/a", exact: true},
			{pathM: 50, eventID: "grid/b", exact: true},
		},
		"third axis event": {
			{pathM: 50, eventID: "latitude/10|longitude/20", exact: true},
			{pathM: 50, eventID: "latitude/11", exact: true},
		},
	} {
		_, err = compactDomeHorizontalRootCandidates(candidates)
		if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
			t.Fatalf("%s compounding error = %v", name, err)
		}
	}

	_, err = compactDomeHorizontalRootCandidates([]domeAstrodomeHorizontalRootCandidate{
		{pathM: 50, leftM: 49.992, rightM: 50.008, eventID: "latitude/10"},
		{pathM: 50.006, leftM: 49.998, rightM: 50.014, eventID: "longitude/20"},
	})
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("overlapping non-exact corner error = %v", err)
	}

	_, err = compactDomeHorizontalRootCandidates([]domeAstrodomeHorizontalRootCandidate{
		{pathM: 50, leftM: 49.992, rightM: 50.008, eventID: "latitude/10"},
		{pathM: 50.015, leftM: 50.009, rightM: 50.019, eventID: "longitude/20"},
	})
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("disjoint near-corner evidence error = %v", err)
	}

	compoundWithConstituent, err := compactDomeHorizontalRootCandidates(
		[]domeAstrodomeHorizontalRootCandidate{
			{pathM: 50, eventID: "latitude/10|longitude/20", exact: true},
			{pathM: 50, eventID: "longitude/20", exact: true},
		},
	)
	if err != nil || len(compoundWithConstituent) != 1 ||
		compoundWithConstituent[0].eventID != "latitude/10|longitude/20" {
		t.Fatalf("existing certified corner plus constituent = %+v, %v", compoundWithConstituent, err)
	}
}

func TestDomeHorizontalCandidateRangeIncludesSameSideTouchOrPair(t *testing.T) {
	t.Parallel()

	// Both illustrative endpoint samples are north of grid line 10. A curved
	// dense segment can nevertheless touch it or cross it twice. Candidate
	// completeness therefore comes only from the certified midpoint reach and
	// must not use endpoint signs as a filter.
	leftEndpoint, rightEndpoint := 10.1, 10.1
	if leftEndpoint <= 10 || rightEndpoint <= 10 {
		t.Fatal("test precondition does not have equal nonzero endpoint signs")
	}
	first, last, err := domeAstrodomeCertifiedGridLineCandidateRange(10.25, 0.3, 20)
	if err != nil || first != 10 || last != 10 {
		t.Fatalf("same-side complete candidate range = [%d,%d], %v; want grid line 10", first, last, err)
	}

	_, _, err = domeAstrodomeCertifiedGridLineCandidateRange(0.05, 0.1, 20)
	if err == nil {
		t.Fatal("candidate range leaving the model footprint did not fail closed")
	}
}

func TestDomeAstrodomePhysicalOperandScaleCoversModelTop(t *testing.T) {
	t.Parallel()

	want := math.Nextafter(forecast.AstrodomeICONSphereRadiusM+200_000, math.Inf(1))
	if got := domeAstrodomePhysicalHeightOperandScaleM(); got != want {
		t.Fatalf("physical operand scale = %.12g m; want %.12g m", got, want)
	}
}

func TestDomeAstrodomeScienceRootToleranceFitsMinimumEventPanel(t *testing.T) {
	t.Parallel()

	minimumSafeHalfM := math.Nextafter(
		forecast.AstrodomeScienceMinimumEventIntervalLengthM, math.Inf(1),
	) / 2
	if strings.HasSuffix(forecast.AstrodomeScienceVersion, "-preview") ||
		minimumSafeHalfM <= forecast.AstrodomeScienceCompoundRootSideGuardM ||
		domeAstrodomePhysicalRootToleranceM >= forecast.AstrodomeScienceCompoundRootSideGuardM {
		t.Fatalf("strict root envelope = safe half-width %.12g m, side guard %.12g m, root radius %.12g m",
			minimumSafeHalfM, forecast.AstrodomeScienceCompoundRootSideGuardM,
			domeAstrodomePhysicalRootToleranceM)
	}
}

func TestDomeAstrodomeCompoundRootGuardCoversWorstRetainedDisplacement(t *testing.T) {
	t.Parallel()

	if domeAstrodomeHorizontalRootToleranceM >= forecast.AstrodomeScienceCompoundRootSideGuardM ||
		domeAstrodomePhysicalRootToleranceM >= forecast.AstrodomeScienceCompoundRootSideGuardM ||
		domeAstrodomeHorizontalMergeToleranceM >= forecast.AstrodomeScienceCompoundRootSideGuardM ||
		domeAstrodomePhysicalMergeToleranceM >= forecast.AstrodomeScienceCompoundRootSideGuardM {
		t.Fatalf("root/cluster contract does not fit the side guard: horizontal root %.12g m, physical root %.12g m, horizontal cluster %.12g m, physical cluster %.12g m, guard %.12g m",
			domeAstrodomeHorizontalRootToleranceM, domeAstrodomePhysicalRootToleranceM,
			domeAstrodomeHorizontalMergeToleranceM, domeAstrodomePhysicalMergeToleranceM,
			forecast.AstrodomeScienceCompoundRootSideGuardM)
	}
	minimumSafeHalfM := math.Nextafter(
		forecast.AstrodomeScienceMinimumEventIntervalLengthM, math.Inf(1),
	) / 2
	if minimumSafeHalfM <= forecast.AstrodomeScienceCompoundRootSideGuardM {
		t.Fatalf("first accepted constrained panel half-width %.12g m does not clear side guard %.12g m",
			minimumSafeHalfM, forecast.AstrodomeScienceCompoundRootSideGuardM)
	}
}

func TestDomeAstrodomePhysicalBreakpointsAcceptProductionShortEventIntervals(t *testing.T) {
	t.Parallel()

	for _, productionGapM := range []float64{
		0.0015311,
		0.0019401,
		0.0037474,
		0.0039203,
		0.004802253,
		0.0159325455,
		0.0267341866,
		0.0311486803,
		0.0415455627,
		0.0487829312,
		0.0853891544,
	} {
		if productionGapM <= forecast.AstrodomeScienceMinimumEventIntervalLengthM {
			t.Fatalf("production gap %.12g m is below the certified short-rule floor", productionGapM)
		}
		interval := domeAstrodomeCellInterval{
			startM: 100,
			endM:   102,
			cellID: "cell-lat0377-lon0973",
		}
		if err := domeValidatePhysicalBreakpoints(interval, []domeAstrodomePhysicalRootCandidate{
			{pathM: 100.6, eventID: "boundary/full-mid/062"},
			{pathM: 100.6 + productionGapM, eventID: "boundary/pbl"},
		}); err != nil {
			t.Fatalf("production physical-root gap %.12g m: %v", productionGapM, err)
		}
	}

	const horizontalEndpointGapM = 0.0311486803
	interval := domeAstrodomeCellInterval{startM: 100, endM: 102, cellID: "cell-lat0391-lon0969"}
	if err := domeValidatePhysicalBreakpoints(interval, []domeAstrodomePhysicalRootCandidate{
		{pathM: interval.startM + horizontalEndpointGapM, eventID: "boundary/full-mid/009"},
	}); err != nil {
		t.Fatalf("horizontal/physical production gap %.12g m: %v", horizontalEndpointGapM, err)
	}
}

func TestDomeAstrodomeMillimetreEndpointGuardFindsNearbyPhysicalRoots(t *testing.T) {
	t.Parallel()

	interval := domeAstrodomeCellInterval{startM: 100, endM: 101, cellID: "production-endpoint-sliver"}
	probes, err := domeAstrodomePhysicalProbePaths(interval)
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []float64{
		interval.startM + 0.0025,
		interval.endM - 0.0025,
		interval.startM + 0.0159325455,
		interval.endM - 0.0159325455,
	} {
		value := func(pathM float64) (float64, error) { return pathM - root, nil }
		roots, isolateErr := domeAstrodomeIsolateFieldAcrossProbes(
			interval, probes, 1, 128, value,
		)
		if isolateErr != nil {
			t.Fatalf("near-endpoint root %.12g: %v", root, isolateErr)
		}
		if len(roots) != 1 || math.Abs(roots[0]-root) > domeAstrodomePhysicalRootToleranceM {
			t.Fatalf("near-endpoint root %.12g isolated as %v", root, roots)
		}
	}
}

func TestDomeHorizontalBreakpointsFindExactDOPRIEndpointCrossing(t *testing.T) {
	t.Parallel()

	ray := traceDomeAstrodomeGridTestRay(t, forecast.Location{Latitude: 50, Longitude: 30, TimeZone: "UTC"}, 0)
	intervals, err := ray.IntegrationIntervals()
	if err != nil || len(intervals) < 3 {
		t.Fatalf("test ray intervals = %d, %v", len(intervals), err)
	}
	leftIndex := len(intervals)/2 - 1
	boundaryM := intervals[leftIndex].EndPathM
	boundaryPoint, err := ray.PointAtPathLength(boundaryM)
	if err != nil {
		t.Fatal(err)
	}
	grid := Coverage()
	grid.MinLat = boundaryPoint.Location.Latitude - 10*grid.Increment
	grid.MaxLat = grid.MinLat + 20*grid.Increment
	grid.MinLon = boundaryPoint.Location.Longitude - 10.5*grid.Increment
	grid.MaxLon = grid.MinLon + 20*grid.Increment
	volume := newDomeAstrodomeGridTestVolume(grid)

	var candidates []domeAstrodomeHorizontalRootCandidate
	for _, interval := range intervals[leftIndex : leftIndex+2] {
		part, partErr := volume.domeHorizontalBreakpointCandidates(context.Background(), ray, interval.StartPathM, interval.EndPathM)
		if partErr != nil {
			t.Fatal(partErr)
		}
		candidates = append(candidates, part...)
	}
	candidates, err = compactDomeHorizontalRootCandidates(candidates)
	if err != nil {
		t.Fatal(err)
	}
	roots := domeHorizontalRootCandidatePaths(candidates)
	if len(roots) != 1 || math.Abs(roots[0]-boundaryM) > domeAstrodomeHorizontalRootToleranceM {
		t.Fatalf("exact internal DOPRI endpoint roots = %v; want %.12g", roots, boundaryM)
	}
}

func TestDomeHorizontalBreakpointsDoNotSkipSubMillimetreDenseInterval(t *testing.T) {
	t.Parallel()

	ray := traceDomeAstrodomeGridTestRay(t, forecast.Location{Latitude: 50, Longitude: 30, TimeZone: "UTC"}, 0)
	intervals, err := ray.IntegrationIntervals()
	if err != nil || len(intervals) < 3 {
		t.Fatalf("test ray intervals = %d, %v", len(intervals), err)
	}
	boundaryM := intervals[len(intervals)/2-1].EndPathM
	boundaryPoint, err := ray.PointAtPathLength(boundaryM)
	if err != nil {
		t.Fatal(err)
	}
	grid := Coverage()
	grid.MinLat = boundaryPoint.Location.Latitude - 10*grid.Increment
	grid.MaxLat = grid.MinLat + 20*grid.Increment
	grid.MinLon = boundaryPoint.Location.Longitude - 10.5*grid.Increment
	grid.MaxLon = grid.MinLon + 20*grid.Increment
	volume := newDomeAstrodomeGridTestVolume(grid)

	const halfWidthM = 0.00005
	if 2*halfWidthM >= domeAstrodomeHorizontalRootToleranceM {
		t.Fatal("test interval is not shorter than the root-localization radius")
	}
	candidates, err := volume.domeHorizontalBreakpointCandidates(
		context.Background(), ray, boundaryM-halfWidthM, boundaryM+halfWidthM,
	)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err = compactDomeHorizontalRootCandidates(candidates)
	if err != nil {
		t.Fatal(err)
	}
	roots := domeHorizontalRootCandidatePaths(candidates)
	if len(roots) != 1 || math.Abs(roots[0]-boundaryM) > domeAstrodomeHorizontalRootToleranceM {
		t.Fatalf("sub-millimetre dense interval roots = %v; want %.12g", roots, boundaryM)
	}
}

func TestDomeHorizontalBreakpointsRejectsUncertifiedNominalLatLonCorner(t *testing.T) {
	t.Parallel()

	ray := traceDomeAstrodomeGridTestRay(t, forecast.Location{Latitude: 50, Longitude: 30, TimeZone: "UTC"}, 45)
	intervals, err := ray.IntegrationIntervals()
	if err != nil || len(intervals) < 3 {
		t.Fatalf("test ray intervals = %d, %v", len(intervals), err)
	}
	index := len(intervals)/2 - 1
	cornerM := intervals[index].EndPathM
	corner, err := ray.PointAtPathLength(cornerM)
	if err != nil {
		t.Fatal(err)
	}
	grid := Coverage()
	grid.MinLat = corner.Location.Latitude - 10*grid.Increment
	grid.MaxLat = grid.MinLat + 20*grid.Increment
	grid.MinLon = corner.Location.Longitude - 10*grid.Increment
	grid.MaxLon = grid.MinLon + 20*grid.Increment
	volume := newDomeAstrodomeGridTestVolume(grid)

	var candidates []domeAstrodomeHorizontalRootCandidate
	for _, interval := range intervals[index : index+2] {
		part, partErr := volume.domeHorizontalBreakpointCandidates(context.Background(), ray, interval.StartPathM, interval.EndPathM)
		if partErr != nil {
			t.Fatal(partErr)
		}
		candidates = append(candidates, part...)
	}
	candidates, err = compactDomeHorizontalRootCandidates(candidates)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("nominal latitude/longitude corner error = %v; candidates = %+v", err, candidates)
	}
}

func TestDomeHorizontalBreakpointsRejectsUncertifiedSubMergeCornerCluster(t *testing.T) {
	t.Parallel()

	ray := traceDomeAstrodomeGridTestRay(t, forecast.Location{Latitude: 50, Longitude: 30, TimeZone: "UTC"}, 45)
	intervals, err := ray.IntegrationIntervals()
	if err != nil || len(intervals) < 3 {
		t.Fatalf("test ray intervals = %d, %v", len(intervals), err)
	}
	centreM := intervals[len(intervals)/2-1].EndPathM
	const separationM = 0.75 * domeAstrodomeHorizontalMergeToleranceM
	latitudePoint, err := ray.PointAtPathLength(centreM)
	if err != nil {
		t.Fatal(err)
	}
	longitudePoint, err := ray.PointAtPathLength(centreM + separationM)
	if err != nil {
		t.Fatal(err)
	}
	grid := Coverage()
	grid.MinLat = latitudePoint.Location.Latitude - 10*grid.Increment
	grid.MaxLat = grid.MinLat + 20*grid.Increment
	grid.MinLon = longitudePoint.Location.Longitude - 10*grid.Increment
	grid.MaxLon = grid.MinLon + 20*grid.Increment
	volume := newDomeAstrodomeGridTestVolume(grid)
	_, err = volume.domeHorizontalBreakpoints(context.Background(), ray, centreM-0.1, centreM+0.1)
	if !errors.Is(err, forecast.ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("near-corner error = %v; want fail-closed", err)
	}
}

func TestDomeHorizontalBreakpointsGridLineUsesNorthCellWithoutFalseCrossing(t *testing.T) {
	t.Parallel()

	observer := forecast.Location{Latitude: 0, Longitude: 30, TimeZone: "UTC"}
	ray := traceDomeAstrodomeGridTestRay(t, observer, 90)
	grid := Coverage()
	grid.MinLat = -10 * grid.Increment
	grid.MaxLat = grid.MinLat + 20*grid.Increment
	grid.MinLon = observer.Longitude - 10.5*grid.Increment
	grid.MaxLon = grid.MinLon + 20*grid.Increment
	volume := newDomeAstrodomeGridTestVolume(grid)
	breaks, err := volume.domeHorizontalBreakpoints(context.Background(), ray, 0, ray.PathLengthM)
	if err != nil {
		t.Fatal(err)
	}
	if len(breaks) != 0 {
		t.Fatalf("grid-line-following ray produced false crossings: %v", breaks)
	}
	stencil, err := volume.HorizontalStencil(context.Background(), observer)
	if err != nil {
		t.Fatal(err)
	}
	cellID, err := volume.HorizontalCellID(stencil)
	if err != nil || cellID != "cell-lat0010-lon0010" {
		t.Fatalf("exact grid-line cell ownership = %q, %v", cellID, err)
	}
}

type domeAstrodomeGridTestRefractionField struct {
	surfaceHeightM float64
	topHeightM     float64
}

func (field domeAstrodomeGridTestRefractionField) EvaluateAstrodomeRefraction(
	_ context.Context,
	position forecast.AstrodomeECEFVector,
) (forecast.AstrodomeRefractionFieldSample, error) {
	heightM := position.Norm() - forecast.AstrodomeICONSphereRadiusM
	return forecast.AstrodomeRefractionFieldSample{
		RefractivityVersion:     "grid-test-constant-index-v1",
		RefractiveIndex:         1.00027,
		PartitionID:             "grid-test",
		SignedSurfaceDistanceM:  heightM - field.surfaceHeightM,
		SignedModelTopDistanceM: heightM - field.topHeightM,
	}, nil
}

func traceDomeAstrodomeGridTestRay(
	t *testing.T,
	observer forecast.Location,
	azimuthDegrees float64,
) forecast.AstrodomeRefractedRay {
	t.Helper()
	const observerHeightM = 100.0
	initial, err := forecast.NewAstrodomeRay(observer, observerHeightM, 10, &azimuthDegrees)
	if err != nil {
		t.Fatal(err)
	}
	ray, err := forecast.TraceAstrodomeRefractedRay(context.Background(),
		domeAstrodomeGridTestRefractionField{surfaceHeightM: observerHeightM, topHeightM: 500},
		initial, forecast.DefaultAstrodomeRefractionCalibration())
	if err != nil {
		t.Fatal(err)
	}
	return ray
}

func newDomeAstrodomeGridTestVolume(grid model.Coverage) *DomeVolume {
	return &DomeVolume{
		manifest: LoadedDomeManifest{DomeManifest: DomeManifest{Grid: grid}},
		cache:    make(map[string]domeVolumeCacheEntry), groups: make(map[string][]domeColumnAddress),
		flights: make(map[string]*domeVolumeFlight), cacheLimit: 4,
	}
}
