package render

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"bot_astrosferum/internal/astronomy"
	"bot_astrosferum/internal/forecast"
)

const ForecastInteractiveSchema = "forecast-interactive-v5-explicit-heuristics"

// ForecastInteractiveDataset contains the exact prepared values behind the
// seven ordinary forecast charts. It is presentation data, not a second
// forecast model: the browser must never recompute scientific indices.
type ForecastInteractiveDataset struct {
	SchemaVersion string                            `json:"schema_version"`
	ArtifactKey   string                            `json:"artifact_key,omitempty"`
	Location      forecast.Location                 `json:"location"`
	Provider      string                            `json:"provider"`
	Product       string                            `json:"product"`
	RunID         string                            `json:"run_id"`
	Grid          string                            `json:"grid"`
	BaseTime      time.Time                         `json:"base_time"`
	GeneratedAt   time.Time                         `json:"generated_at"`
	Algorithms    ForecastInteractiveAlgorithms     `json:"algorithms"`
	Inputs        ForecastInteractiveInputs         `json:"inputs"`
	UpperAir      ForecastInteractiveUpperAir       `json:"upper_air"`
	Weather       ForecastInteractiveWeather        `json:"weather"`
	Cloud         ForecastInteractiveCloud          `json:"cloud"`
	Overall       []forecast.OverallIndexFrame      `json:"overall"`
	PenaltyPoints []ForecastInteractivePenaltyFrame `json:"overall_penalty_points"`
	SolarPhases   []ForecastInteractiveSolarPeriod  `json:"solar_phases"`
}

type ForecastInteractivePenaltyFrame struct {
	ValidAt       time.Time                          `json:"valid_at"`
	Contributions []ForecastInteractivePenaltyPoints `json:"contributions"`
}

type ForecastInteractivePenaltyPoints struct {
	Key    string  `json:"key"`
	Points float64 `json:"points"`
}

type ForecastInteractiveAlgorithms struct {
	Seeing                   string `json:"seeing"`
	Overall                  string `json:"overall"`
	CloudObstruction         string `json:"cloud_obstruction"`
	OverallCalibrationSHA256 string `json:"overall_calibration_sha256"`
	Render                   string `json:"render"`
	ReferenceV               string `json:"reference_v"`
	ReferenceVAtm            string `json:"reference_v_atmosphere"`
	CelestialEphemeris       string `json:"celestial_ephemeris"`
}

type ForecastInteractiveInputs struct {
	VerticalProduct string `json:"vertical_product"`
	SurfaceProduct  string `json:"surface_product"`
	CloudProduct    string `json:"cloud_product"`
}

type ForecastInteractiveUpperAir struct {
	TimesUTC                 []time.Time  `json:"times_utc"`
	PressureHPA              []float64    `json:"pressure_hpa"`
	HeightKM                 []float64    `json:"height_km"`
	WindSpeedMS              [][]*float64 `json:"wind_speed_ms"`
	VectorShearMSPerKM       [][]*float64 `json:"vector_shear_ms_per_km"`
	DirectionDeltaDegrees    [][]*float64 `json:"direction_delta_deg"`
	SeeingIndex              []*float64   `json:"seeing_index"`
	OpticalSeeingArcsec      []*float64   `json:"optical_seeing_arcsec"`
	LeadTimeQualityHeuristic []float64    `json:"lead_time_quality_heuristic"`
}

type ForecastInteractiveWeather struct {
	Hours           []ForecastInteractiveWeatherHour `json:"hours"`
	Astronomy       astronomy.Series                 `json:"astronomy"`
	CelestialTracks []astronomy.CelestialTrack       `json:"celestial_tracks"`
}

type ForecastInteractiveWeatherHour struct {
	forecast.SurfaceFrame
	DewPointSpreadC              float64  `json:"dew_point_spread_c"`
	TransparencyHeuristicPercent *float64 `json:"transparency_heuristic_percent"`
	FogHeuristic                 int      `json:"fog_heuristic"`
	SunAltitudeDegrees           float64  `json:"sun_altitude_degrees"`
}

