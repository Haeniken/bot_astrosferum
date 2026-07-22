package forecast

import (
	"math"
	"testing"
)

func TestOverallIndexIsHourlyAndCloudsActAsVeto(t *testing.T) {
	vertical := SyntheticVerticalFixture()
	surface := clearSyntheticSurface()
	calibration := DefaultOverallIndexCalibration()
	cloud := SyntheticCloudFixture()
	clear, err := ComputeHourlyOverallIndex(vertical, surface, cloud, calibration)
	if err != nil {
		t.Fatal(err)
	}
	if len(clear) != 73 {
		t.Fatalf("hourly frame count = %d, want 73", len(clear))
	}
	if !clear[0].PhysicalSeeing || !finite(clear[0].SeeingArcsec) {
		t.Fatalf("overall index did not use physical seeing: %+v", clear[0])
	}
	for index := 1; index < len(clear); index++ {
		if clear[index].ValidAt.Sub(clear[index-1].ValidAt).Hours() != 1 {
			t.Fatalf("frame %d is not hourly", index)
		}
	}

	cloudySurface := surface
	for index := range cloudySurface.Frames {
		cloudySurface.Frames[index].CloudCoverPercent = 100
		cloudySurface.Frames[index].CloudLiquidPathKgM2 = 0.2
		cloudySurface.Frames[index].CloudIcePathKgM2 = 0.1
	}
	cloudy, err := ComputeHourlyOverallIndex(vertical, cloudySurface, cloud, calibration)
	if err != nil {
		t.Fatal(err)
	}
	for index, frame := range cloudy {
		if math.Abs(frame.Index-1) > 1e-6 {
			t.Fatalf("cloudy index[%d] = %v, want approximately 1", index, frame.Index)
		}
	}
}

func TestOverallIndexStopsAtDeclaredNativeTKEHorizon(t *testing.T) {
	cloud := SyntheticCloudFixture()
	cloud.TurbulenceValidUntil = cloud.Frames[2].ValidAt
	for index := 3; index < len(cloud.Frames); index++ {
		for level := range cloud.Frames[index].Levels {
			cloud.Frames[index].Levels[level].TKEJkg = math.NaN()
		}
	}
	frames, err := ComputeHourlyOverallIndex(SyntheticVerticalFixture(), clearSyntheticSurface(), cloud, DefaultOverallIndexCalibration())
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 || !frames[len(frames)-1].ValidAt.Equal(cloud.TurbulenceValidUntil) {
		t.Fatalf("Overall horizon = %d frames through %s", len(frames), frames[len(frames)-1].ValidAt)
	}
}

func TestOverallIndexIgnoresDewButPenalizesHighFog(t *testing.T) {
	vertical := SyntheticVerticalFixture()
	surface := clearSyntheticSurface()
	cloud := SyntheticCloudFixture()
	baseline, err := ComputeHourlyOverallIndex(vertical, surface, cloud, DefaultOverallIndexCalibration())
	if err != nil {
		t.Fatal(err)
	}

	dewOnly := surface
	dewOnly.Frames[0].DewPointC = dewOnly.Frames[0].TemperatureC - 0.1
	dewOnly.Frames[0].RelativeHumidityPercent = 99
	dewOnly.Frames[0].VisibilityKM = 30
	withDew, err := ComputeHourlyOverallIndex(vertical, dewOnly, cloud, DefaultOverallIndexCalibration())
	if err != nil {
		t.Fatal(err)
	}
	if withDew[0].Index != baseline[0].Index || withDew[0].HighFog {
		t.Fatalf("dew-only frame changed index: baseline=%v dew=%v fog=%v", baseline[0].Index, withDew[0].Index, withDew[0].HighFog)
	}

	fog := surface
	fog.Frames[0].DewPointC = fog.Frames[0].TemperatureC - 0.5
	fog.Frames[0].RelativeHumidityPercent = 98
	fog.Frames[0].VisibilityKM = 0.5
	withFog, err := ComputeHourlyOverallIndex(vertical, fog, cloud, DefaultOverallIndexCalibration())
	if err != nil {
		t.Fatal(err)
	}
	if !withFog[0].HighFog || !(withFog[0].Index < baseline[0].Index) {
		t.Fatalf("high fog did not reduce index: baseline=%v fog=%v high=%v", baseline[0].Index, withFog[0].Index, withFog[0].HighFog)
	}
}

