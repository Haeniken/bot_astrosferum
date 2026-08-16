package directional

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"time"

	"bot_astrosferum/internal/astronomy"
	"bot_astrosferum/internal/forecast"
)

const (
	AstrodomeDatasetSchemaVersion = 8
	// AstrodomeDatasetWriterVersion participates in the calculation cache key.
	// Increment it when a writer correction requires regeneration while the
	// already-declared dataset schema and scientific meaning remain unchanged.
	AstrodomeDatasetWriterVersion = "astrodome-dataset-writer-v9-observing-ephemerides"
	maximumAstrodomeDatasetBytes  = 128 << 20
)

const (
	AstrodomeDatasetStateValid             = "valid"
	AstrodomeDatasetStatePrecipitationVeto = "precipitation_veto"
	AstrodomeDatasetStateTerrainBlocked    = "terrain_blocked"
	AstrodomeDatasetStateUnavailable       = "unavailable"
)

const (
	AstrodomeTwilightDay          = "day"
	AstrodomeTwilightLight        = "light_twilight"
	AstrodomeTwilightAstronomical = "astronomical_twilight"
	AstrodomeTwilightNight        = "astronomical_night"
)

var astrodomePenaltyKeys = [...]string{
	forecast.OverallPenaltyOpticalTurbulence,
	forecast.OverallPenaltyCloudObstruction,
	forecast.OverallPenaltySurfaceWind,
	forecast.OverallPenaltyFog,
	forecast.OverallPenaltyPrecipitation,
}

// AstrodomeDataset is the canonical language-neutral browser payload. It
// contains physical values and diagnostics only; all human-readable labels
// are selected by the browser locale.
type AstrodomeDataset struct {
	SchemaVersion                 int                        `json:"schema_version"`
	Provider                      string                     `json:"provider"`
	ModelProduct                  string                     `json:"model_product"`
	ModelGrid                     string                     `json:"model_grid"`
	RunID                         string                     `json:"run_id"`
	RunManifestDigest             string                     `json:"run_manifest_digest"`
	SourceColumnPlanDigest        string                     `json:"source_column_plan_digest"`
	RunBaseTime                   time.Time                  `json:"run_base_time"`
	GeneratedAt                   time.Time                  `json:"generated_at"`
	RequestedLocation             AstrodomeDatasetLocation   `json:"requested_location"`
	ModelLocation                 AstrodomeDatasetLocation   `json:"model_location"`
	InputContractVersion          string                     `json:"input_contract_version"`
	GeometryVersion               string                     `json:"geometry_version"`
	RayGeometryVersion            string                     `json:"ray_geometry_version"`
	GridProfile                   string                     `json:"grid_profile"`
	GridGeometryDigest            string                     `json:"grid_geometry_digest"`
	RefractionVersion             string                     `json:"refraction_version"`
	RefractivityVersion           string                     `json:"refractivity_version"`
	DirectionCoordinate           string                     `json:"direction_coordinate"`
	DirectionReferenceSurface     string                     `json:"direction_reference_surface"`
	DirectionReferenceWavelengthM float64                    `json:"direction_reference_wavelength_m"`
	VacuumDirectionAvailable      *bool                      `json:"vacuum_direction_available,omitempty"`
	ScienceVersion                string                     `json:"science_version"`
	SciencePathVersion            string                     `json:"science_path_version,omitempty"`
	CalibrationVersion            string                     `json:"calibration_version"`
	CalibrationSHA256             string                     `json:"science_calibration_sha256,omitempty"`
	CelestialEphemerisVersion     string                     `json:"celestial_ephemeris_version"`
	CelestialTracks               []astronomy.CelestialTrack `json:"celestial_tracks"`
	PolarisTrack                  astronomy.PolarisTrack     `json:"polaris_track"`
	TerrainSkyline                forecast.TerrainSkyline    `json:"terrain_skyline"`
	Grid                          AstrodomeDatasetGrid       `json:"grid"`
	ValidTimes                    []time.Time                `json:"valid_times"`
	Frames                        []AstrodomeDatasetFrame    `json:"frames"`
}

type AstrodomeDatasetLocation struct {
	Latitude          float64  `json:"latitude"`
	Longitude         float64  `json:"longitude"`
	TimeZone          string   `json:"time_zone,omitempty"`
	SurfaceElevationM *float64 `json:"surface_elevation_m,omitempty"`
}

