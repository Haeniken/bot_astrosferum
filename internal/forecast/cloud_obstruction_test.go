package forecast

import (
	"math"
	"testing"
	"time"
)

func TestCloudObstructionUsesTierAwareDiagnosticCLCGuard(t *testing.T) {
	diagnostics := CloudDiagnostics{
		Times:        []time.Time{time.Unix(1, 0)},
		PressureHPA:  []float64{900, 600, 250},
		HeightKM:     []float64{1, 4, 10},
		CoverPercent: [][]float64{{100}, {100}, {100}},
		AirMassKgM2:  [][]float64{{100}, {100}, {100}},
		LiquidMGKG:   [][]float64{{0}, {0}, {0}},
		IceMGKG:      [][]float64{{0}, {0}, {0}},
	}
	values, err := ComputeCloudObstruction(diagnostics, DefaultOverallIndexCalibration())
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(values[0][0]-45) > 1e-9 || math.Abs(values[1][0]-24.75) > 1e-9 || math.Abs(values[2][0]-8.1) > 1e-9 {
		t.Fatalf("tier-aware diagnostic CLC guard = %v, want [45, 24.75, 8.1]", values)
	}
}

func TestCloudObstructionTierHeightIsAboveLocalSurface(t *testing.T) {
	diagnostics := CloudDiagnostics{
		Times:             []time.Time{time.Unix(1, 0)},
		PressureHPA:       []float64{800},
		HeightKM:          []float64{2.8},
		SurfaceElevationM: 1000,
		CoverPercent:      [][]float64{{100}},
		AirMassKgM2:       [][]float64{{100}},
		LiquidMGKG:        [][]float64{{0}},
		IceMGKG:           [][]float64{{0}},
	}
	values, err := ComputeCloudObstruction(diagnostics, DefaultOverallIndexCalibration())
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(values[0][0]-45) > 1e-9 {
		t.Fatalf("cloud at 1.8 km AGL guard = %v, want low-tier 45%%", values)
	}
}

func TestCloudObstructionPreservesStrongerCondensatePhysics(t *testing.T) {
	diagnostics := CloudDiagnostics{
		Times:        []time.Time{time.Unix(1, 0)},
		PressureHPA:  []float64{900, 800},
		HeightKM:     []float64{1, 3},
		CoverPercent: [][]float64{{100}, {100}},
		AirMassKgM2:  [][]float64{{100}, {100}},
		LiquidMGKG:   [][]float64{{1000}, {1000}},
		IceMGKG:      [][]float64{{0}, {0}},
	}
	values, err := ComputeCloudObstruction(diagnostics, DefaultOverallIndexCalibration())
	if err != nil {
		t.Fatal(err)
	}
	if values[0][0] < 99 || values[1][0] < 99 {
		t.Fatalf("dense condensate was weakened to the diagnostic floor: %v", values)
	}
}

func TestCloudDiagnosticsUsesNativeHHLThicknessForAirMass(t *testing.T) {
	base := time.Unix(1, 0)
	frames := make([]CloudFrame, 2)
	for column := range frames {
		frames[column] = CloudFrame{ValidAt: base.Add(time.Duration(column) * time.Hour), Levels: []CloudLevel{
			{ModelLevel: 74, PressureHPA: 900, HeightM: 1000, LayerThicknessM: 120, TemperatureK: 270, CoverPercent: 50},
			{ModelLevel: 25, PressureHPA: 200, HeightM: 12000, LayerThicknessM: 400, TemperatureK: 220, CoverPercent: 20},
		}}
	}
	diagnostics, err := ComputeCloudDiagnostics(CloudSeries{Frames: frames})
	if err != nil {
		t.Fatal(err)
	}
	wantLow := 90000.0 / (287.05 * 270) * 120
	wantHigh := 20000.0 / (287.05 * 220) * 400
	if math.Abs(diagnostics.AirMassKgM2[0][0]-wantLow) > 1e-9 || math.Abs(diagnostics.AirMassKgM2[1][0]-wantHigh) > 1e-9 {
		t.Fatalf("native-layer air mass = %v, want [%v %v]", diagnostics.AirMassKgM2, wantLow, wantHigh)
	}
}

func TestCloudObstructionRejectsInvalidNativeLayerAirMass(t *testing.T) {
	diagnostics := CloudDiagnostics{
		Times:        []time.Time{time.Unix(1, 0)},
		PressureHPA:  []float64{900},
		HeightKM:     []float64{1},
		CoverPercent: [][]float64{{100}},
		AirMassKgM2:  [][]float64{{0}},
		LiquidMGKG:   [][]float64{{0}},
		IceMGKG:      [][]float64{{0}},
	}
	if _, err := ComputeCloudObstruction(diagnostics, DefaultOverallIndexCalibration()); err == nil {
		t.Fatal("zero native-layer air mass was accepted")
	}
}

func TestCloudWindowIncludesCurrentTruncatedHour(t *testing.T) {
	base := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	series := CloudSeries{Frames: make([]CloudFrame, 75)}
	for index := range series.Frames {
		series.Frames[index].ValidAt = base.Add(time.Duration(index) * time.Hour)
	}
	window := series.Window(base.Add(37*time.Minute), 72)
	if len(window.Frames) != 73 || !window.Frames[0].ValidAt.Equal(base) || !window.Frames[72].ValidAt.Equal(base.Add(72*time.Hour)) {
		t.Fatalf("cloud window = %d frames, %v..%v", len(window.Frames), window.Frames[0].ValidAt, window.Frames[len(window.Frames)-1].ValidAt)
	}
}