func TestOverallIndexInterpolatesWindLinearly(t *testing.T) {
	vertical := SyntheticVerticalFixture()
	surface := clearSyntheticSurface()
	frames, err := ComputeHourlyOverallIndex(vertical, surface, SyntheticCloudFixture(), DefaultOverallIndexCalibration())
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(frames[1].WindIndex-(frames[0].WindIndex+(frames[3].WindIndex-frames[0].WindIndex)/3)) > 1e-9 {
		t.Fatalf("wind index was not linearly interpolated: %v %v %v", frames[0].WindIndex, frames[1].WindIndex, frames[3].WindIndex)
	}
}

func TestOverallIndexUsesNativeGroundLayerWhenAvailable(t *testing.T) {
	vertical := SyntheticVerticalFixture()
	surface := clearSyntheticSurface()
	frames, err := ComputeHourlyOverallIndex(vertical, surface, SyntheticCloudFixture(), DefaultOverallIndexCalibration())
	if err != nil {
		t.Fatal(err)
	}
	if !frames[0].GroundLayerPhysics || frames[0].GroundLayerFraction <= 0 || !frames[0].PhysicalCoherence {
		t.Fatalf("native ground layer was not used: %+v", frames[0])
	}
	free := HMNSP99SeeingArcsec(vertical.Frames[0].Levels)
	if !(frames[0].SeeingArcsec > free) {
		t.Fatalf("ground layer did not increase total seeing: hybrid=%v full-HMNSP=%v", frames[0].SeeingArcsec, free)
	}
}

func TestOverallIndexUsesHourlyICONMixedLayerDepthWithinBounds(t *testing.T) {
	vertical := SyntheticVerticalFixture()
	surface := clearSyntheticSurface()
	surface.Frames[0].MixedLayerDepthM = 100
	surface.Frames[1].MixedLayerDepthM = 1200
	surface.Frames[2].MixedLayerDepthM = 3000
	frames, err := ComputeHourlyOverallIndex(vertical, surface, SyntheticCloudFixture(), DefaultOverallIndexCalibration())
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{500, 1200, 2000}
	for index, expected := range want {
		if frames[index].BoundaryLayerDepthM != expected {
			t.Fatalf("frame %d boundary-layer depth = %v, want %v", index, frames[index].BoundaryLayerDepthM, expected)
		}
	}
}

func TestOverallCalibrationRejectsInvalidBoundaryLayerBounds(t *testing.T) {
	for _, bounds := range [][2]float64{{99, 2000}, {2100, 2000}, {500, 4001}} {
		calibration := DefaultOverallIndexCalibration()
		calibration.BoundaryLayerMinM = bounds[0]
		calibration.BoundaryLayerTopM = bounds[1]
		if err := calibration.Validate(); err == nil {
			t.Fatalf("bounds [%v, %v] unexpectedly passed validation", bounds[0], bounds[1])
		}
	}
}

func TestOverallIndexRejectsInvalidICONMixedLayerDepth(t *testing.T) {
	surface := clearSyntheticSurface()
	surface.Frames[0].MixedLayerDepthM = math.NaN()
	if _, err := ComputeHourlyOverallIndex(SyntheticVerticalFixture(), surface, SyntheticCloudFixture(), DefaultOverallIndexCalibration()); err == nil {
		t.Fatal("NaN ICON mixed-layer depth unexpectedly passed validation")
	}
}

func TestOverallIndexRejectsMissingGroundLayerInsteadOfOptimisticFallback(t *testing.T) {
	_, err := ComputeHourlyOverallIndex(
		SyntheticVerticalFixture(), clearSyntheticSurface(), CloudSeries{}, DefaultOverallIndexCalibration(),
	)
	if err == nil {
		t.Fatal("missing native TKE profile silently fell back to free-atmosphere HMNSP")
	}
}

