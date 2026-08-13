package astrodome

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"bot_astrosferum/internal/app/directional"
	"bot_astrosferum/internal/astronomy"
	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
	"bot_astrosferum/internal/model/iconeu"
)

type fixedTimeZone string

func (zone fixedTimeZone) Resolve(float64, float64) string { return string(zone) }

func TestBackendPublicAndAdminAccessUsesManifestProfile(t *testing.T) {
	t.Parallel()
	manifest := astrodomeManifestFixture(t, model.StorageProfileDense)
	now := manifest.BaseTime.Add(6*time.Hour + time.Minute)
	loader := func(string) (iconeu.LoadedDomeManifest, error) { return manifest, nil }

	public, err := NewBackend(Config{
		Enabled: true, DataRoot: "/unused", MaxStaleAge: 12 * time.Hour,
		TimeZones: fixedTimeZone("Europe/Moscow"), Now: func() time.Time { return now }, LoadCurrent: loader,
		Calibration: forecast.DefaultAstrodomeScienceCalibration(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if availability, err := public.Availability(context.Background(), 700); err != nil || !availability.Enabled || !availability.Available {
		t.Fatalf("public availability = %+v, %v", availability, err)
	}

	adminOnly, err := NewBackend(Config{
		Enabled: false, AdminIDs: []int64{42}, DataRoot: "/unused", MaxStaleAge: 12 * time.Hour,
		TimeZones: fixedTimeZone("Europe/Moscow"), Now: func() time.Time { return now }, LoadCurrent: loader,
		Calibration: forecast.DefaultAstrodomeScienceCalibration(),
	})
	if err != nil {
		t.Fatal(err)
	}
	availability, err := adminOnly.Availability(context.Background(), 42)
	if err != nil || availability.GridProfile != string(forecast.AstrodomeGridProductionV2) {
		t.Fatalf("admin-only availability = %+v, %v", availability, err)
	}
	preparedAdmin, err := adminOnly.Prepare(context.Background(), directional.AstrodomeAdmission{
		TelegramUserID: 42, Point: directional.SavedPoint{Latitude: 55, Longitude: 37},
	})
	if err != nil {
		t.Fatalf("admin-only admission: %v", err)
	}
	adminRequest, err := DecodeCalculationRequest(bytes.NewReader(preparedAdmin.Payload))
	if err != nil || adminRequest.StorageProfile != model.StorageProfileDense ||
		adminRequest.GridProfile != forecast.AstrodomeGridProductionV2 {
		t.Fatalf("admin-only request = %+v, %v", adminRequest, err)
	}
	if _, err := adminOnly.Availability(context.Background(), 43); !errors.Is(err, directional.ErrDisabled) {
		t.Fatalf("non-admin error = %v, want disabled", err)
	}
	if _, err := adminOnly.Prepare(context.Background(), directional.AstrodomeAdmission{TelegramUserID: 43}); !errors.Is(err, directional.ErrDisabled) {
		t.Fatalf("non-admin admission error = %v, want disabled", err)
	}

	closed, err := NewBackend(Config{
		Enabled: false, DataRoot: "/unused", MaxStaleAge: 12 * time.Hour,
		TimeZones: fixedTimeZone("UTC"), Now: func() time.Time { return now }, LoadCurrent: loader,
		Calibration: forecast.DefaultAstrodomeScienceCalibration(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := closed.Availability(context.Background(), 42); !errors.Is(err, directional.ErrDisabled) {
		t.Fatalf("empty-admin error = %v, want disabled", err)
	}
}

func TestBackendPreparePinsProductionGridForDenseStorageAndRollingWindow(t *testing.T) {
	t.Parallel()
	manifest := astrodomeManifestFixture(t, model.StorageProfileDense)
	now := manifest.BaseTime.Add(6*time.Hour + time.Minute)
	backend, err := NewBackend(Config{
		Enabled: true, DataRoot: "/unused", MaxStaleAge: 5 * time.Hour,
		TimeZones: fixedTimeZone("Europe/Moscow"), Now: func() time.Time { return now },
		Calibration: forecast.DefaultAstrodomeScienceCalibration(),
		LoadCurrent: func(string) (iconeu.LoadedDomeManifest, error) { return manifest, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	availability, err := backend.Availability(context.Background(), 100)
	if err != nil || !availability.Available || !availability.Stale || availability.Reason != "stale_run" ||
		availability.GridProfile != string(forecast.AstrodomeGridProductionV2) {
		t.Fatalf("availability = %+v, %v", availability, err)
	}
	admission := directional.AstrodomeAdmission{
		TelegramUserID: 100, Point: directional.SavedPoint{ID: 9, Name: "private", Latitude: 59.9, Longitude: 30.2},
		Language: "ru", IdempotencyKey: "0123456789abcdef",
	}
	prepared, err := backend.Prepare(context.Background(), admission)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Source.Provider != "icon-eu" || prepared.Source.RunID != manifest.RunID ||
		prepared.Source.GridProfile != string(forecast.AstrodomeGridProductionV2) || !strings.HasPrefix(prepared.Source.GeometryDigest, "sha256:") {
		t.Fatalf("prepared source = %+v", prepared.Source)
	}
	request, err := DecodeCalculationRequest(bytes.NewReader(prepared.Payload))
	if err != nil {
		t.Fatal(err)
	}
	wantFirst := manifest.BaseTime.Add(7 * time.Hour)
	if len(request.ValidTimes) != forecast.AstrodomeFrameCount || !request.ValidTimes[0].Equal(wantFirst) ||
		!request.ValidTimes[len(request.ValidTimes)-1].Equal(wantFirst.Add(71*time.Hour)) {
		t.Fatalf("valid-time axis = %v .. %v (%d)", request.ValidTimes[0], request.ValidTimes[len(request.ValidTimes)-1], len(request.ValidTimes))
	}
	if request.SourceIdentity.RunManifestDigest != "sha256:"+manifest.ManifestSHA256 ||
		request.InputContractSHA256 != "sha256:"+iconeu.DomeInputContractDigest() ||
		request.StorageProfile != model.StorageProfileDense || request.GridProfile != forecast.AstrodomeGridProductionV2 ||
		request.RayGeometryVersion != forecast.AstrodomeRefractionGeometryVersion ||
		request.RefractionVersion != forecast.AstrodomeRefractionIntegratorVersion ||
		request.RefractivityVersion != forecast.AstrodomeCiddorVersion ||
		request.CelestialEphemerisVersion != astronomy.CelestialEphemerisVersion {
		t.Fatalf("calculation request provenance = %+v", request)
	}
	if strings.Contains(string(prepared.Payload), admission.Point.Name) || strings.Contains(string(prepared.Payload), `"language"`) {
		t.Fatal("presentation/private point metadata leaked into the science payload")
	}

	admission.Point.Name = "another private name"
	admission.Language = "en"
	again, err := backend.Prepare(context.Background(), admission)
	if err != nil {
		t.Fatal(err)
	}
	if again.ScienceCacheKey != prepared.ScienceCacheKey || !bytes.Equal(again.Payload, prepared.Payload) {
		t.Fatal("non-scientific metadata changed the cache identity")
	}
	if !strings.HasPrefix(prepared.ScienceCacheKey, directional.AstrodomeDatasetWriterVersion+":sha256:") {
		t.Fatalf("calculation cache identity = %q", prepared.ScienceCacheKey)
	}
}

func TestCalculationRequestRejectsStaleCelestialEphemeris(t *testing.T) {
	t.Parallel()
	manifest := astrodomeManifestFixture(t, model.StorageProfileDense)
	backend, err := NewBackend(Config{
		Enabled: true, DataRoot: "/unused", MaxStaleAge: 12 * time.Hour,
		TimeZones: fixedTimeZone("UTC"), Now: func() time.Time { return manifest.BaseTime },
		Calibration: forecast.DefaultAstrodomeScienceCalibration(),
		LoadCurrent: func(string) (iconeu.LoadedDomeManifest, error) { return manifest, nil },
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
	request, err := DecodeCalculationRequest(bytes.NewReader(prepared.Payload))
	if err != nil {
		t.Fatal(err)
	}
	request.CelestialEphemerisVersion = "celestial-horizontal-distance-aspect-jpl-meeus-v2"
	if err := request.Validate(); err == nil {
		t.Fatal("stale celestial ephemeris version was accepted")
	}
}

func TestBackendMapsSparseDiskDecisionAndFailsClosedOutsideCoverageOrWindow(t *testing.T) {
	t.Parallel()
	manifest := astrodomeManifestFixture(t, model.StorageProfileSparse)
	now := manifest.BaseTime.Add(4 * time.Hour)
	backend, err := NewBackend(Config{
		Enabled: true, DataRoot: "/unused", MaxStaleAge: 12 * time.Hour,
		TimeZones: fixedTimeZone("UTC"), Now: func() time.Time { return now },
		Calibration: forecast.DefaultAstrodomeScienceCalibration(),
		LoadCurrent: func(string) (iconeu.LoadedDomeManifest, error) { return manifest, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := backend.Prepare(context.Background(), directional.AstrodomeAdmission{
		TelegramUserID: 1, Point: directional.SavedPoint{Latitude: 55, Longitude: 37},
	})
	if err != nil || prepared.Source.GridProfile != string(forecast.AstrodomeGridSparseStorageV1) {
		t.Fatalf("sparse prepared = %+v, %v", prepared, err)
	}
	if _, err := backend.Prepare(context.Background(), directional.AstrodomeAdmission{
		TelegramUserID: 1, Point: directional.SavedPoint{Latitude: 0, Longitude: 0},
	}); !errors.Is(err, directional.ErrUnavailable) {
		t.Fatalf("outside-footprint error = %v", err)
	}

	backend.now = func() time.Time { return manifest.BaseTime.Add(78*time.Hour + time.Minute) }
	availability, err := backend.Availability(context.Background(), 1)
	if err != nil || availability.Available || availability.Reason != "forecast_window_unavailable" {
		t.Fatalf("uncovered availability = %+v, %v", availability, err)
	}
	if _, err := backend.Prepare(context.Background(), directional.AstrodomeAdmission{
		TelegramUserID: 1, Point: directional.SavedPoint{Latitude: 55, Longitude: 37},
	}); !errors.Is(err, directional.ErrUnavailable) {
		t.Fatalf("uncovered admission error = %v", err)
	}
}

func TestBackendStopsRollingWindowBeforeNativeHourlyCadenceGap(t *testing.T) {
	t.Parallel()
	manifest := astrodomeManifestFixture(t, model.StorageProfileSparse)
	backend, err := NewBackend(Config{
		Enabled: true, DataRoot: "/unused", MaxStaleAge: 12 * time.Hour,
		TimeZones:   fixedTimeZone("UTC"),
		Calibration: forecast.DefaultAstrodomeScienceCalibration(),
		Now:         func() time.Time { return manifest.BaseTime.Add(8*time.Hour + time.Minute) },
		LoadCurrent: func(string) (iconeu.LoadedDomeManifest, error) {
			return manifest, nil
		},
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
	request, err := DecodeCalculationRequest(bytes.NewReader(prepared.Payload))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(request.ValidTimes), 70; got != want {
		t.Fatalf("short rolling window has %d frames, want %d", got, want)
	}
	if got, want := request.ValidTimes[0], manifest.BaseTime.Add(9*time.Hour); !got.Equal(want) {
		t.Fatalf("first valid time = %s, want %s", got, want)
	}
	if got, want := request.ValidTimes[len(request.ValidTimes)-1], manifest.BaseTime.Add(78*time.Hour); !got.Equal(want) {
		t.Fatalf("last valid time = %s, want %s", got, want)
	}
}

func TestCalculationRequestDecoderRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	manifest := astrodomeManifestFixture(t, model.StorageProfileSparse)
	backend, err := NewBackend(Config{
		Enabled: true, DataRoot: "/unused", MaxStaleAge: 12 * time.Hour,
		TimeZones: fixedTimeZone("UTC"), Now: func() time.Time { return manifest.BaseTime },
		Calibration: forecast.DefaultAstrodomeScienceCalibration(),
		LoadCurrent: func(string) (iconeu.LoadedDomeManifest, error) { return manifest, nil },
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
	schemaPrefix := []byte(fmt.Sprintf(`{"schema_version":%d`, CalculationRequestSchemaVersion))
	mutatedPrefix := []byte(fmt.Sprintf(`{"unknown":true,"schema_version":%d`, CalculationRequestSchemaVersion))
	mutated := bytes.Replace(prepared.Payload, schemaPrefix, mutatedPrefix, 1)
	if bytes.Equal(mutated, prepared.Payload) {
		t.Fatal("calculation request fixture did not contain its schema prefix")
	}
	if _, err := DecodeCalculationRequest(bytes.NewReader(mutated)); err == nil {
		t.Fatal("unknown calculation request field was accepted")
	}
}

func TestBackendCalibrationChangesRequestAndCacheIdentity(t *testing.T) {
	t.Parallel()
	manifest := astrodomeManifestFixture(t, model.StorageProfileDense)
	baseTime := manifest.BaseTime.Add(6 * time.Hour)
	loader := func(string) (iconeu.LoadedDomeManifest, error) { return manifest, nil }
	newBackend := func(calibration forecast.AstrodomeScienceCalibration) *Backend {
		backend, err := NewBackend(Config{
			Enabled: true, DataRoot: "/unused", MaxStaleAge: 12 * time.Hour,
			TimeZones: fixedTimeZone("UTC"), Now: func() time.Time { return baseTime },
			LoadCurrent: loader, Calibration: calibration,
		})
		if err != nil {
			t.Fatal(err)
		}
		return backend
	}
	defaultCalibration := forecast.DefaultAstrodomeScienceCalibration()
	customOverall := defaultCalibration.Overall
	customOverall.CloudWeight = 2.5
	customCalibration, err := forecast.AstrodomeScienceCalibrationWithOverall(customOverall)
	if err != nil {
		t.Fatal(err)
	}
	admission := directional.AstrodomeAdmission{
		TelegramUserID: 1, Point: directional.SavedPoint{Latitude: 55, Longitude: 37},
	}
	baseline, err := newBackend(defaultCalibration).Prepare(context.Background(), admission)
	if err != nil {
		t.Fatal(err)
	}
	custom, err := newBackend(customCalibration).Prepare(context.Background(), admission)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.ScienceCacheKey == custom.ScienceCacheKey || bytes.Equal(baseline.Payload, custom.Payload) {
		t.Fatal("custom Astrodome calibration reused the baseline request/cache identity")
	}
	request, err := DecodeCalculationRequest(bytes.NewReader(custom.Payload))
	if err != nil {
		t.Fatal(err)
	}
	wantDigest, err := customCalibration.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if request.ScienceCalibrationSHA256 != wantDigest ||
		request.ScienceCalibrationVersion != customCalibration.Version {
		t.Fatalf("custom calibration provenance = %+v, want %s", request, wantDigest)
	}
}

func astrodomeManifestFixture(t *testing.T, storage model.StorageProfile) iconeu.LoadedDomeManifest {
	t.Helper()
	base := time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC)
	runID := base.Format("2006010215")
	manifest := iconeu.NewDomeManifest(runID, base, strings.Repeat("a", 64))
	manifest.PublishedAt = base.Add(4 * time.Hour)
	manifest.GridProfile = storage
	manifest.Complete = true
	manifest.Geometry = iconeu.DomeStepFile{
		Source: iconeu.DomeFileSourceBaseRun, ForecastHour: 0, ValidAt: base,
		File:  fmt.Sprintf("runs/%s/cloud-hourly-v5-full-hhl/geometry.grib2", runID),
		Bytes: 100, AllocatedBytes: 512, SHA256: strings.Repeat("b", 64), Messages: len(iconeu.DomeHalfModelLevels()),
	}
	const baseCloudMessages = 187
	modelMessages := len(iconeu.DomeFullModelLevels())*8 + len(iconeu.DomeHalfModelLevels())*2
	for _, hour := range iconeu.DomeNativeForecastHours() {
		validAt := base.Add(time.Duration(hour) * time.Hour)
		step := iconeu.DomeModelStep{ForecastHour: hour, ValidAt: validAt, Messages: modelMessages}
		if hour <= 78 {
			step.Parts = append(step.Parts, iconeu.DomeStepFile{
				Source: iconeu.DomeFileSourceBaseRun, ForecastHour: hour, ValidAt: validAt,
				File:  fmt.Sprintf("runs/%s/cloud-hourly-v5-full-hhl/f%03d.grib2", runID, hour),
				Bytes: 100, AllocatedBytes: 512, SHA256: strings.Repeat("c", 64), Messages: baseCloudMessages,
			})
		}
		step.Parts = append(step.Parts, iconeu.DomeStepFile{
			Source: iconeu.DomeFileSourceDomeRun, ForecastHour: hour, ValidAt: validAt,
			File:  fmt.Sprintf("dome-runs/%s/contract/model/f%03d.grib2", runID, hour),
			Bytes: 100, AllocatedBytes: 512, SHA256: strings.Repeat("d", 64),
			Messages: func() int {
				if hour <= 78 {
					return modelMessages - baseCloudMessages
				}
				return modelMessages
			}(),
		})
		manifest.ModelSteps = append(manifest.ModelSteps, step)
	}
	for _, hour := range []int{81, 84} {
		manifest.SurfaceExtensionSteps = append(manifest.SurfaceExtensionSteps, iconeu.DomeStepFile{
			Source: iconeu.DomeFileSourceDomeRun, ForecastHour: hour, ValidAt: base.Add(time.Duration(hour) * time.Hour),
			File:  fmt.Sprintf("dome-runs/%s/contract/surface/f%03d.grib2", runID, hour),
			Bytes: 100, AllocatedBytes: 512, SHA256: strings.Repeat("e", 64), Messages: iconeu.SurfaceBundleSchemaVersion,
		})
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("fixture manifest: %v", err)
	}
	return iconeu.LoadedDomeManifest{
		DomeManifest: manifest, Directory: "/unused/dome-runs/" + runID + "/contract",
		ManifestSHA256: strings.Repeat("f", 64),
	}
}
