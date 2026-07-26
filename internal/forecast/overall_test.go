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

func TestOverallIndexUsesBinaryPrecipitationOperationalVeto(t *testing.T) {
	calibration := DefaultOverallIndexCalibration()
	calibration.PrecipitationDetectMM = 0.1

	belowSurface := clearSyntheticSurface()
	belowSurface.Frames[0].PrecipitationMM = 0.099
	below, err := ComputeHourlyOverallIndex(SyntheticVerticalFixture(), belowSurface, SyntheticCloudFixture(), calibration)
	if err != nil {
		t.Fatal(err)
	}
	if below[0].PrecipitationVeto || below[0].Index <= 1 {
		t.Fatalf("sub-threshold precipitation was vetoed: %+v", below[0])
	}

	atSurface := clearSyntheticSurface()
	atSurface.Frames[0].PrecipitationMM = 0.1
	at, err := ComputeHourlyOverallIndex(SyntheticVerticalFixture(), atSurface, SyntheticCloudFixture(), calibration)
	if err != nil {
		t.Fatal(err)
	}
	if !at[0].PrecipitationVeto || at[0].PrecipitationMM != 0.1 || at[0].Index != 1 {
		t.Fatalf("detected precipitation did not produce an operational veto: %+v", at[0])
	}
	if math.Abs(sumPenaltyContributions(at[0].PenaltyContributions)-1) > 1e-12 || at[0].PenaltyLossFraction != 1 {
		t.Fatalf("veto penalty decomposition is not exact: %+v", at[0])
	}
}

func TestOverallIndexUsesDefaultPrecipitationThresholdForExplicitCalibration(t *testing.T) {
	calibration := DefaultOverallIndexCalibration()
	calibration.PrecipitationDetectMM = 0
	surface := clearSyntheticSurface()
	surface.Frames[0].PrecipitationMM = DefaultOverallPrecipitationDetectMM
	frames, err := ComputeHourlyOverallIndex(SyntheticVerticalFixture(), surface, SyntheticCloudFixture(), calibration)
	if err != nil {
		t.Fatal(err)
	}
	if !frames[0].PrecipitationVeto || frames[0].Index != 1 {
		t.Fatalf("zero calibration did not select the safe default threshold: %+v", frames[0])
	}
}

func TestOverallIndexMarksMissingOptionalVisibilityAsPartial(t *testing.T) {
	surface := clearSyntheticSurface()
	surface.Frames[0].TransparencyAvailable = false
	surface.Frames[0].VisibilityKM = 0
	frames, err := ComputeHourlyOverallIndex(
		SyntheticVerticalFixture(), surface, SyntheticCloudFixture(), DefaultOverallIndexCalibration(),
	)
	if err != nil {
		t.Fatalf("missing optional visibility broke the forecast: %v", err)
	}
	if frames[0].FogAssessmentAvailable || frames[0].DataCompleteness != OverallDataPartial {
		t.Fatalf("missing visibility was presented as complete: %+v", frames[0])
	}
	if !frames[1].FogAssessmentAvailable || frames[1].DataCompleteness != OverallDataComplete {
		t.Fatalf("complete comparison frame was not marked complete: %+v", frames[1])
	}
}

