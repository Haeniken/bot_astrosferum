package forecast

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func TestTerrainSkylineSectorsOwnEveryAzimuthExactlyOnce(t *testing.T) {
	samples := make([]TerrainSkylineSample, TerrainSkylineAzimuthCount)
	for azimuth := range samples {
		samples[azimuth] = TerrainSkylineSample{
			AzimuthDegrees:           float64(azimuth),
			ElevationDegrees:         float64(azimuth) / 100,
			ObstacleSurfaceDistanceM: 1000,
		}
	}
	profile, err := NewSyntheticTerrainSkyline(53.65, 37.3462, 200, samples)
	if err != nil {
		t.Fatal(err)
	}
	owned := make(map[int]HorizonDirection, TerrainSkylineAzimuthCount)
	for _, sector := range profile.HorizonSectors {
		if sector.SampleCount != 45 {
			t.Fatalf("sector %s has %d samples, want 45", sector.Direction, sector.SampleCount)
		}
		for azimuth := 0; azimuth < TerrainSkylineAzimuthCount; azimuth++ {
			index := int(math.Floor(normalizeTerrainAzimuth(float64(azimuth)+TerrainSkylineSectorWidthDeg/2)/TerrainSkylineSectorWidthDeg)) % HorizonDirectionCount
			if fixedHorizonDirections[index].direction != sector.Direction {
				continue
			}
			if previous, duplicate := owned[azimuth]; duplicate {
				t.Fatalf("azimuth %d belongs to both %s and %s", azimuth, previous, sector.Direction)
			}
			owned[azimuth] = sector.Direction
		}
	}
	if len(owned) != TerrainSkylineAzimuthCount {
		t.Fatalf("unique sector ownership covers %d samples, want %d", len(owned), TerrainSkylineAzimuthCount)
	}
	for azimuth, direction := range map[int]HorizonDirection{
		337: HorizonNorthWest, 338: HorizonNorth, 0: HorizonNorth, 22: HorizonNorth, 23: HorizonNorthEast,
	} {
		if owned[azimuth] != direction {
			t.Fatalf("azimuth %d belongs to %s, want %s", azimuth, owned[azimuth], direction)
		}
	}
}

func TestGLO30TileRasterWidthUsesEquatorFacingSouthernBand(t *testing.T) {
	t.Parallel()
	for tileID, want := range map[string]int{
		"Copernicus_DSM_COG_10_N49_00_E000_00_DEM": 3600,
		"Copernicus_DSM_COG_10_N50_00_E000_00_DEM": 2400,
		"Copernicus_DSM_COG_10_S50_00_E000_00_DEM": 3600,
		"Copernicus_DSM_COG_10_S51_00_E000_00_DEM": 2400,
	} {
		got, ok := GLO30TileRasterWidth(tileID)
		if !ok || got != want {
			t.Fatalf("GLO-30 width for %s = %d (%v), want %d", tileID, got, ok, want)
		}
	}
	for _, tileID := range []string{
		"Copernicus_DSM_COG_10_S00_00_E000_00_DEM",
		"Copernicus_DSM_COG_10_N90_00_E000_00_DEM",
		"Copernicus_DSM_COG_10_N53_00_E180_00_DEM",
		"synthetic-test-tile",
	} {
		if _, ok := GLO30TileRasterWidth(tileID); ok {
			t.Fatalf("invalid GLO-30 tile ID %q was accepted", tileID)
		}
	}
}

func TestTerrainSkylineRejectsNonCanonicalSourceSemantics(t *testing.T) {
	samples := make([]TerrainSkylineSample, TerrainSkylineAzimuthCount)
	for azimuth := range samples {
		samples[azimuth] = TerrainSkylineSample{
			AzimuthDegrees: float64(azimuth), ElevationDegrees: 1, ObstacleSurfaceDistanceM: 1000,
		}
	}
	profile, err := NewSyntheticTerrainSkyline(53.65, 37.3462, 200, samples)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*TerrainSkylineInput){
		"nodata": func(input *TerrainSkylineInput) { input.NoDataValue = 0 },
		"scale":  func(input *TerrainSkylineInput) { input.Scale = 0.5 },
		"offset": func(input *TerrainSkylineInput) { input.Offset = 1 },
		"unit":   func(input *TerrainSkylineInput) { input.Unit = "ft" },
		"width":  func(input *TerrainSkylineInput) { input.RasterWidth = 2400 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := profile
			candidate.InputManifest = append([]TerrainSkylineInput(nil), profile.InputManifest...)
			mutate(&candidate.InputManifest[0])
			if err := candidate.Validate(); err == nil {
				t.Fatal("non-canonical GLO-30 source semantics were accepted")
			}
		})
	}
}

func TestPendingTerrainSkylineKeepsItsCoordinateIdentity(t *testing.T) {
	t.Parallel()

	profile := PendingTerrainSkyline(Location{Latitude: 53.65, Longitude: 37.3462})
	if !profile.MatchesLocation(Location{Latitude: 53.65, Longitude: 37.3462}) {
		t.Fatal("pending skyline rejected its own location")
	}
	if profile.MatchesLocation(Location{Latitude: 53.66, Longitude: 37.3462}) {
		t.Fatal("pending skyline accepted a different location")
	}
}