type ForecastInteractiveCloud struct {
	TimesUTC           []time.Time  `json:"times_utc"`
	PressureHPA        []float64    `json:"pressure_hpa"`
	HeightKM           []float64    `json:"height_km"`
	SurfaceElevationM  float64      `json:"surface_elevation_m"`
	ObstructionPercent [][]*float64 `json:"obstruction_percent"`
	CoverPercent       [][]*float64 `json:"cover_percent"`
	LiquidMGKG         [][]*float64 `json:"liquid_mg_kg"`
	IceMGKG            [][]*float64 `json:"ice_mg_kg"`
}

type ForecastInteractiveSolarPeriod struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Phase string    `json:"phase"`
}

func PrepareForecastInteractiveDataset(
	vertical forecast.VerticalSeries,
	surface forecast.SurfaceSeries,
	cloud forecast.CloudSeries,
	sky astronomy.Series,
	celestialTracks []astronomy.CelestialTrack,
	overall []forecast.OverallIndexFrame,
	calibration forecast.OverallIndexCalibration,
) (ForecastInteractiveDataset, forecast.Diagnostics, forecast.CloudDiagnostics, [][]float64, error) {
	if vertical.Provider == "" || vertical.Product == "" || surface.Product == "" || cloud.Product == "" ||
		vertical.Provider != surface.Provider || vertical.Provider != cloud.Provider ||
		vertical.RunID != surface.RunID || vertical.RunID != cloud.RunID ||
		!vertical.BaseTime.Equal(surface.BaseTime) || !vertical.BaseTime.Equal(cloud.BaseTime) ||
		!vertical.Location.Equal(surface.Location) || !vertical.Location.Equal(cloud.Location) {
		return ForecastInteractiveDataset{}, forecast.Diagnostics{}, forecast.CloudDiagnostics{}, nil,
			fmt.Errorf("interactive forecast inputs have inconsistent provenance")
	}
	diagnostics, err := forecast.ComputeDiagnostics(vertical)
	if err != nil {
		return ForecastInteractiveDataset{}, forecast.Diagnostics{}, forecast.CloudDiagnostics{}, nil, err
	}
	cloudDiagnostics, err := forecast.ComputeCloudDiagnostics(cloud)
	if err != nil {
		return ForecastInteractiveDataset{}, forecast.Diagnostics{}, forecast.CloudDiagnostics{}, nil, err
	}
	obstruction, err := forecast.ComputeCloudObstruction(cloudDiagnostics, calibration)
	if err != nil {
		return ForecastInteractiveDataset{}, forecast.Diagnostics{}, forecast.CloudDiagnostics{}, nil, err
	}
	if len(surface.Frames) < 2 || len(overall) < 2 ||
		len(diagnostics.Times) < 2 || len(cloudDiagnostics.Times) < 2 {
		return ForecastInteractiveDataset{}, forecast.Diagnostics{}, forecast.CloudDiagnostics{}, nil,
			fmt.Errorf("interactive forecast inputs have inconsistent axes")
	}
	for index := 1; index < len(diagnostics.Times); index++ {
		if diagnostics.Times[index].Sub(diagnostics.Times[index-1]) != 3*time.Hour {
			return ForecastInteractiveDataset{}, forecast.Diagnostics{}, forecast.CloudDiagnostics{}, nil,
				fmt.Errorf("interactive upper-air time axis is not three-hourly at frame %d", index)
		}
	}
	surfaceTimes := make(map[int64]struct{}, len(surface.Frames))
	celestialTimes := make([]time.Time, len(surface.Frames))
	for index, frame := range surface.Frames {
		if index > 0 && frame.ValidAt.Sub(surface.Frames[index-1].ValidAt) != time.Hour {
			return ForecastInteractiveDataset{}, forecast.Diagnostics{}, forecast.CloudDiagnostics{}, nil,
				fmt.Errorf("interactive surface time axis is not hourly at frame %d", index)
		}
		surfaceTimes[frame.ValidAt.UnixNano()] = struct{}{}
		celestialTimes[index] = frame.ValidAt.UTC()
	}
	if err := astronomy.ValidateCelestialTracks(celestialTracks, celestialTimes); err != nil {
		return ForecastInteractiveDataset{}, forecast.Diagnostics{}, forecast.CloudDiagnostics{}, nil,
			fmt.Errorf("interactive celestial tracks: %w", err)
	}
	cloudTimes := make(map[int64]struct{}, len(cloudDiagnostics.Times))
	for index, validAt := range cloudDiagnostics.Times {
		if index > 0 && validAt.Sub(cloudDiagnostics.Times[index-1]) != time.Hour {
			return ForecastInteractiveDataset{}, forecast.Diagnostics{}, forecast.CloudDiagnostics{}, nil,
				fmt.Errorf("interactive cloud time axis is not hourly at frame %d", index)
		}
		cloudTimes[validAt.UnixNano()] = struct{}{}
	}
	if len(cloudDiagnostics.Times) != len(surface.Frames) {
		return ForecastInteractiveDataset{}, forecast.Diagnostics{}, forecast.CloudDiagnostics{}, nil,
			fmt.Errorf("interactive surface and cloud axes have different lengths")
	}
	for index, validAt := range cloudDiagnostics.Times {
		if !validAt.Equal(surface.Frames[index].ValidAt) {
			return ForecastInteractiveDataset{}, forecast.Diagnostics{}, forecast.CloudDiagnostics{}, nil,
				fmt.Errorf("interactive surface and cloud time axes differ at frame %d", index)
		}
	}
	upperAirStart := diagnostics.Times[0]
	upperAirEnd := diagnostics.Times[len(diagnostics.Times)-1]
	for index, frame := range overall {
		_, inSurface := surfaceTimes[frame.ValidAt.UnixNano()]
		_, inCloud := cloudTimes[frame.ValidAt.UnixNano()]
		if (index > 0 && frame.ValidAt.Sub(overall[index-1].ValidAt) != time.Hour) || !inSurface || !inCloud ||
			frame.ValidAt.Before(upperAirStart) || frame.ValidAt.After(upperAirEnd) {
			return ForecastInteractiveDataset{}, forecast.Diagnostics{}, forecast.CloudDiagnostics{}, nil,
				fmt.Errorf("interactive Overall time %s is outside the hourly source axes", frame.ValidAt.Format(time.RFC3339))
		}
	}
	penaltyPoints := make([]ForecastInteractivePenaltyFrame, len(overall))
	for frameIndex, frame := range overall {
		contributions := make([]ForecastInteractivePenaltyPoints, len(frame.PenaltyContributions))
		for contributionIndex, contribution := range frame.PenaltyContributions {
			points, pointsErr := forecast.OverallPenaltyPoints(contribution.LossFraction)
			if pointsErr != nil {
				return ForecastInteractiveDataset{}, forecast.Diagnostics{}, forecast.CloudDiagnostics{}, nil,
					fmt.Errorf("interactive Overall penalty %q: %w", contribution.Key, pointsErr)
			}
			contributions[contributionIndex] = ForecastInteractivePenaltyPoints{Key: contribution.Key, Points: points}
		}
		penaltyPoints[frameIndex] = ForecastInteractivePenaltyFrame{ValidAt: frame.ValidAt, Contributions: contributions}
	}
	hours := make([]ForecastInteractiveWeatherHour, len(surface.Frames))
	for index, frame := range surface.Frames {
		var transparency *float64
		if value, ok := frame.TransparencyHeuristicPercent(); ok {
			transparency = finitePointer(value)
		}
		hours[index] = ForecastInteractiveWeatherHour{
			SurfaceFrame: frame, DewPointSpreadC: frame.DewPointSpreadC(), TransparencyHeuristicPercent: transparency,
			FogHeuristic: frame.FogHeuristic(), SunAltitudeDegrees: sky.SunAltitudeDegrees(frame.ValidAt),
		}
	}
	periods := []ForecastInteractiveSolarPeriod{}
	if len(surface.Frames) > 0 {
		periods = interactiveSolarPeriods(
			sky,
			surface.Frames[0].ValidAt.Add(-30*time.Minute),
			surface.Frames[len(surface.Frames)-1].ValidAt.Add(30*time.Minute),
		)
	}
	calibrationDigest, err := forecast.OverallCalibrationSHA256(calibration)
	if err != nil {
		return ForecastInteractiveDataset{}, forecast.Diagnostics{}, forecast.CloudDiagnostics{}, nil,
			fmt.Errorf("fingerprint interactive Overall calibration: %w", err)
	}
	dataset := ForecastInteractiveDataset{
		SchemaVersion: ForecastInteractiveSchema,
		Location:      vertical.Location, Provider: vertical.Provider, Product: vertical.Product,
		RunID: vertical.RunID, Grid: vertical.Grid, BaseTime: vertical.BaseTime, GeneratedAt: vertical.GeneratedAt,
		Algorithms: ForecastInteractiveAlgorithms{
			Seeing: vertical.AlgorithmVersion, Overall: forecast.OverallIndexAlgorithmVersion,
			CloudObstruction:         forecast.CloudObstructionAlgorithmVersion,
			OverallCalibrationSHA256: calibrationDigest, Render: Version,
			ReferenceV: forecast.ReferenceVBandContractVersion, ReferenceVAtm: forecast.ReferenceVBandAtmosphereVersion,
			CelestialEphemeris: astronomy.CelestialEphemerisVersion,
		},
		Inputs: ForecastInteractiveInputs{VerticalProduct: vertical.Product, SurfaceProduct: surface.Product, CloudProduct: cloud.Product},
		UpperAir: ForecastInteractiveUpperAir{
			TimesUTC: diagnostics.Times, PressureHPA: diagnostics.PressureHPA, HeightKM: diagnostics.HeightKM,
			WindSpeedMS: nullableMatrix(diagnostics.WindSpeedMS), VectorShearMSPerKM: nullableMatrix(diagnostics.VectorShearMSPerKM),
			DirectionDeltaDegrees: nullableMatrix(diagnostics.DirectionDelta), SeeingIndex: nullableVector(diagnostics.SeeingIndex),
			OpticalSeeingArcsec: nullableVector(diagnostics.OpticalSeeingArcsec), LeadTimeQualityHeuristic: diagnostics.LeadTimeQualityHeuristic,
		},
		Weather: ForecastInteractiveWeather{Hours: hours, Astronomy: sky, CelestialTracks: celestialTracks},
		Cloud: ForecastInteractiveCloud{
			TimesUTC: cloudDiagnostics.Times, PressureHPA: cloudDiagnostics.PressureHPA, HeightKM: cloudDiagnostics.HeightKM,
			SurfaceElevationM: cloudDiagnostics.SurfaceElevationM, ObstructionPercent: nullableMatrix(obstruction),
			CoverPercent: nullableMatrix(cloudDiagnostics.CoverPercent), LiquidMGKG: nullableMatrix(cloudDiagnostics.LiquidMGKG),
			IceMGKG: nullableMatrix(cloudDiagnostics.IceMGKG),
		},
		Overall: overall, PenaltyPoints: penaltyPoints, SolarPhases: periods,
	}
	if _, err := json.Marshal(dataset); err != nil {
		return ForecastInteractiveDataset{}, forecast.Diagnostics{}, forecast.CloudDiagnostics{}, nil, fmt.Errorf("marshal interactive forecast contract: %w", err)
	}
	return dataset, diagnostics, cloudDiagnostics, obstruction, nil
}