type AstrodomeDatasetGrid struct {
	FrameCount int                          `json:"frame_count"`
	NodeCount  int                          `json:"node_count"`
	Rings      []forecast.AstrodomeGridRing `json:"rings"`
	Zenith     AstrodomeDatasetZenith       `json:"zenith"`
}

type AstrodomeDatasetZenith struct {
	ElevationDegrees float64  `json:"elevation_deg"`
	AzimuthDegrees   *float64 `json:"azimuth"`
}

type AstrodomeDatasetFrame struct {
	ValidAt          time.Time                     `json:"valid_at"`
	SurfaceCommon    AstrodomeDatasetSurfaceCommon `json:"surface_common"`
	SolarAltitudeDeg float64                       `json:"solar_altitude_deg"`
	TwilightBand     string                        `json:"twilight_band"`
	Nodes            []AstrodomeDatasetNode        `json:"nodes"`
}

// AstrodomeDatasetSurfaceCommon contains observer-local operational inputs
// shared by every direction in the frame. It deliberately does not duplicate
// these values into thousands of nodes.
type AstrodomeDatasetSurfaceCommon struct {
	WindSpeed10MMS             float64                               `json:"wind_speed_10m_ms"`
	WindGust10MMS              float64                               `json:"wind_gust_10m_ms"`
	FogHeuristic               forecast.AstrodomeScienceFogHeuristic `json:"fog_heuristic"`
	PrecipitationRateMMPerHour float64                               `json:"precipitation_rate_mm_per_hour"`
	ForecastLeadHours          float64                               `json:"forecast_lead_hours"`
}

type AstrodomeDatasetECEFVector struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

type AstrodomeDatasetFactors struct {
	OpticalTurbulence float64 `json:"optical_turbulence"`
	CloudObstruction  float64 `json:"cloud_obstruction"`
	SurfaceWind       float64 `json:"surface_wind"`
	Fog               float64 `json:"fog"`
	Precipitation     float64 `json:"precipitation"`
}

// AstrodomeDatasetQuality keeps temporal resolution in hours. It is an input
// diagnostic, not a dimensionless confidence score and not a probability.
type AstrodomeDatasetQuality struct {
	LeadTimeQualityHeuristic float64  `json:"lead_time_quality_heuristic"`
	GeometryCoverage         float64  `json:"geometry_coverage"`
	TurbulencePathCoverage   float64  `json:"turbulence_path_coverage"`
	CloudPathCoverage        float64  `json:"cloud_path_coverage"`
	HumidityPathCoverage     *float64 `json:"humidity_path_coverage"`
	TemporalResolutionHours  float64  `json:"temporal_resolution_hours"`
	QuadratureConvergence    float64  `json:"quadrature_convergence"`
	ApproximationLengthM     *float64 `json:"approximation_length_m,omitempty"`
	TopClosure               float64  `json:"top_closure"`
	MandatoryComplete        bool     `json:"mandatory_complete"`
	ReasonCodes              []string `json:"reason_codes"`
}

// AstrodomeDatasetNumericalError contains diagnostics defined by the pinned
// science version: the production lineage introduced in v24 uses propagated
// embedded-quadrature estimates, while archived repeat-verification contracts
// retain their observed pass deltas.
// Nil means undefined or no physical result; it is never an invented zero.
type AstrodomeDatasetNumericalError struct {
	OverallAbsolute            *float64 `json:"overall_absolute"`
	TurbulenceIntegralRelative *float64 `json:"turbulence_integral_relative"`
	IntegratedCn2Relative      *float64 `json:"integrated_cn2_relative"`
	WindWeightedCn2Relative    *float64 `json:"wind_weighted_cn2_relative"`
	CloudTransmissionAbsolute  *float64 `json:"cloud_transmission_absolute"`
}

