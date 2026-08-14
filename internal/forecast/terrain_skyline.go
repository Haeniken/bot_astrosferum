package forecast

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

const (
	TerrainSkylineVersion            = "terrain-skyline-glo30-native-cell-v2"
	TerrainSkylineSourceGLO30        = "copernicus-dem-glo30-2021"
	TerrainSkylineVerticalDatum      = "EGM2008"
	TerrainSkylineAzimuthStepDeg     = 1.0
	TerrainSkylineAzimuthCount       = 360
	TerrainSkylineMaximumDistanceM   = 61_000.0
	TerrainSkylineAbsoluteHeightMaxM = 10_500.0
	TerrainSkylineSamplingMethod     = "native_grid_cell_centres"
	TerrainSkylineApertureHeightAGLM = 2.0
	TerrainSkylineSectorWidthDeg     = 45.0
	TerrainSkylineCoordinateStepDeg  = 1e-5
)

// TerrainSkyline is a static, direct-geometric skyline derived from one DSM.
// It is independent of forecast time and is never used as an atmospheric
// lower boundary; ICON HHL retains that separate role.
type TerrainSkyline struct {
	Version                 string                 `json:"version"`
	Source                  string                 `json:"source"`
	VerticalDatum           string                 `json:"vertical_datum"`
	Geometry                string                 `json:"geometry"`
	ObserverLatitude        float64                `json:"observer_latitude"`
	ObserverLongitude       float64                `json:"observer_longitude"`
	ObserverElevationM      float64                `json:"observer_elevation_m"`
	AzimuthStepDegrees      float64                `json:"azimuth_step_degrees"`
	MaximumSurfaceDistanceM float64                `json:"maximum_surface_distance_m"`
	SamplingMethod          string                 `json:"sampling_method"`
	InputManifest           []TerrainSkylineInput  `json:"input_manifest"`
	InputManifestSHA256     string                 `json:"input_manifest_sha256"`
	Samples                 []TerrainSkylineSample `json:"samples"`
	HorizonSectors          []TerrainSkylineSector `json:"horizon_sectors"`
	DigestSHA256            string                 `json:"digest_sha256"`
}

// TerrainSkylineInput binds a derived skyline to the exact immutable source
// objects inspected by GDAL. The profile digest includes this manifest.
type TerrainSkylineInput struct {
	TileID       string  `json:"tile_id"`
	ObjectETag   string  `json:"object_etag"`
	ObjectBytes  int64   `json:"object_bytes"`
	SHA256       string  `json:"sha256"`
	RasterWidth  int     `json:"raster_width"`
	RasterHeight int     `json:"raster_height"`
	NoDataValue  float64 `json:"nodata_value"`
	Scale        float64 `json:"scale"`
	Offset       float64 `json:"offset"`
	Unit         string  `json:"unit"`
}

type TerrainSkylineSample struct {
	AzimuthDegrees           float64 `json:"azimuth_degrees"`
	ElevationDegrees         float64 `json:"elevation_degrees"`
	ObstacleSurfaceDistanceM float64 `json:"obstacle_surface_distance_m"`
}

// TerrainSkylineSector is one half-open 45-degree Horizon sector. Samples at
// the shared boundary belong to the following sector, so no sample is ever
// counted twice.
type TerrainSkylineSector struct {
	Direction               HorizonDirection `json:"direction"`
	CenterAzimuthDegrees    float64          `json:"center_azimuth_degrees"`
	StartAzimuthDegrees     float64          `json:"start_azimuth_degrees"`
	EndAzimuthDegrees       float64          `json:"end_azimuth_degrees"`
	SampleCount             int              `json:"sample_count"`
	MeanElevationDegrees    float64          `json:"mean_elevation_degrees"`
	MaximumElevationDegrees float64          `json:"maximum_elevation_degrees"`
}

func DisabledTerrainSkyline() TerrainSkyline {
	return TerrainSkyline{
		Version: TerrainSkylineVersion, Source: "disabled", Geometry: "direct_spherical",
		InputManifest: []TerrainSkylineInput{}, Samples: []TerrainSkylineSample{}, HorizonSectors: []TerrainSkylineSector{},
	}
}

// PendingTerrainSkyline is a queue-safe placeholder. It has no scientific
// value and must be replaced by Resolve inside the admitted runner before any
// calculation, cache publication, or serialized result.
func PendingTerrainSkyline(location Location) TerrainSkyline {
	return TerrainSkyline{
		Version: TerrainSkylineVersion, Source: "pending", Geometry: "direct_spherical",
		ObserverLatitude: location.Latitude, ObserverLongitude: location.Longitude,
		InputManifest: []TerrainSkylineInput{}, Samples: []TerrainSkylineSample{}, HorizonSectors: []TerrainSkylineSector{},
	}
}

