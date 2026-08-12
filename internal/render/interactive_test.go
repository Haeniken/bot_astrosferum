package render

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"bot_astrosferum/internal/astronomy"
	"bot_astrosferum/internal/forecast"
)

func TestPrepareForecastInteractiveDatasetPreservesPreparedAxesAndMissingValues(t *testing.T) {
	base := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	location := forecast.Location{Latitude: 59.9386, Longitude: 30.3141, TimeZone: "Europe/Moscow"}
	vertical := forecast.VerticalSeries{
		Location: location, Provider: "icon-eu", Product: "pressure", RunID: "2026072212", Grid: "0.0625°",
		AlgorithmVersion: forecast.SeeingPrototypeVersion, BaseTime: base, GeneratedAt: base.Add(time.Hour),
	}
	surface := forecast.SurfaceSeries{
		Location: location, Provider: "icon-eu", Product: "surface", RunID: vertical.RunID,
		BaseTime: base, GeneratedAt: base.Add(time.Hour), StepHours: 1,
	}
	cloud := forecast.CloudSeries{
		Location: location, Provider: "icon-eu", Product: "model-level", RunID: vertical.RunID,
		BaseTime: base, GeneratedAt: base.Add(time.Hour), SurfaceElevationM: 12,
	}
	for index := range 25 {
		validAt := base.Add(time.Duration(index*3) * time.Hour)
		vertical.Frames = append(vertical.Frames, forecast.VerticalFrame{
			ValidAt: validAt, Confidence: 0.96 - 0.26*float64(index)/24,
			Levels: []forecast.VerticalLevel{
				{PressureHPA: 850, HeightM: 1500, TemperatureK: 280, UMS: 3 + float64(index), VMS: 4},
				{PressureHPA: 500, HeightM: 5600, TemperatureK: 250, UMS: 10, VMS: 5 + float64(index)},
			},
		})
	}
	overall := make([]forecast.OverallIndexFrame, 73)
	penalties := []forecast.OverallPenaltyContribution{
		{Key: forecast.OverallPenaltyOpticalTurbulence, LossFraction: 1.0 / 15.0},
		{Key: forecast.OverallPenaltyCloudObstruction, LossFraction: 1.0 / 15.0},
		{Key: forecast.OverallPenaltySurfaceWind, LossFraction: 1.0 / 15.0},
		{Key: forecast.OverallPenaltyFog, LossFraction: 1.0 / 15.0},
		{Key: forecast.OverallPenaltyPrecipitation, LossFraction: 1.0 / 15.0},
	}
	for index := range 73 {
		validAt := base.Add(time.Duration(index) * time.Hour)
		surface.Frames = append(surface.Frames, forecast.SurfaceFrame{
			ValidAt: validAt, TemperatureC: 14, DewPointC: 10, RelativeHumidityPercent: 76,
			CloudCoverPercent: 35, LowCloudCoverPercent: 10, MidCloudCoverPercent: 20, HighCloudCoverPercent: 35,
			WindSpeedMS: 3, WindGustMS: 6, WindDirectionDegrees: 240, PressureHPA: 1005,
			VisibilityKM: 35, PrecipitableWaterMM: 18, TransparencyAvailable: true,
		})
		cloud.Frames = append(cloud.Frames, forecast.CloudFrame{ValidAt: validAt, Levels: []forecast.CloudLevel{
			{ModelLevel: 70, PressureHPA: 850, HeightM: 1500, LayerThicknessM: 500, TemperatureK: 280, CoverPercent: 10, CloudLiquidKgKg: 1e-5},
			{ModelLevel: 50, PressureHPA: 500, HeightM: 5600, LayerThicknessM: 900, TemperatureK: 250, CoverPercent: 35, CloudIceKgKg: 2e-5},
		}})
		overall[index] = forecast.OverallIndexFrame{
			ValidAt: validAt, Index: 7, SeeingArcsec: 1.2, CoherenceTimeMS: 4,
			PenaltyLossFraction: 1.0 / 3.0, PenaltyContributions: penalties,
		}
	}
	sky, err := astronomy.Compute(location, base, base.Add(6*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	celestialTimes := make([]time.Time, len(surface.Frames))
	for index := range surface.Frames {
		celestialTimes[index] = surface.Frames[index].ValidAt
	}
	celestialTracks, err := astronomy.ComputeCelestialTracks(location, celestialTimes)
	if err != nil {
		t.Fatal(err)
	}
	dataset, diagnostics, _, obstruction, err := PrepareForecastInteractiveDataset(
		vertical, surface, cloud, sky, celestialTracks, overall, forecast.DefaultOverallIndexCalibration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if dataset.SchemaVersion != ForecastInteractiveSchema || len(dataset.UpperAir.TimesUTC) != 25 || len(dataset.Weather.Hours) != 73 || len(dataset.Overall) != 73 ||
		len(dataset.Cloud.ObstructionPercent) != 2 || len(dataset.Cloud.ObstructionPercent[0]) != 73 {
		t.Fatalf("interactive forecast dimensions = weather %d overall %d cloud %dx%d",
			len(dataset.Weather.Hours), len(dataset.Overall), len(dataset.Cloud.ObstructionPercent), len(dataset.Cloud.ObstructionPercent[0]))
	}
	if dataset.Algorithms.Overall != forecast.OverallIndexAlgorithmVersion ||
		dataset.Algorithms.CloudObstruction != forecast.CloudObstructionAlgorithmVersion ||
		len(dataset.Algorithms.OverallCalibrationSHA256) != 64 ||
		dataset.Inputs.VerticalProduct != "pressure" || dataset.Inputs.SurfaceProduct != "surface" || dataset.Inputs.CloudProduct != "model-level" {
		t.Fatalf("interactive scientific provenance = %+v, inputs = %+v", dataset.Algorithms, dataset.Inputs)
	}
	if dataset.UpperAir.VectorShearMSPerKM[1][0] != nil || !math.IsNaN(diagnostics.VectorShearMSPerKM[1][0]) {
		t.Fatal("intentional top-row missing shear was not represented as JSON null")
	}
	if dataset.Cloud.ObstructionPercent[0][0] == nil || math.Float64bits(*dataset.Cloud.ObstructionPercent[0][0]) != math.Float64bits(obstruction[0][0]) {
		t.Fatal("finite cloud obstruction changed while preparing JSON")
	}
	if !dataset.UpperAir.TimesUTC[1].Equal(base.Add(3*time.Hour)) || !dataset.Weather.Hours[1].ValidAt.Equal(base.Add(time.Hour)) {
		t.Fatal("native three-hourly upper-air and hourly weather axes were not preserved independently")
	}
	if got := dataset.PenaltyPoints[0].Contributions[0].Points; math.Float64bits(got) != math.Float64bits(9.0/15.0) {
		t.Fatalf("server-derived Overall penalty points = %v", got)
	}
	shortOverall := overall[1:71]
	shortDataset, _, _, _, err := PrepareForecastInteractiveDataset(
		vertical, surface, cloud, sky, celestialTracks, shortOverall, forecast.DefaultOverallIndexCalibration(),
	)
	if err != nil {
		t.Fatalf("prepare narrower physical Overall overlap: %v", err)
	}
	if len(shortDataset.Weather.Hours) != 73 || len(shortDataset.Cloud.TimesUTC) != 73 || len(shortDataset.Overall) != 70 ||
		!shortDataset.Overall[0].ValidAt.Equal(surface.Frames[1].ValidAt) ||
		!shortDataset.Overall[len(shortDataset.Overall)-1].ValidAt.Equal(surface.Frames[70].ValidAt) {
		t.Fatalf("independent interactive axes = weather %d cloud %d Overall %d",
			len(shortDataset.Weather.Hours), len(shortDataset.Cloud.TimesUTC), len(shortDataset.Overall))
	}
	gapSurface := surface
	gapSurface.Frames = append(append([]forecast.SurfaceFrame(nil), surface.Frames[:10]...), surface.Frames[11:]...)
	gapCloud := cloud
	gapCloud.Frames = append(append([]forecast.CloudFrame(nil), cloud.Frames[:10]...), cloud.Frames[11:]...)
	gapOverall := append(append([]forecast.OverallIndexFrame(nil), overall[:10]...), overall[11:]...)
	for name, input := range map[string]struct {
		surface forecast.SurfaceSeries
		cloud   forecast.CloudSeries
		overall []forecast.OverallIndexFrame
	}{
		"surface": {surface: gapSurface, cloud: cloud, overall: overall},
		"cloud":   {surface: surface, cloud: gapCloud, overall: overall},
		"Overall": {surface: surface, cloud: cloud, overall: gapOverall},
	} {
		if _, _, _, _, err := PrepareForecastInteractiveDataset(
			vertical, input.surface, input.cloud, sky, celestialTracks, input.overall, forecast.DefaultOverallIndexCalibration(),
		); err == nil {
			t.Fatalf("%s hourly gap was accepted", name)
		}
	}
	gapVertical := vertical
	gapVertical.Frames = append([]forecast.VerticalFrame(nil), vertical.Frames...)
	gapVertical.Frames[1].ValidAt = gapVertical.Frames[1].ValidAt.Add(time.Hour)
	if _, _, _, _, err := PrepareForecastInteractiveDataset(
		gapVertical, surface, cloud, sky, celestialTracks, overall, forecast.DefaultOverallIndexCalibration(),
	); err == nil || !strings.Contains(err.Error(), "not three-hourly") {
		t.Fatalf("upper-air cadence error = %v", err)
	}
	encoded, err := json.Marshal(dataset)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"vector_shear_ms_per_km"`) || !strings.Contains(string(encoded), `null`) {
		t.Fatalf("missing-value contract absent from JSON: %s", encoded)
	}
	destination := filepath.Join(t.TempDir(), "forecast.json")
	if err := SaveForecastInteractiveDataset(destination, dataset); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(destination)
	if err != nil || len(stored) == 0 || stored[0] != '{' {
		t.Fatalf("saved interactive forecast = %q, %v", stored, err)
	}
}

func TestPrepareHorizonInteractiveDatasetPreservesCanonicalCells(t *testing.T) {
	input := horizonRenderFixture()
	calibration := forecast.DefaultOverallIndexCalibration()
	artifactKey := strings.Repeat("a", sha256HexLength)
	dataset, err := PrepareHorizonInteractiveDataset(input, artifactKey, 12, calibration)
	if err != nil {
		t.Fatal(err)
	}
	if dataset.SchemaVersion != HorizonInteractiveSchema || dataset.ScienceVersion != forecast.HorizonAlgorithmVersion ||
		dataset.ArtifactKey != artifactKey || dataset.ObserverSurfaceElevationM != 12 || len(dataset.OverallCalibrationSHA256) != 64 ||
		len(dataset.Frames) != horizonFrameCount || len(dataset.Directions) != forecast.HorizonDirectionCount || len(dataset.SolarPhases) < 1 {
		t.Fatalf("interactive Horizon metadata = %+v", dataset)
	}
	wantStart := input.Frames[0].ValidAt.Add(-30 * time.Minute)
	wantEnd := input.Frames[len(input.Frames)-1].ValidAt.Add(30 * time.Minute)
	if !dataset.SolarPhases[0].Start.Equal(wantStart) || !dataset.SolarPhases[len(dataset.SolarPhases)-1].End.Equal(wantEnd) {
		t.Fatalf("interactive Horizon solar coverage = %s..%s, want %s..%s",
			dataset.SolarPhases[0].Start, dataset.SolarPhases[len(dataset.SolarPhases)-1].End, wantStart, wantEnd)
	}
	for index, period := range dataset.SolarPhases {
		if !period.End.After(period.Start) {
			t.Fatalf("interactive Horizon solar period %d is empty", index)
		}
		if index > 0 && !period.Start.Equal(dataset.SolarPhases[index-1].End) {
			t.Fatalf("interactive Horizon solar periods %d and %d have a gap or overlap", index-1, index)
		}
	}
	for frameIndex := range input.Frames {
		if !reflect.DeepEqual(dataset.Frames[frameIndex].Results, input.Frames[frameIndex].Results) {
			t.Fatalf("Horizon frame %d changed before serialization", frameIndex)
		}
	}
	encoded, err := json.Marshal(dataset)
	if err != nil {
		t.Fatal(err)
	}
	var decoded HorizonInteractiveDataset
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for frameIndex := range dataset.Frames {
		if !reflect.DeepEqual(decoded.Frames[frameIndex].Results, dataset.Frames[frameIndex].Results) {
			t.Fatalf("Horizon frame %d changed in JSON round trip", frameIndex)
		}
	}
	destination := filepath.Join(t.TempDir(), "horizon.json")
	if err := SaveHorizonInteractiveDataset(destination, dataset); err != nil {
		t.Fatal(err)
	}
}
