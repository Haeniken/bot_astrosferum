package iconeu

import (
	"testing"
	"time"
)

func TestVerticalFromBatchNormalizesCDOPressurePa(t *testing.T) {
	values := make(map[batchValueKey]float64)
	for _, pressure := range DefaultPressureLevelsHPA {
		level := float64(pressure * 100)
		values[batchValueKey{name: "u", level: level}] = 1
		values[batchValueKey{name: "v", level: level}] = 2
		values[batchValueKey{name: "z", level: level}] = float64(1000-pressure) * 9.80665
		values[batchValueKey{name: "t", level: level}] = 270
	}
	step := StepFile{ValidAt: time.Unix(1, 0).UTC(), Messages: len(values)}
	frame, err := verticalFromBatch(values, step, "pressure.grib2")
	if err != nil {
		t.Fatal(err)
	}
	if frame.Levels[0].PressureHPA != 1000 || frame.Levels[len(frame.Levels)-1].PressureHPA != 50 {
		t.Fatalf("pressure levels = %.0f ... %.0f", frame.Levels[0].PressureHPA, frame.Levels[len(frame.Levels)-1].PressureHPA)
	}
}

func TestBatchNormalizersRejectUnexpectedFields(t *testing.T) {
	if _, err := surfaceFromBatch(map[batchValueKey]float64{{name: "unknown"}: 1}, time.Now(), "surface"); err == nil {
		t.Fatal("unknown surface field was accepted")
	}
	if _, err := cloudHeightsFromBatch(map[batchValueKey]float64{{name: "z", level: 75}: 1}, "geometry"); err == nil {
		t.Fatal("unknown geometry field was accepted")
	}
	if _, err := cloudFromBatch(map[batchValueKey]float64{{name: "unknown", level: 75}: 1}, nil, time.Now(), "cloud"); err == nil {
		t.Fatal("unknown cloud field was accepted")
	}
}
