package forecast

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

const SeeingPrototypeVersion = "seeing-hybrid-tke-mh-hmnsp99-v7"

// VerticalLevel contains one pressure-level wind vector in SI units.
type VerticalLevel struct {
	PressureHPA  float64 `json:"pressure_hpa"`
	HeightM      float64 `json:"height_m"`
	TemperatureK float64 `json:"temperature_k"`
	UMS          float64 `json:"u_ms"`
	VMS          float64 `json:"v_ms"`
}

type VerticalFrame struct {
	ValidAt                  time.Time       `json:"valid_at"`
	Levels                   []VerticalLevel `json:"levels"`
	LeadTimeQualityHeuristic float64         `json:"lead_time_quality_heuristic"`
}

// VerticalSeries is provider-neutral input for vertical diagnostics and charts.
// Levels in every frame are ordered from the surface toward the model top.
type VerticalSeries struct {
	Location         Location        `json:"location"`
	Provider         string          `json:"provider"`
	Product          string          `json:"product"`
	RunID            string          `json:"run_id"`
	Grid             string          `json:"grid"`
	AlgorithmVersion string          `json:"algorithm_version"`
	BaseTime         time.Time       `json:"base_time"`
	GeneratedAt      time.Time       `json:"generated_at"`
	Frames           []VerticalFrame `json:"frames"`
}

type Diagnostics struct {
	Times                    []time.Time `json:"times"`
	PressureHPA              []float64   `json:"pressure_hpa"`
	HeightKM                 []float64   `json:"height_km"`
	WindSpeedMS              [][]float64 `json:"wind_speed_ms"`
	VectorShearMSPerKM       [][]float64 `json:"vector_shear_ms_per_km"`
	DirectionDelta           [][]float64 `json:"direction_delta_deg"`
	SeeingIndex              []float64   `json:"seeing_index"`
	OpticalSeeingArcsec      []float64   `json:"optical_seeing_arcsec"`
	LeadTimeQualityHeuristic []float64   `json:"lead_time_quality_heuristic"`
}

func (series VerticalSeries) Validate() error {
	if err := ValidateCoordinates(series.Location.Latitude, series.Location.Longitude); err != nil {
		return fmt.Errorf("location: %w", err)
	}
	if series.Location.TimeZone == "" {
		return errors.New("location timezone is required")
	}
	if series.Provider == "" || series.Product == "" || series.RunID == "" || series.Grid == "" {
		return errors.New("provider, product, run ID, and grid are required")
	}
	if series.AlgorithmVersion == "" {
		return errors.New("algorithm version is required")
	}
	if series.BaseTime.IsZero() || series.GeneratedAt.IsZero() {
		return errors.New("base and generated timestamps are required")
	}
	if len(series.Frames) < 2 {
		return errors.New("at least two forecast frames are required")
	}
	if len(series.Frames[0].Levels) < 2 {
		return errors.New("at least two pressure levels are required")
	}

	pressures := make([]float64, len(series.Frames[0].Levels))
	for i, level := range series.Frames[0].Levels {
		if !finite(level.PressureHPA) || level.PressureHPA <= 0 {
			return fmt.Errorf("frame 0 level %d has invalid pressure", i)
		}
		if i > 0 && level.PressureHPA >= pressures[i-1] {
			return errors.New("pressure levels must be strictly descending from surface to model top")
		}
		pressures[i] = level.PressureHPA
	}

	for frameIndex, frame := range series.Frames {
		if frame.ValidAt.IsZero() {
			return fmt.Errorf("frame %d has no valid timestamp", frameIndex)
		}
		if frameIndex > 0 && !frame.ValidAt.After(series.Frames[frameIndex-1].ValidAt) {
			return errors.New("forecast timestamps must be strictly increasing")
		}
		if len(frame.Levels) != len(pressures) {
			return fmt.Errorf("frame %d has %d levels, expected %d", frameIndex, len(frame.Levels), len(pressures))
		}
		if !finite(frame.LeadTimeQualityHeuristic) || frame.LeadTimeQualityHeuristic < 0 || frame.LeadTimeQualityHeuristic > 1 {
			return fmt.Errorf("frame %d lead-time quality heuristic must be between 0 and 1", frameIndex)
		}
		for levelIndex, level := range frame.Levels {
			if level.PressureHPA != pressures[levelIndex] {
				return fmt.Errorf("frame %d level %d pressure differs from the first frame", frameIndex, levelIndex)
			}
			if !finite(level.HeightM) || level.HeightM < -500 {
				return fmt.Errorf("frame %d level %d has invalid height", frameIndex, levelIndex)
			}
			if levelIndex > 0 && level.HeightM <= frame.Levels[levelIndex-1].HeightM {
				return fmt.Errorf("frame %d heights must increase toward model top", frameIndex)
			}
			if level.TemperatureK != 0 && !(finite(level.TemperatureK) && level.TemperatureK >= 150 && level.TemperatureK <= 350) && !math.IsNaN(level.TemperatureK) {
				return fmt.Errorf("frame %d level %d has invalid temperature", frameIndex, levelIndex)
			}
			if !(finite(level.UMS) && finite(level.VMS)) && !(math.IsNaN(level.UMS) && math.IsNaN(level.VMS)) {
				return fmt.Errorf("frame %d level %d must contain two finite wind components or two NaNs", frameIndex, levelIndex)
			}
		}
	}
	return nil
}

