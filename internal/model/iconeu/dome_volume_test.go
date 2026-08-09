package iconeu

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
)

func TestDomeVolumeIdentityAndNativeFieldAxes(t *testing.T) {
	root := t.TempDir()
	loaded := writeDomeVolumePublication(t, root)
	volume, err := newDomeVolume(root, filepath.Join(root, "tmp"), loaded, &domeVolumeTestRunner{}, 8)
	if err != nil {
		t.Fatal(err)
	}
	identity := volume.Identity()
	if identity.RunManifestDigest != "sha256:"+loaded.ManifestSHA256 || identity.RunID != loaded.RunID ||
		identity.InputContractVersion != forecast.AstrodomePrimitiveInputContractVersion {
		t.Fatalf("volume identity = %+v", identity)
	}
	modelTimes := volume.NativeValidTimes(forecast.AstrodomePrimitiveSpecificHumidity)
	if len(modelTimes) != 81 || !modelTimes[0].Equal(loaded.BaseTime) ||
		!modelTimes[len(modelTimes)-1].Equal(loaded.BaseTime.Add(84*time.Hour)) {
		t.Fatalf("model native axis = %v ... %v (%d)", modelTimes[0], modelTimes[len(modelTimes)-1], len(modelTimes))
	}
	if gap := modelTimes[79].Sub(modelTimes[78]); gap != 3*time.Hour {
		t.Fatalf("f078/f081 native gap = %s", gap)
	}
	precipTimes := volume.NativeValidTimes(forecast.AstrodomePrimitivePrecipitationAccumulation)
	if len(precipTimes) != 80 || !precipTimes[0].Equal(loaded.BaseTime.Add(time.Hour)) {
		t.Fatalf("precipitation native axis starts with %+v", precipTimes)
	}
	if gustTimes := volume.NativeValidTimes(forecast.AstrodomePrimitiveWindGust10M); len(gustTimes) != 80 ||
		!gustTimes[0].Equal(loaded.BaseTime.Add(time.Hour)) {
		t.Fatalf("maximum-gust native axis starts with %+v", gustTimes)
	}
	modelTimes[0] = time.Time{}
	if volume.NativeValidTimes(forecast.AstrodomePrimitiveSpecificHumidity)[0].IsZero() {
		t.Fatal("caller mutated the volume's native-time axis")
	}
	if got := volume.NativeValidTimes(forecast.AstrodomePrimitiveField(1 << 63)); got != nil {
		t.Fatalf("unknown primitive has a native axis: %+v", got)
	}
}