func TestOverallIndexMarksCloudFallbackAsPartial(t *testing.T) {
	surface := clearSyntheticSurface()
	surface.Frames[0].CloudCondensateAvailable = false
	frames, err := ComputeHourlyOverallIndex(
		SyntheticVerticalFixture(), surface, SyntheticCloudFixture(), DefaultOverallIndexCalibration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if frames[0].CloudCondensatePhysics || frames[0].DataCompleteness != OverallDataPartial {
		t.Fatalf("cloud-cover fallback was presented as complete: %+v", frames[0])
	}
}

func TestOverallIndexRejectsMaterialUpperProfileGaps(t *testing.T) {
	vertical := SyntheticVerticalFixture()
	for frameIndex := range vertical.Frames {
		// Remove several consecutive upper-atmosphere layers. Treating this
		// unsampled h^(5/3)-weighted region as zero would bias J, JV, and Jh low.
		for levelIndex := 17; levelIndex <= 20; levelIndex++ {
			vertical.Frames[frameIndex].Levels[levelIndex].TemperatureK = math.NaN()
		}
	}
	if _, err := ComputeHourlyOverallIndex(
		vertical, clearSyntheticSurface(), SyntheticCloudFixture(), DefaultOverallIndexCalibration(),
	); err == nil {
		t.Fatal("material upper-profile gaps unexpectedly produced an Overall score")
	}
}

func TestOverallIndexMarksStructurallyCompleteShallowProfileAsPartial(t *testing.T) {
	vertical := SyntheticVerticalFixture()
	for frameIndex := range vertical.Frames {
		vertical.Frames[frameIndex].Levels = vertical.Frames[frameIndex].Levels[:21]
	}
	frames, err := ComputeHourlyOverallIndex(
		vertical, clearSyntheticSurface(), SyntheticCloudFixture(), DefaultOverallIndexCalibration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if frames[0].TurbulenceProfileQuality != OpticalTurbulenceProfileLimited ||
		frames[0].DataCompleteness != OverallDataPartial {
		t.Fatalf("shallow turbulence profile was not marked partial: %+v", frames[0])
	}
}

func TestOverallIndexRejectsInvalidCriticalSurfaceInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SurfaceFrame)
	}{
		{"precipitation", func(frame *SurfaceFrame) { frame.PrecipitationMM = math.NaN() }},
		{"cloud cover", func(frame *SurfaceFrame) { frame.CloudCoverPercent = math.NaN() }},
		{"wind", func(frame *SurfaceFrame) { frame.WindSpeedMS = -1 }},
		{"available visibility", func(frame *SurfaceFrame) { frame.VisibilityKM = math.NaN() }},
		{"available condensate", func(frame *SurfaceFrame) { frame.CloudLiquidPathKgM2 = math.NaN() }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			surface := clearSyntheticSurface()
			test.mutate(&surface.Frames[0])
			if _, err := ComputeHourlyOverallIndex(
				SyntheticVerticalFixture(), surface, SyntheticCloudFixture(), DefaultOverallIndexCalibration(),
			); err == nil {
				t.Fatal("invalid critical surface input unexpectedly produced a score")
			}
		})
	}
}

func TestShapleyMultiplicativeLossExactProperties(t *testing.T) {
	tests := []struct {
		name    string
		factors []OverallPenaltyFactor
	}{
		{"identity", []OverallPenaltyFactor{{Key: "a", Factor: 1}, {Key: "b", Factor: 1}}},
		{"mixed", []OverallPenaltyFactor{{Key: "a", Factor: 0.8}, {Key: "b", Factor: 0.4}, {Key: "c", Factor: 0.9}}},
		{"zero", []OverallPenaltyFactor{{Key: "a", Factor: 1}, {Key: "veto", Factor: 0}, {Key: "c", Factor: 1}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contributions, totalLoss, err := ShapleyMultiplicativeLoss(test.factors)
			if err != nil {
				t.Fatal(err)
			}
			product := 1.0
			for _, factor := range test.factors {
				product *= factor.Factor
			}
			if math.Abs(totalLoss-(1-product)) > 1e-12 {
				t.Fatalf("total loss = %.16f, want %.16f", totalLoss, 1-product)
			}
			if math.Abs(sumPenaltyContributions(contributions)-totalLoss) > 1e-12 {
				t.Fatalf("contributions %+v do not sum to %.16f", contributions, totalLoss)
			}
			for _, contribution := range contributions {
				if contribution.LossFraction < 0 || contribution.LossFraction > totalLoss+1e-12 {
					t.Fatalf("invalid contribution %+v for total %.16f", contribution, totalLoss)
				}
			}
		})
	}
}

