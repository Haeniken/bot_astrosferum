package forecast

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

const AstrodomeMaximumPrimitiveTemporalGap = 3 * time.Hour

// AstrodomeReconstructionQuery addresses one physical point in the immutable
// primitive volume. HeightM is absolute ICON HHL/HSURF datum height, not AGL,
// geopotential height, or WGS-84 ellipsoid height.
type AstrodomeReconstructionQuery struct {
	ValidAt  time.Time `json:"valid_at"`
	Location Location  `json:"location"`
	HeightM  float64   `json:"height_m"`
}

// AstrodomeReconstructedVerticalDerivatives contains derivatives of the
// declared primitive interpolants with respect to absolute geometric height.
// These are not path derivatives and do not contain Cn2 or any science score.
type AstrodomeReconstructedVerticalDerivatives struct {
	Available                AstrodomePrimitiveFieldSet `json:"available"`
	PressurePaPerM           float64                    `json:"pressure_pa_per_m"`
	TemperatureKPerM         float64                    `json:"temperature_k_per_m"`
	SpecificHumidityKgKgPerM float64                    `json:"specific_humidity_kg_kg_per_m"`
	CloudLiquidKgKgPerM      float64                    `json:"cloud_liquid_kg_kg_per_m"`
	CloudIceKgKgPerM         float64                    `json:"cloud_ice_kg_kg_per_m"`
	CloudFractionPerM        float64                    `json:"cloud_fraction_per_m"`
	TKEJkgPerM               float64                    `json:"tke_j_kg_per_m"`
	WindECEFPerM             AstrodomeECEFVector        `json:"wind_ecef_per_m"`
}

// AstrodomeReconstructedSpatialGradients contains analytic ECEF gradients of
// the primitive interpolation itself. Horizontal derivatives are taken from
// the four-column bilinear polynomial at fixed ICON-sphere height; the radial
// derivative is the already reconstructed native-HHL derivative. These are
// primitive gradients, not finite differences of refractivity or any derived
// science product.
type AstrodomeReconstructedSpatialGradients struct {
	Available            AstrodomePrimitiveFieldSet `json:"available"`
	PressurePaPerM       AstrodomeECEFVector        `json:"pressure_pa_per_m"`
	TemperatureKPerM     AstrodomeECEFVector        `json:"temperature_k_per_m"`
	SpecificHumidityPerM AstrodomeECEFVector        `json:"specific_humidity_kg_kg_per_m"`
}

// AstrodomeReconstructedAtmosphere is the linear/log-linear reconstruction
// of native model primitives at one four-dimensional point. All nonlinear
// turbulence, cloud-radiative, refractive, and index formulas consume this
// state later and are recomputed from it.
type AstrodomeReconstructedAtmosphere struct {
	SourceIdentity       AstrodomePrimitiveVolumeIdentity          `json:"source_identity"`
	ValidAt              time.Time                                 `json:"valid_at"`
	Location             Location                                  `json:"location"`
	HeightM              float64                                   `json:"height_m"`
	HorizontalStencil    AstrodomeHorizontalStencil                `json:"horizontal_stencil"`
	Available            AstrodomePrimitiveFieldSet                `json:"available"`
	PressurePa           float64                                   `json:"pressure_pa"`
	TemperatureK         float64                                   `json:"temperature_k"`
	SpecificHumidityKgKg float64                                   `json:"specific_humidity_kg_kg"`
	CloudLiquidKgKg      float64                                   `json:"cloud_liquid_kg_kg"`
	CloudIceKgKg         float64                                   `json:"cloud_ice_kg_kg"`
	CloudFraction        float64                                   `json:"cloud_fraction"`
	TKEJkg               float64                                   `json:"tke_j_kg"`
	WindECEF             AstrodomeECEFVector                       `json:"wind_ecef"`
	VerticalDerivatives  AstrodomeReconstructedVerticalDerivatives `json:"vertical_derivatives"`
	cloudFractionSupport astrodomeCloudFractionVerticalSupport
}

// AstrodomePrimitiveReconstructor owns one immutable raw model volume. It
// copies native time axes and columns before use, so a provider cannot mutate
// a run underneath an in-flight dome calculation.
type AstrodomePrimitiveReconstructor struct {
	volume     AstrodomePrimitiveVolume
	identity   AstrodomePrimitiveVolumeIdentity
	fieldTimes map[AstrodomePrimitiveField][]time.Time

	columnMu              sync.RWMutex
	columns               map[string]AstrodomePrimitiveColumn
	shareImmutableColumns bool
}

type astrodomeTemporalBracket struct {
	left     time.Time
	right    time.Time
	fraction float64
}

type astrodomeTemporalBracketSet [10]astrodomeTemporalBracket

type astrodomeRefractionSurfaceBracketSet [3]astrodomeTemporalBracket

type astrodomePrimitiveSample struct {
	value      float64
	derivative float64
}

// astrodomeHorizontalScalar is one raw primitive (or one native geometric
// height) after horizontal interpolation on a corresponding terrain-following
// model level. Derivatives are with respect to geographic degrees at fixed
// level index, before the moving-level vertical transform is applied.
type astrodomeHorizontalScalar struct {
	value              float64
	latitudePerDegree  float64
	longitudePerDegree float64
}

type astrodomeTerrainFollowingAnchor struct {
	height astrodomeHorizontalScalar
	value  astrodomeHorizontalScalar
}

// astrodomeTerrainFollowingSample contains the exact derivatives of the
// composed horizontal-level/vertical-height interpolant. The horizontal
// derivatives are at fixed absolute ICON-sphere height, not at fixed model
// level, and therefore include the motion of both vertical anchors.
type astrodomeTerrainFollowingSample struct {
	astrodomePrimitiveSample
	latitudePerDegree  float64
	longitudePerDegree float64
	cloudSupport       astrodomeCloudFractionVerticalSupport
}

type astrodomeCloudFractionVerticalSupport struct {
	lowerLevelIndex int
	upperLevelIndex int
	mode            astrodomeCloudFractionVerticalSupportMode
}

type astrodomeCloudFractionVerticalSupportMode uint8

const (
	astrodomeCloudFractionSupportInvalid astrodomeCloudFractionVerticalSupportMode = iota
	astrodomeCloudFractionSupportPair
	astrodomeCloudFractionSupportConstantBottom
	astrodomeCloudFractionSupportConstantTop
)

// ICON-EU exposes HHL1..HHL75. Keeping the local terrain-following geometry
// in bounded arrays avoids two heap allocations on every adaptive refraction
// field evaluation while still allowing the smaller synthetic columns used by
// tests. A provider with a different vertical contract must declare and wire a
// deliberate bound instead of silently truncating levels.
const astrodomeMaximumTerrainFollowingHalfLevels = 75

type astrodomeTerrainFollowingGeometry struct {
	halfLevels     [astrodomeMaximumTerrainFollowingHalfLevels]astrodomeHorizontalScalar
	fullLevels     [astrodomeMaximumTerrainFollowingHalfLevels - 1]astrodomeHorizontalScalar
	halfLevelCount int
	fullLevelCount int
	surface        astrodomeHorizontalScalar
}

func (geometry *astrodomeTerrainFollowingGeometry) halfLevelSlice() []astrodomeHorizontalScalar {
	return geometry.halfLevels[:geometry.halfLevelCount]
}

func (geometry *astrodomeTerrainFollowingGeometry) fullLevelSlice() []astrodomeHorizontalScalar {
	return geometry.fullLevels[:geometry.fullLevelCount]
}

type astrodomeTerrainFollowingReconstruction struct {
	pressure         astrodomeTerrainFollowingSample
	temperature      astrodomeTerrainFollowingSample
	specificHumidity astrodomeTerrainFollowingSample
	cloudLiquid      astrodomeTerrainFollowingSample
	cloudIce         astrodomeTerrainFollowingSample
	cloudFraction    astrodomeTerrainFollowingSample
	tke              astrodomeTerrainFollowingSample
	windECEF         AstrodomeECEFVector
	windDerivative   AstrodomeECEFVector
}

// NewAstrodomePrimitiveReconstructor validates and snapshots one versioned
// volume contract. Field time axes are independent because ICON primitives
// can have different native publication cadences.
func NewAstrodomePrimitiveReconstructor(volume AstrodomePrimitiveVolume) (*AstrodomePrimitiveReconstructor, error) {
	if volume == nil {
		return nil, fmt.Errorf("astrodome primitive volume is required")
	}
	identity := volume.Identity()
	if err := ValidateAstrodomePrimitiveVolumeIdentity(identity); err != nil {
		return nil, err
	}
	reconstructor := &AstrodomePrimitiveReconstructor{
		volume:     volume,
		identity:   identity,
		fieldTimes: make(map[AstrodomePrimitiveField][]time.Time),
		columns:    make(map[string]AstrodomePrimitiveColumn),
	}
	if immutable, ok := volume.(AstrodomeImmutableColumnViewVolume); ok {
		reconstructor.shareImmutableColumns = immutable.TrustAstrodomeImmutableColumnViews()
	}
	for _, field := range astrodomeRequiredReconstructionPrimitives() {
		times := append([]time.Time(nil), volume.NativeValidTimes(field)...)
		if err := validateAstrodomePrimitiveTimeAxis(field, times); err != nil {
			return nil, err
		}
		for index := range times {
			times[index] = times[index].UTC()
		}
		reconstructor.fieldTimes[field] = times
	}
	if err := reconstructor.ensureIdentity(); err != nil {
		return nil, err
	}
	return reconstructor, nil
}

// ValidateAstrodomePrimitiveVolumeIdentity enforces a complete immutable-run
// key. A different input contract must construct a different reconstructor.
func ValidateAstrodomePrimitiveVolumeIdentity(identity AstrodomePrimitiveVolumeIdentity) error {
	if strings.TrimSpace(identity.Provider) == "" || strings.TrimSpace(identity.Product) == "" ||
		strings.TrimSpace(identity.Grid) == "" || strings.TrimSpace(identity.RunID) == "" ||
		strings.TrimSpace(identity.RunManifestDigest) == "" {
		return fmt.Errorf("astrodome primitive volume identity is incomplete")
	}
	if identity.InputContractVersion != AstrodomePrimitiveInputContractVersion {
		return fmt.Errorf("unsupported astrodome primitive input contract %q", identity.InputContractVersion)
	}
	if err := validateAstrodomeUTCWholeHour(identity.RunBaseTime); err != nil {
		return fmt.Errorf("astrodome primitive run base time: %w", err)
	}
	return nil
}

