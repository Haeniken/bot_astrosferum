package astrodome

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"bot_astrosferum/internal/app/directional"
	"bot_astrosferum/internal/astronomy"
	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
	"bot_astrosferum/internal/model/iconeu"
)

const (
	CalculationRequestSchemaVersion = 6
	maximumCalculationRequestBytes  = 64 << 10
	maximumNativeForecastHour       = 84
)

type TimeZoneResolver interface {
	Resolve(latitude, longitude float64) string
}

type TerrainSkylineSource interface {
	CacheKey(forecast.Location) (string, bool, error)
	Resolve(context.Context, forecast.Location) (forecast.TerrainSkyline, error)
}

type disabledTerrainSkylineSource struct{}

func (disabledTerrainSkylineSource) Resolve(context.Context, forecast.Location) (forecast.TerrainSkyline, error) {
	return forecast.DisabledTerrainSkyline(), nil
}
func (disabledTerrainSkylineSource) CacheKey(forecast.Location) (string, bool, error) {
	return forecast.TerrainSkylineVersion + ":disabled", true, nil
}

type CurrentManifestLoader func(string) (iconeu.LoadedDomeManifest, error)

type Config struct {
	// Enabled opens Astrodome to every authenticated Telegram identity. When
	// false, configured Telegram admins retain preview access.
	Enabled     bool
	DataRoot    string
	MaxStaleAge time.Duration
	AdminIDs    []int64
	TimeZones   TimeZoneResolver
	Now         func() time.Time
	LoadCurrent CurrentManifestLoader
	Calibration forecast.AstrodomeScienceCalibration
	Terrain     TerrainSkylineSource
}

// Backend performs only admission and immutable-source pinning. Acquisition
// belongs to the ICON-EU scheduler and physical calculation belongs to the
// isolated directional worker.
type Backend struct {
	publicEnabled     bool
	dataRoot          string
	maxStaleAge       time.Duration
	admins            map[int64]struct{}
	timeZones         TimeZoneResolver
	now               func() time.Time
	loadCurrent       CurrentManifestLoader
	calibration       forecast.AstrodomeScienceCalibration
	calibrationDigest string
	terrain           TerrainSkylineSource
}

// CalculationRequest is the language-neutral, immutable worker input. It
// contains native-source identity and requested coordinates, never a derived
// seeing, tau0, cloud transmission, Overall, or quality value.
type CalculationRequest struct {
	SchemaVersion             int                                       `json:"schema_version"`
	SourceIdentity            forecast.AstrodomePrimitiveVolumeIdentity `json:"source_identity"`
	InputContractSHA256       string                                    `json:"input_contract_sha256"`
	StorageProfile            model.StorageProfile                      `json:"storage_profile"`
	RequestedLocation         forecast.Location                         `json:"requested_location"`
	ValidTimes                []time.Time                               `json:"valid_times"`
	GridProfile               forecast.AstrodomeGridProfileID           `json:"grid_profile"`
	GridGeometryDigest        string                                    `json:"grid_geometry_digest"`
	RayGeometryVersion        string                                    `json:"ray_geometry_version"`
	RefractionVersion         string                                    `json:"refraction_version"`
	RefractivityVersion       string                                    `json:"refractivity_version"`
	ScienceVersion            string                                    `json:"science_version"`
	SciencePathVersion        string                                    `json:"science_path_version"`
	DirectionCoordinate       string                                    `json:"direction_coordinate"`
	DirectionReferenceSurface string                                    `json:"direction_reference_surface"`
	DirectionWavelengthM      float64                                   `json:"direction_reference_wavelength_m"`
	ScienceCalibrationVersion string                                    `json:"science_calibration_version"`
	ScienceCalibrationSHA256  string                                    `json:"science_calibration_sha256"`
	CelestialEphemerisVersion string                                    `json:"celestial_ephemeris_version"`
	TerrainSkyline            forecast.TerrainSkyline                   `json:"terrain_skyline"`
	TerrainPreparationKey     string                                    `json:"terrain_preparation_key"`
}

type manifestSnapshot struct {
	manifest   iconeu.LoadedDomeManifest
	profile    forecast.AstrodomeGridProfile
	validTimes []time.Time
	freshness  time.Duration
	stale      bool
}