type AstrodomeDatasetNode struct {
	AzimuthDegrees                                    *float64                                   `json:"azimuth_deg"`
	ElevationDegrees                                  float64                                    `json:"elevation_deg"`
	State                                             string                                     `json:"state"`
	TerrainObstructionSource                          forecast.AstrodomeTerrainObstructionSource `json:"terrain_obstruction_source"`
	TerrainSkylineElevationDegrees                    *float64                                   `json:"terrain_skyline_elevation_deg"`
	TerrainSkylineHasObstructionAtEvaluationDirection *bool                                      `json:"terrain_skyline_has_obstruction_at_evaluation_direction"`
	GeometryMode                                      string                                     `json:"geometry_mode"`
	Overall                                           *float64                                   `json:"overall"`
	SeeingArcsec500NM                                 *float64                                   `json:"seeing_arcsec_500nm"`
	Tau0MS500NM                                       *float64                                   `json:"tau0_ms_500nm"`
	Tau0ConservativeMS500NM                           *float64                                   `json:"tau0_conservative_ms_500nm"`
	Tau0UnboundedAbove                                bool                                       `json:"tau0_unbounded_above"`
	IntegratedCn2                                     *float64                                   `json:"J"`
	WindWeightedCn2                                   *float64                                   `json:"JV"`
	DirectionAtModelTopECEF                           *AstrodomeDatasetECEFVector                `json:"direction_at_model_top_ecef"`
	NominalCloudTransmission                          *float64                                   `json:"nominal_cloud_transmission"`
	ConservativeCloudTransmission                     *float64                                   `json:"conservative_cloud_transmission"`
	EffectiveCloudTransmission                        *float64                                   `json:"effective_cloud_transmission"`
	CloudOpticalDepthLiquid                           *float64                                   `json:"cloud_optical_depth_liquid"`
	CloudOpticalDepthIce                              *float64                                   `json:"cloud_optical_depth_ice"`
	SlantWaterVapourKgM2                              *float64                                   `json:"slant_water_vapour_kg_m2"`
	Factors                                           *AstrodomeDatasetFactors                   `json:"factors"`
	PenaltyContributions                              []forecast.OverallPenaltyContribution      `json:"penalty_contributions"`
	PenaltyLossFraction                               *float64                                   `json:"penalty_loss_fraction"`
	LimitingFactor                                    string                                     `json:"limiting_factor"`
	DataQuality                                       forecast.AstrodomeScienceQualityCategory   `json:"data_quality"`
	QualityComponents                                 AstrodomeDatasetQuality                    `json:"quality_components"`
	NumericalError                                    AstrodomeDatasetNumericalError             `json:"numerical_error"`
}

// AstrodomeDatasetInput is produced by a calculation/acquisition adapter. The
// dataset builder does not know the concrete ICON volume store or runner.
type AstrodomeDatasetInput struct {
	AdmissionScienceCacheKey string
	FinalScienceCacheKey     string
	SourceIdentity           forecast.AstrodomePrimitiveVolumeIdentity
	SourceColumnPlanDigest   string
	GeneratedAt              time.Time
	RequestedLocation        AstrodomeDatasetLocation
	ModelLocation            AstrodomeDatasetLocation
	Profile                  forecast.AstrodomeGridProfile
	RayGeometryVersion       string
	RefractionVersion        string
	RefractivityVersion      string
	CalibrationVersion       string
	CalibrationSHA256        string
	CelestialTracks          []astronomy.CelestialTrack
	PolarisTrack             astronomy.PolarisTrack
	TerrainSkyline           forecast.TerrainSkyline
	Frames                   []AstrodomeDatasetFrameInput
}

type AstrodomeDatasetFrameInput struct {
	ValidAt          time.Time
	Surface          forecast.AstrodomeScienceSiteInputs
	SolarAltitudeDeg float64
	Nodes            []forecast.AstrodomeScienceNode
}

