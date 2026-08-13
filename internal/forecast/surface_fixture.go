package forecast

import (
	"math"
	"time"
)

func SyntheticSurfaceFixture() SurfaceSeries {
	vertical := SyntheticVerticalFixture()
	frames := make([]SurfaceFrame, 73)
	for index := range frames {
		hour := float64(index)
		diurnal := math.Sin((hour-5)*2*math.Pi/24) / 2
		cloud := clamp(52+48*math.Sin(hour*0.19)+18*math.Cos(hour*0.43), 0, 100)
		precipitation := 0.0
		if cloud > 82 && index%9 >= 5 {
			precipitation = 0.2 + float64(index%4)*0.35
		}
		temperature := 15 + 8*diurnal
		dewPoint := 11 + 3*diurnal + 2*math.Sin(hour*0.11)
		frames[index] = SurfaceFrame{
			ValidAt:      vertical.BaseTime.Add(time.Duration(index) * time.Hour),
			TemperatureC: temperature, DewPointC: dewPoint,
			RelativeHumidityPercent:  clamp(68-25*diurnal+cloud/8, 35, 100),
			CloudCoverPercent:        cloud,
			LowCloudCoverPercent:     clamp(cloud+18*math.Sin(hour*0.31), 0, 100),
			MidCloudCoverPercent:     clamp(cloud*0.65+22*math.Cos(hour*0.23), 0, 100),
			HighCloudCoverPercent:    clamp(cloud*0.45+30*math.Sin(hour*0.13), 0, 100),
			PrecipitationMM:          precipitation,
			WindSpeedMS:              2.5 + 2*math.Abs(math.Sin(hour*0.15)),
			WindGustMS:               5 + 6*math.Abs(math.Sin(hour*0.12)),
			WindDirectionDegrees:     math.Mod(340+hour*7, 360),
			PressureHPA:              1012 + 5*math.Sin(hour*0.07),
			VisibilityKM:             35 + 15*math.Sin(hour*0.09),
			PrecipitableWaterMM:      18 + 7*math.Cos(hour*0.08),
			CloudLiquidPathKgM2:      cloud / 100 * 0.08,
			CloudIcePathKgM2:         cloud / 100 * 0.025,
			MixedLayerDepthM:         350 + 1150*math.Max(0, diurnal),
			CloudCondensateAvailable: true,
			FogHeuristicAvailable:    true, TransparencyHeuristicAvailable: true,
		}
	}
	return SurfaceSeries{
		Location: vertical.Location, Provider: "fixture", Product: "ICON-EU-like synthetic single-level",
		RunID: vertical.RunID, BaseTime: vertical.BaseTime, GeneratedAt: vertical.GeneratedAt,
		StepHours: 1, Frames: frames,
	}
}