func NewBackend(config Config) (*Backend, error) {
	if strings.TrimSpace(config.DataRoot) == "" {
		return nil, errors.New("astrodome data root is required")
	}
	if config.MaxStaleAge <= 0 {
		return nil, errors.New("astrodome maximum stale age must be positive")
	}
	if config.TimeZones == nil {
		return nil, errors.New("astrodome time-zone resolver is required")
	}
	if config.Terrain == nil {
		config.Terrain = disabledTerrainSkylineSource{}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.LoadCurrent == nil {
		config.LoadCurrent = iconeu.LoadCurrentDomeManifest
	}
	if err := config.Calibration.Validate(); err != nil {
		return nil, fmt.Errorf("astrodome science calibration: %w", err)
	}
	calibrationDigest, err := config.Calibration.Digest()
	if err != nil {
		return nil, fmt.Errorf("digest astrodome science calibration: %w", err)
	}
	admins := make(map[int64]struct{}, len(config.AdminIDs))
	for _, userID := range config.AdminIDs {
		if userID <= 0 {
			return nil, errors.New("astrodome Telegram admin IDs must be positive")
		}
		admins[userID] = struct{}{}
	}
	return &Backend{
		publicEnabled: config.Enabled, dataRoot: config.DataRoot, maxStaleAge: config.MaxStaleAge,
		admins: admins, timeZones: config.TimeZones, now: config.Now, loadCurrent: config.LoadCurrent,
		calibration: config.Calibration, calibrationDigest: calibrationDigest, terrain: config.Terrain,
	}, nil
}

func (backend *Backend) Availability(ctx context.Context, telegramUserID int64) (directional.AstrodomeAvailability, error) {
	if err := backend.authorize(ctx, telegramUserID); err != nil {
		return directional.AstrodomeAvailability{}, err
	}
	snapshot, reason, err := backend.currentSnapshot(ctx)
	if err != nil {
		return directional.AstrodomeAvailability{}, err
	}
	if reason != "" {
		return directional.AstrodomeAvailability{Enabled: true, Reason: reason}, nil
	}
	availability := directional.AstrodomeAvailability{
		Enabled: true, Available: true, Provider: "icon-eu", RunID: snapshot.manifest.RunID,
		RunBaseTime: snapshot.manifest.BaseTime.UTC(), FreshnessSeconds: int64(snapshot.freshness / time.Second),
		Stale: snapshot.stale, GridProfile: string(snapshot.profile.ID),
	}
	if snapshot.stale {
		availability.Reason = "stale_run"
	}
	return availability, nil
}

func (backend *Backend) Prepare(ctx context.Context, admission directional.AstrodomeAdmission) (directional.PreparedAstrodome, error) {
	if err := backend.authorize(ctx, admission.TelegramUserID); err != nil {
		return directional.PreparedAstrodome{}, err
	}
	if err := forecast.ValidateCoordinates(admission.Point.Latitude, admission.Point.Longitude); err != nil {
		return directional.PreparedAstrodome{}, directional.ErrUnavailable
	}
	location := forecast.Location{
		Latitude: canonicalZero(admission.Point.Latitude), Longitude: canonicalZero(admission.Point.Longitude),
	}
	if !iconeu.Coverage().Contains(location) {
		return directional.PreparedAstrodome{}, directional.ErrUnavailable
	}
	location.TimeZone = strings.TrimSpace(backend.timeZones.Resolve(location.Latitude, location.Longitude))
	if location.TimeZone == "" {
		location.TimeZone = "UTC"
	}
	if _, err := time.LoadLocation(location.TimeZone); err != nil {
		return directional.PreparedAstrodome{}, fmt.Errorf("resolve Astrodome coordinate time zone: %w", err)
	}
	snapshot, reason, err := backend.currentSnapshot(ctx)
	if err != nil {
		return directional.PreparedAstrodome{}, err
	}
	if reason != "" {
		return directional.PreparedAstrodome{}, directional.ErrUnavailable
	}
	geometryDigest, err := snapshot.profile.GeometryDigest()
	if err != nil {
		return directional.PreparedAstrodome{}, err
	}
	terrainPreparationKey, terrainReady, err := backend.terrain.CacheKey(location)
	if err != nil {
		return directional.PreparedAstrodome{}, fmt.Errorf("identify Astrodome Copernicus DEM GLO-30 skyline: %w", err)
	}
	terrainSkyline := forecast.PendingTerrainSkyline(location)
	if terrainReady {
		terrainSkyline, err = backend.terrain.Resolve(ctx, location)
		if err != nil {
			return directional.PreparedAstrodome{}, fmt.Errorf("read Astrodome Copernicus DEM GLO-30 skyline: %w", err)
		}
	}
	request := CalculationRequest{
		SchemaVersion: CalculationRequestSchemaVersion,
		SourceIdentity: forecast.AstrodomePrimitiveVolumeIdentity{
			Provider: snapshot.manifest.Provider, Product: snapshot.manifest.Product,
			Grid: snapshot.manifest.Grid.GridName, RunID: snapshot.manifest.RunID,
			RunBaseTime:          snapshot.manifest.BaseTime.UTC(),
			RunManifestDigest:    "sha256:" + snapshot.manifest.ManifestSHA256,
			InputContractVersion: snapshot.manifest.InputContractVersion,
		},
		InputContractSHA256: "sha256:" + snapshot.manifest.InputContractSHA256,
		StorageProfile:      snapshot.manifest.GridProfile, RequestedLocation: location,
		ValidTimes: append([]time.Time(nil), snapshot.validTimes...), GridProfile: snapshot.profile.ID,
		GridGeometryDigest:        geometryDigest,
		RayGeometryVersion:        forecast.AstrodomeRefractionGeometryVersion,
		RefractionVersion:         forecast.AstrodomeRefractionIntegratorVersion,
		RefractivityVersion:       forecast.AstrodomeCiddorVersion,
		ScienceVersion:            forecast.AstrodomeScienceVersion,
		SciencePathVersion:        forecast.AstrodomeSciencePathContractVersion,
		DirectionCoordinate:       forecast.AstrodomeDirectionCoordinate,
		DirectionReferenceSurface: forecast.AstrodomeDirectionReferenceSurface,
		DirectionWavelengthM:      forecast.AstrodomeDirectionReferenceWavelengthM,
		ScienceCalibrationVersion: backend.calibration.Version,
		ScienceCalibrationSHA256:  backend.calibrationDigest,
		CelestialEphemerisVersion: astronomy.CelestialEphemerisVersion,
		TerrainSkyline:            terrainSkyline,
		TerrainPreparationKey:     terrainPreparationKey,
	}
	payload, scienceCacheKey, err := calculationScienceCacheKey(request)
	if err != nil {
		return directional.PreparedAstrodome{}, err
	}
	_, requestFamilyKey, err := calculationRequestFamilyKey(request)
	if err != nil {
		return directional.PreparedAstrodome{}, err
	}
	return directional.PreparedAstrodome{
		RequestFamilyKey: requestFamilyKey, ScienceCacheKey: scienceCacheKey,
		Source: directional.SourceIdentity{
			Provider: "icon-eu", RunID: snapshot.manifest.RunID, GridProfile: string(snapshot.profile.ID),
			GeometryDigest: geometryDigest,
		},
		Payload: json.RawMessage(payload),
	}, nil
}

func calculationRequestFamilyKey(request CalculationRequest) ([]byte, string, error) {
	request.TerrainSkyline = forecast.PendingTerrainSkyline(request.RequestedLocation)
	return calculationScienceCacheKey(request)
}

func calculationScienceCacheKey(request CalculationRequest) ([]byte, string, error) {
	payload, err := EncodeCalculationRequest(request)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(payload)
	return payload, directional.AstrodomeDatasetWriterVersion + ":sha256:" + hex.EncodeToString(digest[:]), nil
}

func (backend *Backend) authorize(ctx context.Context, telegramUserID int64) error {
	if ctx == nil {
		return errors.New("astrodome backend context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if backend == nil {
		return directional.ErrDisabled
	}
	_, admin := backend.admins[telegramUserID]
	if telegramUserID <= 0 || (!backend.publicEnabled && !admin) {
		return directional.ErrDisabled
	}
	return nil
}

func (backend *Backend) currentSnapshot(ctx context.Context) (manifestSnapshot, string, error) {
	if err := ctx.Err(); err != nil {
		return manifestSnapshot{}, "", err
	}
	loaded, err := backend.loadCurrent(backend.dataRoot)
	if err != nil {
		return manifestSnapshot{}, "dome_run_unavailable", nil
	}
	if err := loaded.Validate(); err != nil || !validRawSHA256(loaded.ManifestSHA256) {
		return manifestSnapshot{}, "dome_run_unavailable", nil
	}
	profile, err := profileSelectedByDiskBudget(loaded.GridProfile)
	if err != nil {
		return manifestSnapshot{}, "storage_profile_unavailable", nil
	}
	now := backend.now().UTC()
	nativeValidTimes := make([]time.Time, len(loaded.ModelSteps))
	for index := range loaded.ModelSteps {
		nativeValidTimes[index] = loaded.ModelSteps[index].ValidAt.UTC()
	}
	validTimes, err := astrodomeWindow(now, loaded.BaseTime.UTC(), nativeValidTimes)
	if err != nil {
		return manifestSnapshot{}, "forecast_window_unavailable", nil
	}
	if err := ctx.Err(); err != nil {
		return manifestSnapshot{}, "", err
	}
	freshness := now.Sub(loaded.BaseTime)
	if freshness < 0 {
		freshness = 0
	}
	return manifestSnapshot{
		manifest: loaded, profile: profile, validTimes: validTimes,
		freshness: freshness, stale: freshness > backend.maxStaleAge,
	}, "", nil
}

// profileSelectedByDiskBudget maps the immutable storage decision to an
// explicit calculation grid. A complete dense primitive volume uses the
// bounded production-v2 angular contract; a disk-constrained sparse volume
// retains its separately versioned legacy sparse contract. Runtime latency or
// a transient calculation error can never change either selection.
func profileSelectedByDiskBudget(storage model.StorageProfile) (forecast.AstrodomeGridProfile, error) {
	var profileID forecast.AstrodomeGridProfileID
	switch storage {
	case model.StorageProfileDense:
		profileID = forecast.AstrodomeGridProductionV2
	case model.StorageProfileSparse:
		profileID = forecast.AstrodomeGridSparseStorageV1
	default:
		return forecast.AstrodomeGridProfile{}, errors.New("dome-ready manifest has no DiskBudget-selected storage profile")
	}
	return forecast.NewAstrodomeGridProfile(profileID)
}

func astrodomeWindow(now, runBase time.Time, nativeValidTimes []time.Time) ([]time.Time, error) {
	if !wholeUTCHour(runBase) {
		return nil, errors.New("astrodome run base time is not a whole UTC hour")
	}
	first := now.UTC().Truncate(time.Hour)
	if !now.UTC().Equal(first) {
		first = first.Add(time.Hour)
	}
	// Surface precipitation and gusts are one-hour intervals, therefore f000
	// cannot be a published Astrodome frame: it has no preceding run term.
	if !first.After(runBase) {
		first = runBase.Add(time.Hour)
	}
	native := make(map[time.Time]struct{}, len(nativeValidTimes))
	for _, validAt := range nativeValidTimes {
		validAt = validAt.UTC()
		if !wholeUTCHour(validAt) || validAt.Before(runBase) {
			return nil, errors.New("ICON-EU Astrodome manifest has an invalid native time")
		}
		native[validAt] = struct{}{}
	}
	validTimes := make([]time.Time, 0, forecast.AstrodomeFrameCount)
	for candidate := first; len(validTimes) < forecast.AstrodomeFrameCount; candidate = candidate.Add(time.Hour) {
		if candidate.After(runBase.Add(maximumNativeForecastHour * time.Hour)) {
			break
		}
		_, currentAvailable := native[candidate]
		_, previousAvailable := native[candidate.Add(-time.Hour)]
		if !currentAvailable || !previousAvailable {
			break
		}
		validTimes = append(validTimes, candidate)
	}
	if len(validTimes) == 0 {
		return nil, errors.New("run from ICON-EU has no consecutive native hourly Astrodome terms")
	}
	return validTimes, nil
}

func (request CalculationRequest) Validate() error {
	if request.SchemaVersion != CalculationRequestSchemaVersion {
		return errors.New("unsupported Astrodome calculation request schema")
	}
	if err := forecast.ValidateAstrodomePrimitiveVolumeIdentity(request.SourceIdentity); err != nil {
		return err
	}
	coverage := iconeu.Coverage()
	if request.SourceIdentity.Provider != "icon-eu" || request.SourceIdentity.Product != iconeu.DomeProductName ||
		request.SourceIdentity.Grid != coverage.GridName || !validPrefixedSHA256(request.SourceIdentity.RunManifestDigest) ||
		request.InputContractSHA256 != "sha256:"+iconeu.DomeInputContractDigest() {
		return errors.New("astrodome calculation request source provenance is invalid")
	}
	parsedRun, err := time.Parse("2006010215", request.SourceIdentity.RunID)
	if err != nil || !parsedRun.Equal(request.SourceIdentity.RunBaseTime) || !wholeUTCHour(request.SourceIdentity.RunBaseTime) {
		return errors.New("astrodome calculation request run identity is invalid")
	}
	if err := forecast.ValidateCoordinates(request.RequestedLocation.Latitude, request.RequestedLocation.Longitude); err != nil ||
		!coverage.Contains(request.RequestedLocation) {
		return errors.New("astrodome calculation request is outside ICON-EU")
	}
	if _, err := time.LoadLocation(request.RequestedLocation.TimeZone); err != nil {
		return errors.New("astrodome calculation request time zone is invalid")
	}
	if err := forecast.ValidateAstrodomeValidTimes(request.ValidTimes); err != nil {
		return err
	}
	if request.ValidTimes[0].Before(request.SourceIdentity.RunBaseTime) ||
		request.ValidTimes[len(request.ValidTimes)-1].After(request.SourceIdentity.RunBaseTime.Add(maximumNativeForecastHour*time.Hour)) {
		return errors.New("astrodome calculation request window exceeds its immutable run")
	}
	storageProfile, err := profileSelectedByDiskBudget(request.StorageProfile)
	if err != nil || storageProfile.ID != request.GridProfile {
		return errors.New("astrodome calculation request storage/grid profile is inconsistent")
	}
	profile, err := forecast.NewAstrodomeGridProfile(request.GridProfile)
	if err != nil {
		return errors.New("astrodome calculation request grid profile is invalid")
	}
	digest, err := profile.GeometryDigest()
	if err != nil || request.GridGeometryDigest != digest {
		return errors.New("astrodome calculation request geometry digest is invalid")
	}
	if request.RayGeometryVersion != forecast.AstrodomeRefractionGeometryVersion ||
		request.RefractionVersion != forecast.AstrodomeRefractionIntegratorVersion ||
		request.RefractivityVersion != forecast.AstrodomeCiddorVersion ||
		request.ScienceVersion != forecast.AstrodomeScienceVersion ||
		request.SciencePathVersion != forecast.AstrodomeSciencePathContractVersion ||
		request.DirectionCoordinate != forecast.AstrodomeDirectionCoordinate ||
		request.DirectionReferenceSurface != forecast.AstrodomeDirectionReferenceSurface ||
		request.DirectionWavelengthM != forecast.AstrodomeDirectionReferenceWavelengthM ||
		request.ScienceCalibrationVersion != forecast.AstrodomeScienceVersion ||
		request.CelestialEphemerisVersion != astronomy.CelestialEphemerisVersion ||
		!validPrefixedSHA256(request.ScienceCalibrationSHA256) {
		return errors.New("astrodome calculation request scientific versions are inconsistent")
	}
	if err := request.TerrainSkyline.Validate(); err != nil {
		return fmt.Errorf("astrodome calculation request terrain skyline: %w", err)
	}
	if strings.TrimSpace(request.TerrainPreparationKey) == "" {
		return errors.New("astrodome calculation request terrain preparation identity is missing")
	}
	if !request.TerrainSkyline.MatchesLocation(request.RequestedLocation) {
		return errors.New("astrodome calculation request terrain skyline belongs to another location")
	}
	return nil
}

func EncodeCalculationRequest(request CalculationRequest) ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(request)
}

func DecodeCalculationRequest(reader io.Reader) (CalculationRequest, error) {
	if reader == nil {
		return CalculationRequest{}, errors.New("astrodome calculation request reader is required")
	}
	encoded, err := io.ReadAll(io.LimitReader(reader, maximumCalculationRequestBytes+1))
	if err != nil {
		return CalculationRequest{}, err
	}
	if len(encoded) > maximumCalculationRequestBytes {
		return CalculationRequest{}, errors.New("astrodome calculation request is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var request CalculationRequest
	if err := decoder.Decode(&request); err != nil {
		return CalculationRequest{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return CalculationRequest{}, errors.New("astrodome calculation request must contain exactly one JSON value")
	}
	if err := request.Validate(); err != nil {
		return CalculationRequest{}, err
	}
	return request, nil
}

func validRawSHA256(value string) bool {
	if value != strings.ToLower(value) || len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validPrefixedSHA256(value string) bool {
	return strings.HasPrefix(value, "sha256:") && validRawSHA256(strings.TrimPrefix(value, "sha256:"))
}

func canonicalZero(value float64) float64 {
	if value == 0 {
		return 0
	}
	return value
}

func wholeUTCHour(value time.Time) bool {
	_, offset := value.Zone()
	return !value.IsZero() && offset == 0 && value.Minute() == 0 && value.Second() == 0 && value.Nanosecond() == 0
}
