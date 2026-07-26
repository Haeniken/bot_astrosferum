// Package geoscf retrieves point atmospheric-composition forecasts from the
// public NASA GEOS-CF OPeNDAP service. It deliberately exposes only measured
// model quantities; wavelength conversion and observing-score formulas belong
// to the forecast layer.
package geoscf

import (
	"errors"
	"time"

	"bot_astrosferum/internal/forecast"
)

const (
	// DefaultDatasetURL is the mutable public alias for the latest GEOS-CF v2
	// hourly single-level composition forecast.
	DefaultDatasetURL = "https://opendap.nccs.nasa.gov/dods/gmao/geos-cf/v2/fcst/xgc_tavg_1hr_glo_L1440x721_slv.latest"

	ProviderName = "NASA GEOS-CF v2"
	ProductName  = "xgc_tavg_1hr_glo_L1440x721_slv"
)

var (
	ErrInvalidRequest  = errors.New("invalid GEOS-CF request")
	ErrOutsideCoverage = errors.New("requested time is outside GEOS-CF forecast coverage")
	ErrStaleDataset    = errors.New("latest GEOS-CF forecast is stale")
	ErrBusy            = errors.New("GEOS-CF data request concurrency limit reached")
	ErrMalformedData   = errors.New("malformed GEOS-CF OPeNDAP response")
	ErrMissingData     = errors.New("GEOS-CF response contains missing data")
)

// AOD550Components retains the seven aerosol optical-depth components
// published by GEOS-CF at 550 nm. All values are dimensionless.
type AOD550Components struct {
	BlackCarbon                float64 `json:"black_carbon"`
	Dust                       float64 `json:"dust"`
	OrganicCarbon              float64 `json:"organic_carbon"`
	PolarStratosphericCloud    float64 `json:"polar_stratospheric_cloud"`
	StratosphericLiquidAerosol float64 `json:"stratospheric_liquid_aerosol"`
	SulfateNitrateAmmonium     float64 `json:"sulfate_nitrate_ammonium"`
	SeaSalt                    float64 `json:"sea_salt"`
}

func (components AOD550Components) Total() float64 {
	return components.BlackCarbon +
		components.Dust +
		components.OrganicCarbon +
		components.PolarStratosphericCloud +
		components.StratosphericLiquidAerosol +
		components.SulfateNitrateAmmonium +
		components.SeaSalt
}

// Frame is a provider-neutral point sample suitable for downstream physical
// calculations. TotalColumnOzoneDU is the full atmospheric column in Dobson
// units; no ozone absorption or aerosol spectral law is inferred here.
type Frame struct {
	ValidTimeUTC       time.Time        `json:"valid_time_utc"`
	AOD550             float64          `json:"aod_550"`
	AOD550Components   AOD550Components `json:"aod_550_components"`
	TotalColumnOzoneDU float64          `json:"total_column_ozone_du"`
}

type GridPoint struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// Freshness makes the independent GEOS-CF publication age explicit. It must
// not be confused with the run age of the ICON forecast it may later enrich.
type Freshness struct {
	ReferenceTimeUTC time.Time     `json:"reference_time_utc"`
	Age              time.Duration `json:"age"`
	MaximumAge       time.Duration `json:"maximum_age"`
	Stale            bool          `json:"stale"`
}

// Provenance records exactly which source variables and interpolation methods
// produced a series.
type Provenance struct {
	Institution           string   `json:"institution"`
	System                string   `json:"system"`
	Product               string   `json:"product"`
	DatasetURL            string   `json:"dataset_url"`
	Variables             []string `json:"variables"`
	SpatialInterpolation  string   `json:"spatial_interpolation"`
	TemporalInterpolation string   `json:"temporal_interpolation"`
	RunTimeDerivation     string   `json:"run_time_derivation"`
}

// SourceMetadata identifies the mutable latest dataset by a stable run ID and
// valid-time window. GEOS-CF publishes hourly means at their interval midpoint;
// RunTimeUTC is therefore derived as the first midpoint minus half an hour.
type SourceMetadata struct {
	RunID                  string        `json:"run_id"`
	RunTimeUTC             time.Time     `json:"run_time_utc"`
	ForecastWindowStartUTC time.Time     `json:"forecast_window_start_utc"`
	ForecastWindowEndUTC   time.Time     `json:"forecast_window_end_utc"`
	PublicationTimeUTC     time.Time     `json:"publication_time_utc,omitempty"`
	RetrievedAtUTC         time.Time     `json:"retrieved_at_utc"`
	GridResolutionDegrees  float64       `json:"grid_resolution_degrees"`
	TemporalResolution     time.Duration `json:"temporal_resolution"`
	SpatialSupport         []GridPoint   `json:"spatial_support"`
	Freshness              Freshness     `json:"freshness"`
	Provenance             Provenance    `json:"provenance"`
}

type Series struct {
	Location forecast.Location `json:"location"`
	Source   SourceMetadata    `json:"source"`
	Frames   []Frame           `json:"frames"`
}