// Reconstruct evaluates every required native atmospheric primitive. Missing
// columns, levels, field timestamps, or brackets fail the point; no edge
// clamping, nearest-time substitution, or derived-field interpolation occurs.
func (reconstructor *AstrodomePrimitiveReconstructor) Reconstruct(
	ctx context.Context,
	query AstrodomeReconstructionQuery,
) (AstrodomeReconstructedAtmosphere, error) {
	if reconstructor == nil || reconstructor.volume == nil {
		return AstrodomeReconstructedAtmosphere{}, fmt.Errorf("astrodome primitive reconstructor is required")
	}
	if err := ctx.Err(); err != nil {
		return AstrodomeReconstructedAtmosphere{}, err
	}
	if err := reconstructor.ensureIdentity(); err != nil {
		return AstrodomeReconstructedAtmosphere{}, err
	}
	if err := ValidateCoordinates(query.Location.Latitude, query.Location.Longitude); err != nil {
		return AstrodomeReconstructedAtmosphere{}, fmt.Errorf("astrodome reconstruction location: %w", err)
	}
	if !finite(query.HeightM) || query.HeightM <= -AstrodomeICONSphereRadiusM {
		return AstrodomeReconstructedAtmosphere{}, fmt.Errorf("astrodome reconstruction height is invalid")
	}
	if err := validateAstrodomeUTCWholeHour(query.ValidAt); err != nil {
		return AstrodomeReconstructedAtmosphere{}, fmt.Errorf("astrodome reconstruction valid time: %w", err)
	}
	query.ValidAt = query.ValidAt.UTC()

	brackets := astrodomeTemporalBracketSet{}
	for _, field := range astrodomeRequiredAtmosphericPrimitives() {
		bracket, err := astrodomePrimitiveBracket(reconstructor.fieldTimes[field], query.ValidAt)
		if err != nil {
			return AstrodomeReconstructedAtmosphere{}, fmt.Errorf("astrodome primitive field %#x: %w", uint64(field), err)
		}
		brackets[astrodomeAtmosphericPrimitiveIndex(field)] = bracket
	}
	surfaceBrackets := astrodomeRefractionSurfaceBracketSet{}
	for _, field := range astrodomeRequiredRefractionSurfacePrimitives() {
		bracket, err := astrodomePrimitiveBracket(reconstructor.fieldTimes[field], query.ValidAt)
		if err != nil {
			return AstrodomeReconstructedAtmosphere{}, fmt.Errorf("astrodome primitive field %#x: %w", uint64(field), err)
		}
		surfaceBrackets[astrodomeRefractionSurfacePrimitiveIndex(field)] = bracket
	}

	stencil, err := reconstructor.volume.HorizontalStencil(ctx, query.Location)
	if err != nil {
		return AstrodomeReconstructedAtmosphere{}, fmt.Errorf("astrodome horizontal stencil: %w", err)
	}
	if err := stencil.Validate(); err != nil {
		return AstrodomeReconstructedAtmosphere{}, err
	}
	if err := reconstructor.ensureIdentity(); err != nil {
		return AstrodomeReconstructedAtmosphere{}, err
	}

	var columns [4]AstrodomePrimitiveColumn
	weights := [4]float64{}
	for index, support := range stencil.Supports {
		if err := ctx.Err(); err != nil {
			return AstrodomeReconstructedAtmosphere{}, err
		}
		column, err := reconstructor.column(ctx, support.ColumnID)
		if err != nil {
			return AstrodomeReconstructedAtmosphere{}, err
		}
		if !astrodomeSameGridLocation(column.Location, support.Location) {
			return AstrodomeReconstructedAtmosphere{}, fmt.Errorf("astrodome column %q location changed between stencil and volume", support.ColumnID)
		}
		columns[index] = column
		weights[index] = support.Weight
	}
	if err := reconstructor.ensureIdentity(); err != nil {
		return AstrodomeReconstructedAtmosphere{}, err
	}
	reconstructed, err := reconstructAstrodomeTerrainFollowing(
		columns, weights, nil, query.HeightM, brackets, surfaceBrackets, false,
	)
	if err != nil {
		return AstrodomeReconstructedAtmosphere{}, err
	}

	available := AstrodomePrimitiveFieldSet(0)
	for _, field := range astrodomeRequiredAtmosphericPrimitives() {
		available |= AstrodomePrimitiveFieldSet(field)
	}
	result := AstrodomeReconstructedAtmosphere{
		SourceIdentity:       reconstructor.identity,
		ValidAt:              query.ValidAt,
		Location:             query.Location,
		HeightM:              query.HeightM,
		HorizontalStencil:    stencil,
		Available:            available,
		PressurePa:           reconstructed.pressure.value,
		TemperatureK:         reconstructed.temperature.value,
		SpecificHumidityKgKg: reconstructed.specificHumidity.value,
		CloudLiquidKgKg:      reconstructed.cloudLiquid.value,
		CloudIceKgKg:         reconstructed.cloudIce.value,
		CloudFraction:        reconstructed.cloudFraction.value,
		TKEJkg:               reconstructed.tke.value,
		WindECEF:             reconstructed.windECEF,
		cloudFractionSupport: reconstructed.cloudFraction.cloudSupport,
	}
	result.VerticalDerivatives = AstrodomeReconstructedVerticalDerivatives{
		Available:                available,
		PressurePaPerM:           reconstructed.pressure.derivative,
		TemperatureKPerM:         reconstructed.temperature.derivative,
		SpecificHumidityKgKgPerM: reconstructed.specificHumidity.derivative,
		CloudLiquidKgKgPerM:      reconstructed.cloudLiquid.derivative,
		CloudIceKgKgPerM:         reconstructed.cloudIce.derivative,
		CloudFractionPerM:        reconstructed.cloudFraction.derivative,
		TKEJkgPerM:               reconstructed.tke.derivative,
		WindECEFPerM:             reconstructed.windDerivative,
	}
	if err := validateAstrodomeReconstructedAtmosphere(result, false); err != nil {
		return AstrodomeReconstructedAtmosphere{}, err
	}
	return result, nil
}

// ReconstructRefractivePrimitives returns P/T/QV and their analytic ECEF
// gradients for the full-refraction ray solver. It deliberately validates the
// stencil weights against the query geometry: accepting arbitrary convex
// weights would make the reported horizontal derivative inconsistent with
// the reconstructed primitive value.
func (reconstructor *AstrodomePrimitiveReconstructor) ReconstructRefractivePrimitives(
	ctx context.Context,
	query AstrodomeReconstructionQuery,
) (AstrodomeReconstructedAtmosphere, AstrodomeReconstructedSpatialGradients, error) {
	if reconstructor == nil || reconstructor.volume == nil {
		return AstrodomeReconstructedAtmosphere{}, AstrodomeReconstructedSpatialGradients{}, fmt.Errorf("astrodome primitive reconstructor is required")
	}
	if err := ctx.Err(); err != nil {
		return AstrodomeReconstructedAtmosphere{}, AstrodomeReconstructedSpatialGradients{}, err
	}
	if err := reconstructor.ensureIdentity(); err != nil {
		return AstrodomeReconstructedAtmosphere{}, AstrodomeReconstructedSpatialGradients{}, err
	}
	if err := ValidateCoordinates(query.Location.Latitude, query.Location.Longitude); err != nil {
		return AstrodomeReconstructedAtmosphere{}, AstrodomeReconstructedSpatialGradients{}, fmt.Errorf("astrodome refractive location: %w", err)
	}
	if !finite(query.HeightM) || query.HeightM <= -AstrodomeICONSphereRadiusM {
		return AstrodomeReconstructedAtmosphere{}, AstrodomeReconstructedSpatialGradients{}, fmt.Errorf("astrodome refractive height is invalid")
	}
	if err := validateAstrodomeUTCWholeHour(query.ValidAt); err != nil {
		return AstrodomeReconstructedAtmosphere{}, AstrodomeReconstructedSpatialGradients{}, fmt.Errorf("astrodome refractive valid time: %w", err)
	}
	query.ValidAt = query.ValidAt.UTC()
	brackets := astrodomeTemporalBracketSet{}
	for _, field := range astrodomeRequiredAtmosphericPrimitives() {
		bracket, bracketErr := astrodomePrimitiveBracket(reconstructor.fieldTimes[field], query.ValidAt)
		if bracketErr != nil {
			return AstrodomeReconstructedAtmosphere{}, AstrodomeReconstructedSpatialGradients{}, bracketErr
		}
		brackets[astrodomeAtmosphericPrimitiveIndex(field)] = bracket
	}
	surfaceBrackets := astrodomeRefractionSurfaceBracketSet{}
	for _, field := range astrodomeRequiredRefractionSurfacePrimitives() {
		bracket, bracketErr := astrodomePrimitiveBracket(reconstructor.fieldTimes[field], query.ValidAt)
		if bracketErr != nil {
			return AstrodomeReconstructedAtmosphere{}, AstrodomeReconstructedSpatialGradients{}, bracketErr
		}
		surfaceBrackets[astrodomeRefractionSurfacePrimitiveIndex(field)] = bracket
	}
	stencil, err := reconstructor.volume.HorizontalStencil(ctx, query.Location)
	if err != nil {
		return AstrodomeReconstructedAtmosphere{}, AstrodomeReconstructedSpatialGradients{}, fmt.Errorf("astrodome refractive horizontal stencil: %w", err)
	}
	if err := stencil.Validate(); err != nil {
		return AstrodomeReconstructedAtmosphere{}, AstrodomeReconstructedSpatialGradients{}, err
	}

	geometry, err := astrodomeResolveBilinearGeometry(query.Location, stencil)
	if err != nil {
		return AstrodomeReconstructedAtmosphere{}, AstrodomeReconstructedSpatialGradients{}, err
	}
	var columns [4]AstrodomePrimitiveColumn
	weights := [4]float64{}
	for index, support := range stencil.Supports {
		column, columnErr := reconstructor.column(ctx, support.ColumnID)
		if columnErr != nil {
			return AstrodomeReconstructedAtmosphere{}, AstrodomeReconstructedSpatialGradients{}, columnErr
		}
		if !astrodomeSameGridLocation(column.Location, support.Location) {
			return AstrodomeReconstructedAtmosphere{}, AstrodomeReconstructedSpatialGradients{},
				fmt.Errorf("astrodome refractive column %q location changed", support.ColumnID)
		}
		columns[index] = column
		weights[index] = support.Weight
	}
	reconstructed, err := reconstructAstrodomeTerrainFollowing(
		columns, weights, &geometry, query.HeightM, brackets, surfaceBrackets, true,
	)
	if err != nil {
		return AstrodomeReconstructedAtmosphere{}, AstrodomeReconstructedSpatialGradients{}, err
	}
	available := AstrodomePrimitiveFieldSet(AstrodomePrimitivePressure) |
		AstrodomePrimitiveFieldSet(AstrodomePrimitiveTemperature) |
		AstrodomePrimitiveFieldSet(AstrodomePrimitiveSpecificHumidity)
	state := AstrodomeReconstructedAtmosphere{
		SourceIdentity: reconstructor.identity, ValidAt: query.ValidAt, Location: query.Location,
		HeightM: query.HeightM, HorizontalStencil: stencil, Available: available,
		PressurePa: reconstructed.pressure.value, TemperatureK: reconstructed.temperature.value,
		SpecificHumidityKgKg: reconstructed.specificHumidity.value,
		VerticalDerivatives: AstrodomeReconstructedVerticalDerivatives{
			Available: available, PressurePaPerM: reconstructed.pressure.derivative,
			TemperatureKPerM:         reconstructed.temperature.derivative,
			SpecificHumidityKgKgPerM: reconstructed.specificHumidity.derivative,
		},
	}
	gradients := AstrodomeReconstructedSpatialGradients{
		Available: available,
		PressurePaPerM: astrodomeTerrainFollowingECEFGradient(
			state, reconstructed.pressure,
		),
		TemperatureKPerM: astrodomeTerrainFollowingECEFGradient(
			state, reconstructed.temperature,
		),
		SpecificHumidityPerM: astrodomeTerrainFollowingECEFGradient(
			state, reconstructed.specificHumidity,
		),
	}
	if err := validateAstrodomeReconstructedAtmosphere(state, true); err != nil {
		return AstrodomeReconstructedAtmosphere{}, AstrodomeReconstructedSpatialGradients{}, err
	}
	for _, gradient := range []AstrodomeECEFVector{
		gradients.PressurePaPerM,
		gradients.TemperatureKPerM,
		gradients.SpecificHumidityPerM,
	} {
		if !finiteAstrodomeECEFVector(gradient) {
			return AstrodomeReconstructedAtmosphere{}, AstrodomeReconstructedSpatialGradients{},
				fmt.Errorf("astrodome reconstructed spatial gradient is not finite")
		}
	}
	if err := reconstructor.ensureIdentity(); err != nil {
		return AstrodomeReconstructedAtmosphere{}, AstrodomeReconstructedSpatialGradients{}, err
	}
	return state, gradients, nil
}