func (profile TerrainSkyline) Enabled() bool {
	return profile.Source != "disabled" && profile.Source != "pending"
}

func (profile TerrainSkyline) MatchesLocation(location Location) bool {
	if profile.Source == "disabled" {
		return true
	}
	longitudeDifference := math.Abs(profile.ObserverLongitude - location.Longitude)
	longitudeDifference = math.Min(longitudeDifference, 360-longitudeDifference)
	return math.Abs(profile.ObserverLatitude-location.Latitude) <= TerrainSkylineCoordinateStepDeg/2+1e-12 &&
		longitudeDifference <= TerrainSkylineCoordinateStepDeg/2+1e-12
}

func (profile TerrainSkyline) Validate() error {
	if profile.Version != TerrainSkylineVersion || profile.Geometry != "direct_spherical" {
		return fmt.Errorf("terrain skyline version or geometry is unsupported")
	}
	if profile.Source == "disabled" {
		if profile.VerticalDatum != "" || profile.ObserverLatitude != 0 || profile.ObserverLongitude != 0 ||
			profile.ObserverElevationM != 0 || profile.AzimuthStepDegrees != 0 ||
			profile.MaximumSurfaceDistanceM != 0 || profile.SamplingMethod != "" || len(profile.InputManifest) != 0 ||
			profile.InputManifestSHA256 != "" ||
			len(profile.Samples) != 0 || len(profile.HorizonSectors) != 0 || profile.DigestSHA256 != "" {
			return fmt.Errorf("disabled terrain skyline must not contain data")
		}
		return nil
	}
	if profile.Source == "pending" {
		if err := ValidateCoordinates(profile.ObserverLatitude, profile.ObserverLongitude); err != nil ||
			profile.VerticalDatum != "" || profile.ObserverElevationM != 0 || profile.AzimuthStepDegrees != 0 ||
			profile.MaximumSurfaceDistanceM != 0 || profile.SamplingMethod != "" || len(profile.InputManifest) != 0 ||
			profile.InputManifestSHA256 != "" || len(profile.Samples) != 0 || len(profile.HorizonSectors) != 0 || profile.DigestSHA256 != "" {
			return fmt.Errorf("pending terrain skyline must contain only its location")
		}
		return nil
	}
	if profile.Source != TerrainSkylineSourceGLO30 || profile.VerticalDatum != TerrainSkylineVerticalDatum {
		return fmt.Errorf("terrain skyline source provenance is unsupported")
	}
	if err := ValidateCoordinates(profile.ObserverLatitude, profile.ObserverLongitude); err != nil ||
		!finite(profile.ObserverElevationM) || profile.ObserverElevationM < -500 || profile.ObserverElevationM > 10_000 ||
		profile.AzimuthStepDegrees != TerrainSkylineAzimuthStepDeg ||
		profile.MaximumSurfaceDistanceM != TerrainSkylineMaximumDistanceM ||
		profile.SamplingMethod != TerrainSkylineSamplingMethod || len(profile.InputManifest) == 0 {
		return fmt.Errorf("terrain skyline geometry metadata is invalid")
	}
	for index, input := range profile.InputManifest {
		expectedWidth, ok := GLO30TileRasterWidth(input.TileID)
		if input.TileID == "" || input.ObjectETag == "" || input.ObjectBytes <= 0 ||
			!lowerHexSHA256(input.SHA256) || !ok || input.RasterWidth != expectedWidth || input.RasterHeight != 3600 ||
			input.NoDataValue != -32767 || input.Scale != 1 || input.Offset != 0 || input.Unit != "m" {
			return fmt.Errorf("terrain skyline input %d is invalid", index)
		}
		if index > 0 && profile.InputManifest[index-1].TileID >= input.TileID {
			return fmt.Errorf("terrain skyline inputs are not strictly ordered")
		}
	}
	manifestDigest, err := terrainSkylineManifestDigest(profile.InputManifest)
	if err != nil || profile.InputManifestSHA256 != manifestDigest {
		return fmt.Errorf("terrain skyline input manifest digest is invalid")
	}
	if len(profile.Samples) != TerrainSkylineAzimuthCount || len(profile.HorizonSectors) != HorizonDirectionCount {
		return fmt.Errorf("terrain skyline has invalid sample or sector count")
	}
	for index, sample := range profile.Samples {
		if sample.AzimuthDegrees != float64(index) || !finite(sample.ElevationDegrees) ||
			sample.ElevationDegrees < -90 || sample.ElevationDegrees > 90 ||
			!finite(sample.ObstacleSurfaceDistanceM) || sample.ObstacleSurfaceDistanceM <= 0 ||
			sample.ObstacleSurfaceDistanceM > profile.MaximumSurfaceDistanceM {
			return fmt.Errorf("terrain skyline sample %d is invalid", index)
		}
	}
	expected := terrainSkylineSectors(profile.Samples)
	for index, sector := range profile.HorizonSectors {
		wanted := expected[index]
		if sector.Direction != wanted.Direction || sector.CenterAzimuthDegrees != wanted.CenterAzimuthDegrees ||
			sector.StartAzimuthDegrees != wanted.StartAzimuthDegrees || sector.EndAzimuthDegrees != wanted.EndAzimuthDegrees ||
			sector.SampleCount != wanted.SampleCount || math.Abs(sector.MeanElevationDegrees-wanted.MeanElevationDegrees) > 1e-12 ||
			math.Abs(sector.MaximumElevationDegrees-wanted.MaximumElevationDegrees) > 1e-12 {
			return fmt.Errorf("terrain skyline sector %d is inconsistent with its unique samples", index)
		}
	}
	digest, err := profile.calculateDigest()
	if err != nil || profile.DigestSHA256 != digest {
		return fmt.Errorf("terrain skyline digest is invalid")
	}
	return nil
}

