package forecast

import (
	"context"
	"fmt"
	"math"
	"time"
)

const AstrodomePrimitiveInputContractVersion = "astrodome-icon-primitives-v2"

// AstrodomePrimitiveField identifies a native meteorological quantity, never
// a derived science product. In particular, seeing, tau0, transmission,
// Overall and quality diagnostics are deliberately absent from this type.
type AstrodomePrimitiveField uint64

const (
	AstrodomePrimitivePressure AstrodomePrimitiveField = 1 << iota
	AstrodomePrimitiveTemperature
	AstrodomePrimitiveSpecificHumidity
	AstrodomePrimitiveCloudLiquid
	AstrodomePrimitiveCloudIce
	AstrodomePrimitiveCloudFraction
	AstrodomePrimitiveEastwardWind
	AstrodomePrimitiveNorthwardWind
	AstrodomePrimitiveVerticalWind
	AstrodomePrimitiveTKE
	AstrodomePrimitiveMixedLayerDepth
	AstrodomePrimitiveVisibility
	AstrodomePrimitiveRelativeHumidity
	AstrodomePrimitiveTemperature2M
	AstrodomePrimitiveDewPoint2M
	AstrodomePrimitiveEastwardWind10M
	AstrodomePrimitiveNorthwardWind10M
	AstrodomePrimitiveWindGust10M
	AstrodomePrimitivePrecipitationAccumulation
	AstrodomePrimitiveTotalColumnWaterVapour
	AstrodomePrimitiveTotalColumnCloudLiquid
	AstrodomePrimitiveTotalColumnCloudIce
	AstrodomePrimitiveSurfacePressure
	AstrodomePrimitiveSpecificHumidity2M
)

// AstrodomePrimitiveFieldSet explicitly records which values are present.
// Zero is a valid value for wind and condensate, so missing input must never
// be represented by a numeric zero.
type AstrodomePrimitiveFieldSet uint64

// Has reports whether a native primitive is present.
func (set AstrodomePrimitiveFieldSet) Has(field AstrodomePrimitiveField) bool {
	return uint64(set)&uint64(field) != 0
}

// AstrodomePrimitiveVolumeIdentity binds every primitive to one immutable
// model run and manifest. Implementations must never mix brackets from
// different run IDs.
type AstrodomePrimitiveVolumeIdentity struct {
	Provider             string    `json:"provider"`
	Product              string    `json:"product"`
	Grid                 string    `json:"grid"`
	RunID                string    `json:"run_id"`
	RunBaseTime          time.Time `json:"run_base_time"`
	RunManifestDigest    string    `json:"run_manifest_digest"`
	InputContractVersion string    `json:"input_contract_version"`
}

// AstrodomeHorizontalSupport is one of exactly four surrounding regular-grid
// columns. Weight is geometric only. Corresponding raw native levels and HHL
// surfaces are mixed horizontally first; vertical reconstruction is then
// performed in the resulting local terrain-following column.
type AstrodomeHorizontalSupport struct {
	ColumnID string   `json:"column_id"`
	Location Location `json:"location"`
	Weight   float64  `json:"weight"`
}

// AstrodomeHorizontalStencil is a bilinear support set on the published
// regular-lat-lon grid. It contains geometry and weights, not interpolated
// meteorology.
type AstrodomeHorizontalStencil struct {
	Supports [4]AstrodomeHorizontalSupport `json:"supports"`
}

// Validate verifies the geometric weight contract without trying to infer or
// repair a missing support column.
func (stencil AstrodomeHorizontalStencil) Validate() error {
	weightSum := 0.0
	for index, support := range stencil.Supports {
		if support.ColumnID == "" {
			return fmt.Errorf("astrodome horizontal support %d has no column ID", index)
		}
		for previous := 0; previous < index; previous++ {
			if stencil.Supports[previous].ColumnID == support.ColumnID {
				return fmt.Errorf("astrodome horizontal support repeats column %q", support.ColumnID)
			}
		}
		if err := ValidateCoordinates(support.Location.Latitude, support.Location.Longitude); err != nil {
			return fmt.Errorf("astrodome horizontal support %d: %w", index, err)
		}
		if !finite(support.Weight) || support.Weight < 0 || support.Weight > 1 {
			return fmt.Errorf("astrodome horizontal support %d has invalid weight", index)
		}
		weightSum += support.Weight
	}
	if math.Abs(weightSum-1) > 1e-12 {
		return fmt.Errorf("astrodome horizontal support weights sum to %.17g, want 1", weightSum)
	}
	return nil
}

// AstrodomeHalfLevelGeometry carries time-invariant ICON HHL in its native
// mean-sea-level datum. It must not be replaced by WGS-84 ellipsoid height or
// by a pressure-to-height lookup table.
type AstrodomeHalfLevelGeometry struct {
	ModelHalfLevel int     `json:"model_half_level"`
	HeightM        float64 `json:"height_m"`
}

