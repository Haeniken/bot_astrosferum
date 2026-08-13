package forecast

import (
	"math"
	"time"
)

type SurfaceFrame struct {
	ValidAt                        time.Time `json:"valid_at"`
	TemperatureC                   float64   `json:"temperature_c"`
	DewPointC                      float64   `json:"dew_point_c"`
	RelativeHumidityPercent        float64   `json:"relative_humidity_percent"`
	CloudCoverPercent              float64   `json:"cloud_cover_percent"`
	LowCloudCoverPercent           float64   `json:"low_cloud_cover_percent"`
	MidCloudCoverPercent           float64   `json:"mid_cloud_cover_percent"`
	HighCloudCoverPercent          float64   `json:"high_cloud_cover_percent"`
	PrecipitationMM                float64   `json:"precipitation_mm"`
	WindSpeedMS                    float64   `json:"wind_speed_ms"`
	WindGustMS                     float64   `json:"wind_gust_ms"`
	WindDirectionDegrees           float64   `json:"wind_direction_degrees"`
	PressureHPA                    float64   `json:"pressure_hpa"`
	VisibilityKM                   float64   `json:"visibility_km"`
	PrecipitableWaterMM            float64   `json:"precipitable_water_mm"`
	CloudLiquidPathKgM2            float64   `json:"cloud_liquid_path_kg_m2"`
	CloudIcePathKgM2               float64   `json:"cloud_ice_path_kg_m2"`
	MixedLayerDepthM               float64   `json:"mixed_layer_depth_m"`
	CloudCondensateAvailable       bool      `json:"cloud_condensate_available"`
	FogHeuristicAvailable          bool      `json:"fog_heuristic_available"`
	TransparencyHeuristicAvailable bool      `json:"transparency_heuristic_available"`
}

func (frame SurfaceFrame) DewPointSpreadC() float64 {
	return frame.TemperatureC - frame.DewPointC
}

// FogHeuristic uses ICON-EU's direct horizontal-visibility forecast and requires
// near-saturation so rain, snow, smoke, or dry haze are not mislabeled as fog.
// It is an uncalibrated warning heuristic, not a categorical observation.
func (frame SurfaceFrame) FogHeuristic() int {
	// A zero value with unavailable visibility represents a provider that
	// does not publish direct visibility (currently ICON Global), not zero
	// meteorological visibility.
	if !frame.FogHeuristicAvailable {
		return 0
	}
	spread := frame.DewPointSpreadC()
	if frame.VisibilityKM < 1 && frame.RelativeHumidityPercent >= 95 && spread <= 1.5 {
		return 2
	}
	if frame.VisibilityKM < 5 && frame.RelativeHumidityPercent >= 90 && spread <= 2.5 {
		return 1
	}
	return 0
}

// TransparencyHeuristicPercent is a conservative sorting aid rather than optical
// transmission or extinction. Cloud cover dominates the score; horizontal
// meteorological visibility and precipitable water only refine it. Direct AOD
// or stellar-extinction observations are not available in ICON-EU.
func (frame SurfaceFrame) TransparencyHeuristicPercent() (float64, bool) {
	if !frame.TransparencyHeuristicAvailable {
		return 0, false
	}
	cloudCover := math.Max(frame.CloudCoverPercent,
		math.Max(frame.LowCloudCoverPercent, math.Max(frame.MidCloudCoverPercent, frame.HighCloudCoverPercent)))
	clearFraction := 1 - clampSurfaceValue(cloudCover, 0, 100)/100
	visibilityFactor := clampSurfaceValue((frame.VisibilityKM-5)/45, 0, 1)
	pwvFactor := 1 - clampSurfaceValue((frame.PrecipitableWaterMM-5)/35, 0, 1)
	proxy := 100 * clearFraction * (0.65 + 0.25*visibilityFactor + 0.10*pwvFactor)
	return clampSurfaceValue(proxy, 0, 100), true
}

func clampSurfaceValue(value, minimum, maximum float64) float64 {
	return math.Max(minimum, math.Min(maximum, value))
}

type SurfaceSeries struct {
	Location    Location       `json:"location"`
	Provider    string         `json:"provider"`
	Product     string         `json:"product"`
	RunID       string         `json:"run_id"`
	BaseTime    time.Time      `json:"base_time"`
	GeneratedAt time.Time      `json:"generated_at"`
	StepHours   int            `json:"step_hours"`
	Frames      []SurfaceFrame `json:"frames"`
}

// Window returns the current local-hour frame and up to the requested number
// of following hours without fabricating data beyond the published model run.
func (series SurfaceSeries) Window(now time.Time, hours int) SurfaceSeries {
	if len(series.Frames) == 0 || hours < 1 {
		return series
	}
	threshold := now.UTC().Truncate(time.Hour)
	start := 0
	for start < len(series.Frames) && series.Frames[start].ValidAt.Before(threshold) {
		start++
	}
	if start >= len(series.Frames) {
		start = len(series.Frames) - 1
	}
	limit := series.Frames[start].ValidAt.Add(time.Duration(hours) * time.Hour)
	end := start
	for end < len(series.Frames) && !series.Frames[end].ValidAt.After(limit) {
		end++
	}
	series.Frames = series.Frames[start:end]
	return series
}
