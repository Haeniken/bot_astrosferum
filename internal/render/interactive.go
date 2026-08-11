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

const ForecastInteractiveSchema = "forecast-interactive-v1"

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
}

type ForecastInteractiveInputs struct {
	VerticalProduct string `json:"vertical_product"`
	SurfaceProduct  string `json:"surface_product"`
	CloudProduct    string `json:"cloud_product"`
}

type ForecastInteractiveUpperAir struct {
	TimesUTC              []time.Time  `json:"times_utc"`
	PressureHPA           []float64    `json:"pressure_hpa"`
	HeightKM              []float64    `json:"height_km"`
	WindSpeedMS           [][]*float64 `json:"wind_speed_ms"`
	VectorShearMSPerKM    [][]*float64 `json:"vector_shear_ms_per_km"`
	DirectionDeltaDegrees [][]*float64 `json:"direction_delta_deg"`
	SeeingIndex           []*float64   `json:"seeing_index"`
	OpticalSeeingArcsec   []*float64   `json:"optical_seeing_arcsec"`
	Confidence            []float64    `json:"confidence"`
}

type ForecastInteractiveWeather struct {
	Hours     []ForecastInteractiveWeatherHour `json:"hours"`
	Astronomy astronomy.Series                 `json:"astronomy"`
}

type ForecastInteractiveWeatherHour struct {
	forecast.SurfaceFrame
	DewPointSpreadC     float64  `json:"dew_point_spread_c"`
	TransparencyPercent *float64 `json:"transparency_percent"`
	FogRisk             int      `json:"fog_risk"`
	SunAltitudeDegrees  float64  `json:"sun_altitude_degrees"`
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
	overall []forecast.OverallIndexFrame,
	calibration forecast.OverallIndexCalibration,
) (ForecastInteractiveDataset, forecast.Diagnostics, forecast.CloudDiagnostics, [][]float64, error) {
	if vertical.Provider == "" || vertical.Product == "" || surface.Product == "" || cloud.Product == "" ||
		vertical.Provider != surface.Provider || vertical.Provider != cloud.Provider ||
		vertical.RunID != surface.RunID || vertical.RunID != cloud.RunID ||
		!vertical.BaseTime.Equal(surface.BaseTime) || !vertical.BaseTime.Equal(cloud.BaseTime) ||
		vertical.Location != surface.Location || vertical.Location != cloud.Location {
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
	if len(surface.Frames) < 2 || len(overall) != len(surface.Frames) ||
		len(diagnostics.Times) < 2 || len(cloudDiagnostics.Times) != len(surface.Frames) {
		return ForecastInteractiveDataset{}, forecast.Diagnostics{}, forecast.CloudDiagnostics{}, nil,
			fmt.Errorf("interactive forecast inputs have inconsistent axes")
	}
	for index, frame := range surface.Frames {
		if !frame.ValidAt.Equal(overall[index].ValidAt) || !frame.ValidAt.Equal(cloudDiagnostics.Times[index]) {
			return ForecastInteractiveDataset{}, forecast.Diagnostics{}, forecast.CloudDiagnostics{}, nil,
				fmt.Errorf("interactive hourly forecast time axis differs at frame %d", index)
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
		if value, ok := frame.TransparencyProxyPercent(); ok {
			transparency = finitePointer(value)
		}
		hours[index] = ForecastInteractiveWeatherHour{
			SurfaceFrame: frame, DewPointSpreadC: frame.DewPointSpreadC(), TransparencyPercent: transparency,
			FogRisk: frame.FogRisk(), SunAltitudeDegrees: sky.SunAltitudeDegrees(frame.ValidAt),
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
		},
		Inputs: ForecastInteractiveInputs{VerticalProduct: vertical.Product, SurfaceProduct: surface.Product, CloudProduct: cloud.Product},
		UpperAir: ForecastInteractiveUpperAir{
			TimesUTC: diagnostics.Times, PressureHPA: diagnostics.PressureHPA, HeightKM: diagnostics.HeightKM,
			WindSpeedMS: nullableMatrix(diagnostics.WindSpeedMS), VectorShearMSPerKM: nullableMatrix(diagnostics.VectorShearMSPerKM),
			DirectionDeltaDegrees: nullableMatrix(diagnostics.DirectionDelta), SeeingIndex: nullableVector(diagnostics.SeeingIndex),
			OpticalSeeingArcsec: nullableVector(diagnostics.OpticalSeeingArcsec), Confidence: diagnostics.Confidence,
		},
		Weather: ForecastInteractiveWeather{Hours: hours, Astronomy: sky},
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