func TestCloudTransmissionDistinguishesThinOvercastFromDenseOvercast(t *testing.T) {
	calibration := DefaultOverallIndexCalibration()
	thin := SurfaceFrame{CloudCoverPercent: 100, CloudIcePathKgM2: 0.0001, CloudCondensateAvailable: true}
	dense := thin
	dense.CloudIcePathKgM2 = 0.2
	thinTransmission, thinTau, thinPhysical, thinGuard := cloudTransmission(thin, calibration)
	denseTransmission, denseTau, densePhysical, _ := cloudTransmission(dense, calibration)
	if !thinPhysical || !densePhysical || !thinGuard || !(thinTau < denseTau) || math.Abs(thinTransmission-0.55) > 0.01 || !(denseTransmission < 0.01) {
		t.Fatalf("unexpected cloud optics: thin T=%v tau=%v, dense T=%v tau=%v", thinTransmission, thinTau, denseTransmission, denseTau)
	}
}

func TestCloudTransmissionDoesNotTreatThinHighCloudAsOpaqueLowCloud(t *testing.T) {
	calibration := DefaultOverallIndexCalibration()
	low := SurfaceFrame{
		CloudCoverPercent: 100, LowCloudCoverPercent: 100,
		CloudIcePathKgM2: 0.0001, CloudCondensateAvailable: true,
	}
	high := low
	high.LowCloudCoverPercent = 0
	high.HighCloudCoverPercent = 100
	lowTransmission, _, _, lowGuard := cloudTransmission(low, calibration)
	highTransmission, _, _, highGuard := cloudTransmission(high, calibration)
	if !lowGuard || !highGuard || math.Abs(lowTransmission-0.55) > 0.01 || math.Abs(highTransmission-0.919) > 0.01 || highTransmission <= lowTransmission {
		t.Fatalf("tier-aware cloud transmission: low=%v high=%v", lowTransmission, highTransmission)
	}
}

func TestOverallSeeingScaleReservesTenForExceptionalSeeing(t *testing.T) {
	calibration := DefaultOverallIndexCalibration()
	checks := []struct {
		seeing float64
		want   float64
	}{
		{0.5, 1},
		{0.7, math.Log(2.0/0.7) / math.Log(2.0/0.5)},
		{2.0, 0},
	}
	for _, check := range checks {
		got := logarithmicLowerIsBetter(check.seeing, calibration.GoodSeeingArcsec, calibration.BadSeeingArcsec)
		if math.Abs(got-check.want) > 1e-12 {
			t.Fatalf("seeing quality(%v) = %v, want %v", check.seeing, got, check.want)
		}
	}
	if got := 1 + 9*logarithmicLowerIsBetter(0.7, calibration.GoodSeeingArcsec, calibration.BadSeeingArcsec); got >= 8 {
		t.Fatalf("0.7 arcsec still rounds to an over-optimistic top score: %v", got)
	}
}

func TestSurfaceWindPenaltyIsDelayedAndMild(t *testing.T) {
	calibration := DefaultOverallIndexCalibration()
	if got := surfaceWindFactor(SurfaceFrame{WindSpeedMS: 5, WindGustMS: 8}, calibration); got != 1 {
		t.Fatalf("shelterable moderate wind was penalized: %v", got)
	}
	if got := surfaceWindFactor(SurfaceFrame{WindSpeedMS: 30, WindGustMS: 40}, calibration); math.Abs(got-0.8) > 1e-12 {
		t.Fatalf("maximum surface-wind factor = %v, want 0.8", got)
	}
}

func clearSyntheticSurface() SurfaceSeries {
	series := SyntheticSurfaceFixture()
	for index := range series.Frames {
		frame := &series.Frames[index]
		frame.CloudCoverPercent = 0
		frame.LowCloudCoverPercent = 0
		frame.MidCloudCoverPercent = 0
		frame.HighCloudCoverPercent = 0
		frame.CloudLiquidPathKgM2 = 0
		frame.CloudIcePathKgM2 = 0
		frame.VisibilityKM = 50
		frame.RelativeHumidityPercent = 50
		frame.DewPointC = frame.TemperatureC - 10
	}
	return series
}
