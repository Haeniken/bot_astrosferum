package copdem

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
)

func TestNativeCellSkylineKeepsGDALDiagnosticsOutOfJSON(t *testing.T) {
	if os.Getenv("ASTRO_COPDEM_SCANNER_HELPER") == "1" {
		_, _ = fmt.Fprintln(os.Stderr, "Warning 1: diagnostic emitted by GDAL")
		samples := make([]forecast.TerrainSkylineSample, forecast.TerrainSkylineAzimuthCount)
		for index := range samples {
			samples[index] = forecast.TerrainSkylineSample{
				AzimuthDegrees:           float64(index),
				ElevationDegrees:         0.1,
				ObstacleSurfaceDistanceM: 1000,
			}
		}
		_ = json.NewEncoder(os.Stdout).Encode(samples)
		os.Exit(0)
	}

	directory := t.TempDir()
	helper := filepath.Join(directory, "python3")
	script := "#!/bin/sh\nexec \"$ASTRO_COPDEM_TEST_BINARY\" -test.run '^TestNativeCellSkylineKeepsGDALDiagnosticsOutOfJSON$'\n"
	if err := os.WriteFile(helper, []byte(script), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ASTRO_COPDEM_TEST_BINARY", os.Args[0])
	t.Setenv("ASTRO_COPDEM_SCANNER_HELPER", "1")

	samples, err := nativeCellSkyline(context.Background(), []string{"unused.tif"}, forecast.Location{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != forecast.TerrainSkylineAzimuthCount || samples[359].AzimuthDegrees != 359 {
		t.Fatalf("scanner returned %d samples, final azimuth %.1f", len(samples), samples[len(samples)-1].AzimuthDegrees)
	}
}

func TestDirectSphericalElevationMatchesAnalyticFlatEarthCurvature(t *testing.T) {
	const distance = 1000.0
	got := directSphericalElevation(200, 200, distance)
	angle := distance / forecast.HorizonEarthRadiusM
	want := math.Atan2((forecast.HorizonEarthRadiusM+200)*(math.Cos(angle)-1), (forecast.HorizonEarthRadiusM+200)*math.Sin(angle)) * 180 / math.Pi
	// Both expressions are algebraically equal; this tolerance covers the
	// cancellation in cos(angle)-1 used only by this independent test oracle.
	if math.Abs(got-want) > 3e-11 {
		t.Fatalf("flat equal-height terrain angle = %.15g°, want %.15g°", got, want)
	}
	if got >= 0 {
		t.Fatalf("Earth curvature must put equal-height terrain below the local horizontal, got %.12g°", got)
	}
}

func TestTwoMetreApertureLowersTheTerrainAngle(t *testing.T) {
	t.Parallel()

	withoutAperture := directSphericalElevation(200, 200, 60)
	withAperture := directSphericalElevation(200+forecast.TerrainSkylineApertureHeightAGLM, 200, 60)
	if !(withAperture < withoutAperture) {
		t.Fatalf("2 m aperture angle = %.12g°, surface angle = %.12g°", withAperture, withoutAperture)
	}
}

func TestDestinationPreservesRequestedGreatCircleDistance(t *testing.T) {
	want := forecast.TerrainSkylineMaximumDistanceM
	latitude, longitude := destination(53.65, 37.3462, 315, want)
	lat1, lon1 := 53.65*math.Pi/180, 37.3462*math.Pi/180
	lat2, lon2 := latitude*math.Pi/180, longitude*math.Pi/180
	a := math.Pow(math.Sin((lat2-lat1)/2), 2) + math.Cos(lat1)*math.Cos(lat2)*math.Pow(math.Sin((lon2-lon1)/2), 2)
	got := 2 * forecast.HorizonEarthRadiusM * math.Asin(math.Sqrt(a))
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("great-circle destination distance = %.9f m, want %.9f m", got, want)
	}
}

func TestTerrainTileNamesCoverEveryBoundaryTileWithoutDuplicates(t *testing.T) {
	t.Parallel()

	location := forecast.Location{Latitude: 53.65, Longitude: 37.3462}
	names := terrainTileNames(location)
	known := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, duplicate := known[name]; duplicate {
			t.Fatalf("terrain tile %s is duplicated", name)
		}
		known[name] = struct{}{}
	}
	for azimuth := 0; azimuth < 3600; azimuth++ {
		latitude, longitude := destination(location.Latitude, location.Longitude, float64(azimuth)/10, forecast.TerrainSkylineMaximumDistanceM)
		name := tileName(latitude, longitude)
		if _, found := known[name]; !found {
			t.Fatalf("boundary azimuth %.1f° reaches missing tile %s", float64(azimuth)/10, name)
		}
	}
}

func TestMaximumDistanceExcludesAnyTenDegreeObstacleBeyondProfile(t *testing.T) {
	t.Parallel()

	// The validation contract accepts DSM surfaces only in [-500, 10500] m.
	// The worst angular obstruction therefore combines the lowest observer
	// aperture with the highest accepted source cell.
	got := directSphericalElevation(
		-500+forecast.TerrainSkylineApertureHeightAGLM,
		forecast.TerrainSkylineAbsoluteHeightMaxM,
		forecast.TerrainSkylineMaximumDistanceM,
	)
	if got >= forecast.HorizonGeometricElevationDegrees {
		t.Fatalf("worst accepted obstacle at the profile boundary = %.12g°, want < %.12g°", got, forecast.HorizonGeometricElevationDegrees)
	}
}

func TestTileCachePrunesOldestAndRetainsProtectedTile(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(Config{Root: root, CacheLimitBytes: 20})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	paths := make([]string, 3)
	for index := range paths {
		paths[index] = filepath.Join(root, "tiles", string(rune('a'+index)), "tile.tif")
		if err := os.MkdirAll(filepath.Dir(paths[index]), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(paths[index], make([]byte, 10), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(paths[index]+".manifest.json", []byte("manifest"), 0o640); err != nil {
			t.Fatal(err)
		}
		instant := base.Add(time.Duration(index) * time.Hour)
		if err := os.Chtimes(paths[index], instant, instant); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.pruneTiles(map[string]struct{}{paths[0]: {}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatalf("protected tile was removed: %v", err)
	}
	if _, err := os.Stat(paths[1]); !os.IsNotExist(err) {
		t.Fatalf("oldest unprotected tile still exists: %v", err)
	}
	if _, err := os.Stat(paths[1] + ".manifest.json"); !os.IsNotExist(err) {
		t.Fatalf("pruned tile manifest still exists: %v", err)
	}
	if _, err := os.Stat(paths[2]); err != nil {
		t.Fatalf("newest tile was removed: %v", err)
	}
}

func TestCopernicusTileNameAcrossNegativeCoordinates(t *testing.T) {
	if got, want := tileName(-0.1, -0.1), "Copernicus_DSM_COG_10_S01_00_W001_00_DEM"; got != want {
		t.Fatalf("tile name = %s, want %s", got, want)
	}
}

func TestGLO30RasterWidthLatitudeBands(t *testing.T) {
	t.Parallel()

	for tileID, want := range map[string]int{
		"Copernicus_DSM_COG_10_N49_00_E000_00_DEM": 3600,
		"Copernicus_DSM_COG_10_N50_00_E000_00_DEM": 2400,
		"Copernicus_DSM_COG_10_N85_00_E000_00_DEM": 360,
		"Copernicus_DSM_COG_10_S50_00_E000_00_DEM": 3600,
		"Copernicus_DSM_COG_10_S51_00_E000_00_DEM": 2400,
		"Copernicus_DSM_COG_10_S86_00_E000_00_DEM": 360,
	} {
		if got, ok := forecast.GLO30TileRasterWidth(tileID); !ok || got != want {
			t.Fatalf("tile width for %s = %d (%v), want %d", tileID, got, ok, want)
		}
	}
}