func validateAstrodomeReconstructedAtmosphere(state AstrodomeReconstructedAtmosphere, refractiveOnly bool) error {
	if !finite(state.PressurePa) || state.PressurePa <= 0 ||
		!finite(state.TemperatureK) || state.TemperatureK <= 0 ||
		!finite(state.SpecificHumidityKgKg) || state.SpecificHumidityKgKg < 0 || state.SpecificHumidityKgKg >= 1 ||
		!finite(state.VerticalDerivatives.PressurePaPerM) || state.VerticalDerivatives.PressurePaPerM >= 0 ||
		!finite(state.VerticalDerivatives.TemperatureKPerM) ||
		!finite(state.VerticalDerivatives.SpecificHumidityKgKgPerM) {
		return fmt.Errorf("astrodome reconstructed thermodynamic state is outside its physical contract")
	}
	if refractiveOnly {
		return nil
	}
	if !finite(state.CloudLiquidKgKg) || state.CloudLiquidKgKg < 0 ||
		!finite(state.CloudIceKgKg) || state.CloudIceKgKg < 0 ||
		state.SpecificHumidityKgKg+state.CloudLiquidKgKg+state.CloudIceKgKg >= 1 ||
		!finite(state.CloudFraction) || state.CloudFraction < 0 || state.CloudFraction > 1 ||
		!finite(state.TKEJkg) || state.TKEJkg < 0 ||
		!finiteAstrodomeECEFVector(state.WindECEF) ||
		!finite(state.VerticalDerivatives.CloudLiquidKgKgPerM) ||
		!finite(state.VerticalDerivatives.CloudIceKgKgPerM) ||
		!finite(state.VerticalDerivatives.CloudFractionPerM) ||
		!finite(state.VerticalDerivatives.TKEJkgPerM) ||
		!finiteAstrodomeECEFVector(state.VerticalDerivatives.WindECEFPerM) {
		return fmt.Errorf("astrodome reconstructed cloud, turbulence, or wind state is outside its physical contract")
	}
	return nil
}

func finiteAstrodomeECEFVector(value AstrodomeECEFVector) bool {
	return finite(value.X) && finite(value.Y) && finite(value.Z)
}

func astrodomeRequiredAtmosphericPrimitives() [10]AstrodomePrimitiveField {
	return [10]AstrodomePrimitiveField{
		AstrodomePrimitivePressure,
		AstrodomePrimitiveTemperature,
		AstrodomePrimitiveSpecificHumidity,
		AstrodomePrimitiveCloudLiquid,
		AstrodomePrimitiveCloudIce,
		AstrodomePrimitiveCloudFraction,
		AstrodomePrimitiveEastwardWind,
		AstrodomePrimitiveNorthwardWind,
		AstrodomePrimitiveVerticalWind,
		AstrodomePrimitiveTKE,
	}
}

func astrodomeRequiredRefractionSurfacePrimitives() [3]AstrodomePrimitiveField {
	return [3]AstrodomePrimitiveField{
		AstrodomePrimitiveSurfacePressure,
		AstrodomePrimitiveTemperature2M,
		AstrodomePrimitiveSpecificHumidity2M,
	}
}

func astrodomeRequiredReconstructionPrimitives() []AstrodomePrimitiveField {
	fields := make([]AstrodomePrimitiveField, 0, 13)
	for _, field := range astrodomeRequiredAtmosphericPrimitives() {
		fields = append(fields, field)
	}
	for _, field := range astrodomeRequiredRefractionSurfacePrimitives() {
		fields = append(fields, field)
	}
	return fields
}

type astrodomeBilinearGeometry struct {
	cornerIndex          [2][2]int // [south/north][west/east]
	latitudeT            float64
	longitudeT           float64
	latitudeSpanDegrees  float64
	longitudeSpanDegrees float64
}

func astrodomeResolveBilinearGeometry(
	query Location,
	stencil AstrodomeHorizontalStencil,
) (astrodomeBilinearGeometry, error) {
	latitudes := [4]float64{}
	longitudes := [4]float64{}
	for index, support := range stencil.Supports {
		latitudes[index] = support.Location.Latitude
		longitudes[index] = query.Longitude + normalizeAstrodomeLongitude(support.Location.Longitude-query.Longitude)
	}
	south, north := latitudes[0], latitudes[0]
	west, east := longitudes[0], longitudes[0]
	for index := 1; index < len(latitudes); index++ {
		south = math.Min(south, latitudes[index])
		north = math.Max(north, latitudes[index])
		west = math.Min(west, longitudes[index])
		east = math.Max(east, longitudes[index])
	}
	if !finite(south) || !finite(north) || !finite(west) || !finite(east) ||
		north <= south || east <= west || east-west >= 180 {
		return astrodomeBilinearGeometry{}, fmt.Errorf("astrodome refractive stencil is not a non-degenerate regular latitude-longitude cell")
	}
	queryLongitude := query.Longitude
	for queryLongitude < west {
		queryLongitude += 360
	}
	for queryLongitude > east {
		queryLongitude -= 360
	}
	latitudeT := (query.Latitude - south) / (north - south)
	longitudeT := (queryLongitude - west) / (east - west)
	const coordinateTolerance = 1e-11
	if latitudeT < -coordinateTolerance || latitudeT > 1+coordinateTolerance ||
		longitudeT < -coordinateTolerance || longitudeT > 1+coordinateTolerance {
		return astrodomeBilinearGeometry{}, fmt.Errorf("astrodome reconstruction query lies outside its four-column stencil")
	}
	latitudeT = clampSurfaceValue(latitudeT, 0, 1)
	longitudeT = clampSurfaceValue(longitudeT, 0, 1)
	geometry := astrodomeBilinearGeometry{
		cornerIndex: [2][2]int{{-1, -1}, {-1, -1}},
		latitudeT:   latitudeT, longitudeT: longitudeT,
		latitudeSpanDegrees: north - south, longitudeSpanDegrees: east - west,
	}
	for index := range stencil.Supports {
		latitudeSide := -1
		switch {
		case math.Abs(latitudes[index]-south) <= coordinateTolerance:
			latitudeSide = 0
		case math.Abs(latitudes[index]-north) <= coordinateTolerance:
			latitudeSide = 1
		}
		longitudeSide := -1
		switch {
		case math.Abs(longitudes[index]-west) <= coordinateTolerance:
			longitudeSide = 0
		case math.Abs(longitudes[index]-east) <= coordinateTolerance:
			longitudeSide = 1
		}
		if latitudeSide < 0 || longitudeSide < 0 || geometry.cornerIndex[latitudeSide][longitudeSide] >= 0 {
			return astrodomeBilinearGeometry{}, fmt.Errorf("astrodome refractive stencil does not contain four unique rectangle corners")
		}
		geometry.cornerIndex[latitudeSide][longitudeSide] = index
	}
	expected := [2][2]float64{
		{(1 - latitudeT) * (1 - longitudeT), (1 - latitudeT) * longitudeT},
		{latitudeT * (1 - longitudeT), latitudeT * longitudeT},
	}
	for latitudeSide := range 2 {
		for longitudeSide := range 2 {
			index := geometry.cornerIndex[latitudeSide][longitudeSide]
			if index < 0 || math.Abs(stencil.Supports[index].Weight-expected[latitudeSide][longitudeSide]) > 1e-10 {
				return astrodomeBilinearGeometry{}, fmt.Errorf("astrodome refractive stencil weights are inconsistent with query geometry")
			}
		}
	}
	return geometry, nil
}

func astrodomeAtmosphericPrimitiveIndex(field AstrodomePrimitiveField) int {
	switch field {
	case AstrodomePrimitivePressure:
		return 0
	case AstrodomePrimitiveTemperature:
		return 1
	case AstrodomePrimitiveSpecificHumidity:
		return 2
	case AstrodomePrimitiveCloudLiquid:
		return 3
	case AstrodomePrimitiveCloudIce:
		return 4
	case AstrodomePrimitiveCloudFraction:
		return 5
	case AstrodomePrimitiveEastwardWind:
		return 6
	case AstrodomePrimitiveNorthwardWind:
		return 7
	case AstrodomePrimitiveVerticalWind:
		return 8
	case AstrodomePrimitiveTKE:
		return 9
	default:
		panic("unsupported astrodome atmospheric primitive")
	}
}

func astrodomeRefractionSurfacePrimitiveIndex(field AstrodomePrimitiveField) int {
	switch field {
	case AstrodomePrimitiveSurfacePressure:
		return 0
	case AstrodomePrimitiveTemperature2M:
		return 1
	case AstrodomePrimitiveSpecificHumidity2M:
		return 2
	default:
		panic("unsupported astrodome refraction surface primitive")
	}
}

func validateAstrodomePrimitiveTimeAxis(field AstrodomePrimitiveField, times []time.Time) error {
	if len(times) == 0 {
		return fmt.Errorf("astrodome primitive field %#x has no native valid times", uint64(field))
	}
	for index, validAt := range times {
		if err := validateAstrodomeUTCWholeHour(validAt); err != nil {
			return fmt.Errorf("astrodome primitive field %#x time %d: %w", uint64(field), index, err)
		}
		if index == 0 {
			continue
		}
		gap := validAt.Sub(times[index-1])
		if gap <= 0 {
			return fmt.Errorf("astrodome primitive field %#x native times are not strictly increasing", uint64(field))
		}
		if gap > AstrodomeMaximumPrimitiveTemporalGap {
			return fmt.Errorf("astrodome primitive field %#x has an unsupported %s native-time gap", uint64(field), gap)
		}
	}
	return nil
}

func astrodomePrimitiveBracket(times []time.Time, validAt time.Time) (astrodomeTemporalBracket, error) {
	index := sort.Search(len(times), func(index int) bool { return !times[index].Before(validAt) })
	if index < len(times) && times[index].Equal(validAt) {
		return astrodomeTemporalBracket{left: times[index], right: times[index]}, nil
	}
	if index == 0 || index == len(times) {
		return astrodomeTemporalBracket{}, fmt.Errorf("valid time %s lies outside native input brackets", validAt.Format(time.RFC3339))
	}
	left, right := times[index-1], times[index]
	gap := right.Sub(left)
	if gap <= 0 || gap > AstrodomeMaximumPrimitiveTemporalGap {
		return astrodomeTemporalBracket{}, fmt.Errorf("valid time %s has no supported native input bracket", validAt.Format(time.RFC3339))
	}
	return astrodomeTemporalBracket{
		left:     left,
		right:    right,
		fraction: float64(validAt.Sub(left)) / float64(gap),
	}, nil
}

func (reconstructor *AstrodomePrimitiveReconstructor) ensureIdentity() error {
	current := reconstructor.volume.Identity()
	if !astrodomePrimitiveIdentityEqual(current, reconstructor.identity) {
		return fmt.Errorf("astrodome primitive volume identity changed during reconstruction")
	}
	return nil
}

func astrodomePrimitiveIdentityEqual(a, b AstrodomePrimitiveVolumeIdentity) bool {
	return a.Provider == b.Provider && a.Product == b.Product && a.Grid == b.Grid && a.RunID == b.RunID &&
		a.RunBaseTime.Equal(b.RunBaseTime) && a.RunManifestDigest == b.RunManifestDigest &&
		a.InputContractVersion == b.InputContractVersion
}