func TestTerrainSkylineCyclicInterpolationAndInformationalHorizonProfile(t *testing.T) {
	samples := make([]TerrainSkylineSample, TerrainSkylineAzimuthCount)
	for azimuth := range samples {
		samples[azimuth] = TerrainSkylineSample{AzimuthDegrees: float64(azimuth), ElevationDegrees: 1, ObstacleSurfaceDistanceM: 1000}
	}
	samples[359].ElevationDegrees = 3
	samples[0].ElevationDegrees = 5
	samples[44].ElevationDegrees = 12
	samples[134].ElevationDegrees = 10
	profile, err := NewSyntheticTerrainSkyline(53.65, 37.3462, 200, samples)
	if err != nil {
		t.Fatal(err)
	}
	interpolated, err := profile.ElevationAt(359.5)
	if err != nil || interpolated != 4 {
		t.Fatalf("cyclic interpolation = %v, %v; want 4", interpolated, err)
	}
	validAt := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	frames := []HorizonFrame{{ValidAt: validAt, Results: make([]HorizonResult, HorizonDirectionCount)}}
	for index, fixed := range fixedHorizonDirections {
		frames[0].Results[index] = HorizonResult{
			ValidAt: validAt, Direction: fixed.direction, AzimuthDegrees: fixed.azimuth,
			GeometricElevationDegrees: 10, Available: true, Index: 7,
			TerrainAssessment: HorizonTerrainModelHHL,
			LimitingFactor:    HorizonFactorNone, LimitingFactors: []HorizonLimitingFactor{HorizonFactorNone},
		}
	}
	before := frames[0].Results[1]
	if err := ApplyTerrainSkylineToHorizon(frames, profile); err != nil {
		t.Fatal(err)
	}
	after := frames[0].Results[1]
	withoutTerrainInformation := after
	withoutTerrainInformation.TerrainSkylineAvailable = before.TerrainSkylineAvailable
	withoutTerrainInformation.TerrainSectorMeanElevationDegrees = before.TerrainSectorMeanElevationDegrees
	withoutTerrainInformation.TerrainSectorMaximumElevationDegrees = before.TerrainSectorMaximumElevationDegrees
	withoutTerrainInformation.TerrainSectorHasObstructionAtEvaluationElevation = before.TerrainSectorHasObstructionAtEvaluationElevation
	withoutTerrainInformation.TerrainAssessment = before.TerrainAssessment
	if !reflect.DeepEqual(withoutTerrainInformation, before) {
		t.Fatalf("GLO-30 attachment changed non-informational Horizon fields:\n before: %+v\n  after: %+v", before, after)
	}
	if !frames[0].Results[1].Available || frames[0].Results[1].TerrainBlocked || frames[0].Results[1].Index != 7 ||
		frames[0].Results[1].TerrainSectorMaximumElevationDegrees != 12 ||
		!frames[0].Results[1].TerrainSectorHasObstructionAtEvaluationElevation ||
		frames[0].Results[1].LimitingFactor != HorizonFactorNone ||
		len(frames[0].Results[1].LimitingFactors) != 1 || frames[0].Results[1].LimitingFactors[0] != HorizonFactorNone {
		t.Fatalf("NE sector did not preserve the atmospheric result with an informational obstruction: %+v", frames[0].Results[1])
	}
	if frames[0].Results[2].TerrainBlocked || frames[0].Results[2].TerrainSectorMeanElevationDegrees != 1 ||
		frames[0].Results[2].TerrainSectorHasObstructionAtEvaluationElevation {
		t.Fatalf("E sector was changed by a sample owned by NE: %+v", frames[0].Results[2])
	}
	if !frames[0].Results[3].TerrainSectorHasObstructionAtEvaluationElevation || frames[0].Results[3].Index != 7 {
		t.Fatalf("a skyline exactly at 10 degrees was not informationally classified as an obstruction: %+v", frames[0].Results[3])
	}
}

func TestTerrainSkylineDoesNotClearModelHHLUnavailability(t *testing.T) {
	samples := make([]TerrainSkylineSample, TerrainSkylineAzimuthCount)
	for azimuth := range samples {
		samples[azimuth] = TerrainSkylineSample{
			AzimuthDegrees: float64(azimuth), ElevationDegrees: 12, ObstacleSurfaceDistanceM: 1000,
		}
	}
	profile, err := NewSyntheticTerrainSkyline(53.65, 37.3462, 200, samples)
	if err != nil {
		t.Fatal(err)
	}
	validAt := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	frames := []HorizonFrame{{ValidAt: validAt, Results: []HorizonResult{{
		ValidAt: validAt, Direction: HorizonNorth, AzimuthDegrees: 0,
		GeometricElevationDegrees: 10, Available: false, Index: 1,
		TerrainBlocked: true, TerrainAssessment: HorizonTerrainModelHHL,
		LimitingFactor:  HorizonFactorTerrain,
		LimitingFactors: []HorizonLimitingFactor{HorizonFactorTerrain, HorizonFactorUnavailable},
	}}}}
	if err := ApplyTerrainSkylineToHorizon(frames, profile); err != nil {
		t.Fatal(err)
	}
	result := frames[0].Results[0]
	if result.Available || !result.TerrainBlocked || result.LimitingFactor != HorizonFactorTerrain ||
		!result.TerrainSectorHasObstructionAtEvaluationElevation {
		t.Fatalf("GLO-30 attachment changed the independent model-HHL unavailable state: %+v", result)
	}
}