func TestShapleyMultiplicativeLossIsSymmetricAndOrderIndependent(t *testing.T) {
	const factor = 0.6
	equal := []OverallPenaltyFactor{{Key: "a", Factor: factor}, {Key: "b", Factor: factor}, {Key: "c", Factor: factor}}
	contributions, totalLoss, err := ShapleyMultiplicativeLoss(equal)
	if err != nil {
		t.Fatal(err)
	}
	wantEach := totalLoss / float64(len(equal))
	for _, contribution := range contributions {
		if math.Abs(contribution.LossFraction-wantEach) > 1e-12 {
			t.Fatalf("equal factors have unequal Shapley contribution: %+v, want %.16f", contributions, wantEach)
		}
	}

	forward := []OverallPenaltyFactor{{Key: "a", Factor: 0.8}, {Key: "b", Factor: 0.4}, {Key: "c", Factor: 0.9}}
	reverse := []OverallPenaltyFactor{{Key: "c", Factor: 0.9}, {Key: "b", Factor: 0.4}, {Key: "a", Factor: 0.8}}
	forwardContributions, _, err := ShapleyMultiplicativeLoss(forward)
	if err != nil {
		t.Fatal(err)
	}
	reverseContributions, _, err := ShapleyMultiplicativeLoss(reverse)
	if err != nil {
		t.Fatal(err)
	}
	for _, contribution := range forwardContributions {
		if math.Abs(contribution.LossFraction-penaltyContributionByKey(reverseContributions, contribution.Key)) > 1e-12 {
			t.Fatalf("factor order changed contribution for %q: forward=%+v reverse=%+v", contribution.Key, forwardContributions, reverseContributions)
		}
	}
}