// BuildAstrodomeDataset maps fully recomputed physical nodes to the browser
// shape and validates its complete consecutive one-to-72-frame product.
func BuildAstrodomeDataset(input AstrodomeDatasetInput) (AstrodomeDataset, error) {
	if input.TerrainSkyline.Version == "" {
		input.TerrainSkyline = forecast.DisabledTerrainSkyline()
	}
	if err := forecast.ValidateAstrodomePrimitiveVolumeIdentity(input.SourceIdentity); err != nil {
		return AstrodomeDataset{}, err
	}
	if input.SourceIdentity.Provider != "icon-eu" || !validPrefixedSHA256(input.SourceIdentity.RunManifestDigest) {
		return AstrodomeDataset{}, errors.New("astrodome dataset requires a canonical ICON-EU manifest identity")
	}
	if err := input.TerrainSkyline.Validate(); err != nil {
		return AstrodomeDataset{}, fmt.Errorf("astrodome terrain skyline: %w", err)
	}
	if input.TerrainSkyline.Source == "pending" {
		return AstrodomeDataset{}, errors.New("astrodome terrain skyline preparation is incomplete")
	}
	if len(input.Frames) == 0 || len(input.Frames) > forecast.AstrodomeFrameCount {
		return AstrodomeDataset{}, fmt.Errorf("astrodome input has %d frames, want 1..%d", len(input.Frames), forecast.AstrodomeFrameCount)
	}
	profile := input.Profile
	if err := profile.Validate(); err != nil {
		return AstrodomeDataset{}, err
	}
	nodeDefinitions, err := profile.Nodes()
	if err != nil {
		return AstrodomeDataset{}, err
	}
	digest, err := profile.GeometryDigest()
	if err != nil {
		return AstrodomeDataset{}, err
	}
	terrainElevations := make([]*float64, len(nodeDefinitions))
	terrainObstructions := make([]bool, len(nodeDefinitions))
	for nodeIndex, definition := range nodeDefinitions {
		terrainElevations[nodeIndex], terrainObstructions[nodeIndex], err = astrodomeTerrainSkylineNodeInformation(
			input.TerrainSkyline, definition,
		)
		if err != nil {
			return AstrodomeDataset{}, fmt.Errorf("astrodome terrain skyline node %d: %w", nodeIndex, err)
		}
	}
	validTimes := make([]time.Time, len(input.Frames))
	frames := make([]AstrodomeDatasetFrame, len(input.Frames))
	for frameIndex, frameInput := range input.Frames {
		validTimes[frameIndex] = frameInput.ValidAt.UTC()
		if !wholeUTCHour(frameInput.ValidAt) || !frameInput.ValidAt.Equal(frameInput.Surface.ValidAt) ||
			!astrodomeIdentityEqual(frameInput.Surface.SourceIdentity, input.SourceIdentity) {
			return AstrodomeDataset{}, fmt.Errorf("astrodome frame %d surface input is not pinned to the dataset source and time", frameIndex)
		}
		if !frameInput.Surface.PrecipitationIntervalEnd.Equal(frameInput.ValidAt) ||
			frameInput.Surface.PrecipitationIntervalEnd.Sub(frameInput.Surface.PrecipitationIntervalStart) != time.Hour ||
			!isUTC(frameInput.Surface.PrecipitationIntervalStart) || !isUTC(frameInput.Surface.PrecipitationIntervalEnd) {
			return AstrodomeDataset{}, fmt.Errorf("astrodome frame %d precipitation is not an explicit one-hour UTC accumulation", frameIndex)
		}
		if len(frameInput.Nodes) != len(nodeDefinitions) {
			return AstrodomeDataset{}, fmt.Errorf("astrodome frame %d has %d science nodes, want %d", frameIndex, len(frameInput.Nodes), len(nodeDefinitions))
		}
		mapped := make([]AstrodomeDatasetNode, len(nodeDefinitions))
		for nodeIndex, definition := range nodeDefinitions {
			if !frameInput.Nodes[nodeIndex].ValidAt.Equal(frameInput.ValidAt) ||
				!astrodomeIdentityEqual(frameInput.Nodes[nodeIndex].SourceIdentity, input.SourceIdentity) ||
				frameInput.Nodes[nodeIndex].ScienceVersion != forecast.AstrodomeScienceVersion ||
				!astrodomeNodeVersionsMatch(frameInput.Nodes[nodeIndex], input) {
				return AstrodomeDataset{}, fmt.Errorf("astrodome frame %d node %d is not pinned to the dataset source, time, and science version", frameIndex, nodeIndex)
			}
			mapped[nodeIndex], err = MapAstrodomeScienceNode(definition, frameInput.Nodes[nodeIndex])
			if err != nil {
				return AstrodomeDataset{}, fmt.Errorf("map astrodome frame %d node %d: %w", frameIndex, nodeIndex, err)
			}
			mapped[nodeIndex].TerrainSkylineElevationDegrees = cloneFloat(terrainElevations[nodeIndex])
			mapped[nodeIndex].TerrainSkylineHasObstructionAtEvaluationDirection = boolPointer(terrainObstructions[nodeIndex])
		}
		frames[frameIndex] = AstrodomeDatasetFrame{
			ValidAt: frameInput.ValidAt.UTC(), SurfaceCommon: mapAstrodomeSurface(frameInput.Surface),
			SolarAltitudeDeg: frameInput.SolarAltitudeDeg,
			TwilightBand:     astrodomeTwilightBand(frameInput.SolarAltitudeDeg), Nodes: mapped,
		}
	}
	vacuumDirectionAvailable := false
	dataset := AstrodomeDataset{
		SchemaVersion: AstrodomeDatasetSchemaVersion, Provider: "icon-eu",
		ModelProduct: input.SourceIdentity.Product, ModelGrid: input.SourceIdentity.Grid,
		RunID: input.SourceIdentity.RunID, RunManifestDigest: input.SourceIdentity.RunManifestDigest,
		SourceColumnPlanDigest: input.SourceColumnPlanDigest,
		RunBaseTime:            input.SourceIdentity.RunBaseTime.UTC(), GeneratedAt: input.GeneratedAt.UTC(),
		RequestedLocation: input.RequestedLocation, ModelLocation: input.ModelLocation,
		InputContractVersion: input.SourceIdentity.InputContractVersion,
		GeometryVersion:      forecast.AstrodomeGridGeometryVersion,
		RayGeometryVersion:   input.RayGeometryVersion,
		GridProfile:          string(profile.ID), GridGeometryDigest: digest,
		RefractionVersion: input.RefractionVersion, RefractivityVersion: input.RefractivityVersion,
		DirectionCoordinate:           forecast.AstrodomeDirectionCoordinate,
		DirectionReferenceSurface:     forecast.AstrodomeDirectionReferenceSurface,
		DirectionReferenceWavelengthM: forecast.AstrodomeDirectionReferenceWavelengthM,
		VacuumDirectionAvailable:      &vacuumDirectionAvailable,
		ScienceVersion:                forecast.AstrodomeScienceVersion,
		SciencePathVersion:            forecast.AstrodomeSciencePathContractVersion,
		CalibrationVersion:            input.CalibrationVersion,
		CalibrationSHA256:             input.CalibrationSHA256,
		CelestialEphemerisVersion:     astronomy.CelestialEphemerisVersion,
		CelestialTracks:               input.CelestialTracks,
		PolarisTrack:                  input.PolarisTrack,
		TerrainSkyline:                input.TerrainSkyline,
		Grid: AstrodomeDatasetGrid{
			FrameCount: len(frames), NodeCount: profile.NodeCount(),
			Rings:  append([]forecast.AstrodomeGridRing(nil), profile.Rings...),
			Zenith: AstrodomeDatasetZenith{ElevationDegrees: forecast.AstrodomeZenithElevationDegrees},
		},
		ValidTimes: validTimes, Frames: frames,
	}
	if err := dataset.Validate(); err != nil {
		return AstrodomeDataset{}, err
	}
	return dataset, nil
}