func NewTerrainSkyline(latitude, longitude, observerElevationM float64, inputs []TerrainSkylineInput, samples []TerrainSkylineSample) (TerrainSkyline, error) {
	profile := TerrainSkyline{
		Version: TerrainSkylineVersion, Source: TerrainSkylineSourceGLO30,
		VerticalDatum: TerrainSkylineVerticalDatum, Geometry: "direct_spherical",
		ObserverLatitude: latitude, ObserverLongitude: longitude, ObserverElevationM: observerElevationM,
		AzimuthStepDegrees:      TerrainSkylineAzimuthStepDeg,
		MaximumSurfaceDistanceM: TerrainSkylineMaximumDistanceM,
		SamplingMethod:          TerrainSkylineSamplingMethod,
		InputManifest:           append([]TerrainSkylineInput(nil), inputs...),
		Samples:                 append([]TerrainSkylineSample(nil), samples...),
	}
	sort.Slice(profile.InputManifest, func(left, right int) bool {
		return profile.InputManifest[left].TileID < profile.InputManifest[right].TileID
	})
	manifestDigest, err := terrainSkylineManifestDigest(profile.InputManifest)
	if err != nil {
		return TerrainSkyline{}, err
	}
	profile.InputManifestSHA256 = manifestDigest
	profile.HorizonSectors = terrainSkylineSectors(profile.Samples)
	digest, err := profile.calculateDigest()
	if err != nil {
		return TerrainSkyline{}, err
	}
	profile.DigestSHA256 = digest
	if err := profile.Validate(); err != nil {
		return TerrainSkyline{}, err
	}
	return profile, nil
}

// NewSyntheticTerrainSkyline is restricted to deterministic tests. Runtime
// profiles must always bind exact source tiles through NewTerrainSkyline.
func NewSyntheticTerrainSkyline(latitude, longitude, observerElevationM float64, samples []TerrainSkylineSample) (TerrainSkyline, error) {
	return NewTerrainSkyline(latitude, longitude, observerElevationM, []TerrainSkylineInput{{
		TileID: "Copernicus_DSM_COG_10_N00_00_E000_00_DEM", ObjectETag: "synthetic", ObjectBytes: 1,
		SHA256: strings.Repeat("0", sha256.Size*2), RasterWidth: 3600, RasterHeight: 3600,
		NoDataValue: -32767, Scale: 1, Unit: "m",
	}}, samples)
}

// GLO30TileRasterWidth returns the exact longitude sample count of an AWS
// Copernicus DEM GLO-30 Public COG. Tile IDs encode the south-west degree;
// southern bands therefore use the equator-facing edge (latitude+1).
func GLO30TileRasterWidth(tileID string) (int, bool) {
	latitude, _, ok := parseGLO30TileID(tileID)
	if !ok {
		return 0, false
	}
	bandLatitude := latitude
	if bandLatitude < 0 {
		bandLatitude = -(bandLatitude + 1)
	}
	switch {
	case bandLatitude < 50:
		return 3600, true
	case bandLatitude < 60:
		return 2400, true
	case bandLatitude < 70:
		return 1800, true
	case bandLatitude < 80:
		return 1200, true
	case bandLatitude < 85:
		return 720, true
	default:
		return 360, true
	}
}

