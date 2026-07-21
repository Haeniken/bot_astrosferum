package forecast

import (
	"math"
	"testing"
)

func TestComputeDiagnostics(t *testing.T) {
	series := SyntheticVerticalFixture()
	diagnostics, err := ComputeDiagnostics(series)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(diagnostics.Times), 25; got != want {
		t.Fatalf("time count = %d, want %d", got, want)
	}
	if got, want := len(diagnostics.PressureHPA), 23; got != want {
		t.Fatalf("level count = %d, want %d", got, want)
	}
	if !math.IsNaN(diagnostics.VectorShearMSPerKM[len(diagnostics.PressureHPA)-1][0]) {
		t.Fatal("top-level vector shear must be missing")
	}
	for index, value := range diagnostics.SeeingIndex {
		if math.IsNaN(value) || value < 1 || value > 10 {
			t.Fatalf("seeing[%d] = %v, want 1..10", index, value)
		}
	}
	if !math.IsNaN(diagnostics.WindSpeedMS[17][11]) {
		t.Fatal("fixture gap was not preserved")
	}
}

func TestDirectionDeltaIsZeroForCalmAdjacentLevel(t *testing.T) {
	series := SyntheticVerticalFixture()
	series.Frames[0].Levels[0].UMS = 0.5
	series.Frames[0].Levels[0].VMS = 0
	diagnostics, err := ComputeDiagnostics(series)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.DirectionDelta[0][0] != 0 {
		t.Fatalf("direction delta at calm level = %v, want 0", diagnostics.DirectionDelta[0][0])
	}
}

func TestAngularDifferenceWrapsAtNorth(t *testing.T) {
	if got, want := angularDifference(355, 5), 10.0; got != want {
		t.Fatalf("angular difference = %v, want %v", got, want)
	}
}

func TestVerticalSeriesRejectsInconsistentPressure(t *testing.T) {
	series := SyntheticVerticalFixture()
	series.Frames[2].Levels[3].PressureHPA++
	if err := series.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}