func astrodomeTerrainSkylineNodeInformation(
	profile forecast.TerrainSkyline,
	definition forecast.AstrodomeGridNode,
) (*float64, bool, error) {
	if !profile.Enabled() || definition.AzimuthDegrees == nil {
		return nil, false, nil
	}
	elevation, err := profile.ElevationAt(*definition.AzimuthDegrees)
	if err != nil {
		return nil, false, err
	}
	return &elevation, definition.ElevationDegrees <= elevation, nil
}

func MapAstrodomeScienceNode(definition forecast.AstrodomeGridNode, source forecast.AstrodomeScienceNode) (AstrodomeDatasetNode, error) {
	if source.ValidAt.IsZero() || !sameNullableFloat(source.AzimuthDegrees, definition.AzimuthDegrees, 1e-7) ||
		math.Abs(source.ElevationDegrees-definition.ElevationDegrees) > 1e-6 {
		return AstrodomeDatasetNode{}, errors.New("science node does not match the canonical grid node")
	}
	expectedAvailable := source.State == forecast.AstrodomeScienceNodeAvailable || source.State == forecast.AstrodomeScienceNodeTerrainGrazing
	if source.Available != expectedAvailable {
		return AstrodomeDatasetNode{}, errors.New("science node availability contradicts its state")
	}
	quality := mapAstrodomeQuality(source)
	terrainSource := source.TerrainObstructionSource
	if terrainSource == "" && source.State != forecast.AstrodomeScienceNodeTerrainBlocked {
		terrainSource = forecast.AstrodomeTerrainObstructionNone
	}
	result := AstrodomeDatasetNode{
		AzimuthDegrees: cloneFloat(definition.AzimuthDegrees), ElevationDegrees: definition.ElevationDegrees,
		State: mapAstrodomeNodeState(source), GeometryMode: source.GeometryMode,
		TerrainObstructionSource: terrainSource,
		Tau0UnboundedAbove:       source.Tau0UnboundedAbove,
		DirectionAtModelTopECEF:  cloneAstrodomeECEFVector(source.DirectionAtModelTopECEF),
		PenaltyContributions:     []forecast.OverallPenaltyContribution{}, LimitingFactor: "unavailable_data",
		DataQuality: source.Quality.Category, QualityComponents: quality,
		NumericalError: AstrodomeDatasetNumericalError{},
	}
	if !source.Available || source.State == forecast.AstrodomeScienceNodeUnavailable ||
		source.State == forecast.AstrodomeScienceNodeTerrainBlocked {
		if source.State == forecast.AstrodomeScienceNodeTerrainBlocked {
			switch source.TerrainObstructionSource {
			case forecast.AstrodomeTerrainObstructionHHL:
				result.LimitingFactor = "coarse_terrain"
			default:
				return AstrodomeDatasetNode{}, errors.New("terrain-blocked science node is not an ICON HHL obstruction")
			}
		}
		return result, nil
	}
	if source.IntegratedCn2 == nil || source.WindWeightedCn2 == nil || source.LiquidOpticalDepth == nil ||
		source.IceOpticalDepth == nil || source.Seeing500Arcsec == nil ||
		source.CloudTransmissionNominal == nil || source.CloudTransmissionConservative == nil ||
		source.CloudTransmission == nil ||
		source.Overall == nil || source.Factors == nil || source.NumericalError == nil {
		return AstrodomeDatasetNode{}, errors.New("available science node is missing mandatory physical output")
	}
	result.Overall = cloneFloat(source.Overall)
	result.SeeingArcsec500NM = cloneFloat(source.Seeing500Arcsec)
	result.Tau0MS500NM = cloneFloat(source.Tau0500MS)
	result.Tau0ConservativeMS500NM = cloneFloat(source.Tau0ConservativeScoreMS)
	result.IntegratedCn2 = floatPointer(source.IntegratedCn2.Value)
	result.WindWeightedCn2 = floatPointer(source.WindWeightedCn2.Value)
	result.NominalCloudTransmission = cloneFloat(source.CloudTransmissionNominal)
	result.ConservativeCloudTransmission = cloneFloat(source.CloudTransmissionConservative)
	result.EffectiveCloudTransmission = cloneFloat(source.CloudTransmissionConservative)
	result.CloudOpticalDepthLiquid = floatPointer(source.LiquidOpticalDepth.Value)
	result.CloudOpticalDepthIce = floatPointer(source.IceOpticalDepth.Value)
	if source.SlantWaterKgM2 != nil {
		result.SlantWaterVapourKgM2 = floatPointer(source.SlantWaterKgM2.Value)
	}
	result.Factors = &AstrodomeDatasetFactors{
		OpticalTurbulence: source.Factors.Turbulence, CloudObstruction: source.Factors.Cloud,
		SurfaceWind: source.Factors.SurfaceWind, Fog: source.Factors.Fog,
		Precipitation: source.Factors.Precipitation,
	}
	contributions, err := canonicalAstrodomeContributions(source.PenaltyContributions)
	if err != nil {
		return AstrodomeDatasetNode{}, err
	}
	loss, err := canonicalAstrodomePenaltyLoss(*result.Factors)
	if err != nil {
		return AstrodomeDatasetNode{}, err
	}
	result.PenaltyContributions = contributions
	result.PenaltyLossFraction = floatPointer(loss)
	result.LimitingFactor = astrodomeLimitingFactor(contributions)
	result.NumericalError = AstrodomeDatasetNumericalError{
		OverallAbsolute:            floatPointer(source.NumericalError.OverallAbsolute),
		TurbulenceIntegralRelative: cloneFloat(source.NumericalError.TurbulenceIntegralRelative),
		IntegratedCn2Relative:      cloneFloat(source.NumericalError.IntegratedCn2Relative),
		WindWeightedCn2Relative:    cloneFloat(source.NumericalError.WindWeightedCn2Relative),
		CloudTransmissionAbsolute:  floatPointer(source.NumericalError.CloudTransmissionAbsolute),
	}
	return result, nil
}

