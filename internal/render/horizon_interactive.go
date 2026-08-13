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

const HorizonInteractiveSchema = "horizon-interactive-v4-straight-ray"

type HorizonInteractiveDirection struct {
	Key            forecast.HorizonDirection `json:"key"`
	AzimuthDegrees float64                   `json:"azimuth_degrees"`
}

type HorizonInteractiveFrame struct {
	ValidAt    time.Time                `json:"valid_at"`
	SolarPhase string                   `json:"solar_phase"`
	Results    []forecast.HorizonResult `json:"results"`
}

// HorizonInteractiveDataset is a direct serialization of already computed
// Horizon cells. The browser never recalculates indices, quality heuristics
// or limiting factors.
type HorizonInteractiveDataset struct {
	SchemaVersion             string                           `json:"schema_version"`
	ArtifactKey               string                           `json:"artifact_key"`
	ScienceVersion            string                           `json:"science_version"`
	OverallCalibrationSHA256  string                           `json:"overall_calibration_sha256"`
	Location                  forecast.Location                `json:"location"`
	Provider                  string                           `json:"provider"`
	RunID                     string                           `json:"run_id"`
	Grid                      string                           `json:"grid"`
	ObserverSurfaceElevationM float64                          `json:"observer_surface_elevation_m"`
	ElevationDeg              float64                          `json:"elevation_deg"`
	Directions                []HorizonInteractiveDirection    `json:"directions"`
	Frames                    []HorizonInteractiveFrame        `json:"frames"`
	SolarPhases               []ForecastInteractiveSolarPeriod `json:"solar_phases"`
}

func PrepareHorizonInteractiveDataset(
	input HorizonInput,
	artifactKey string,
	observerSurfaceElevationM float64,
	calibration forecast.OverallIndexCalibration,
) (HorizonInteractiveDataset, error) {
	frames, _, err := validateHorizonInput(input)
	if err != nil {
		return HorizonInteractiveDataset{}, err
	}
	if math.IsNaN(observerSurfaceElevationM) || math.IsInf(observerSurfaceElevationM, 0) ||
		observerSurfaceElevationM < -1000 || observerSurfaceElevationM > 10000 {
		return HorizonInteractiveDataset{}, fmt.Errorf("interactive Horizon observer surface elevation is invalid")
	}
	if len(artifactKey) != sha256HexLength || !isLowerHex(artifactKey) {
		return HorizonInteractiveDataset{}, fmt.Errorf("interactive Horizon artifact key is invalid")
	}
	calibrationDigest, err := forecast.OverallCalibrationSHA256(calibration)
	if err != nil {
		return HorizonInteractiveDataset{}, fmt.Errorf("fingerprint interactive Horizon Overall calibration: %w", err)
	}
	directions := make([]HorizonInteractiveDirection, len(frames[0].Results))
	for index, result := range frames[0].Results {
		directions[index] = HorizonInteractiveDirection{Key: result.Direction, AzimuthDegrees: result.AzimuthDegrees}
	}
	sky := astronomy.Series{Location: input.Location}
	periods := interactiveSolarPeriods(
		sky,
		frames[0].ValidAt.Add(-30*time.Minute),
		frames[len(frames)-1].ValidAt.Add(30*time.Minute),
	)
	resultFrames := make([]HorizonInteractiveFrame, len(frames))
	for index, frame := range frames {
		resultFrames[index] = HorizonInteractiveFrame{
			ValidAt:    frame.ValidAt,
			SolarPhase: interactiveSolarPhase(phaseForSunAltitude(sky.SunAltitudeDegrees(frame.ValidAt))),
			Results:    append([]forecast.HorizonResult(nil), frame.Results...),
		}
	}
	dataset := HorizonInteractiveDataset{
		SchemaVersion: HorizonInteractiveSchema, ArtifactKey: artifactKey, ScienceVersion: forecast.HorizonAlgorithmVersion,
		OverallCalibrationSHA256: calibrationDigest,
		Location:                 input.Location, Provider: input.Provider, RunID: input.RunID, Grid: input.Grid,
		ObserverSurfaceElevationM: observerSurfaceElevationM,
		ElevationDeg:              forecast.HorizonGeometricElevationDegrees, Directions: directions, Frames: resultFrames,
		SolarPhases: periods,
	}
	if _, err := json.Marshal(dataset); err != nil {
		return HorizonInteractiveDataset{}, fmt.Errorf("marshal interactive Horizon contract: %w", err)
	}
	return dataset, nil
}

const sha256HexLength = 64

func isLowerHex(value string) bool {
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func SaveHorizonInteractiveDataset(destination string, dataset HorizonInteractiveDataset) error {
	if filepath.Base(destination) != "horizon.json" {
		return fmt.Errorf("interactive Horizon destination must be horizon.json")
	}
	encoded, err := json.Marshal(dataset)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(destination, encoded, 0o640)
}