func (reconstructor *AstrodomePrimitiveReconstructor) column(
	ctx context.Context,
	columnID string,
) (AstrodomePrimitiveColumn, error) {
	reconstructor.columnMu.RLock()
	if cached, ok := reconstructor.columns[columnID]; ok {
		reconstructor.columnMu.RUnlock()
		return cached, nil
	}
	reconstructor.columnMu.RUnlock()

	reconstructor.columnMu.Lock()
	defer reconstructor.columnMu.Unlock()
	if cached, ok := reconstructor.columns[columnID]; ok {
		return cached, nil
	}
	if err := reconstructor.ensureIdentity(); err != nil {
		return AstrodomePrimitiveColumn{}, err
	}
	column, err := reconstructor.volume.Column(ctx, columnID)
	if err != nil {
		return AstrodomePrimitiveColumn{}, fmt.Errorf("load astrodome primitive column %q: %w", columnID, err)
	}
	if err := reconstructor.ensureIdentity(); err != nil {
		return AstrodomePrimitiveColumn{}, err
	}
	if column.ColumnID != columnID {
		return AstrodomePrimitiveColumn{}, fmt.Errorf("astrodome primitive column identity %q does not match requested %q", column.ColumnID, columnID)
	}
	if !reconstructor.shareImmutableColumns {
		if err := validateAstrodomePrimitiveColumn(column, reconstructor.fieldTimes); err != nil {
			return AstrodomePrimitiveColumn{}, fmt.Errorf("astrodome primitive column %q: %w", columnID, err)
		}
		column = cloneAstrodomePrimitiveColumn(column)
	}
	reconstructor.columns[columnID] = column
	return column, nil
}

func validateAstrodomePrimitiveColumn(
	column AstrodomePrimitiveColumn,
	fieldTimes map[AstrodomePrimitiveField][]time.Time,
) error {
	if strings.TrimSpace(column.ColumnID) == "" {
		return fmt.Errorf("column ID is required")
	}
	if err := ValidateCoordinates(column.Location.Latitude, column.Location.Longitude); err != nil {
		return fmt.Errorf("column location: %w", err)
	}
	if !finite(column.HSURFHeightM) || column.HSURFHeightM <= -AstrodomeICONSphereRadiusM {
		return fmt.Errorf("column HSURF height is invalid")
	}
	if len(column.HalfLevelGeometry) < 3 {
		return fmt.Errorf("column needs at least three native HHL half levels")
	}
	for index, level := range column.HalfLevelGeometry {
		if level.ModelHalfLevel < 1 || !finite(level.HeightM) {
			return fmt.Errorf("HHL geometry %d is invalid", index)
		}
		if index > 0 {
			previous := column.HalfLevelGeometry[index-1]
			if level.ModelHalfLevel != previous.ModelHalfLevel+1 {
				return fmt.Errorf("HHL native half levels are not contiguous and top-to-surface ordered")
			}
			if level.HeightM >= previous.HeightM {
				return fmt.Errorf("HHL heights must strictly decrease as native half-level number increases")
			}
		}
	}
	if len(column.Frames) == 0 {
		return fmt.Errorf("column has no native frames")
	}
	frameByUnix := make(map[int64]AstrodomePrimitiveColumnFrame, len(column.Frames))
	for index, frame := range column.Frames {
		if err := validateAstrodomeUTCWholeHour(frame.ValidAt); err != nil {
			return fmt.Errorf("column frame %d: %w", index, err)
		}
		unix := frame.ValidAt.Unix()
		if _, duplicate := frameByUnix[unix]; duplicate {
			return fmt.Errorf("column repeats native frame %s", frame.ValidAt.Format(time.RFC3339))
		}
		if index > 0 && !frame.ValidAt.After(column.Frames[index-1].ValidAt) {
			return fmt.Errorf("column frames are not strictly increasing")
		}
		frameByUnix[unix] = frame
	}
	for _, field := range astrodomeRequiredAtmosphericPrimitives() {
		times := fieldTimes[field]
		for _, validAt := range times {
			frame, ok := frameByUnix[validAt.Unix()]
			if !ok {
				return fmt.Errorf("field %#x is missing native frame %s", uint64(field), validAt.Format(time.RFC3339))
			}
			if err := validateAstrodomeFrameField(column.HalfLevelGeometry, frame, field); err != nil {
				return fmt.Errorf("field %#x at %s: %w", uint64(field), validAt.Format(time.RFC3339), err)
			}
		}
	}
	for _, field := range astrodomeRequiredRefractionSurfacePrimitives() {
		for _, validAt := range fieldTimes[field] {
			frame, ok := frameByUnix[validAt.Unix()]
			if !ok {
				return fmt.Errorf("surface field %#x is missing native frame %s", uint64(field), validAt.Format(time.RFC3339))
			}
			if err := validateAstrodomeRefractionSurfaceField(frame.Surface, field); err != nil {
				return fmt.Errorf("surface field %#x at %s: %w", uint64(field), validAt.Format(time.RFC3339), err)
			}
		}
	}
	return nil
}

func validateAstrodomeRefractionSurfaceField(surface AstrodomeSurfacePrimitives, field AstrodomePrimitiveField) error {
	if !surface.Available.Has(field) {
		return fmt.Errorf("mandatory primitive is unavailable")
	}
	var value float64
	switch field {
	case AstrodomePrimitiveSurfacePressure:
		value = surface.SurfacePressurePa
		if !finite(value) || value <= 0 || value > 2e5 {
			return fmt.Errorf("surface pressure is outside its physical domain")
		}
	case AstrodomePrimitiveTemperature2M:
		value = surface.Temperature2MK
		if !finite(value) || value < 150 || value > 400 {
			return fmt.Errorf("two-metre temperature is outside its physical domain")
		}
	case AstrodomePrimitiveSpecificHumidity2M:
		value = surface.SpecificHumidity2MKgKg
		if !finite(value) || value < 0 || value >= 1 {
			return fmt.Errorf("two-metre specific humidity is outside its physical domain")
		}
	default:
		return fmt.Errorf("field is not a refraction lower-boundary primitive")
	}
	return nil
}