// EncodeAstrodomeDataset validates before publication so a partial or
// malformed calculation cannot enter the immutable result cache.
func EncodeAstrodomeDataset(dataset AstrodomeDataset) ([]byte, error) {
	if err := dataset.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(dataset)
}

// DecodeAstrodomeDataset is strict: unknown fields, trailing values, and
// oversized payloads are rejected before the scientific invariants run.
func DecodeAstrodomeDataset(reader io.Reader) (AstrodomeDataset, error) {
	return decodeAstrodomeDataset(reader)
}

// DecodeAstrodomeDatasetBytes validates an already bounded immutable payload
// without copying it through another io.ReadAll allocation.
func DecodeAstrodomeDatasetBytes(encoded []byte) (AstrodomeDataset, error) {
	return decodeAstrodomeDatasetBytes(encoded)
}

// DecodeArchivedAstrodomeDataset reads an immutable saved visualization while
// enforcing the exact current contract. Its separate name documents the
// storage boundary; it does not provide legacy scientific compatibility.
func DecodeArchivedAstrodomeDataset(reader io.Reader) (AstrodomeDataset, error) {
	return decodeAstrodomeDataset(reader)
}

func decodeAstrodomeDataset(reader io.Reader) (AstrodomeDataset, error) {
	if reader == nil {
		return AstrodomeDataset{}, errors.New("astrodome dataset reader is required")
	}
	encoded, err := io.ReadAll(io.LimitReader(reader, maximumAstrodomeDatasetBytes+1))
	if err != nil {
		return AstrodomeDataset{}, err
	}
	if len(encoded) > maximumAstrodomeDatasetBytes {
		return AstrodomeDataset{}, errors.New("astrodome dataset exceeds the maximum decoded size")
	}
	return decodeAstrodomeDatasetBytes(encoded)
}