func parseGLO30TileID(tileID string) (latitude, longitude int, ok bool) {
	const prefix = "Copernicus_DSM_COG_10_"
	if len(tileID) != len(prefix)+18 || !strings.HasPrefix(tileID, prefix) ||
		tileID[len(prefix)+3:len(prefix)+6] != "_00" || tileID[len(prefix)+11:len(prefix)+14] != "_00" ||
		tileID[len(prefix)+14:] != "_DEM" {
		return 0, 0, false
	}
	latText := tileID[len(prefix)+1 : len(prefix)+3]
	lonStart := len(prefix) + 7
	lonText := tileID[lonStart+1 : lonStart+4]
	lat, latErr := strconv.Atoi(latText)
	lon, lonErr := strconv.Atoi(lonText)
	if latErr != nil || lonErr != nil || lat > 90 || lon > 180 {
		return 0, 0, false
	}
	switch tileID[len(prefix)] {
	case 'N':
		latitude = lat
	case 'S':
		latitude = -lat
	default:
		return 0, 0, false
	}
	switch tileID[lonStart] {
	case 'E':
		longitude = lon
	case 'W':
		longitude = -lon
	default:
		return 0, 0, false
	}
	if latitude < -90 || latitude > 89 || longitude < -180 || longitude > 179 ||
		(latitude == 0 && tileID[len(prefix)] != 'N') || (longitude == 0 && tileID[lonStart] != 'E') {
		return 0, 0, false
	}
	return latitude, longitude, true
}

func terrainSkylineManifestDigest(inputs []TerrainSkylineInput) (string, error) {
	encoded, err := json.Marshal(inputs)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func lowerHexSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func (profile TerrainSkyline) ElevationAt(azimuthDegrees float64) (float64, error) {
	if err := profile.Validate(); err != nil {
		return 0, err
	}
	if !profile.Enabled() {
		return 0, fmt.Errorf("terrain skyline is disabled")
	}
	azimuth := normalizeTerrainAzimuth(azimuthDegrees)
	lower := int(math.Floor(azimuth))
	upper := (lower + 1) % len(profile.Samples)
	fraction := azimuth - float64(lower)
	return profile.Samples[lower].ElevationDegrees*(1-fraction) + profile.Samples[upper].ElevationDegrees*fraction, nil
}

func (profile TerrainSkyline) Sector(direction HorizonDirection) (TerrainSkylineSector, bool) {
	for _, sector := range profile.HorizonSectors {
		if sector.Direction == direction {
			return sector, true
		}
	}
	return TerrainSkylineSector{}, false
}

func terrainSkylineSectors(samples []TerrainSkylineSample) []TerrainSkylineSector {
	sectors := make([]TerrainSkylineSector, HorizonDirectionCount)
	sums := make([]float64, HorizonDirectionCount)
	for index, fixed := range fixedHorizonDirections {
		sectors[index] = TerrainSkylineSector{
			Direction: fixed.direction, CenterAzimuthDegrees: fixed.azimuth,
			StartAzimuthDegrees:     normalizeTerrainAzimuth(fixed.azimuth - TerrainSkylineSectorWidthDeg/2),
			EndAzimuthDegrees:       normalizeTerrainAzimuth(fixed.azimuth + TerrainSkylineSectorWidthDeg/2),
			MaximumElevationDegrees: -90,
		}
	}
	for _, sample := range samples {
		// Shifting by half a sector converts the circular half-open ownership
		// [center-22.5, center+22.5) into an ordinary floor operation.
		index := int(math.Floor(normalizeTerrainAzimuth(sample.AzimuthDegrees+TerrainSkylineSectorWidthDeg/2)/TerrainSkylineSectorWidthDeg)) % HorizonDirectionCount
		sectors[index].SampleCount++
		sums[index] += sample.ElevationDegrees
		sectors[index].MaximumElevationDegrees = math.Max(sectors[index].MaximumElevationDegrees, sample.ElevationDegrees)
	}
	for index := range sectors {
		if sectors[index].SampleCount > 0 {
			sectors[index].MeanElevationDegrees = sums[index] / float64(sectors[index].SampleCount)
		}
	}
	return sectors
}

func (profile TerrainSkyline) calculateDigest() (string, error) {
	copyProfile := profile
	copyProfile.DigestSHA256 = ""
	encoded, err := json.Marshal(copyProfile)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func normalizeTerrainAzimuth(value float64) float64 {
	value = math.Mod(value, 360)
	if value < 0 {
		value += 360
	}
	return value
}
