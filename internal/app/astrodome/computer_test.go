package astrodome

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"bot_astrosferum/internal/app/directional"
	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
	"bot_astrosferum/internal/model/iconeu"
)

func TestNewComputerAppliesOnlyOperationalDefaults(t *testing.T) {
	t.Parallel()

	computer, err := NewComputer(ComputerConfig{
		DataRoot: "/data", ECCodesWorkers: 2,
		ScienceCalibration: forecast.DefaultAstrodomeScienceCalibration(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if computer.nodeWorkers != max(1, runtime.GOMAXPROCS(0)) ||
		computer.residentLimitBytes != iconeu.DomeAstrodomeDefaultResidentLimitBytes || computer.logf == nil {
		t.Fatalf("computer defaults = %+v", computer)
	}
	for _, config := range []ComputerConfig{
		{ECCodesWorkers: 1, ScienceCalibration: forecast.DefaultAstrodomeScienceCalibration()},
		{DataRoot: "/data", ECCodesWorkers: 0, ScienceCalibration: forecast.DefaultAstrodomeScienceCalibration()},
		{DataRoot: "/data", ECCodesWorkers: 17, ScienceCalibration: forecast.DefaultAstrodomeScienceCalibration()},
		{DataRoot: "/data", ECCodesWorkers: 1, NodeWorkers: 65, ScienceCalibration: forecast.DefaultAstrodomeScienceCalibration()},
		{DataRoot: "/data", ECCodesWorkers: 1},
	} {
		if _, err := NewComputer(config); err == nil {
			t.Fatalf("invalid computer config %+v was accepted", config)
		}
	}
}

func TestComputerRejectsBotWorkerCalibrationMismatchBeforeModelAccess(t *testing.T) {
	t.Parallel()
	manifest := astrodomeManifestFixture(t, model.StorageProfileDense)
	baseline := forecast.DefaultAstrodomeScienceCalibration()
	backend, err := NewBackend(Config{
		Enabled: true, DataRoot: "/unused", MaxStaleAge: 12 * time.Hour,
		TimeZones: fixedTimeZone("UTC"), Now: func() time.Time { return manifest.BaseTime.Add(6 * time.Hour) },
		LoadCurrent: func(string) (iconeu.LoadedDomeManifest, error) { return manifest, nil },
		Calibration: baseline,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := backend.Prepare(context.Background(), directional.AstrodomeAdmission{
		TelegramUserID: 1, Point: directional.SavedPoint{Latitude: 55, Longitude: 37},
	})
	if err != nil {
		t.Fatal(err)
	}
	customOverall := baseline.Overall
	customOverall.CloudWeight = 2.5
	custom, err := forecast.AstrodomeScienceCalibrationWithOverall(customOverall)
	if err != nil {
		t.Fatal(err)
	}
	computer, err := NewComputer(ComputerConfig{
		DataRoot: "/definitely-not-read", ECCodesWorkers: 1, ScienceCalibration: custom,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = computer.ComputeAstrodomeDataset(context.Background(), prepared.Source, prepared.Payload)
	var coded directional.CodedError
	if !errors.As(err, &coded) || coded.Code != "calibration_mismatch" {
		t.Fatalf("calibration mismatch error = %v", err)
	}
}

func TestAstrodomeUnavailableNodePreservesDatasetIdentity(t *testing.T) {
	t.Parallel()

	azimuth := 22.5
	validAt := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	identity := forecast.AstrodomePrimitiveVolumeIdentity{Provider: "icon-eu", RunID: "2026073006"}
	site := forecast.AstrodomeScienceSiteInputs{ForecastLeadHours: 36}
	node := astrodomeUnavailableNode(forecast.AstrodomeGridNode{
		ElevationDegrees: 10, AzimuthDegrees: &azimuth,
	}, validAt, identity, site, errors.New("preview boundary isolation"))
	if node.Available || node.State != forecast.AstrodomeScienceNodeUnavailable ||
		node.UnavailableReason != "preview boundary isolation" ||
		node.ScienceVersion != forecast.AstrodomeScienceVersion ||
		node.SourceIdentity != identity || !node.ValidAt.Equal(validAt) ||
		node.AzimuthDegrees == nil || *node.AzimuthDegrees != azimuth ||
		node.Quality.Category != forecast.AstrodomeScienceQualityUnavailable ||
		node.Quality.TemporalResolutionHours != 1 ||
		node.Quality.LeadTimeQualityHeuristic != forecast.AstrodomeForecastLeadTimeQualityHeuristic(36) {
		t.Fatalf("unavailable preview node = %+v", node)
	}
}

func TestAstrodomeDatasetLocationsKeepPresentationTimeZoneOutOfModelLocation(t *testing.T) {
	t.Parallel()

	requested, model := astrodomeDatasetLocations(forecast.Location{
		Latitude: 59.9386, Longitude: 30.3141, TimeZone: "Europe/Moscow",
	}, 17.5)
	if requested.TimeZone != "Europe/Moscow" {
		t.Fatalf("requested location time zone = %q", requested.TimeZone)
	}
	if model.TimeZone != "" {
		t.Fatalf("model location carries presentation time zone %q", model.TimeZone)
	}
	if model.SurfaceElevationM == nil || *model.SurfaceElevationM != 17.5 {
		t.Fatalf("model surface elevation = %v", model.SurfaceElevationM)
	}
}
