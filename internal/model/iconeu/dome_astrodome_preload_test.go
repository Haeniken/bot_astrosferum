package iconeu

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"bot_astrosferum/internal/forecast"
)

type domeAstrodomeScalarTestRunner struct {
	name   string
	args   []string
	output []byte
	err    error
}

func (runner *domeAstrodomeScalarTestRunner) CombinedOutput(
	ctx context.Context,
	name string,
	args ...string,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runner.name = name
	runner.args = append([]string(nil), args...)
	return runner.output, runner.err
}

func TestParseDomeAstrodomeScalarRequiresExactlyOneFiniteValue(t *testing.T) {
	t.Parallel()

	value, err := parseDomeAstrodomeScalar([]byte("#      value\n  30214.1250000\n"))
	if err != nil || value != 30214.125 {
		t.Fatalf("parse scalar = %.12g, %v", value, err)
	}
	for _, output := range [][]byte{
		[]byte(""),
		[]byte("NaN\n"),
		[]byte("30000 30001\n"),
		[]byte("warning 30000\n"),
	} {
		if _, err := parseDomeAstrodomeScalar(output); err == nil {
			t.Fatalf("invalid fldmax output %q was accepted", output)
		}
	}
}

func TestDomeAstrodomeMaximumTopHeightUsesExactNativeHHL1Reduction(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	geometryPath := filepath.Join(root, "geometry.grib2")
	contents := []byte("immutable geometry fixture")
	if err := os.WriteFile(geometryPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &domeAstrodomeScalarTestRunner{output: []byte("# value\n30214.125\n")}
	volume := &DomeVolume{
		providerRoot: root,
		manifest: LoadedDomeManifest{DomeManifest: DomeManifest{Geometry: DomeStepFile{
			File: "geometry.grib2", Bytes: int64(len(contents)), Messages: domeHalfLevelCount,
		}}},
		runner: runner,
	}
	height, err := volume.domeAstrodomeMaximumTopHeight(context.Background())
	if err != nil || height != 30214.125 {
		t.Fatalf("maximum HHL1 = %.12g, %v", height, err)
	}
	if runner.name != "cdo" || !slices.Contains(runner.args, "-fldmax") ||
		!slices.Contains(runner.args, "-sellevel,1") || !slices.Contains(runner.args, "-selname,HHL") ||
		!slices.Contains(runner.args, geometryPath) {
		t.Fatalf("HHL1 reduction command = %q %q", runner.name, runner.args)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := volume.domeAstrodomeMaximumTopHeight(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled HHL1 reduction error = %v", err)
	}
}

func TestDomeAstrodomeCanonicalEnvelopePreservesEveryNodeElevation(t *testing.T) {
	t.Parallel()

	profile, err := forecast.NewAstrodomeGridProfile(forecast.AstrodomeGridDenseV1)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := profile.Nodes()
	if err != nil {
		t.Fatal(err)
	}
	observer := forecast.Location{Latitude: 55, Longitude: 30, TimeZone: "UTC"}
	rays, maximumPathM, err := domeAstrodomeCanonicalEnvelopeRays(observer, nodes, 30_000, 500_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(rays) != len(nodes) || maximumPathM <= 0 || maximumPathM >= 500_000 {
		t.Fatalf("canonical envelope = %d rays, max %.3f m", len(rays), maximumPathM)
	}
	zeniths := 0
	for index, ray := range rays {
		node := nodes[index]
		if ray.elevationDegrees != node.ElevationDegrees || ray.pathLimitM <= 0 {
			t.Fatalf("envelope ray %d = %+v, node = %+v", index, ray, node)
		}
		if node.AzimuthDegrees == nil {
			zeniths++
			if ray.azimuthDegrees != nil || ray.elevationDegrees != forecast.AstrodomeZenithElevationDegrees {
				t.Fatalf("zenith envelope = %+v", ray)
			}
			continue
		}
		if ray.azimuthDegrees == nil || *ray.azimuthDegrees != *node.AzimuthDegrees {
			t.Fatalf("envelope ray %d changed azimuth", index)
		}
	}
	if zeniths != 1 {
		t.Fatalf("canonical envelope contains %d zenith rays, want 1", zeniths)
	}
}

func TestDomeAstrodomeCorridorUsesActualElevationAndCoversItsGridSupports(t *testing.T) {
	t.Parallel()

	volume := &DomeVolume{manifest: LoadedDomeManifest{DomeManifest: DomeManifest{Grid: Coverage()}}}
	observer := forecast.Location{Latitude: 55, Longitude: 30, TimeZone: "UTC"}
	azimuth := 90.0
	highRay := domeAstrodomeEnvelopeRay{
		elevationDegrees: 80,
		azimuthDegrees:   &azimuth,
		pathLimitM:       50_000,
	}
	high, err := volume.domeAstrodomeCorridorAddresses(context.Background(), observer,
		[]domeAstrodomeEnvelopeRay{highRay}, false)
	if err != nil {
		t.Fatal(err)
	}
	low, err := volume.domeAstrodomeCorridorAddresses(context.Background(), observer,
		[]domeAstrodomeEnvelopeRay{{elevationDegrees: 10, azimuthDegrees: &azimuth, pathLimitM: 50_000}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(high) >= len(low) {
		t.Fatalf("80-degree corridor has %d columns, 10-degree corridor has %d; high-only azimuth was overextended",
			len(high), len(low))
	}
	highIDs := make(map[string]struct{}, len(high))
	for _, address := range high {
		highIDs[address.id] = struct{}{}
	}
	geometry, err := forecast.NewAstrodomeRay(observer, 0, highRay.elevationDegrees, highRay.azimuthDegrees)
	if err != nil {
		t.Fatal(err)
	}
	grid := volume.manifest.Grid
	latitudeMaximum := int(math.Round((grid.MaxLat - grid.MinLat) / grid.Increment))
	longitudeMaximum := int(math.Round((grid.MaxLon - grid.MinLon) / grid.Increment))
	for pathM := 0.0; pathM <= highRay.pathLimitM; pathM += DomeAstrodomeEnvelopeSampleM {
		point, pointErr := geometry.PointAtPathLength(pathM)
		if pointErr != nil {
			t.Fatal(pointErr)
		}
		cell, cellErr := domeGridCell(grid, point.Location)
		if cellErr != nil {
			t.Fatal(cellErr)
		}
		for latitudeIndex := max(0, cell.south-DomeAstrodomeRefractionGridMargin); latitudeIndex <= min(latitudeMaximum, cell.north+DomeAstrodomeRefractionGridMargin); latitudeIndex++ {
			for longitudeIndex := max(0, cell.west-DomeAstrodomeRefractionGridMargin); longitudeIndex <= min(longitudeMaximum, cell.east+DomeAstrodomeRefractionGridMargin); longitudeIndex++ {
				id := volume.domeColumnAddress(latitudeIndex, longitudeIndex).id
				if _, ok := highIDs[id]; !ok {
					t.Fatalf("actual 80-degree ray support %s at path %.0f m was not preloaded", id, pathM)
				}
			}
		}
	}

	lowGeometry, err := forecast.NewAstrodomeRay(observer, 0, 10, &azimuth)
	if err != nil {
		t.Fatal(err)
	}
	farPoint, err := lowGeometry.PointAtPathLength(50_000)
	if err != nil {
		t.Fatal(err)
	}
	farCell, err := domeGridCell(grid, farPoint.Location)
	if err != nil {
		t.Fatal(err)
	}
	farID := volume.domeColumnAddress(farCell.south, farCell.west).id
	if _, present := highIDs[farID]; present {
		t.Fatalf("80-degree corridor contains remote support %s needed only by the artificial 10-degree extension", farID)
	}
}

func TestDomeAstrodomeCorridorRejectsDomainEscapeUnlessExplicitlyClippingProbe(t *testing.T) {
	t.Parallel()

	volume := &DomeVolume{manifest: LoadedDomeManifest{DomeManifest: DomeManifest{Grid: Coverage()}}}
	grid := volume.manifest.Grid
	observer := forecast.Location{Latitude: 55, Longitude: grid.MaxLon - 0.001, TimeZone: "UTC"}
	azimuth := 90.0
	ray := domeAstrodomeEnvelopeRay{elevationDegrees: 10, azimuthDegrees: &azimuth, pathLimitM: 20_000}
	if _, err := volume.domeAstrodomeCorridorAddresses(context.Background(), observer,
		[]domeAstrodomeEnvelopeRay{ray}, false); err == nil || !strings.Contains(err.Error(), "exits the provider domain") {
		t.Fatalf("strict domain escape error = %v", err)
	}
	addresses, err := volume.domeAstrodomeCorridorAddresses(context.Background(), observer,
		[]domeAstrodomeEnvelopeRay{ray}, true)
	if err != nil || len(addresses) == 0 {
		t.Fatalf("explicitly clipped probe = %d columns, %v", len(addresses), err)
	}
}

func TestDomeAstrodomeSourceColumnPlanDigestBindsExactNativeIndices(t *testing.T) {
	t.Parallel()

	grid := Coverage()
	volume := &DomeVolume{manifest: LoadedDomeManifest{DomeManifest: DomeManifest{Grid: grid}}}
	addresses := []domeColumnAddress{
		volume.domeColumnAddress(12, 34),
		volume.domeColumnAddress(11, 33),
	}
	digest, err := domeAstrodomeSourceColumnPlanDigest(addresses, grid)
	if err != nil {
		t.Fatal(err)
	}
	reordered := []domeColumnAddress{addresses[1], addresses[0]}
	reorderedDigest, err := domeAstrodomeSourceColumnPlanDigest(reordered, grid)
	if err != nil || reorderedDigest != digest || !strings.HasPrefix(digest, "sha256:") || len(digest) != 71 {
		t.Fatalf("canonical source-plan digests = %q/%q, err=%v", digest, reorderedDigest, err)
	}
	mutated := append([]domeColumnAddress(nil), addresses...)
	mutated[0].location.Latitude = math.Nextafter(mutated[0].location.Latitude, math.Inf(1))
	if _, err := domeAstrodomeSourceColumnPlanDigest(mutated, grid); err == nil {
		t.Fatal("non-native source coordinate was accepted")
	}
}