// AstrodomeFullLevelPrimitives preserves the native full-level staggering of
// P/T/QV/QC/QI/CLC/U/V. Eastward and northward wind remain in the support
// column's local ENU basis until the forecast kernel rotates vectors to ECEF.
type AstrodomeFullLevelPrimitives struct {
	ModelLevel           int                        `json:"model_level"`
	Available            AstrodomePrimitiveFieldSet `json:"available"`
	PressurePa           float64                    `json:"pressure_pa"`
	TemperatureK         float64                    `json:"temperature_k"`
	SpecificHumidityKgKg float64                    `json:"specific_humidity_kg_kg"`
	CloudLiquidKgKg      float64                    `json:"cloud_liquid_kg_kg"`
	CloudIceKgKg         float64                    `json:"cloud_ice_kg_kg"`
	CloudFraction        float64                    `json:"cloud_fraction"`
	EastwardWindMS       float64                    `json:"eastward_wind_ms"`
	NorthwardWindMS      float64                    `json:"northward_wind_ms"`
}

// AstrodomeHalfLevelPrimitives preserves W and TKE on their native ICON half
// levels. A later reconstruction must resolve staggering before forming the
// three-dimensional wind vector or turbulence equations.
type AstrodomeHalfLevelPrimitives struct {
	ModelHalfLevel int                        `json:"model_half_level"`
	Available      AstrodomePrimitiveFieldSet `json:"available"`
	VerticalWindMS float64                    `json:"vertical_wind_ms"`
	TKEJkg         float64                    `json:"tke_j_kg"`
}

// AstrodomeSurfacePrimitives contains site-operational native inputs. The
// precipitation interval is explicit because an accumulation must first be
// differenced according to its GRIB interval semantics; it is not an
// instantaneous scalar suitable for temporal interpolation.
type AstrodomeSurfacePrimitives struct {
	Available                   AstrodomePrimitiveFieldSet `json:"available"`
	SurfacePressurePa           float64                    `json:"surface_pressure_pa"`
	SpecificHumidity2MKgKg      float64                    `json:"specific_humidity_2m_kg_kg"`
	MixedLayerDepthM            float64                    `json:"mixed_layer_depth_m"`
	VisibilityM                 float64                    `json:"visibility_m"`
	RelativeHumidityFraction    float64                    `json:"relative_humidity_fraction"`
	Temperature2MK              float64                    `json:"temperature_2m_k"`
	DewPoint2MK                 float64                    `json:"dew_point_2m_k"`
	EastwardWind10MMS           float64                    `json:"eastward_wind_10m_ms"`
	NorthwardWind10MMS          float64                    `json:"northward_wind_10m_ms"`
	WindGust10MMS               float64                    `json:"wind_gust_10m_ms"`
	PrecipitationAccumulationMM float64                    `json:"precipitation_accumulation_mm"`
	PrecipitationIntervalStart  time.Time                  `json:"precipitation_interval_start"`
	PrecipitationIntervalEnd    time.Time                  `json:"precipitation_interval_end"`
	TotalColumnWaterVapourKgM2  float64                    `json:"total_column_water_vapour_kg_m2"`
	TotalColumnCloudLiquidKgM2  float64                    `json:"total_column_cloud_liquid_kg_m2"`
	TotalColumnCloudIceKgM2     float64                    `json:"total_column_cloud_ice_kg_m2"`
}

// AstrodomePrimitiveColumnFrame is one native model timestamp. Available
// masks allow different published cadences without inventing a value. A
// consumer finds field-specific temporal brackets and interpolates only the
// primitive named by that field before recomputing the complete physics.
type AstrodomePrimitiveColumnFrame struct {
	ValidAt    time.Time                      `json:"valid_at"`
	FullLevels []AstrodomeFullLevelPrimitives `json:"full_levels"`
	HalfLevels []AstrodomeHalfLevelPrimitives `json:"half_levels"`
	Surface    AstrodomeSurfacePrimitives     `json:"surface"`
}

// AstrodomePrimitiveColumn holds an immutable 72-hour raw support column plus
// any native bracketing timestamps required by lower-cadence fields. HSURF is
// retained separately from the complete HHL sequence; callers must not merge
// their vertical-datum roles implicitly.
type AstrodomePrimitiveColumn struct {
	ColumnID          string                          `json:"column_id"`
	Location          Location                        `json:"location"`
	HSURFHeightM      float64                         `json:"hsurf_height_m"`
	HalfLevelGeometry []AstrodomeHalfLevelGeometry    `json:"half_level_geometry"`
	Frames            []AstrodomePrimitiveColumnFrame `json:"frames"`
}

// AstrodomePrimitiveVolume is the consumer-owned boundary between ICON data
// acquisition and provider-neutral science. It exposes native primitives and
// grid geometry only. Implementations must not spatially or temporally
// interpolate derived seeing, tau0, cloud transmission, Overall, or data
// quality; those quantities are recomputed by the forecast kernel after
// primitive reconstruction at every quadrature point.
type AstrodomePrimitiveVolume interface {
	Identity() AstrodomePrimitiveVolumeIdentity
	NativeValidTimes(field AstrodomePrimitiveField) []time.Time
	HorizontalStencil(ctx context.Context, location Location) (AstrodomeHorizontalStencil, error)
	Column(ctx context.Context, columnID string) (AstrodomePrimitiveColumn, error)
}

// AstrodomeImmutableColumnViewVolume is an opt-in worker-only contract. A
// provider may return shared column slices only when their publication is
// immutable for the full job lifetime and every returned column has already
// passed the complete primitive-contract validation. The reconstructor then
// avoids both a second multi-gigabyte copy and repeated O(nodes*frames*levels)
// validation. General/public providers must not implement this marker.
type AstrodomeImmutableColumnViewVolume interface {
	AstrodomePrimitiveVolume
	TrustAstrodomeImmutableColumnViews() bool
}