func ComputeDiagnostics(series VerticalSeries) (Diagnostics, error) {
	if err := series.Validate(); err != nil {
		return Diagnostics{}, err
	}

	levelCount := len(series.Frames[0].Levels)
	frameCount := len(series.Frames)
	result := Diagnostics{
		Times:                    make([]time.Time, frameCount),
		PressureHPA:              make([]float64, levelCount),
		HeightKM:                 make([]float64, levelCount),
		WindSpeedMS:              matrix(levelCount, frameCount),
		VectorShearMSPerKM:       matrix(levelCount, frameCount),
		DirectionDelta:           matrix(levelCount, frameCount),
		SeeingIndex:              make([]float64, frameCount),
		OpticalSeeingArcsec:      make([]float64, frameCount),
		LeadTimeQualityHeuristic: make([]float64, frameCount),
	}
	for levelIndex, level := range series.Frames[0].Levels {
		result.PressureHPA[levelIndex] = level.PressureHPA
		for _, frame := range series.Frames {
			result.HeightKM[levelIndex] += frame.Levels[levelIndex].HeightM / 1000
		}
		result.HeightKM[levelIndex] /= float64(frameCount)
	}

	for frameIndex, frame := range series.Frames {
		result.Times[frameIndex] = frame.ValidAt
		result.LeadTimeQualityHeuristic[frameIndex] = frame.LeadTimeQualityHeuristic
		for levelIndex, level := range frame.Levels {
			if math.IsNaN(level.UMS) {
				result.WindSpeedMS[levelIndex][frameIndex] = math.NaN()
				continue
			}
			result.WindSpeedMS[levelIndex][frameIndex] = math.Hypot(level.UMS, level.VMS)
		}

		for levelIndex := range levelCount {
			if levelIndex == levelCount-1 {
				result.VectorShearMSPerKM[levelIndex][frameIndex] = math.NaN()
				result.DirectionDelta[levelIndex][frameIndex] = math.NaN()
				continue
			}
			lower := frame.Levels[levelIndex]
			upper := frame.Levels[levelIndex+1]
			if math.IsNaN(lower.UMS) || math.IsNaN(upper.UMS) {
				result.VectorShearMSPerKM[levelIndex][frameIndex] = math.NaN()
				result.DirectionDelta[levelIndex][frameIndex] = math.NaN()
				continue
			}
			lowerSpeed := result.WindSpeedMS[levelIndex][frameIndex]
			upperSpeed := result.WindSpeedMS[levelIndex+1][frameIndex]
			distanceKM := math.Abs(upper.HeightM-lower.HeightM) / 1000
			if distanceKM <= 0 {
				result.VectorShearMSPerKM[levelIndex][frameIndex] = math.NaN()
			} else {
				result.VectorShearMSPerKM[levelIndex][frameIndex] = math.Hypot(upper.UMS-lower.UMS, upper.VMS-lower.VMS) / distanceKM
			}
			if math.Min(lowerSpeed, upperSpeed) < 2 {
				// Near-calm direction is numerically unstable, but the flow has
				// negligible observational relevance. Treat it as zero directional
				// disturbance while retaining NaN for genuinely missing model data.
				result.DirectionDelta[levelIndex][frameIndex] = 0
			} else {
				result.DirectionDelta[levelIndex][frameIndex] = angularDifference(
					meteorologicalDirection(lower.UMS, lower.VMS),
					meteorologicalDirection(upper.UMS, upper.VMS),
				)
			}
		}

		result.SeeingIndex[frameIndex] = prototypeSeeing(frame.Levels)
		result.OpticalSeeingArcsec[frameIndex] = HMNSP99SeeingArcsec(frame.Levels)
	}
	return result, nil
}

func prototypeSeeing(levels []VerticalLevel) float64 {
	var jetSpeed float64
	var shears []float64
	for index, level := range levels {
		if math.IsNaN(level.UMS) {
			continue
		}
		speed := math.Hypot(level.UMS, level.VMS)
		if level.PressureHPA <= 300 && speed > jetSpeed {
			jetSpeed = speed
		}
		if index+1 < len(levels) && level.PressureHPA <= 700 {
			upper := levels[index+1]
			if !math.IsNaN(upper.UMS) {
				distanceKM := math.Abs(upper.HeightM-level.HeightM) / 1000
				if distanceKM > 0 {
					shears = append(shears, math.Hypot(upper.UMS-level.UMS, upper.VMS-level.VMS)/distanceKM)
				}
			}
		}
	}
	if len(shears) == 0 {
		return math.NaN()
	}
	sort.Float64s(shears)
	medianShear := shears[len(shears)/2]
	penalty := 0.6*clamp(jetSpeed/70, 0, 1) + 0.4*clamp(medianShear/12, 0, 1)
	return clamp(10-9*penalty, 1, 10)
}

func meteorologicalDirection(u, v float64) float64 {
	return math.Mod(math.Atan2(-u, -v)*180/math.Pi+360, 360)
}

func angularDifference(a, b float64) float64 {
	delta := math.Abs(a - b)
	return math.Min(delta, 360-delta)
}

func matrix(rows, columns int) [][]float64 {
	result := make([][]float64, rows)
	for row := range result {
		result[row] = make([]float64, columns)
	}
	return result
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func clamp(value, minimum, maximum float64) float64 {
	return math.Max(minimum, math.Min(maximum, value))
}