func interactiveSolarPeriods(sky astronomy.Series, start, end time.Time) []ForecastInteractiveSolarPeriod {
	intervals := solarPhaseIntervals(sky, start, end)
	periods := make([]ForecastInteractiveSolarPeriod, 0, len(intervals))
	for _, interval := range intervals {
		periods = append(periods, ForecastInteractiveSolarPeriod{
			Start: interval.Start,
			End:   interval.End,
			Phase: interactiveSolarPhase(interval.Phase),
		})
	}
	return periods
}

func SaveForecastInteractiveDataset(destination string, dataset ForecastInteractiveDataset) error {
	if filepath.Base(destination) != "forecast.json" {
		return fmt.Errorf("interactive forecast destination must be forecast.json")
	}
	encoded, err := json.Marshal(dataset)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".forecast-*.json")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o640); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, destination)
}

func nullableVector(values []float64) []*float64 {
	result := make([]*float64, len(values))
	for index, value := range values {
		result[index] = finitePointer(value)
	}
	return result
}

func nullableMatrix(values [][]float64) [][]*float64 {
	result := make([][]*float64, len(values))
	for row := range values {
		result[row] = nullableVector(values[row])
	}
	return result
}

func finitePointer(value float64) *float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	copy := value
	return &copy
}

func interactiveSolarPhase(phase solarPhase) string {
	switch phase {
	case solarNight:
		return "astronomical_night"
	case solarAstronomicalTwilight:
		return "astronomical_twilight"
	case solarBrightTwilight:
		return "bright_twilight"
	default:
		return "day"
	}
}