func validateAstrodomeFrameField(
	geometry []AstrodomeHalfLevelGeometry,
	frame AstrodomePrimitiveColumnFrame,
	field AstrodomePrimitiveField,
) error {
	if astrodomeFullLevelField(field) {
		if len(frame.FullLevels) != len(geometry)-1 {
			return fmt.Errorf("full-level count is %d, want %d", len(frame.FullLevels), len(geometry)-1)
		}
		for index, level := range frame.FullLevels {
			if level.ModelLevel != geometry[index].ModelHalfLevel {
				return fmt.Errorf("full level %d does not match adjacent HHL%d/HHL%d", level.ModelLevel,
					geometry[index].ModelHalfLevel, geometry[index+1].ModelHalfLevel)
			}
			value, err := astrodomeFullLevelValue(level, field)
			if err != nil {
				return fmt.Errorf("model level %d: %w", level.ModelLevel, err)
			}
			if err := validateAstrodomePrimitiveValue(field, value); err != nil {
				return fmt.Errorf("model level %d: %w", level.ModelLevel, err)
			}
		}
		if field == AstrodomePrimitivePressure {
			if err := ValidateAstrodomeNativePressureOrdering(frame.FullLevels); err != nil {
				return err
			}
			if frame.Surface.Available.Has(AstrodomePrimitiveSurfacePressure) {
				if err := ValidateAstrodomeNativeSurfacePressureBoundary(frame.FullLevels, frame.Surface); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if astrodomeHalfLevelField(field) {
		if len(frame.HalfLevels) != len(geometry) {
			return fmt.Errorf("half-level count is %d, want %d", len(frame.HalfLevels), len(geometry))
		}
		for index, level := range frame.HalfLevels {
			if level.ModelHalfLevel != geometry[index].ModelHalfLevel {
				return fmt.Errorf("half level %d does not match HHL%d", level.ModelHalfLevel, geometry[index].ModelHalfLevel)
			}
			value, err := astrodomeHalfLevelValue(level, field)
			if err != nil {
				return fmt.Errorf("model half level %d: %w", level.ModelHalfLevel, err)
			}
			if err := validateAstrodomePrimitiveValue(field, value); err != nil {
				return fmt.Errorf("model half level %d: %w", level.ModelHalfLevel, err)
			}
		}
		return nil
	}
	return fmt.Errorf("field is not an atmospheric full- or half-level primitive")
}

// ValidateAstrodomeNativePressureOrdering proves the ICON top-to-surface
// pressure invariant before any horizontal, temporal, or vertical
// reconstruction. Native full-level pressure must strictly increase as model
// level number and array index move downward toward the surface.
func ValidateAstrodomeNativePressureOrdering(levels []AstrodomeFullLevelPrimitives) error {
	if len(levels) == 0 {
		return fmt.Errorf("native pressure profile is empty")
	}
	for index, level := range levels {
		if !level.Available.Has(AstrodomePrimitivePressure) || !finite(level.PressurePa) || level.PressurePa <= 0 {
			return fmt.Errorf("native pressure at model level %d is unavailable or invalid", level.ModelLevel)
		}
		if index > 0 && level.PressurePa <= levels[index-1].PressurePa {
			return fmt.Errorf(
				"native pressure must strictly increase toward the surface: level %d has %.17g Pa after %.17g Pa",
				level.ModelLevel, level.PressurePa, levels[index-1].PressurePa,
			)
		}
	}
	return nil
}

// ValidateAstrodomeNativeSurfacePressureBoundary proves that the pressure at
// the ICON surface lies below the lowest full level in geometric height and
// therefore is strictly greater in pressure. It must be called only for a
// frame where PS is part of the declared native time axis.
func ValidateAstrodomeNativeSurfacePressureBoundary(
	levels []AstrodomeFullLevelPrimitives,
	surface AstrodomeSurfacePrimitives,
) error {
	if err := ValidateAstrodomeNativePressureOrdering(levels); err != nil {
		return err
	}
	if !surface.Available.Has(AstrodomePrimitiveSurfacePressure) ||
		!finite(surface.SurfacePressurePa) || surface.SurfacePressurePa <= 0 {
		return fmt.Errorf("native surface pressure is unavailable or invalid")
	}
	lowest := levels[len(levels)-1]
	if surface.SurfacePressurePa <= lowest.PressurePa {
		return fmt.Errorf(
			"native surface pressure %.17g Pa must exceed lowest full-level pressure %.17g Pa",
			surface.SurfacePressurePa, lowest.PressurePa,
		)
	}
	return nil
}

// reconstructAstrodomeTerrainFollowing first interpolates corresponding
// native model levels and their HHL geometry horizontally, then locates the
// requested absolute height in that local column. This order is essential
// near orography: a valid point above the bilinear model terrain may lie below
// the surface of one remote support corner, which is not a physical terrain
// intersection at the query location.
func reconstructAstrodomeTerrainFollowing(
	columns [4]AstrodomePrimitiveColumn,
	weights [4]float64,
	bilinear *astrodomeBilinearGeometry,
	heightM float64,
	brackets astrodomeTemporalBracketSet,
	surfaceBrackets astrodomeRefractionSurfaceBracketSet,
	refractiveOnly bool,
) (astrodomeTerrainFollowingReconstruction, error) {
	result := astrodomeTerrainFollowingReconstruction{}
	geometry := astrodomeTerrainFollowingGeometry{}
	if err := astrodomeBuildTerrainFollowingGeometry(columns, weights, bilinear, &geometry); err != nil {
		return result, err
	}
	return reconstructAstrodomeTerrainFollowingWithGeometry(
		columns, weights, bilinear, &geometry, heightM, brackets, surfaceBrackets, refractiveOnly,
	)
}

// reconstructAstrodomeTerrainFollowingWithGeometry consumes a geometry that
// was reconstructed from the same columns, weights, and bilinear point. The
// prepared refraction evaluator uses this form so its domain distances and
// P/T/QV gradients come from one and the same HHL reconstruction.
func reconstructAstrodomeTerrainFollowingWithGeometry(
	columns [4]AstrodomePrimitiveColumn,
	weights [4]float64,
	bilinear *astrodomeBilinearGeometry,
	geometry *astrodomeTerrainFollowingGeometry,
	heightM float64,
	brackets astrodomeTemporalBracketSet,
	surfaceBrackets astrodomeRefractionSurfaceBracketSet,
	refractiveOnly bool,
) (astrodomeTerrainFollowingReconstruction, error) {
	result := astrodomeTerrainFollowingReconstruction{}
	if geometry == nil || geometry.halfLevelCount < 3 || geometry.fullLevelCount != geometry.halfLevelCount-1 {
		return result, fmt.Errorf("terrain-following geometry is incomplete")
	}
	lower, err := astrodomeTerrainFollowingLowerAnchors(columns, weights, bilinear, geometry,
		surfaceBrackets)
	if err != nil {
		return result, err
	}
	thermodynamic := func(field AstrodomePrimitiveField) (astrodomeTerrainFollowingSample, error) {
		return astrodomeTerrainFollowingThermodynamicPrimitive(
			columns, weights, bilinear, geometry, lower, field,
			brackets, heightM,
		)
	}
	full := func(field AstrodomePrimitiveField) (astrodomeTerrainFollowingSample, error) {
		return astrodomeTerrainFollowingFullPrimitive(
			columns, weights, bilinear, geometry, field,
			brackets[astrodomeAtmosphericPrimitiveIndex(field)], heightM,
		)
	}
	half := func(field AstrodomePrimitiveField) (astrodomeTerrainFollowingSample, error) {
		return astrodomeTerrainFollowingHalfPrimitive(
			columns, weights, bilinear, geometry, field,
			brackets[astrodomeAtmosphericPrimitiveIndex(field)], heightM,
		)
	}
	if result.pressure, err = thermodynamic(AstrodomePrimitivePressure); err != nil {
		return result, fmt.Errorf("pressure: %w", err)
	}
	if result.temperature, err = thermodynamic(AstrodomePrimitiveTemperature); err != nil {
		return result, fmt.Errorf("temperature: %w", err)
	}
	if result.specificHumidity, err = thermodynamic(AstrodomePrimitiveSpecificHumidity); err != nil {
		return result, fmt.Errorf("specific humidity: %w", err)
	}
	if refractiveOnly {
		return result, nil
	}
	if result.cloudLiquid, err = full(AstrodomePrimitiveCloudLiquid); err != nil {
		return result, fmt.Errorf("cloud liquid: %w", err)
	}
	if result.cloudIce, err = full(AstrodomePrimitiveCloudIce); err != nil {
		return result, fmt.Errorf("cloud ice: %w", err)
	}
	if result.cloudFraction, err = full(AstrodomePrimitiveCloudFraction); err != nil {
		return result, fmt.Errorf("cloud fraction: %w", err)
	}
	if result.tke, err = half(AstrodomePrimitiveTKE); err != nil {
		return result, fmt.Errorf("TKE: %w", err)
	}
	horizontalWind, err := astrodomeTerrainFollowingHorizontalWind(
		columns, weights, geometry, brackets, heightM,
	)
	if err != nil {
		return result, fmt.Errorf("horizontal wind: %w", err)
	}
	verticalWind, err := astrodomeTerrainFollowingVerticalWind(
		columns, weights, geometry,
		brackets[astrodomeAtmosphericPrimitiveIndex(AstrodomePrimitiveVerticalWind)], heightM,
	)
	if err != nil {
		return result, fmt.Errorf("vertical wind: %w", err)
	}
	result.windECEF = horizontalWind.value.add(verticalWind.value)
	result.windDerivative = horizontalWind.derivative.add(verticalWind.derivative)
	return result, nil
}

func astrodomeBuildTerrainFollowingGeometry(
	columns [4]AstrodomePrimitiveColumn,
	weights [4]float64,
	bilinear *astrodomeBilinearGeometry,
	result *astrodomeTerrainFollowingGeometry,
) error {
	if result == nil {
		return fmt.Errorf("terrain-following geometry destination is required")
	}
	*result = astrodomeTerrainFollowingGeometry{}
	levelCount := len(columns[0].HalfLevelGeometry)
	if levelCount < 3 {
		return fmt.Errorf("terrain-following geometry needs at least three HHL levels")
	}
	if levelCount > astrodomeMaximumTerrainFollowingHalfLevels {
		return fmt.Errorf("terrain-following geometry has %d HHL levels, maximum is %d",
			levelCount, astrodomeMaximumTerrainFollowingHalfLevels)
	}
	for index := 1; index < len(columns); index++ {
		if len(columns[index].HalfLevelGeometry) != levelCount {
			return fmt.Errorf("terrain-following support columns have different HHL cardinality")
		}
	}
	result.halfLevelCount = levelCount
	result.fullLevelCount = levelCount - 1
	for level := range levelCount {
		values := [4]float64{}
		for column := range columns {
			values[column] = columns[column].HalfLevelGeometry[level].HeightM
		}
		result.halfLevels[level] = astrodomeCombineHorizontalScalar(values, weights, bilinear)
		if !finite(result.halfLevels[level].value) ||
			(level > 0 && result.halfLevels[level].value >= result.halfLevels[level-1].value) {
			return fmt.Errorf("interpolated HHL geometry is not strictly top-to-surface ordered")
		}
	}
	surfaceValues := [4]float64{}
	for column := range columns {
		surfaceValues[column] = columns[column].HSURFHeightM
	}
	result.surface = astrodomeCombineHorizontalScalar(surfaceValues, weights, bilinear)
	if !finite(result.surface.value) || math.Abs(result.surface.value-result.halfLevels[levelCount-1].value) > 1e-6 {
		return fmt.Errorf("interpolated HSURF and bottom HHL are inconsistent")
	}
	for level := range result.fullLevelCount {
		result.fullLevels[level] = astrodomeHorizontalScalar{
			value: 0.5 * (result.halfLevels[level].value + result.halfLevels[level+1].value),
			latitudePerDegree: 0.5 * (result.halfLevels[level].latitudePerDegree +
				result.halfLevels[level+1].latitudePerDegree),
			longitudePerDegree: 0.5 * (result.halfLevels[level].longitudePerDegree +
				result.halfLevels[level+1].longitudePerDegree),
		}
		if level > 0 && result.fullLevels[level].value >= result.fullLevels[level-1].value {
			return fmt.Errorf("interpolated full-level geometry is not strictly ordered")
		}
	}
	return nil
}

func astrodomeCombineHorizontalScalar(
	values [4]float64,
	weights [4]float64,
	geometry *astrodomeBilinearGeometry,
) astrodomeHorizontalScalar {
	result := astrodomeHorizontalScalar{value: astrodomeCompensatedWeightedSum(values[:], weights[:])}
	if geometry == nil {
		return result
	}
	sw := values[geometry.cornerIndex[0][0]]
	se := values[geometry.cornerIndex[0][1]]
	nw := values[geometry.cornerIndex[1][0]]
	ne := values[geometry.cornerIndex[1][1]]
	result.latitudePerDegree = ((1-geometry.longitudeT)*(nw-sw) +
		geometry.longitudeT*(ne-se)) / geometry.latitudeSpanDegrees
	result.longitudePerDegree = ((1-geometry.latitudeT)*(se-sw) +
		geometry.latitudeT*(ne-nw)) / geometry.longitudeSpanDegrees
	return result
}

func astrodomeTerrainFollowingFullAnchor(
	columns [4]AstrodomePrimitiveColumn,
	weights [4]float64,
	bilinear *astrodomeBilinearGeometry,
	geometry *astrodomeTerrainFollowingGeometry,
	field AstrodomePrimitiveField,
	bracket astrodomeTemporalBracket,
	level int,
) (astrodomeTerrainFollowingAnchor, error) {
	result := astrodomeTerrainFollowingAnchor{}
	if geometry == nil || level < 0 || level >= geometry.fullLevelCount {
		return result, fmt.Errorf("full-level anchor index is outside terrain-following geometry")
	}
	values := [4]float64{}
	for index, column := range columns {
		left, right, err := astrodomePrimitiveFrames(column.Frames, bracket)
		if err != nil {
			return result, err
		}
		leftValue, err := astrodomeFullLevelValue(left.FullLevels[level], field)
		if err != nil {
			return result, err
		}
		rightValue := leftValue
		if !bracket.right.Equal(bracket.left) {
			rightValue, err = astrodomeFullLevelValue(right.FullLevels[level], field)
			if err != nil {
				return result, err
			}
		}
		values[index] = astrodomeTemporalPrimitive(leftValue, rightValue, bracket.fraction)
	}
	result.height = geometry.fullLevels[level]
	result.value = astrodomeCombineHorizontalScalar(values, weights, bilinear)
	return result, nil
}

func astrodomeTerrainFollowingHalfAnchor(
	columns [4]AstrodomePrimitiveColumn,
	weights [4]float64,
	bilinear *astrodomeBilinearGeometry,
	geometry *astrodomeTerrainFollowingGeometry,
	field AstrodomePrimitiveField,
	bracket astrodomeTemporalBracket,
	level int,
) (astrodomeTerrainFollowingAnchor, error) {
	result := astrodomeTerrainFollowingAnchor{}
	if geometry == nil || level < 0 || level >= geometry.halfLevelCount {
		return result, fmt.Errorf("half-level anchor index is outside terrain-following geometry")
	}
	values := [4]float64{}
	for index, column := range columns {
		left, right, err := astrodomePrimitiveFrames(column.Frames, bracket)
		if err != nil {
			return result, err
		}
		leftValue, err := astrodomeHalfLevelValue(left.HalfLevels[level], field)
		if err != nil {
			return result, err
		}
		rightValue := leftValue
		if !bracket.right.Equal(bracket.left) {
			rightValue, err = astrodomeHalfLevelValue(right.HalfLevels[level], field)
			if err != nil {
				return result, err
			}
		}
		values[index] = astrodomeTemporalPrimitive(leftValue, rightValue, bracket.fraction)
	}
	result.height = geometry.halfLevels[level]
	result.value = astrodomeCombineHorizontalScalar(values, weights, bilinear)
	return result, nil
}

type astrodomeThermodynamicLowerAnchors struct {
	surfacePressure         astrodomeTerrainFollowingAnchor
	surfaceTemperature      astrodomeTerrainFollowingAnchor
	surfaceSpecificHumidity astrodomeTerrainFollowingAnchor
	pressure                astrodomeTerrainFollowingAnchor
	temperature             astrodomeTerrainFollowingAnchor
	specificHumidity        astrodomeTerrainFollowingAnchor
}

func astrodomeTerrainFollowingLowerAnchors(
	columns [4]AstrodomePrimitiveColumn,
	weights [4]float64,
	bilinear *astrodomeBilinearGeometry,
	geometry *astrodomeTerrainFollowingGeometry,
	brackets astrodomeRefractionSurfaceBracketSet,
) (astrodomeThermodynamicLowerAnchors, error) {
	result := astrodomeThermodynamicLowerAnchors{}
	surface := func(field AstrodomePrimitiveField) (astrodomeHorizontalScalar, error) {
		values := [4]float64{}
		bracket := brackets[astrodomeRefractionSurfacePrimitiveIndex(field)]
		for index, column := range columns {
			value, err := astrodomeSurfacePrimitiveAt(column.Frames, field, bracket)
			if err != nil {
				return astrodomeHorizontalScalar{}, err
			}
			values[index] = value
		}
		return astrodomeCombineHorizontalScalar(values, weights, bilinear), nil
	}
	pressureSurface, err := surface(AstrodomePrimitiveSurfacePressure)
	if err != nil {
		return result, fmt.Errorf("surface pressure: %w", err)
	}
	temperature, err := surface(AstrodomePrimitiveTemperature2M)
	if err != nil {
		return result, fmt.Errorf("two-metre temperature: %w", err)
	}
	humidity, err := surface(AstrodomePrimitiveSpecificHumidity2M)
	if err != nil {
		return result, fmt.Errorf("two-metre specific humidity: %w", err)
	}
	calibration := DefaultAstrodomeScienceCalibration()
	pressure, err := AstrodomeHydrostaticPressureAtAperture(
		pressureSurface.value, temperature.value, humidity.value,
		calibration.DryAirGasConstantJKgK, calibration.WaterVapourGasConstantJKgK,
	)
	if err != nil {
		return result, err
	}
	mixGasConstant := (1-humidity.value)*calibration.DryAirGasConstantJKgK +
		humidity.value*calibration.WaterVapourGasConstantJKgK
	exponent := -AstrodomeICONReferenceGravityMS2 * AstrodomeRefractionApertureHeightAGLM /
		(mixGasConstant * temperature.value)
	pressureDerivative := func(
		pressureSurfaceDerivative, temperatureDerivative, humidityDerivative float64,
	) float64 {
		mixDerivative := (calibration.WaterVapourGasConstantJKgK - calibration.DryAirGasConstantJKgK) *
			humidityDerivative
		exponentDerivative := -exponent * (mixDerivative/mixGasConstant +
			temperatureDerivative/temperature.value)
		return pressure * (pressureSurfaceDerivative/pressureSurface.value + exponentDerivative)
	}
	pressure2M := astrodomeHorizontalScalar{
		value: pressure,
		latitudePerDegree: pressureDerivative(
			pressureSurface.latitudePerDegree, temperature.latitudePerDegree, humidity.latitudePerDegree,
		),
		longitudePerDegree: pressureDerivative(
			pressureSurface.longitudePerDegree, temperature.longitudePerDegree, humidity.longitudePerDegree,
		),
	}
	aperture := geometry.surface
	aperture.value += AstrodomeRefractionApertureHeightAGLM
	result.surfacePressure = astrodomeTerrainFollowingAnchor{height: geometry.surface, value: pressureSurface}
	result.surfaceTemperature = astrodomeTerrainFollowingAnchor{height: geometry.surface, value: temperature}
	result.surfaceSpecificHumidity = astrodomeTerrainFollowingAnchor{height: geometry.surface, value: humidity}
	result.pressure = astrodomeTerrainFollowingAnchor{height: aperture, value: pressure2M}
	result.temperature = astrodomeTerrainFollowingAnchor{height: aperture, value: temperature}
	result.specificHumidity = astrodomeTerrainFollowingAnchor{height: aperture, value: humidity}
	return result, nil
}

func astrodomeTerrainFollowingThermodynamicPrimitive(
	columns [4]AstrodomePrimitiveColumn,
	weights [4]float64,
	bilinear *astrodomeBilinearGeometry,
	geometry *astrodomeTerrainFollowingGeometry,
	lower astrodomeThermodynamicLowerAnchors,
	field AstrodomePrimitiveField,
	brackets astrodomeTemporalBracketSet,
	heightM float64,
) (astrodomeTerrainFollowingSample, error) {
	var surfaceAnchor, lowerAnchor astrodomeTerrainFollowingAnchor
	switch field {
	case AstrodomePrimitivePressure:
		surfaceAnchor = lower.surfacePressure
		lowerAnchor = lower.pressure
	case AstrodomePrimitiveTemperature:
		surfaceAnchor = lower.surfaceTemperature
		lowerAnchor = lower.temperature
	case AstrodomePrimitiveSpecificHumidity:
		surfaceAnchor = lower.surfaceSpecificHumidity
		lowerAnchor = lower.specificHumidity
	default:
		return astrodomeTerrainFollowingSample{}, fmt.Errorf("primitive is not thermodynamic P/T/QV")
	}
	bracket := brackets[astrodomeAtmosphericPrimitiveIndex(field)]
	const boundaryToleranceM = 1e-7
	if heightM < surfaceAnchor.height.value-AstrodomeRefractionPrimitiveEventGuardM-boundaryToleranceM {
		return astrodomeTerrainFollowingSample{}, fmt.Errorf(
			"height %.3f m lies beyond the local terrain event guard ending at %.3f m",
			heightM, surfaceAnchor.height.value-AstrodomeRefractionPrimitiveEventGuardM,
		)
	}
	if heightM < surfaceAnchor.height.value {
		if field != AstrodomePrimitivePressure {
			return astrodomeConstantTerrainFollowingSample(surfaceAnchor.value), nil
		}
		return astrodomeTerrainFollowingHydrostaticPressure(
			surfaceAnchor, lower.surfaceTemperature, lower.surfaceSpecificHumidity, heightM,
		), nil
	}
	if heightM < lowerAnchor.height.value {
		return astrodomeInterpolateMovingVerticalPair(
			surfaceAnchor, lowerAnchor, heightM, field == AstrodomePrimitivePressure,
		)
	}
	topFull := geometry.fullLevels[0]
	if heightM > topFull.value {
		if heightM > geometry.halfLevels[0].value+AstrodomeRefractionPrimitiveEventGuardM {
			return astrodomeTerrainFollowingSample{}, fmt.Errorf(
				"height %.3f m lies beyond the local HHL1 event guard ending at %.3f m",
				heightM, geometry.halfLevels[0].value+AstrodomeRefractionPrimitiveEventGuardM,
			)
		}
		top, err := astrodomeTerrainFollowingFullAnchor(columns, weights, bilinear, geometry, field, bracket, 0)
		if err != nil {
			return astrodomeTerrainFollowingSample{}, err
		}
		if field != AstrodomePrimitivePressure {
			return astrodomeConstantTerrainFollowingSample(top.value), nil
		}
		temperature, err := astrodomeTerrainFollowingFullAnchor(
			columns, weights, bilinear, geometry, AstrodomePrimitiveTemperature,
			brackets[astrodomeAtmosphericPrimitiveIndex(AstrodomePrimitiveTemperature)], 0,
		)
		if err != nil {
			return astrodomeTerrainFollowingSample{}, err
		}
		humidity, err := astrodomeTerrainFollowingFullAnchor(
			columns, weights, bilinear, geometry, AstrodomePrimitiveSpecificHumidity,
			brackets[astrodomeAtmosphericPrimitiveIndex(AstrodomePrimitiveSpecificHumidity)], 0,
		)
		if err != nil {
			return astrodomeTerrainFollowingSample{}, err
		}
		return astrodomeTerrainFollowingHydrostaticPressure(top, temperature, humidity, heightM), nil
	}
	bottomIndex := geometry.fullLevelCount - 1
	bottomFull := geometry.fullLevels[bottomIndex]
	if heightM < bottomFull.value {
		upper, err := astrodomeTerrainFollowingFullAnchor(columns, weights, bilinear, geometry, field, bracket, bottomIndex)
		if err != nil {
			return astrodomeTerrainFollowingSample{}, err
		}
		return astrodomeInterpolateMovingVerticalPair(lowerAnchor, upper, heightM, field == AstrodomePrimitivePressure)
	}
	lowerIndex, upperIndex, err := astrodomeTerrainFollowingBracket(geometry.fullLevelSlice(), heightM)
	if err != nil {
		return astrodomeTerrainFollowingSample{}, err
	}
	lowerLevel, err := astrodomeTerrainFollowingFullAnchor(columns, weights, bilinear, geometry, field, bracket, lowerIndex)
	if err != nil {
		return astrodomeTerrainFollowingSample{}, err
	}
	upperLevel, err := astrodomeTerrainFollowingFullAnchor(columns, weights, bilinear, geometry, field, bracket, upperIndex)
	if err != nil {
		return astrodomeTerrainFollowingSample{}, err
	}
	return astrodomeInterpolateMovingVerticalPair(lowerLevel, upperLevel, heightM, field == AstrodomePrimitivePressure)
}

func astrodomeTerrainFollowingHydrostaticPressure(
	pressure,
	temperature,
	humidity astrodomeTerrainFollowingAnchor,
	heightM float64,
) astrodomeTerrainFollowingSample {
	calibration := DefaultAstrodomeScienceCalibration()
	mixGasConstant := (1-humidity.value.value)*calibration.DryAirGasConstantJKgK +
		humidity.value.value*calibration.WaterVapourGasConstantJKgK
	exponentPerM := -AstrodomeICONReferenceGravityMS2 / (mixGasConstant * temperature.value.value)
	deltaHeight := heightM - pressure.height.value
	value := pressure.value.value * math.Exp(exponentPerM*deltaHeight)
	horizontalDerivative := func(
		pressureDerivative, temperatureDerivative, humidityDerivative, heightDerivative float64,
	) float64 {
		mixDerivative := (calibration.WaterVapourGasConstantJKgK - calibration.DryAirGasConstantJKgK) *
			humidityDerivative
		exponentDerivative := -exponentPerM * (mixDerivative/mixGasConstant +
			temperatureDerivative/temperature.value.value)
		return value * (pressureDerivative/pressure.value.value + exponentDerivative*deltaHeight -
			exponentPerM*heightDerivative)
	}
	return astrodomeTerrainFollowingSample{
		astrodomePrimitiveSample: astrodomePrimitiveSample{value: value, derivative: exponentPerM * value},
		latitudePerDegree: horizontalDerivative(
			pressure.value.latitudePerDegree, temperature.value.latitudePerDegree,
			humidity.value.latitudePerDegree, pressure.height.latitudePerDegree,
		),
		longitudePerDegree: horizontalDerivative(
			pressure.value.longitudePerDegree, temperature.value.longitudePerDegree,
			humidity.value.longitudePerDegree, pressure.height.longitudePerDegree,
		),
	}
}

func astrodomeTerrainFollowingFullPrimitive(
	columns [4]AstrodomePrimitiveColumn,
	weights [4]float64,
	bilinear *astrodomeBilinearGeometry,
	geometry *astrodomeTerrainFollowingGeometry,
	field AstrodomePrimitiveField,
	bracket astrodomeTemporalBracket,
	heightM float64,
) (astrodomeTerrainFollowingSample, error) {
	if !astrodomeFullLevelField(field) || field == AstrodomePrimitivePressure ||
		field == AstrodomePrimitiveTemperature || field == AstrodomePrimitiveSpecificHumidity {
		return astrodomeTerrainFollowingSample{}, fmt.Errorf("primitive is not a non-thermodynamic full-level field")
	}
	const boundaryToleranceM = 1e-7
	if heightM < geometry.surface.value-boundaryToleranceM ||
		heightM > geometry.halfLevels[0].value+boundaryToleranceM {
		return astrodomeTerrainFollowingSample{}, fmt.Errorf(
			"height %.3f m lies outside local HHL cell support %.3f..%.3f m",
			heightM, geometry.surface.value, geometry.halfLevels[0].value,
		)
	}
	heightM = clampSurfaceValue(heightM, geometry.surface.value, geometry.halfLevels[0].value)
	bottomIndex := geometry.fullLevelCount - 1
	if heightM < geometry.fullLevels[bottomIndex].value {
		anchor, err := astrodomeTerrainFollowingFullAnchor(
			columns, weights, bilinear, geometry, field, bracket, bottomIndex,
		)
		if err != nil {
			return astrodomeTerrainFollowingSample{}, err
		}
		result := astrodomeConstantTerrainFollowingSample(anchor.value)
		result.cloudSupport = astrodomeCloudFractionVerticalSupport{
			lowerLevelIndex: bottomIndex, upperLevelIndex: bottomIndex,
			mode: astrodomeCloudFractionSupportConstantBottom,
		}
		return result, nil
	}
	if heightM > geometry.fullLevels[0].value {
		anchor, err := astrodomeTerrainFollowingFullAnchor(columns, weights, bilinear, geometry, field, bracket, 0)
		if err != nil {
			return astrodomeTerrainFollowingSample{}, err
		}
		result := astrodomeConstantTerrainFollowingSample(anchor.value)
		result.cloudSupport = astrodomeCloudFractionVerticalSupport{
			lowerLevelIndex: 0, upperLevelIndex: 0,
			mode: astrodomeCloudFractionSupportConstantTop,
		}
		return result, nil
	}
	lowerIndex, upperIndex, err := astrodomeTerrainFollowingBracket(geometry.fullLevelSlice(), heightM)
	if err != nil {
		return astrodomeTerrainFollowingSample{}, err
	}
	lower, err := astrodomeTerrainFollowingFullAnchor(columns, weights, bilinear, geometry, field, bracket, lowerIndex)
	if err != nil {
		return astrodomeTerrainFollowingSample{}, err
	}
	upper, err := astrodomeTerrainFollowingFullAnchor(columns, weights, bilinear, geometry, field, bracket, upperIndex)
	if err != nil {
		return astrodomeTerrainFollowingSample{}, err
	}
	result, err := astrodomeInterpolateMovingVerticalPair(lower, upper, heightM, false)
	if err != nil {
		return astrodomeTerrainFollowingSample{}, err
	}
	result.cloudSupport = astrodomeCloudFractionVerticalSupport{
		lowerLevelIndex: lowerIndex, upperLevelIndex: upperIndex,
		mode: astrodomeCloudFractionSupportPair,
	}
	return result, nil
}

func astrodomeTerrainFollowingHalfPrimitive(
	columns [4]AstrodomePrimitiveColumn,
	weights [4]float64,
	bilinear *astrodomeBilinearGeometry,
	geometry *astrodomeTerrainFollowingGeometry,
	field AstrodomePrimitiveField,
	bracket astrodomeTemporalBracket,
	heightM float64,
) (astrodomeTerrainFollowingSample, error) {
	if !astrodomeHalfLevelField(field) {
		return astrodomeTerrainFollowingSample{}, fmt.Errorf("primitive is not a half-level field")
	}
	const boundaryToleranceM = 1e-7
	if heightM < geometry.surface.value-boundaryToleranceM ||
		heightM > geometry.halfLevels[0].value+boundaryToleranceM {
		return astrodomeTerrainFollowingSample{}, fmt.Errorf(
			"height %.3f m lies outside local HHL support %.3f..%.3f m",
			heightM, geometry.surface.value, geometry.halfLevels[0].value,
		)
	}
	heightM = clampSurfaceValue(heightM, geometry.surface.value, geometry.halfLevels[0].value)
	lowerIndex, upperIndex, err := astrodomeTerrainFollowingBracket(geometry.halfLevelSlice(), heightM)
	if err != nil {
		return astrodomeTerrainFollowingSample{}, err
	}
	lower, err := astrodomeTerrainFollowingHalfAnchor(columns, weights, bilinear, geometry, field, bracket, lowerIndex)
	if err != nil {
		return astrodomeTerrainFollowingSample{}, err
	}
	upper, err := astrodomeTerrainFollowingHalfAnchor(columns, weights, bilinear, geometry, field, bracket, upperIndex)
	if err != nil {
		return astrodomeTerrainFollowingSample{}, err
	}
	return astrodomeInterpolateMovingVerticalPair(lower, upper, heightM, false)
}

func astrodomeTerrainFollowingBracket(
	heights []astrodomeHorizontalScalar,
	heightM float64,
) (lowerIndex, upperIndex int, err error) {
	if len(heights) < 2 || heightM < heights[len(heights)-1].value || heightM > heights[0].value {
		return 0, 0, fmt.Errorf("height %.3f m lies outside terrain-following vertical support", heightM)
	}
	firstAtOrBelow := sort.Search(len(heights), func(index int) bool { return heights[index].value <= heightM })
	if firstAtOrBelow == 0 {
		return 1, 0, nil
	}
	if firstAtOrBelow >= len(heights) {
		return 0, 0, fmt.Errorf("failed to locate terrain-following vertical bracket")
	}
	return firstAtOrBelow, firstAtOrBelow - 1, nil
}

func astrodomeInterpolateMovingVerticalPair(
	lower,
	upper astrodomeTerrainFollowingAnchor,
	heightM float64,
	logarithmic bool,
) (astrodomeTerrainFollowingSample, error) {
	deltaHeight := upper.height.value - lower.height.value
	if !finite(deltaHeight) || deltaHeight <= 0 || heightM < lower.height.value || heightM > upper.height.value {
		return astrodomeTerrainFollowingSample{}, fmt.Errorf("terrain-following vertical primitive bracket is invalid")
	}
	beta := (heightM - lower.height.value) / deltaHeight
	betaDerivative := func(lowerHeightDerivative, upperHeightDerivative float64) float64 {
		return -((1-beta)*lowerHeightDerivative + beta*upperHeightDerivative) / deltaHeight
	}
	latitudeBeta := betaDerivative(lower.height.latitudePerDegree, upper.height.latitudePerDegree)
	longitudeBeta := betaDerivative(lower.height.longitudePerDegree, upper.height.longitudePerDegree)
	if logarithmic {
		if lower.value.value <= 0 || upper.value.value <= 0 {
			return astrodomeTerrainFollowingSample{}, fmt.Errorf("logarithmic terrain-following primitive is not positive")
		}
		lowerLog, upperLog := math.Log(lower.value.value), math.Log(upper.value.value)
		value := math.Exp((1-beta)*lowerLog + beta*upperLog)
		horizontalDerivative := func(lowerDerivative, upperDerivative, betaDerivative float64) float64 {
			return value * ((1-beta)*lowerDerivative/lower.value.value +
				beta*upperDerivative/upper.value.value + (upperLog-lowerLog)*betaDerivative)
		}
		return astrodomeTerrainFollowingSample{
			astrodomePrimitiveSample: astrodomePrimitiveSample{
				value: value, derivative: value * (upperLog - lowerLog) / deltaHeight,
			},
			latitudePerDegree: horizontalDerivative(
				lower.value.latitudePerDegree, upper.value.latitudePerDegree, latitudeBeta,
			),
			longitudePerDegree: horizontalDerivative(
				lower.value.longitudePerDegree, upper.value.longitudePerDegree, longitudeBeta,
			),
		}, nil
	}
	value := (1-beta)*lower.value.value + beta*upper.value.value
	horizontalDerivative := func(lowerDerivative, upperDerivative, betaDerivative float64) float64 {
		return (1-beta)*lowerDerivative + beta*upperDerivative +
			(upper.value.value-lower.value.value)*betaDerivative
	}
	return astrodomeTerrainFollowingSample{
		astrodomePrimitiveSample: astrodomePrimitiveSample{
			value: value, derivative: (upper.value.value - lower.value.value) / deltaHeight,
		},
		latitudePerDegree: horizontalDerivative(
			lower.value.latitudePerDegree, upper.value.latitudePerDegree, latitudeBeta,
		),
		longitudePerDegree: horizontalDerivative(
			lower.value.longitudePerDegree, upper.value.longitudePerDegree, longitudeBeta,
		),
	}, nil
}

func astrodomeConstantTerrainFollowingSample(value astrodomeHorizontalScalar) astrodomeTerrainFollowingSample {
	return astrodomeTerrainFollowingSample{
		astrodomePrimitiveSample: astrodomePrimitiveSample{value: value.value},
		latitudePerDegree:        value.latitudePerDegree, longitudePerDegree: value.longitudePerDegree,
	}
}

type astrodomeTerrainFollowingVectorAnchor struct {
	height astrodomeHorizontalScalar
	value  AstrodomeECEFVector
}

type astrodomeTerrainFollowingVectorSample struct {
	value      AstrodomeECEFVector
	derivative AstrodomeECEFVector
}

func astrodomeTerrainFollowingHorizontalWind(
	columns [4]AstrodomePrimitiveColumn,
	weights [4]float64,
	geometry *astrodomeTerrainFollowingGeometry,
	brackets astrodomeTemporalBracketSet,
	heightM float64,
) (astrodomeTerrainFollowingVectorSample, error) {
	anchor := func(level int) (astrodomeTerrainFollowingVectorAnchor, error) {
		vectors := [4]AstrodomeECEFVector{}
		for index, column := range columns {
			u, err := astrodomeFullLevelTemporalValue(column, AstrodomePrimitiveEastwardWind,
				brackets[astrodomeAtmosphericPrimitiveIndex(AstrodomePrimitiveEastwardWind)], level)
			if err != nil {
				return astrodomeTerrainFollowingVectorAnchor{}, err
			}
			v, err := astrodomeFullLevelTemporalValue(column, AstrodomePrimitiveNorthwardWind,
				brackets[astrodomeAtmosphericPrimitiveIndex(AstrodomePrimitiveNorthwardWind)], level)
			if err != nil {
				return astrodomeTerrainFollowingVectorAnchor{}, err
			}
			_, east, north, _ := astrodomeObserverBasis(column.Location, 0)
			vectors[index] = east.scale(u).add(north.scale(v))
		}
		return astrodomeTerrainFollowingVectorAnchor{
			height: geometry.fullLevels[level], value: astrodomeWeightedVector4(vectors, weights),
		}, nil
	}
	return astrodomeTerrainFollowingVectorAtFullLevels(geometry, heightM, anchor)
}

func astrodomeTerrainFollowingVerticalWind(
	columns [4]AstrodomePrimitiveColumn,
	weights [4]float64,
	geometry *astrodomeTerrainFollowingGeometry,
	bracket astrodomeTemporalBracket,
	heightM float64,
) (astrodomeTerrainFollowingVectorSample, error) {
	const boundaryToleranceM = 1e-7
	if heightM < geometry.surface.value-boundaryToleranceM ||
		heightM > geometry.halfLevels[0].value+boundaryToleranceM {
		return astrodomeTerrainFollowingVectorSample{}, fmt.Errorf("vertical-wind height is outside local HHL support")
	}
	heightM = clampSurfaceValue(heightM, geometry.surface.value, geometry.halfLevels[0].value)
	lowerIndex, upperIndex, err := astrodomeTerrainFollowingBracket(geometry.halfLevelSlice(), heightM)
	if err != nil {
		return astrodomeTerrainFollowingVectorSample{}, err
	}
	anchor := func(level int) (astrodomeTerrainFollowingVectorAnchor, error) {
		vectors := [4]AstrodomeECEFVector{}
		for index, column := range columns {
			w, valueErr := astrodomeHalfLevelTemporalValue(
				column, AstrodomePrimitiveVerticalWind, bracket, level,
			)
			if valueErr != nil {
				return astrodomeTerrainFollowingVectorAnchor{}, valueErr
			}
			_, _, _, up := astrodomeObserverBasis(column.Location, 0)
			vectors[index] = up.scale(w)
		}
		return astrodomeTerrainFollowingVectorAnchor{
			height: geometry.halfLevels[level], value: astrodomeWeightedVector4(vectors, weights),
		}, nil
	}
	lower, err := anchor(lowerIndex)
	if err != nil {
		return astrodomeTerrainFollowingVectorSample{}, err
	}
	upper, err := anchor(upperIndex)
	if err != nil {
		return astrodomeTerrainFollowingVectorSample{}, err
	}
	return astrodomeInterpolateMovingVectorPair(lower, upper, heightM), nil
}

func astrodomeTerrainFollowingVectorAtFullLevels(
	geometry *astrodomeTerrainFollowingGeometry,
	heightM float64,
	anchor func(int) (astrodomeTerrainFollowingVectorAnchor, error),
) (astrodomeTerrainFollowingVectorSample, error) {
	const boundaryToleranceM = 1e-7
	if heightM < geometry.surface.value-boundaryToleranceM ||
		heightM > geometry.halfLevels[0].value+boundaryToleranceM {
		return astrodomeTerrainFollowingVectorSample{}, fmt.Errorf("horizontal-wind height is outside local HHL support")
	}
	heightM = clampSurfaceValue(heightM, geometry.surface.value, geometry.halfLevels[0].value)
	bottom := geometry.fullLevelCount - 1
	if heightM < geometry.fullLevels[bottom].value {
		value, err := anchor(bottom)
		return astrodomeTerrainFollowingVectorSample{value: value.value}, err
	}
	if heightM > geometry.fullLevels[0].value {
		value, err := anchor(0)
		return astrodomeTerrainFollowingVectorSample{value: value.value}, err
	}
	lowerIndex, upperIndex, err := astrodomeTerrainFollowingBracket(geometry.fullLevelSlice(), heightM)
	if err != nil {
		return astrodomeTerrainFollowingVectorSample{}, err
	}
	lower, err := anchor(lowerIndex)
	if err != nil {
		return astrodomeTerrainFollowingVectorSample{}, err
	}
	upper, err := anchor(upperIndex)
	if err != nil {
		return astrodomeTerrainFollowingVectorSample{}, err
	}
	return astrodomeInterpolateMovingVectorPair(lower, upper, heightM), nil
}

func astrodomeInterpolateMovingVectorPair(
	lower,
	upper astrodomeTerrainFollowingVectorAnchor,
	heightM float64,
) astrodomeTerrainFollowingVectorSample {
	deltaHeight := upper.height.value - lower.height.value
	beta := (heightM - lower.height.value) / deltaHeight
	return astrodomeTerrainFollowingVectorSample{
		value:      lower.value.scale(1 - beta).add(upper.value.scale(beta)),
		derivative: upper.value.add(lower.value.scale(-1)).scale(1 / deltaHeight),
	}
}

func astrodomeFullLevelTemporalValue(
	column AstrodomePrimitiveColumn,
	field AstrodomePrimitiveField,
	bracket astrodomeTemporalBracket,
	level int,
) (float64, error) {
	left, right, err := astrodomePrimitiveFrames(column.Frames, bracket)
	if err != nil {
		return 0, err
	}
	leftValue, err := astrodomeFullLevelValue(left.FullLevels[level], field)
	if err != nil {
		return 0, err
	}
	rightValue := leftValue
	if !bracket.right.Equal(bracket.left) {
		rightValue, err = astrodomeFullLevelValue(right.FullLevels[level], field)
		if err != nil {
			return 0, err
		}
	}
	return astrodomeTemporalPrimitive(leftValue, rightValue, bracket.fraction), nil
}

func astrodomeHalfLevelTemporalValue(
	column AstrodomePrimitiveColumn,
	field AstrodomePrimitiveField,
	bracket astrodomeTemporalBracket,
	level int,
) (float64, error) {
	left, right, err := astrodomePrimitiveFrames(column.Frames, bracket)
	if err != nil {
		return 0, err
	}
	leftValue, err := astrodomeHalfLevelValue(left.HalfLevels[level], field)
	if err != nil {
		return 0, err
	}
	rightValue := leftValue
	if !bracket.right.Equal(bracket.left) {
		rightValue, err = astrodomeHalfLevelValue(right.HalfLevels[level], field)
		if err != nil {
			return 0, err
		}
	}
	return astrodomeTemporalPrimitive(leftValue, rightValue, bracket.fraction), nil
}

func astrodomeWeightedVector4(values [4]AstrodomeECEFVector, weights [4]float64) AstrodomeECEFVector {
	x := [4]float64{values[0].X, values[1].X, values[2].X, values[3].X}
	y := [4]float64{values[0].Y, values[1].Y, values[2].Y, values[3].Y}
	z := [4]float64{values[0].Z, values[1].Z, values[2].Z, values[3].Z}
	return AstrodomeECEFVector{
		X: astrodomeCompensatedWeightedSum(x[:], weights[:]),
		Y: astrodomeCompensatedWeightedSum(y[:], weights[:]),
		Z: astrodomeCompensatedWeightedSum(z[:], weights[:]),
	}
}

func astrodomeTerrainFollowingECEFGradient(
	state AstrodomeReconstructedAtmosphere,
	sample astrodomeTerrainFollowingSample,
) AstrodomeECEFVector {
	radius := AstrodomeICONSphereRadiusM + state.HeightM
	latitudeRadians := state.Location.Latitude * math.Pi / 180
	_, east, north, up := astrodomeObserverBasis(state.Location, state.HeightM)
	perRadian := 180 / math.Pi
	return up.scale(sample.derivative).
		add(north.scale(sample.latitudePerDegree * perRadian / radius)).
		add(east.scale(sample.longitudePerDegree * perRadian / (radius * math.Cos(latitudeRadians))))
}

func astrodomeSurfacePrimitiveAt(
	frames []AstrodomePrimitiveColumnFrame,
	field AstrodomePrimitiveField,
	bracket astrodomeTemporalBracket,
) (float64, error) {
	left, right, err := astrodomePrimitiveFrames(frames, bracket)
	if err != nil {
		return 0, err
	}
	leftValue, err := astrodomeSurfacePrimitiveValue(left.Surface, field)
	if err != nil {
		return 0, err
	}
	rightValue := leftValue
	if !bracket.right.Equal(bracket.left) {
		rightValue, err = astrodomeSurfacePrimitiveValue(right.Surface, field)
		if err != nil {
			return 0, err
		}
	}
	return astrodomeTemporalPrimitive(leftValue, rightValue, bracket.fraction), nil
}

func astrodomeSurfacePrimitiveValue(surface AstrodomeSurfacePrimitives, field AstrodomePrimitiveField) (float64, error) {
	if err := validateAstrodomeRefractionSurfaceField(surface, field); err != nil {
		return 0, err
	}
	switch field {
	case AstrodomePrimitiveSurfacePressure:
		return surface.SurfacePressurePa, nil
	case AstrodomePrimitiveTemperature2M:
		return surface.Temperature2MK, nil
	case AstrodomePrimitiveSpecificHumidity2M:
		return surface.SpecificHumidity2MKgKg, nil
	default:
		return 0, fmt.Errorf("field is not a refraction lower-boundary primitive")
	}
}

func astrodomePrimitiveFrames(
	frames []AstrodomePrimitiveColumnFrame,
	bracket astrodomeTemporalBracket,
) (AstrodomePrimitiveColumnFrame, AstrodomePrimitiveColumnFrame, error) {
	left, ok := astrodomeColumnFrameAt(frames, bracket.left)
	if !ok {
		return AstrodomePrimitiveColumnFrame{}, AstrodomePrimitiveColumnFrame{},
			fmt.Errorf("missing left native frame %s", bracket.left.Format(time.RFC3339))
	}
	right := left
	if !bracket.right.Equal(bracket.left) {
		var found bool
		right, found = astrodomeColumnFrameAt(frames, bracket.right)
		if !found {
			return AstrodomePrimitiveColumnFrame{}, AstrodomePrimitiveColumnFrame{},
				fmt.Errorf("missing right native frame %s", bracket.right.Format(time.RFC3339))
		}
	}
	return left, right, nil
}

func astrodomeTemporalPrimitive(left, right, fraction float64) float64 {
	if fraction == 0 {
		return left
	}
	values := [2]float64{left, right}
	weights := [2]float64{1 - fraction, fraction}
	return astrodomeCompensatedWeightedSum(values[:], weights[:])
}

func astrodomeColumnFrameAt(frames []AstrodomePrimitiveColumnFrame, validAt time.Time) (AstrodomePrimitiveColumnFrame, bool) {
	index := sort.Search(len(frames), func(index int) bool { return !frames[index].ValidAt.Before(validAt) })
	if index < len(frames) && frames[index].ValidAt.Equal(validAt) {
		return frames[index], true
	}
	return AstrodomePrimitiveColumnFrame{}, false
}

func astrodomeFullLevelField(field AstrodomePrimitiveField) bool {
	switch field {
	case AstrodomePrimitivePressure, AstrodomePrimitiveTemperature, AstrodomePrimitiveSpecificHumidity,
		AstrodomePrimitiveCloudLiquid, AstrodomePrimitiveCloudIce, AstrodomePrimitiveCloudFraction,
		AstrodomePrimitiveEastwardWind, AstrodomePrimitiveNorthwardWind:
		return true
	default:
		return false
	}
}

func astrodomeHalfLevelField(field AstrodomePrimitiveField) bool {
	return field == AstrodomePrimitiveVerticalWind || field == AstrodomePrimitiveTKE
}

func astrodomeFullLevelValue(level AstrodomeFullLevelPrimitives, field AstrodomePrimitiveField) (float64, error) {
	if !level.Available.Has(field) {
		return 0, fmt.Errorf("mandatory primitive is unavailable")
	}
	switch field {
	case AstrodomePrimitivePressure:
		return level.PressurePa, nil
	case AstrodomePrimitiveTemperature:
		return level.TemperatureK, nil
	case AstrodomePrimitiveSpecificHumidity:
		return level.SpecificHumidityKgKg, nil
	case AstrodomePrimitiveCloudLiquid:
		return level.CloudLiquidKgKg, nil
	case AstrodomePrimitiveCloudIce:
		return level.CloudIceKgKg, nil
	case AstrodomePrimitiveCloudFraction:
		return level.CloudFraction, nil
	case AstrodomePrimitiveEastwardWind:
		return level.EastwardWindMS, nil
	case AstrodomePrimitiveNorthwardWind:
		return level.NorthwardWindMS, nil
	default:
		return 0, fmt.Errorf("primitive is not stored on a full level")
	}
}

func astrodomeHalfLevelValue(level AstrodomeHalfLevelPrimitives, field AstrodomePrimitiveField) (float64, error) {
	if !level.Available.Has(field) {
		return 0, fmt.Errorf("mandatory primitive is unavailable")
	}
	switch field {
	case AstrodomePrimitiveVerticalWind:
		return level.VerticalWindMS, nil
	case AstrodomePrimitiveTKE:
		return level.TKEJkg, nil
	default:
		return 0, fmt.Errorf("primitive is not stored on a half level")
	}
}

func validateAstrodomePrimitiveValue(field AstrodomePrimitiveField, value float64) error {
	if !finite(value) {
		return fmt.Errorf("primitive is not finite")
	}
	switch field {
	case AstrodomePrimitivePressure, AstrodomePrimitiveTemperature:
		if value <= 0 {
			return fmt.Errorf("primitive must be positive")
		}
	case AstrodomePrimitiveSpecificHumidity:
		if value < 0 || value >= 1 {
			return fmt.Errorf("specific humidity must be in [0,1)")
		}
	case AstrodomePrimitiveCloudLiquid, AstrodomePrimitiveCloudIce, AstrodomePrimitiveTKE:
		if value < 0 {
			return fmt.Errorf("primitive must be non-negative")
		}
	case AstrodomePrimitiveCloudFraction:
		if value < 0 || value > 1 {
			return fmt.Errorf("cloud fraction must be in [0,1]")
		}
	}
	return nil
}

func astrodomeCompensatedWeightedSum(values, weights []float64) float64 {
	sum, correction := 0.0, 0.0
	for index, value := range values {
		term := value * weights[index]
		next := sum + term
		if math.Abs(sum) >= math.Abs(term) {
			correction += (sum - next) + term
		} else {
			correction += (term - next) + sum
		}
		sum = next
	}
	return sum + correction
}

func astrodomeSameGridLocation(a, b Location) bool {
	return math.Abs(a.Latitude-b.Latitude) <= 1e-12 &&
		math.Abs(normalizeAstrodomeLongitude(a.Longitude-b.Longitude)) <= 1e-12
}

func cloneAstrodomePrimitiveColumn(column AstrodomePrimitiveColumn) AstrodomePrimitiveColumn {
	clone := column
	clone.HalfLevelGeometry = append([]AstrodomeHalfLevelGeometry(nil), column.HalfLevelGeometry...)
	clone.Frames = append([]AstrodomePrimitiveColumnFrame(nil), column.Frames...)
	for index := range clone.Frames {
		clone.Frames[index].FullLevels = append([]AstrodomeFullLevelPrimitives(nil), column.Frames[index].FullLevels...)
		clone.Frames[index].HalfLevels = append([]AstrodomeHalfLevelPrimitives(nil), column.Frames[index].HalfLevels...)
	}
	return clone
}
