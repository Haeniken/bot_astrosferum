package forecast

import (
	"math"
	"time"
)

// SyntheticVerticalFixture produces deterministic, model-like values for
// renderer tests. It contains no downloaded or derived weather-model data.
func SyntheticVerticalFixture() VerticalSeries {
	pressures := []float64{1000, 975, 950, 925, 900, 850, 800, 750, 700, 650, 600, 550, 500, 450, 400, 350, 300, 250, 200, 150, 100, 70, 50}
	base := time.Date(2026, time.July, 14, 6, 0, 0, 0, time.UTC)
	frames := make([]VerticalFrame, 25)
	for frameIndex := range frames {
		levels := make([]VerticalLevel, len(pressures))
		for levelIndex, pressure := range pressures {
			height := 44330 * (1 - math.Pow(pressure/1013.25, 0.190284))
			heightFactor := float64(levelIndex) / float64(len(pressures)-1)
			phase := float64(frameIndex)*0.42 + heightFactor*4.2
			speed := 2.5 + 42*heightFactor + 8*math.Sin(phase) + 4*math.Cos(float64(frameIndex)*0.23-heightFactor*2)
			speed = math.Max(0.4, speed)
			direction := math.Mod(205+95*heightFactor+45*math.Sin(phase*0.73), 360) * math.Pi / 180
			levels[levelIndex] = VerticalLevel{
				PressureHPA:  pressure,
				HeightM:      height,
				TemperatureK: 288.15 - 0.0065*math.Min(height, 11000),
				UMS:          -speed * math.Sin(direction),
				VMS:          -speed * math.Cos(direction),
			}
		}
		frames[frameIndex] = VerticalFrame{
			ValidAt:                  base.Add(time.Duration(frameIndex) * 3 * time.Hour),
			Levels:                   levels,
			LeadTimeQualityHeuristic: clamp(0.96-float64(frameIndex)*0.012, 0.62, 0.96),
		}
	}
	location, _ := NewLocation(59.9386, 30.3141, "Europe/Moscow")
	return VerticalSeries{
		Location:         location,
		Provider:         "fixture",
		Product:          "ICON-EU-like synthetic",
		RunID:            "20260714T0600Z",
		Grid:             "0.0625°",
		AlgorithmVersion: SeeingPrototypeVersion,
		BaseTime:         base,
		GeneratedAt:      time.Date(2026, time.July, 14, 6, 5, 0, 0, time.UTC),
		Frames:           frames,
	}
}

func SyntheticCloudFixture() CloudSeries {
	location, _ := NewLocation(59.9386, 30.3141, "Europe/Moscow")
	base := time.Date(2026, time.July, 14, 6, 0, 0, 0, time.UTC)
	modelLevels := []int{74, 73, 72, 71, 70, 69, 68, 67, 66, 65, 64, 63, 62, 61, 60, 59, 58, 56, 54, 52, 50, 48, 45, 40, 35, 30, 25}
	heightsAGL := []float64{10, 41, 93, 162, 246, 346, 464, 600, 756, 934, 1136, 1364, 1620, 1905, 2222, 2573, 2961, 3850, 4900, 6000, 7100, 8300, 10000, 12500, 15000, 18000, 21000}
	const surfaceElevationM = 25
	frames := make([]CloudFrame, 73)
	for column := range frames {
		levels := make([]CloudLevel, len(modelLevels))
		for row, modelLevel := range modelLevels {
			height := surfaceElevationM + heightsAGL[row]
			pressure := 1013.25 * math.Pow(1-height/44330, 1/0.190284)
			heightFactor := heightsAGL[row] / heightsAGL[len(heightsAGL)-1]
			lowOffset := (heightsAGL[row] - 800) / 900
			highOffset := (heightsAGL[row] - 9000) / 2200
			lowDeck := 75 * math.Exp(-lowOffset*lowOffset) * (0.55 + 0.45*math.Sin(float64(column)/8))
			highDeck := 55 * math.Exp(-highOffset*highOffset) * (0.55 + 0.45*math.Cos(float64(column)/6))
			cover := math.Max(0, math.Min(100, lowDeck+highDeck))
			liquid := lowDeck / 100 * 1.8e-4
			ice := highDeck / 100 * 7e-5
			temperature := 288.15 - 0.0055*math.Min(height, 11000)
			layerThicknessM := 25 + 0.055*heightsAGL[row]
			u, v, tke := math.NaN(), math.NaN(), math.NaN()
			if modelLevel >= 58 {
				u = 2 + 24*heightFactor + 2*math.Sin(float64(column)/9+heightFactor)
				v = 1 + 12*heightFactor + math.Cos(float64(column)/11-heightFactor)
				// Keep the deterministic sample within a useful seeing range while
				// still exercising hour-to-hour boundary-layer variability.
				tkeCycle := 0.85 + 0.45*math.Sin(float64(column)/7+0.4)
				tke = 0.003 + 0.04*tkeCycle*math.Exp(-heightsAGL[row]/1800)
			}
			levels[row] = CloudLevel{
				ModelLevel: modelLevel, PressureHPA: pressure, HeightM: height, LayerThicknessM: layerThicknessM,
				TemperatureK: temperature, UMS: u, VMS: v, TKEJkg: tke,
				CoverPercent: cover, CloudLiquidKgKg: liquid, CloudIceKgKg: ice,
			}
		}
		frames[column] = CloudFrame{ValidAt: base.Add(time.Duration(column) * time.Hour), Levels: levels}
	}
	return CloudSeries{
		Location: location, Provider: "fixture", Product: "ICON-EU-like model cloud",
		RunID: "20260714T0600Z", BaseTime: base, GeneratedAt: base.Add(5 * time.Minute),
		SurfaceElevationM: surfaceElevationM, Frames: frames,
	}
}