func TestDomeVolumeRejectsChangedManifestBindings(t *testing.T) {
	t.Run("dome manifest", func(t *testing.T) {
		root := t.TempDir()
		loaded := writeDomeVolumePublication(t, root)
		file, err := os.OpenFile(filepath.Join(loaded.Directory, "manifest.json"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("\n"); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		if _, err := newDomeVolume(root, "", loaded, &domeVolumeTestRunner{}, 8); err == nil ||
			!strings.Contains(err.Error(), "changed") {
			t.Fatalf("changed dome manifest error = %v", err)
		}
	})
	t.Run("base manifest", func(t *testing.T) {
		root := t.TempDir()
		loaded := writeDomeVolumePublication(t, root)
		basePath := filepath.Join(root, "models", "icon-eu", "runs", loaded.RunID, "manifest.json")
		file, err := os.OpenFile(basePath, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("\n"); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		if _, err := newDomeVolume(root, "", loaded, &domeVolumeTestRunner{}, 8); err == nil ||
			!strings.Contains(err.Error(), "base manifest changed") {
			t.Fatalf("changed base manifest error = %v", err)
		}
	})
}

func TestDomeVolumeExactFourColumnStencilIncludingDomainEdges(t *testing.T) {
	root := t.TempDir()
	volume, err := newDomeVolume(root, "", writeDomeVolumePublication(t, root), &domeVolumeTestRunner{}, 8)
	if err != nil {
		t.Fatal(err)
	}
	grid := Coverage()
	location := forecast.Location{
		Latitude: grid.MinLat + 10.25*grid.Increment, Longitude: grid.MinLon + 20.75*grid.Increment,
	}
	stencil, err := volume.HorizontalStencil(context.Background(), location)
	if err != nil {
		t.Fatal(err)
	}
	wantWeights := [4]float64{0.1875, 0.5625, 0.0625, 0.1875}
	wantIDs := [4]string{"lat0010-lon0020", "lat0010-lon0021", "lat0011-lon0020", "lat0011-lon0021"}
	for index, support := range stencil.Supports {
		if support.ColumnID != wantIDs[index] || math.Abs(support.Weight-wantWeights[index]) > 1e-14 {
			t.Fatalf("support %d = %+v", index, support)
		}
	}
	if cellID, err := volume.HorizontalCellID(stencil); err != nil || cellID != "cell-lat0010-lon0020" {
		t.Fatalf("cell identity = %q, %v", cellID, err)
	}

	for _, test := range []struct {
		name     string
		location forecast.Location
		weights  [4]float64
	}{
		{name: "minimum", location: forecast.Location{Latitude: grid.MinLat, Longitude: grid.MinLon}, weights: [4]float64{1, 0, 0, 0}},
		{name: "maximum", location: forecast.Location{Latitude: grid.MaxLat, Longitude: grid.MaxLon}, weights: [4]float64{0, 0, 0, 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := volume.HorizontalStencil(context.Background(), test.location)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			for index, support := range got.Supports {
				seen[support.ColumnID] = true
				if support.Weight != test.weights[index] {
					t.Fatalf("edge support %d weight = %.17g", index, support.Weight)
				}
			}
			if len(seen) != 4 {
				t.Fatalf("edge stencil has %d unique columns", len(seen))
			}
		})
	}
	if _, err := volume.HorizontalStencil(context.Background(), forecast.Location{
		Latitude: grid.MinLat - grid.Increment, Longitude: grid.MinLon,
	}); err == nil {
		t.Fatal("out-of-domain stencil was accepted")
	}
}

func TestDomeVolumeExtractsRawCompleteColumnsInBatches(t *testing.T) {
	root := t.TempDir()
	runner := &domeVolumeTestRunner{}
	loaded := writeDomeVolumePublication(t, root)
	volume, err := newDomeVolume(root, filepath.Join(root, "tmp"), loaded, runner, 8)
	if err != nil {
		t.Fatal(err)
	}
	grid := Coverage()
	stencil, err := volume.HorizontalStencil(context.Background(), forecast.Location{
		Latitude: grid.MinLat + 2.5*grid.Increment, Longitude: grid.MinLon + 3.5*grid.Increment,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := volume.Column(context.Background(), stencil.Supports[0].ColumnID)
	if err != nil {
		t.Fatal(err)
	}
	for _, support := range stencil.Supports[1:] {
		if _, err := volume.Column(context.Background(), support.ColumnID); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := runner.calls(), 1+len(DomeNativeForecastHours()); got != want {
		t.Fatalf("CDO calls = %d, want %d (one four-column pass per immutable object set)", got, want)
	}
	if runner.maximumPoints() != 4 {
		t.Fatalf("largest extraction batch = %d points, want 4", runner.maximumPoints())
	}
	if len(first.HalfLevelGeometry) != 75 || len(first.Frames) != 81 ||
		len(first.Frames[0].FullLevels) != 74 || len(first.Frames[0].HalfLevels) != 75 {
		t.Fatalf("column dimensions = HHL%d frames%d full%d half%d",
			len(first.HalfLevelGeometry), len(first.Frames), len(first.Frames[0].FullLevels), len(first.Frames[0].HalfLevels))
	}
	if err := validateDomeAstrodomeImmutableColumn(first, loaded.ModelSteps); err != nil {
		t.Fatalf("validate complete immutable column: %v", err)
	}
	incomplete := cloneDomeColumn(first)
	incomplete.Frames[0].Surface.Available &^= forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveSpecificHumidity2M)
	if err := validateDomeAstrodomeImmutableColumn(incomplete, loaded.ModelSteps); err == nil {
		t.Fatal("immutable preload validator accepted a missing QV_2M refraction boundary")
	}
	invertedPressure := cloneDomeColumn(first)
	invertedPressure.Frames[0].FullLevels[1].PressurePa = invertedPressure.Frames[0].FullLevels[0].PressurePa
	if err := validateDomeAstrodomeImmutableColumn(invertedPressure, loaded.ModelSteps); err == nil {
		t.Fatal("immutable preload validator accepted a non-increasing pressure profile")
	}
	invertedSurfacePressure := cloneDomeColumn(first)
	lowest := invertedSurfacePressure.Frames[0].FullLevels[len(invertedSurfacePressure.Frames[0].FullLevels)-1].PressurePa
	invertedSurfacePressure.Frames[0].Surface.SurfacePressurePa = lowest
	if err := validateDomeAstrodomeImmutableColumn(invertedSurfacePressure, loaded.ModelSteps); err == nil {
		t.Fatal("immutable preload validator accepted PS not exceeding the lowest full-level pressure")
	}
	if first.Frames[79].ValidAt.Sub(loaded.BaseTime) != 81*time.Hour ||
		first.Frames[80].ValidAt.Sub(loaded.BaseTime) != 84*time.Hour {
		t.Fatalf("native bracket frames = %v / %v", first.Frames[79].ValidAt, first.Frames[80].ValidAt)
	}
	level := first.Frames[0].FullLevels[0]
	if level.Available != domeFullAvailable || math.Abs(level.PressurePa-domeVolumeTestModelValue("pres", 1, 0,
		batchPoint{Latitude: first.Location.Latitude, Longitude: first.Location.Longitude})) > 1e-9 ||
		level.CloudFraction != 0.25 {
		t.Fatalf("raw full level = %+v", level)
	}
	if first.Frames[0].HalfLevels[0].Available != domeHalfAvailable {
		t.Fatalf("raw half-level mask = %#x", first.Frames[0].HalfLevels[0].Available)
	}
	if first.Frames[0].Surface.Available.Has(forecast.AstrodomePrimitivePrecipitationAccumulation) {
		t.Fatal("zero-length f000 precipitation interval marked available")
	}
	if first.Frames[0].Surface.Available.Has(forecast.AstrodomePrimitiveWindGust10M) {
		t.Fatal("missing native f000 gust was replaced by a synthetic value")
	}
	f001 := first.Frames[1].Surface
	if !f001.Available.Has(forecast.AstrodomePrimitivePrecipitationAccumulation) ||
		f001.PrecipitationIntervalStart != loaded.BaseTime || f001.PrecipitationIntervalEnd != loaded.BaseTime.Add(time.Hour) ||
		math.Abs(f001.RelativeHumidityFraction-0.6) > 1e-14 {
		t.Fatalf("raw f001 surface = %+v", f001)
	}

	first.Frames[0].FullLevels[0].PressurePa = -1
	again, err := volume.Column(context.Background(), stencil.Supports[0].ColumnID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Frames[0].FullLevels[0].PressurePa <= 0 {
		t.Fatal("caller mutated the cached immutable column")
	}
}

func TestDomeVolumeNativeContextReconstructsPrimitivesAtF079(t *testing.T) {
	root := t.TempDir()
	loaded := writeDomeVolumePublication(t, root)
	volume, err := newDomeVolume(root, filepath.Join(root, "tmp"), loaded, &domeVolumeTestRunner{}, 8)
	if err != nil {
		t.Fatal(err)
	}
	grid := Coverage()
	location := forecast.Location{
		Latitude: grid.MinLat + 4.25*grid.Increment, Longitude: grid.MinLon + 5.75*grid.Increment,
	}
	stencil, err := volume.HorizontalStencil(context.Background(), location)
	if err != nil {
		t.Fatal(err)
	}
	point := forecast.AstrodomeRayPoint{Location: location, HeightM: 5000}
	contextAt79, err := volume.ResolveAstrodomeScienceNativeContext(
		context.Background(), loaded.BaseTime.Add(79*time.Hour), point, stencil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if contextAt79.HorizontalCellID != "cell-lat0004-lon0005" || len(contextAt79.ThermalProfile) != 74 {
		t.Fatalf("native context identity/profile = %q/%d", contextAt79.HorizontalCellID, len(contextAt79.ThermalProfile))
	}
	if contextAt79.ThermalProfile[0].HeightM >= contextAt79.ThermalProfile[1].HeightM ||
		contextAt79.ThermalProfile[72].HeightM >= contextAt79.ThermalProfile[73].HeightM {
		t.Fatal("native thermal profile is not bottom-to-top")
	}
	// f079 is exactly one third of the raw f078..f081 bracket. The synthetic
	// MH is 700+lead, so a complete primitive interpolation must yield 779 m.
	if math.Abs(contextAt79.MixedLayerDepthM-779) > 1e-10 {
		t.Fatalf("interpolated MH = %.12g, want 779", contextAt79.MixedLayerDepthM)
	}
	if _, err := volume.NativeSurfaceAt(context.Background(), loaded.BaseTime.Add(79*time.Hour), location); err == nil {
		t.Fatal("non-native cumulative surface timestamp was accepted")
	}
	interpolatedSurface, err := volume.SurfaceAt(context.Background(), loaded.BaseTime.Add(79*time.Hour), location)
	if err != nil {
		t.Fatal(err)
	}
	if interpolatedSurface.Available.Has(forecast.AstrodomePrimitivePrecipitationAccumulation) ||
		!interpolatedSurface.Available.Has(forecast.AstrodomePrimitiveSurfacePressure) ||
		math.Abs(interpolatedSurface.MixedLayerDepthM-779) > 1e-10 {
		t.Fatalf("interpolated f079 surface = %+v", interpolatedSurface)
	}
	surface, err := volume.NativeSurfaceAt(context.Background(), loaded.BaseTime.Add(81*time.Hour), location)
	if err != nil {
		t.Fatal(err)
	}
	if !surface.Available.Has(forecast.AstrodomePrimitiveTotalColumnWaterVapour) ||
		!surface.Available.Has(forecast.AstrodomePrimitiveSurfacePressure) ||
		!surface.Available.Has(forecast.AstrodomePrimitiveSpecificHumidity2M) ||
		math.Abs(surface.SurfacePressurePa-(100000+location.Latitude)) > 1e-10 ||
		math.Abs(surface.SpecificHumidity2MKgKg-0.006) > 1e-12 ||
		math.Abs(surface.MixedLayerDepthM-781) > 1e-10 {
		t.Fatalf("native f081 surface = %+v", surface)
	}
}

func TestDomeVolumeCoalescesConcurrentFourColumnExtraction(t *testing.T) {
	root := t.TempDir()
	runner := &domeVolumeTestRunner{delay: time.Millisecond}
	loaded := writeDomeVolumePublication(t, root)
	volume, err := newDomeVolume(root, filepath.Join(root, "tmp"), loaded, runner, 8)
	if err != nil {
		t.Fatal(err)
	}
	grid := Coverage()
	stencil, err := volume.HorizontalStencil(context.Background(), forecast.Location{
		Latitude: grid.MinLat + 6.5*grid.Increment, Longitude: grid.MinLon + 7.5*grid.Increment,
	})
	if err != nil {
		t.Fatal(err)
	}
	errorsByCaller := make(chan error, 2)
	start := make(chan struct{})
	for _, supportIndex := range []int{0, 1} {
		go func(index int) {
			<-start
			_, columnErr := volume.Column(context.Background(), stencil.Supports[index].ColumnID)
			errorsByCaller <- columnErr
		}(supportIndex)
	}
	close(start)
	for range 2 {
		if err := <-errorsByCaller; err != nil {
			t.Fatal(err)
		}
	}
	if got, want := runner.calls(), 1+len(DomeNativeForecastHours()); got != want {
		t.Fatalf("concurrent CDO calls = %d, want one coalesced %d-call extraction", got, want)
	}
}

func TestDomeVolumeRejectsChangedPublishedObjectSize(t *testing.T) {
	root := t.TempDir()
	loaded := writeDomeVolumePublication(t, root)
	volume, err := newDomeVolume(root, filepath.Join(root, "tmp"), loaded, &domeVolumeTestRunner{}, 8)
	if err != nil {
		t.Fatal(err)
	}
	part := loaded.ModelSteps[0].Parts[len(loaded.ModelSteps[0].Parts)-1]
	file, err := os.OpenFile(filepath.Join(root, "models", "icon-eu", part.File), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("changed"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	grid := Coverage()
	stencil, err := volume.HorizontalStencil(context.Background(), forecast.Location{
		Latitude: grid.MinLat + grid.Increment/2, Longitude: grid.MinLon + grid.Increment/2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := volume.Column(context.Background(), stencil.Supports[0].ColumnID); err == nil ||
		!strings.Contains(err.Error(), "changed size or type") {
		t.Fatalf("changed immutable object error = %v", err)
	}
}

func writeDomeVolumePublication(t *testing.T, root string) LoadedDomeManifest {
	t.Helper()
	base, _ := writeDomeTestBase(t, root)
	providerRoot := filepath.Join(root, "models", "icon-eu")
	baseDigest, _, err := fileDigest(filepath.Join(base.Directory, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := NewDomeManifest(base.RunID, base.BaseTime, baseDigest)
	manifest.PublishedAt = base.BaseTime.Add(6 * time.Hour)
	manifest.Complete = true
	manifest.Geometry = domeVolumeBaseFile(t, base, 0, base.BaseTime, *base.CloudGeometry, domeHalfLevelCount)
	for _, hour := range DomeNativeForecastHours() {
		validAt := base.BaseTime.Add(time.Duration(hour) * time.Hour)
		step := DomeModelStep{ForecastHour: hour, ValidAt: validAt, Messages: domeModelMessagesPerStep}
		if hour <= 78 {
			cloud := base.CloudSteps[hour]
			step.Parts = append(step.Parts, domeVolumeBaseFile(t, base, hour, validAt,
				BundleFile{File: cloud.File, Bytes: cloud.Bytes, SHA256: cloud.SHA256, Messages: cloud.Messages}, cloudStepMessageCount()))
		}
		relative := filepath.Join("dome-runs", base.RunID, DomeInputContractDigest(), "model", fmt.Sprintf("f%03d.grib2", hour))
		bundle := writeDomeTestFile(t, providerRoot, relative, fmt.Sprintf("dome-volume-model-%03d", hour))
		messages := domeModelMessagesPerStep
		if hour <= 78 {
			messages -= cloudStepMessageCount()
		}
		step.Parts = append(step.Parts, domeVolumeDomeFile(t, providerRoot, hour, validAt, bundle, relative, messages))
		manifest.ModelSteps = append(manifest.ModelSteps, step)
	}
	for _, hour := range []int{81, 84} {
		validAt := base.BaseTime.Add(time.Duration(hour) * time.Hour)
		relative := filepath.Join("dome-runs", base.RunID, DomeInputContractDigest(), "surface", fmt.Sprintf("f%03d.grib2", hour))
		bundle := writeDomeTestFile(t, providerRoot, relative, fmt.Sprintf("dome-volume-surface-%03d", hour))
		manifest.SurfaceExtensionSteps = append(manifest.SurfaceExtensionSteps,
			domeVolumeDomeFile(t, providerRoot, hour, validAt, bundle, relative, SurfaceBundleSchemaVersion))
	}
	directory := filepath.Join(providerRoot, "dome-runs", base.RunID, DomeInputContractDigest())
	if err := writeDomeManifest(filepath.Join(directory, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDomeManifest(filepath.Join(directory, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func domeVolumeBaseFile(t *testing.T, base LoadedManifest, hour int, validAt time.Time, bundle BundleFile, messages int) DomeStepFile {
	t.Helper()
	path := filepath.Join(base.Directory, bundle.File)
	allocated, err := domeAllocatedBytes(path)
	if err != nil {
		t.Fatal(err)
	}
	return DomeStepFile{
		Source: DomeFileSourceBaseRun, ForecastHour: hour, ValidAt: validAt,
		File: filepath.Join("runs", base.RunID, bundle.File), Bytes: bundle.Bytes, AllocatedBytes: allocated,
		SHA256: bundle.SHA256, Messages: messages,
	}
}

func domeVolumeDomeFile(t *testing.T, providerRoot string, hour int, validAt time.Time, bundle BundleFile, relative string, messages int) DomeStepFile {
	t.Helper()
	allocated, err := domeAllocatedBytes(filepath.Join(providerRoot, relative))
	if err != nil {
		t.Fatal(err)
	}
	return DomeStepFile{
		Source: DomeFileSourceDomeRun, ForecastHour: hour, ValidAt: validAt,
		File: relative, Bytes: bundle.Bytes, AllocatedBytes: allocated, SHA256: bundle.SHA256, Messages: messages,
	}
}

type domeVolumeTestRunner struct {
	mu        sync.Mutex
	callCount int
	maxPoints int
	delay     time.Duration
}

type domeVolumeArgumentRunner struct {
	arguments []string
}

type domeRemapPlanTestRunner struct {
	calls               [][]string
	weightSourceAddress int
	weight              float64
	sourceMetadata      string
}

func (runner *domeRemapPlanTestRunner) CombinedOutput(_ context.Context, name string, arguments ...string) ([]byte, error) {
	if name == "grib_get" {
		metadata := runner.sourceMetadata
		if metadata == "" {
			metadata = "regular_ll 3 3 56 36 54 38 1 1 0 0\n"
		}
		return []byte(metadata), nil
	}
	if name == "ncdump" {
		sourceAddress := runner.weightSourceAddress
		if sourceAddress == 0 {
			sourceAddress = 5
		}
		weight := runner.weight
		if weight == 0 {
			weight = 1
		}
		return []byte(fmt.Sprintf(`netcdf weights {
dimensions:
	src_grid_size = 9 ;
	dst_grid_size = 1 ;
	src_grid_rank = 2 ;
	dst_grid_rank = 1 ;
	num_links = 1 ;
	num_wgts = 1 ;
data:
	src_grid_dims = 3, 3 ;
	dst_grid_dims = 1 ;
	src_address = %d ;
	dst_address = 1 ;
	remap_matrix = %.17g ;
}
`, sourceAddress, weight)), nil
	}
	if name != "cdo" {
		return nil, fmt.Errorf("unexpected command %q", name)
	}
	runner.calls = append(runner.calls, append([]string(nil), arguments...))
	for _, argument := range arguments {
		if strings.HasPrefix(argument, "gennn,") {
			if err := os.WriteFile(arguments[len(arguments)-1], []byte("test weights"), 0o600); err != nil {
				return nil, err
			}
			return nil, nil
		}
	}
	return []byte("t 1 37.00000000 55.00000000 273.15\n"), nil
}

func (runner *domeVolumeArgumentRunner) CombinedOutput(_ context.Context, name string, arguments ...string) ([]byte, error) {
	if name != "cdo" {
		return nil, fmt.Errorf("unexpected command %q", name)
	}
	runner.arguments = append([]string(nil), arguments...)
	return nil, errors.New("stop after capturing arguments")
}

func TestDomeVolumeExtractionGroupsMultipleCDOInputs(t *testing.T) {
	t.Parallel()
	runner := &domeVolumeArgumentRunner{}
	volume := &DomeVolume{runner: runner, tempRoot: t.TempDir()}
	_, err := volume.extractDomeSources(context.Background(), []string{"model.grib2", "surface.grib2"},
		[]batchPoint{{Latitude: 55, Longitude: 37}}, 1)
	if err == nil {
		t.Fatal("capturing runner did not stop extraction")
	}
	wantTail := []string{"-merge", "[", "model.grib2", "surface.grib2", "]"}
	if len(runner.arguments) < len(wantTail) ||
		!slices.Equal(runner.arguments[len(runner.arguments)-len(wantTail):], wantTail) {
		t.Fatalf("CDO arguments = %q, want tail %q", runner.arguments, wantTail)
	}
}

func TestDomePreloadReusesOneNearestNeighbourWeightPlan(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	geometryPath := filepath.Join(root, "geometry.grib2")
	if err := os.WriteFile(geometryPath, []byte("geometry"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &domeRemapPlanTestRunner{}
	volume := &DomeVolume{
		runner: runner, tempRoot: root,
		manifest: LoadedDomeManifest{DomeManifest: DomeManifest{
			Grid: model.Coverage{MinLat: 54, MaxLat: 56, MinLon: 36, MaxLon: 38, Increment: 1},
		}},
	}
	points := []batchPoint{{Latitude: 55, Longitude: 37}}
	plan, err := volume.prepareDomeRemapPlan(context.Background(), geometryPath, points)
	if err != nil {
		t.Fatal(err)
	}
	planDirectory := plan.directory
	defer func() { _ = plan.Close() }()
	if len(runner.calls) != 1 || !slices.Contains(runner.calls[0], "gennn,"+plan.gridPath) ||
		!slices.Contains(runner.calls[0], "-sellevel,1") || !slices.Contains(runner.calls[0], "-selname,HHL") ||
		runner.calls[0][len(runner.calls[0])-1] != plan.weightsPath {
		t.Fatalf("weight-generation command = %q", runner.calls)
	}
	values, err := volume.extractDomeSourcesWithRemapPlan(context.Background(), []string{"model.grib2"}, points, 1, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0][batchValueKey{name: "t", level: 1}].value != 273.15 {
		t.Fatalf("planned extraction values = %+v", values)
	}
	if len(runner.calls) != 2 || !slices.Contains(runner.calls[1], "-remap,"+plan.gridPath+","+plan.weightsPath) {
		t.Fatalf("planned extraction command = %q", runner.calls[1])
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(planDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remap plan directory still exists: %v", err)
	}
}

func TestDomeRemapPlanRejectsSourceGridDifferentFromManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	geometryPath := filepath.Join(root, "geometry.grib2")
	if err := os.WriteFile(geometryPath, []byte("geometry"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &domeRemapPlanTestRunner{}
	volume := &DomeVolume{
		runner: runner, tempRoot: root,
		manifest: LoadedDomeManifest{DomeManifest: DomeManifest{
			Grid: model.Coverage{MinLat: 54, MaxLat: 57, MinLon: 36, MaxLon: 38, Increment: 1},
		}},
	}
	_, err := volume.prepareDomeRemapPlan(
		context.Background(),
		geometryPath,
		[]batchPoint{{Latitude: 55, Longitude: 37}},
	)
	if err == nil || !strings.Contains(err.Error(), "HHL regular grid differs from manifest") {
		t.Fatalf("mismatched source-grid error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("nearest-neighbour weights were generated before source-grid proof: %q", runner.calls)
	}
}

func TestDomeRemapPlanRejectsNonNativeSCRIPLinksAndMismatchedReuseGrid(t *testing.T) {
	t.Parallel()

	newVolume := func(t *testing.T, runner *domeRemapPlanTestRunner) (*DomeVolume, string) {
		t.Helper()
		root := t.TempDir()
		geometryPath := filepath.Join(root, "geometry.grib2")
		if err := os.WriteFile(geometryPath, []byte("geometry"), 0o600); err != nil {
			t.Fatal(err)
		}
		return &DomeVolume{
			runner: runner, tempRoot: root, sourceGrids: make(map[string]domeNativeSourceGrid),
			manifest: LoadedDomeManifest{DomeManifest: DomeManifest{
				Grid: model.Coverage{MinLat: 54, MaxLat: 56, MinLon: 36, MaxLon: 38, Increment: 1},
			}},
		}, geometryPath
	}
	points := []batchPoint{{Latitude: 55, Longitude: 37}}
	for _, test := range []struct {
		name          string
		sourceAddress int
		weight        float64
	}{
		{name: "wrong source address", sourceAddress: 6, weight: 1},
		{name: "non-unit weight", sourceAddress: 5, weight: 0.5},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &domeRemapPlanTestRunner{weightSourceAddress: test.sourceAddress, weight: test.weight}
			volume, geometryPath := newVolume(t, runner)
			plan, err := volume.prepareDomeRemapPlan(context.Background(), geometryPath, points)
			if plan != nil {
				_ = plan.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "prove exact native-node remap plan") {
				t.Fatalf("invalid SCRIP link error = %v", err)
			}
		})
	}

	runner := &domeRemapPlanTestRunner{}
	volume, geometryPath := newVolume(t, runner)
	plan, err := volume.prepareDomeRemapPlan(context.Background(), geometryPath, points)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = plan.Close() }()
	runner.sourceMetadata = "regular_ll 3 3 54 36 56 38 1 1 0 1\n"
	_, err = volume.extractDomeSourcesWithRemapPlan(
		context.Background(), []string{"model.grib2"}, points, 1, plan,
	)
	if err == nil || !strings.Contains(err.Error(), "differs from the proven HHL remap grid") {
		t.Fatalf("mismatched reusable source-grid error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("remap ran before source-grid mismatch was rejected: %q", runner.calls)
	}
}

var domeVolumeForecastHourPattern = regexp.MustCompile(`f([0-9]{3})\.grib2`)

func (runner *domeVolumeTestRunner) CombinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	if name == "grib_get" {
		if strings.Contains(strings.Join(args, " "), "gridType,Ni,Nj") {
			return []byte("regular_ll 1377 657 70.5 -23.5 29.5 62.5 0.0625 0.0625 0 0\n"), nil
		}
		return []byte("0.0001220703125\n"), nil
	}
	if name != "cdo" {
		return nil, fmt.Errorf("unexpected command %q", name)
	}
	gridPath := ""
	geometry := false
	hour := -1
	for _, argument := range args {
		if strings.HasPrefix(argument, "-remapnn,") {
			gridPath = strings.TrimPrefix(argument, "-remapnn,")
		}
		if strings.HasSuffix(argument, "geometry.grib2") {
			geometry = true
		}
		if match := domeVolumeForecastHourPattern.FindStringSubmatch(argument); len(match) == 2 {
			parsed, _ := strconv.Atoi(match[1])
			hour = parsed
		}
	}
	points, err := domeVolumeTestGridPoints(gridPath)
	if err != nil {
		return nil, err
	}
	runner.mu.Lock()
	runner.callCount++
	if len(points) > runner.maxPoints {
		runner.maxPoints = len(points)
	}
	runner.mu.Unlock()
	if runner.delay > 0 {
		time.Sleep(runner.delay)
	}
	var output strings.Builder
	for _, point := range points {
		row := func(field string, level int, value float64) {
			_, _ = fmt.Fprintf(&output, "%s %d %.8f %.8f %.12g\n", field, level, point.Longitude, point.Latitude, value)
		}
		if geometry {
			for level := 1; level <= domeHalfLevelCount; level++ {
				row("HHL", level, 30400-float64(level)*400+point.Latitude*0.01+point.Longitude*0.001)
			}
			continue
		}
		if hour < 0 {
			return nil, errors.New("test CDO call has no forecast hour")
		}
		for level := 1; level <= domeFullLevelCount; level++ {
			for _, field := range []string{"pres", "t", "q", "qc", "qi", "ccl", "u", "v"} {
				row(field, level, domeVolumeTestModelValue(field, level, hour, point))
			}
		}
		for level := 1; level <= domeHalfLevelCount; level++ {
			row("wz", level, domeVolumeTestModelValue("wz", level, hour, point))
			row("tke", level, domeVolumeTestModelValue("tke", level, hour, point))
		}
		for index, field := range surfaceFields {
			value := domeVolumeTestSurfaceValue(field.ShortName, hour, point)
			if field.ShortName == "VMAX_10M" && hour == 0 {
				value = -9e33
			}
			row(field.ShortName, 1000+index, value)
		}
	}
	return []byte(output.String()), nil
}

func (runner *domeVolumeTestRunner) calls() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.callCount
}

func (runner *domeVolumeTestRunner) maximumPoints() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.maxPoints
}

func domeVolumeTestGridPoints(path string) ([]batchPoint, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(contents), "\n")
	var longitudeFields, latitudeFields []string
	for _, line := range lines {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch strings.TrimSpace(parts[0]) {
		case "xvals":
			longitudeFields = strings.Fields(parts[1])
		case "yvals":
			latitudeFields = strings.Fields(parts[1])
		}
	}
	if len(longitudeFields) == 0 || len(longitudeFields) != len(latitudeFields) {
		return nil, errors.New("invalid test target grid")
	}
	points := make([]batchPoint, len(longitudeFields))
	for index := range points {
		longitude, lonErr := strconv.ParseFloat(longitudeFields[index], 64)
		latitude, latErr := strconv.ParseFloat(latitudeFields[index], 64)
		if lonErr != nil || latErr != nil {
			return nil, errors.New("invalid test target coordinate")
		}
		points[index] = batchPoint{Latitude: latitude, Longitude: longitude}
	}
	return points, nil
}

func domeVolumeTestModelValue(field string, level, hour int, point batchPoint) float64 {
	switch field {
	case "pres":
		return 8000 + float64(level)*1200 + float64(hour)*2 + point.Latitude*0.1 + point.Longitude*0.01
	case "t":
		return 200 + float64(level)*0.8 + float64(hour)*0.1 + point.Latitude*0.001
	case "q":
		return 0.001 + float64(level)*1e-6 + float64(hour)*1e-8
	case "qc":
		return 1e-5 + float64(level)*1e-8
	case "qi":
		return 2e-5 + float64(level)*1e-8
	case "ccl":
		return 25
	case "u":
		return 2 + float64(level)*0.01 + float64(hour)*0.001
	case "v":
		return -1 + float64(level)*0.01
	case "wz":
		return float64(level) * 0.001
	case "tke":
		return 0.2 + float64(level)*0.001
	default:
		return math.NaN()
	}
}

func domeVolumeTestSurfaceValue(field string, hour int, point batchPoint) float64 {
	switch field {
	case "2t":
		return 280 + float64(hour)*0.1
	case "2d":
		return 275 + float64(hour)*0.1
	case "2r":
		return 60
	case "CLCT", "CLCL", "CLCM", "CLCH":
		return 25
	case "tp":
		return float64(hour) * 0.1
	case "10u":
		return 3
	case "10v":
		return 4
	case "VMAX_10M":
		return 7
	case "prmsl":
		return 101325
	case "vis":
		return 20000
	case "TQV":
		return 12
	case "TQC":
		return 0.05
	case "TQI":
		return 0.02
	case "mld":
		return 700 + float64(hour)
	case "sp":
		return 100000 + point.Latitude
	case "2sh":
		return 0.006
	default:
		return 1
	}
}