func TestShapleyMultiplicativeLossAssignsIdentityZeroFactorTheFullVeto(t *testing.T) {
	contributions, totalLoss, err := ShapleyMultiplicativeLoss([]OverallPenaltyFactor{
		{Key: "identity-a", Factor: 1}, {Key: "veto", Factor: 0}, {Key: "identity-b", Factor: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if totalLoss != 1 || penaltyContributionByKey(contributions, "veto") != 1 ||
		penaltyContributionByKey(contributions, "identity-a") != 0 || penaltyContributionByKey(contributions, "identity-b") != 0 {
		t.Fatalf("unexpected zero-factor allocation: total=%v contributions=%+v", totalLoss, contributions)
	}
}

func sumPenaltyContributions(contributions []OverallPenaltyContribution) float64 {
	total := 0.0
	for _, contribution := range contributions {
		total += contribution.LossFraction
	}
	return total
}

func penaltyContributionByKey(contributions []OverallPenaltyContribution, key string) float64 {
	for _, contribution := range contributions {
		if contribution.Key == key {
			return contribution.LossFraction
		}
	}
	return math.NaN()
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

func TestOverallIndexDerivesModelSurfacePressureFromNativeLowestLevel(t *testing.T) {
	cloud := SyntheticCloudFixture()
	frames, err := ComputeHourlyOverallIndex(
		SyntheticVerticalFixture(), clearSyntheticSurface(), cloud, DefaultOverallIndexCalibration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	lowest := cloud.Frames[0].Levels[0]
	for _, level := range cloud.Frames[0].Levels[1:] {
		if level.HeightM < lowest.HeightM {
			lowest = level
		}
	}
	want := lowest.PressureHPA * math.Exp(9.80665*(lowest.HeightM-cloud.SurfaceElevationM)/(287.05*lowest.TemperatureK))
	if math.Abs(frames[0].SurfacePressureHPA-want) > 1e-12 ||
		frames[0].SurfacePressureProvenance != ModelSurfacePressureProvenance {
		t.Fatalf("surface pressure = %.12g (%q), want %.12g (%q)", frames[0].SurfacePressureHPA,
			frames[0].SurfacePressureProvenance, want, ModelSurfacePressureProvenance)
	}
}

func TestModelSurfacePressureRejectsDistantOrInvalidProfile(t *testing.T) {
	if _, ok := modelSurfacePressureHPA([]CloudLevel{{PressureHPA: 900, HeightM: 2500, TemperatureK: 280}}, 0); ok {
		t.Fatal("distant model level unexpectedly produced surface pressure")
	}
	if _, ok := modelSurfacePressureHPA([]CloudLevel{{PressureHPA: math.NaN(), HeightM: 10, TemperatureK: 280}}, 0); ok {
		t.Fatal("invalid model pressure unexpectedly produced surface pressure")
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

func TestOverallCalibrationRejectsInvalidPrecipitationThreshold(t *testing.T) {
	for _, threshold := range []float64{-0.01, 10.01, math.NaN()} {
		calibration := DefaultOverallIndexCalibration()
		calibration.PrecipitationDetectMM = threshold
		if err := calibration.Validate(); err == nil {
			t.Fatalf("precipitation detection threshold %v unexpectedly passed validation", threshold)
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

func TestOverallOpticalTurbulencePenaltyIsBoundedAndWeakerThanCloudObstruction(t *testing.T) {
	calibration := DefaultOverallIndexCalibration()
	best := boundedOpticalTurbulenceFactor(1, 1, calibration)
	if math.Abs(best-1) > 1e-12 {
		t.Fatalf("best optical-turbulence factor = %v, want 1", best)
	}
	worst := boundedOpticalTurbulenceFactor(0, 0, calibration)
	wantWorst := 1 - calibration.OpticalTurbulenceMaxPenalty
	if math.Abs(worst-wantWorst) > 1e-12 {
		t.Fatalf("worst optical-turbulence factor = %v, want bounded floor %v", worst, wantWorst)
	}
	// At the default square, 20% effective cloud obstruction is already a
	// larger penalty and cloud transmission can continue all the way to zero.
	cloudFactor := math.Pow(0.80, calibration.CloudWeight)
	if !(cloudFactor < worst) {
		t.Fatalf("20%% cloud factor %v is not stronger than worst turbulence %v", cloudFactor, worst)
	}
	moderate := boundedOpticalTurbulenceFactor(0.5, 0.5, calibration)
	if !(moderate > worst && moderate < 1) {
		t.Fatalf("moderate optical-turbulence factor = %v, want between %v and 1", moderate, worst)
	}
}

func TestOverallPoorSeeingDoesNotVetoOtherwiseClearObserving(t *testing.T) {
	calibration := DefaultOverallIndexCalibration()
	// Force every physically valid synthetic value beyond the poor
	// high-resolution boundary without changing the physical calculation.
	calibration.GoodSeeingArcsec = 0.01
	calibration.BadSeeingArcsec = 0.02
	surface := clearSyntheticSurface()
	for index := range surface.Frames {
		surface.Frames[index].WindSpeedMS = 0
		surface.Frames[index].WindGustMS = 0
	}
	frames, err := ComputeHourlyOverallIndex(SyntheticVerticalFixture(), surface, SyntheticCloudFixture(), calibration)
	if err != nil {
		t.Fatal(err)
	}
	want := 1 + 9*(1-calibration.OpticalTurbulenceMaxPenalty)
	for index, frame := range frames {
		if frame.SeeingQualityPercent != 0 {
			t.Fatalf("frame %d seeing quality = %v, want forced poor boundary", index, frame.SeeingQualityPercent)
		}
		if math.Abs(frame.Index-want) > 1e-9 {
			t.Fatalf("frame %d clear poor-seeing index = %v, want bounded %v", index, frame.Index, want)
		}
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
		frame.PrecipitationMM = 0
		frame.VisibilityKM = 50
		frame.RelativeHumidityPercent = 50
		frame.DewPointC = frame.TemperatureC - 10
	}
	return series
}