func decodeAstrodomeDatasetBytes(encoded []byte) (AstrodomeDataset, error) {
	if len(encoded) > maximumAstrodomeDatasetBytes {
		return AstrodomeDataset{}, errors.New("astrodome dataset exceeds the maximum decoded size")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var dataset AstrodomeDataset
	if err := decoder.Decode(&dataset); err != nil {
		return AstrodomeDataset{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return AstrodomeDataset{}, errors.New("astrodome dataset must contain exactly one JSON value")
	}
	if err := dataset.Validate(); err != nil {
		return AstrodomeDataset{}, err
	}
	return dataset, nil
}

func mapAstrodomeSurface(source forecast.AstrodomeScienceSiteInputs) AstrodomeDatasetSurfaceCommon {
	return AstrodomeDatasetSurfaceCommon{
		WindSpeed10MMS: source.WindSpeed10MMS, WindGust10MMS: source.WindGust10MMS,
		FogHeuristic: source.FogHeuristic, PrecipitationRateMMPerHour: source.PrecipitationRateMMPerHour,
		ForecastLeadHours: source.ForecastLeadHours,
	}
}

func mapAstrodomeQuality(source forecast.AstrodomeScienceNode) AstrodomeDatasetQuality {
	convergence, topClosure := 0.0, 0.0
	approximationLengthM := source.Quality.ApproximationLengthM
	if source.Quality.QuadratureConverged {
		convergence = 1
	}
	if source.Quality.TopClosed {
		topClosure = 1
	}
	return AstrodomeDatasetQuality{
		LeadTimeQualityHeuristic: source.Quality.LeadTimeQualityHeuristic, GeometryCoverage: source.Quality.GeometryCoverage,
		TurbulencePathCoverage:  source.Quality.TurbulencePathCoverage,
		CloudPathCoverage:       source.Quality.CloudPathCoverage,
		HumidityPathCoverage:    cloneFloat(source.Quality.HumidityPathCoverage),
		TemporalResolutionHours: source.Quality.TemporalResolutionHours,
		QuadratureConvergence:   convergence,
		ApproximationLengthM:    &approximationLengthM,
		TopClosure:              topClosure,
		MandatoryComplete:       source.Available,
		ReasonCodes:             astrodomeQualityReasons(source),
	}
}

func astrodomeQualityReasons(source forecast.AstrodomeScienceNode) []string {
	reasons := make([]string, 0, 8)
	appendReason := func(condition bool, code string) {
		if condition {
			reasons = append(reasons, code)
		}
	}
	appendReason(source.State == forecast.AstrodomeScienceNodeTerrainBlocked, "terrain_blocked")
	appendReason(source.State == forecast.AstrodomeScienceNodeTerrainGrazing, "terrain_grazing")
	appendReason(!source.Quality.QuadratureConverged, "quadrature_not_converged")
	appendReason(source.Quality.ApproximationLengthM > 0, "short_path_approximation")
	appendReason(!source.Quality.TopClosed, "model_top_not_closed")
	appendReason(source.Quality.GeometryCoverage < 1, "geometry_coverage_limited")
	appendReason(source.Quality.TurbulencePathCoverage < 1, "turbulence_coverage_limited")
	appendReason(source.Quality.CloudPathCoverage < 1, "cloud_coverage_limited")
	appendReason(source.Quality.HumidityPathCoverage == nil, "humidity_unavailable")
	appendReason(source.Quality.TemporalResolutionHours > 1, "coarse_temporal_resolution")
	appendReason(source.Quality.LeadTimeQualityHeuristic < 0.85, "forecast_lead_limited")
	if !source.Available && len(reasons) == 0 {
		reasons = append(reasons, "mandatory_data_incomplete")
	}
	return reasons
}

func mapAstrodomeNodeState(source forecast.AstrodomeScienceNode) string {
	if !source.Available {
		if source.State == forecast.AstrodomeScienceNodeTerrainBlocked {
			return AstrodomeDatasetStateTerrainBlocked
		}
		return AstrodomeDatasetStateUnavailable
	}
	if source.Factors != nil && source.Factors.Precipitation == 0 {
		return AstrodomeDatasetStatePrecipitationVeto
	}
	return AstrodomeDatasetStateValid
}

func canonicalAstrodomeContributions(source []forecast.OverallPenaltyContribution) ([]forecast.OverallPenaltyContribution, error) {
	byKey := make(map[string]float64, len(source))
	for _, contribution := range source {
		if _, exists := byKey[contribution.Key]; exists {
			return nil, fmt.Errorf("duplicate astrodome penalty contribution %q", contribution.Key)
		}
		byKey[contribution.Key] = contribution.LossFraction
	}
	result := make([]forecast.OverallPenaltyContribution, 0, len(astrodomePenaltyKeys))
	for _, key := range astrodomePenaltyKeys {
		value, exists := byKey[key]
		if !exists {
			return nil, fmt.Errorf("missing astrodome penalty contribution %q", key)
		}
		result = append(result, forecast.OverallPenaltyContribution{Key: key, LossFraction: value})
		delete(byKey, key)
	}
	if len(byKey) != 0 {
		keys := make([]string, 0, len(byKey))
		for key := range byKey {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("unsupported astrodome penalty contribution %q", keys[0])
	}
	return result, nil
}

// canonicalAstrodomePenaltyLoss evaluates the authoritative multiplicative
// loss directly from the physical factors. Shapley values are an attribution
// of this loss and their floating-point sum is deliberately not used as the
// serialized total: at an exact veto it may round a few ulps above one.
func canonicalAstrodomePenaltyLoss(factors AstrodomeDatasetFactors) (float64, error) {
	values := []float64{
		factors.OpticalTurbulence,
		factors.CloudObstruction,
		factors.SurfaceWind,
		factors.Fog,
		factors.Precipitation,
	}
	product := 1.0
	for _, value := range values {
		if !finiteDatasetNumber(value) || value < 0 || value > 1 {
			return 0, errors.New("astrodome penalty factor must be finite and between zero and one")
		}
		product *= value
	}
	return 1 - product, nil
}

func astrodomeLimitingFactor(contributions []forecast.OverallPenaltyContribution) string {
	limiting := "none"
	maximum := 0.0
	for _, contribution := range contributions {
		if contribution.LossFraction > maximum {
			maximum = contribution.LossFraction
			limiting = contribution.Key
		}
	}
	return limiting
}

func astrodomeTwilightBand(solarAltitude float64) string {
	switch {
	case solarAltitude >= 0:
		return AstrodomeTwilightDay
	case solarAltitude >= -12:
		return AstrodomeTwilightLight
	case solarAltitude >= -18:
		return AstrodomeTwilightAstronomical
	default:
		return AstrodomeTwilightNight
	}
}

func floatPointer(value float64) *float64 { return &value }

func boolPointer(value bool) *bool { return &value }

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	return floatPointer(*value)
}

func cloneAstrodomeECEFVector(value *forecast.AstrodomeECEFVector) *AstrodomeDatasetECEFVector {
	if value == nil {
		return nil
	}
	return &AstrodomeDatasetECEFVector{X: value.X, Y: value.Y, Z: value.Z}
}

func sameNullableFloat(first, second *float64, tolerance float64) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return math.Abs(*first-*second) <= tolerance
}

func astrodomeNodeVersionsMatch(node forecast.AstrodomeScienceNode, input AstrodomeDatasetInput) bool {
	if node.RayGeometryVersion != input.RayGeometryVersion {
		return false
	}
	if node.GeometryMode == forecast.AstrodomeScienceGeometryStraight {
		return input.RefractionVersion == "not-applicable" && input.RefractivityVersion == "not-applicable" &&
			node.RefractionVersion == "" && node.RefractivityVersion == ""
	}
	return node.GeometryMode == forecast.AstrodomeScienceGeometryRefractionFull &&
		node.RefractionVersion == input.RefractionVersion && node.RefractivityVersion == input.RefractivityVersion
}
