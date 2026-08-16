package forecast

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

type astrodomeScienceTestShortVerifier struct{}

func (astrodomeScienceTestShortVerifier) VerifyAstrodomeScienceShortIntervalNode(
	context.Context,
	time.Time,
	float64,
	AstrodomeScienceCertifiedShortInterval,
) error {
	return nil
}

type astrodomeScienceRecordingShortVerifier struct {
	paths   map[uint64]struct{}
	failBit uint64
}

func (verifier *astrodomeScienceRecordingShortVerifier) VerifyAstrodomeScienceShortIntervalNode(
	_ context.Context,
	_ time.Time,
	pathM float64,
	_ AstrodomeScienceCertifiedShortInterval,
) error {
	bits := math.Float64bits(pathM)
	if verifier.paths == nil {
		verifier.paths = make(map[uint64]struct{})
	}
	verifier.paths[bits] = struct{}{}
	if verifier.failBit != 0 && bits == verifier.failBit {
		return errors.New("selected represented node rejected")
	}
	return nil
}

func TestAstrodomeScienceZenithMatchesAnalyticConstantAtmosphere(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	setAstrodomeScienceConstantAtmosphere(volume, 80000, 270, 0.005, 0, 0, 0, 0, 0, 0)
	reconstructor, ray, path, site, calibration := astrodomeScienceFixture(t, volume, validAt, 90)

	node, err := ComputeAstrodomeScienceNode(context.Background(), reconstructor, ray, validAt, path, site, calibration)
	if err != nil {
		t.Fatalf("ComputeAstrodomeScienceNode: %v", err)
	}
	if !node.Available || node.IntegratedCn2 == nil || node.SlantWaterKgM2 == nil || node.Seeing500Arcsec == nil {
		t.Fatalf("zenith node is incomplete: %+v", node)
	}
	if node.IntegratedCn2.Value > 1e-30 || *node.Seeing500Arcsec > 1e-10 {
		t.Fatalf("near-constant neutral atmosphere produced material J=%g seeing=%g", node.IntegratedCn2.Value, *node.Seeing500Arcsec)
	}
	virtualTemperature := 270 * (1 + (calibration.WaterVapourGasConstantJKgK/calibration.DryAirGasConstantJKgK-1)*0.005)
	density := 80000 / (calibration.DryAirGasConstantJKgK * virtualTemperature)
	pathLength := path.Cells[0].EndPathM - path.Cells[0].StartPathM
	wantWater := density * 0.005 * pathLength
	assertAstrodomeScienceClose(t, "analytic zenith slant water", node.SlantWaterKgM2.Value, wantWater, 2e-12)
	if node.Tau0500MS != nil || !node.Tau0UnboundedAbove || node.Tau0State != "calm_numerical_limit" {
		t.Fatalf("calm tau0 semantics = value %v unbounded %v state %q", node.Tau0500MS, node.Tau0UnboundedAbove, node.Tau0State)
	}
	if node.Overall == nil || *node.Overall < 9.999999 {
		t.Fatalf("clear constant-atmosphere Overall = %v", node.Overall)
	}
}

func TestAstrodomeScienceConstantIntegralUsesExactSphericalRayLength(t *testing.T) {
	t.Parallel()

	observer := Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"}
	azimuth := 90.0
	ray, err := NewAstrodomeRay(observer, 1000, 30, &azimuth)
	if err != nil {
		t.Fatal(err)
	}
	top, err := ray.IntersectAltitude(11000)
	if err != nil {
		t.Fatal(err)
	}
	interval := astrodomeScienceAtomicInterval{startM: 0, endM: top.PathLengthM, cellID: "analytic"}
	calibration := DefaultAstrodomeScienceCalibration()
	evaluate := func(_ context.Context, _ float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
		return astrodomeScienceEvaluation{values: astrodomeScienceVector{2, 3, 4, 5, 6}}, nil
	}
	pass, err := integrateAstrodomeSciencePath(context.Background(), evaluate, []astrodomeScienceAtomicInterval{interval}, calibration, 1)
	if err != nil {
		t.Fatal(err)
	}
	for index, constant := range []float64{2, 3, 4, 5, 6} {
		assertAstrodomeScienceClose(t, "constant spherical integral", pass.values[index], constant*top.PathLengthM, 2e-13)
	}
	flatCosecantLength := 10000 / math.Sin(30*math.Pi/180)
	if math.Abs(top.PathLengthM-flatCosecantLength) < 1 {
		t.Fatalf("spherical path unexpectedly collapsed to flat airmass: spherical=%g flat=%g", top.PathLengthM, flatCosecantLength)
	}
}

func TestAstrodomeSciencePartitionInteriorEndpointsUseMetricGuard(t *testing.T) {
	t.Parallel()

	shortLengthM := 1.1 * AstrodomeScienceMinimumEventIntervalLengthM
	guardInsetM := math.Nextafter(AstrodomeScienceCompoundRootSideGuardM, math.Inf(1))
	for _, test := range []struct {
		name                string
		startM, endM        float64
		wantLeft, wantRight float64
	}{
		{name: "long partition", startM: 100, endM: 110, wantLeft: 100 + guardInsetM, wantRight: 110 - guardInsetM},
		{name: "short accepted partition", startM: 100, endM: 100 + 2*shortLengthM,
			wantLeft: 100 + guardInsetM, wantRight: 100 + 2*shortLengthM - guardInsetM},
		{name: "event-forced short partition", startM: 100, endM: 100 + shortLengthM,
			wantLeft: 100 + guardInsetM, wantRight: 100 + shortLengthM - guardInsetM},
	} {
		t.Run(test.name, func(t *testing.T) {
			leftM, rightM, err := AstrodomeSciencePartitionInteriorEndpoints(test.startM, test.endM)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(leftM-test.wantLeft) > 1e-12 || math.Abs(rightM-test.wantRight) > 1e-12 {
				t.Fatalf("interior endpoints = %.12g, %.12g; want %.12g, %.12g", leftM, rightM, test.wantLeft, test.wantRight)
			}
		})
	}
	if _, _, err := AstrodomeSciencePartitionInteriorEndpoints(100, 100+AstrodomeScienceMinimumEventIntervalLengthM/2); err == nil {
		t.Fatal("uncertified compound-root interval was accepted")
	}
}

func TestAstrodomeScienceAcceptsDistinctCertifiedAtomicPartition(t *testing.T) {
	t.Parallel()

	pathForLength := func(lengthM float64) AstrodomeSciencePath {
		return AstrodomeSciencePath{
			NativeContext: &astrodomeScienceTestNativeResolver{},
			Availability: AstrodomeSciencePathAvailability{
				Geometry: true, Turbulence: true, Cloud: true, TemporalBrackets: true, Terrain: true,
			},
			Cells: []AstrodomeSciencePathCell{{
				StartPathM: 0, EndPathM: lengthM, HorizontalCellID: "boundary-resolution",
				NativeVerticalPredicatesIsolated: true, TropopausePredicatesIsolated: true,
				TerrainState: AstrodomeScienceTerrainClear,
			}},
			TopClosed: true, GeometryCoverage: 1, TurbulencePathCoverage: 1,
			CloudPathCoverage: 1, TemporalResolutionHours: 1,
		}
	}
	for _, lengthM := range []float64{
		1.01 * AstrodomeScienceMinimumEventIntervalLengthM,
		2 * AstrodomeScienceMinimumEventIntervalLengthM,
	} {
		intervals, err := prepareAstrodomeSciencePath(pathForLength(lengthM))
		if err != nil || len(intervals) != 1 || intervals[0].endM-intervals[0].startM != lengthM {
			t.Fatalf("%.9g m distinct atomic partition = %+v, %v", lengthM, intervals, err)
		}
	}
	_, err := prepareAstrodomeSciencePath(pathForLength(AstrodomeScienceMinimumEventIntervalLengthM / 2))
	if err == nil || !errors.Is(err, ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("compound-root guard atomic partition error = %v", err)
	}
	_, err = prepareAstrodomeSciencePath(pathForLength(AstrodomeScienceMinimumEventIntervalLengthM))
	if err == nil || !errors.Is(err, ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("exact certified-minimum atomic partition error = %v", err)
	}
	nextLengthM := math.Nextafter(AstrodomeScienceMinimumEventIntervalLengthM, math.Inf(1))
	intervals, err := prepareAstrodomeSciencePath(pathForLength(nextLengthM))
	if err != nil || len(intervals) != 1 || intervals[0].endM != nextLengthM {
		t.Fatalf("nextafter certified-minimum atomic partition = %+v, %v", intervals, err)
	}
}

func TestAstrodomeScienceProductionEvidenceCertifiedShortPanel(t *testing.T) {
	t.Parallel()

	physical := AstrodomeScienceRootEvidence{
		RepresentativePathM: 22409.822978115939,
		LeftPathM:           22409.822977871001,
		RightPathM:          22409.822978360877,
		EventID:             "boundary/hhl/010",
		Kind:                "physical",
	}
	horizontal := AstrodomeScienceRootEvidence{
		RepresentativePathM: 22409.823594937123,
		LeftPathM:           22409.82359439064,
		RightPathM:          22409.823595483602,
		EventID:             "longitude/976",
		Kind:                "horizontal",
	}
	observer := AstrodomeScienceRootEvidence{
		RepresentativePathM: 0, LeftPathM: 0, RightPathM: 0,
		EventID: "ray/observer-aperture", Kind: "path-endpoint", Exact: true,
	}
	certificate := AstrodomeScienceCertifiedShortInterval{
		CertificateID:      "production-f031-node70",
		HorizontalCellID:   "cell-lat0384-lon0975",
		Start:              physical,
		End:                horizontal,
		OpenSafeStartPathM: physical.RightPathM + 0.8e-6,
		OpenSafeEndPathM:   horizontal.LeftPathM - 0.8e-6,
	}
	path := AstrodomeSciencePath{
		NativeContext:         &astrodomeScienceTestNativeResolver{},
		ShortIntervalVerifier: astrodomeScienceTestShortVerifier{},
		Availability: AstrodomeSciencePathAvailability{
			Geometry: true, Turbulence: true, Cloud: true, TemporalBrackets: true, Terrain: true,
		},
		Cells: []AstrodomeSciencePathCell{{
			StartPathM: 0, EndPathM: horizontal.RepresentativePathM,
			HorizontalCellID: certificate.HorizontalCellID,
			BreakpointsPathM: []float64{physical.RepresentativePathM},
			StartEvidence:    &observer, EndEvidence: &horizontal,
			BreakpointEvidence:               []AstrodomeScienceRootEvidence{physical},
			CertifiedShortIntervals:          []AstrodomeScienceCertifiedShortInterval{certificate},
			NativeVerticalPredicatesIsolated: true, TropopausePredicatesIsolated: true,
			TerrainState: AstrodomeScienceTerrainClear,
		}},
		TopClosed: true, GeometryCoverage: 1, TurbulencePathCoverage: 1,
		CloudPathCoverage: 1, TemporalResolutionHours: 1,
	}
	intervals, err := prepareAstrodomeSciencePath(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(intervals) != 2 || intervals[1].certifiedShort == nil {
		t.Fatalf("production evidence intervals = %+v", intervals)
	}
	interval := intervals[1]
	leftSafeM, rightSafeM, err := astrodomeSciencePartitionInteriorEndpoints(interval)
	if err != nil {
		t.Fatal(err)
	}
	if leftSafeM <= certificate.OpenSafeStartPathM || rightSafeM >= certificate.OpenSafeEndPathM {
		t.Fatalf("represented safe endpoints %.17g..%.17g escaped open certificate %.17g..%.17g",
			leftSafeM, rightSafeM, certificate.OpenSafeStartPathM, certificate.OpenSafeEndPathM)
	}
	block := astrodomeScienceCloudBlockKey{cellID: interval.cellID, tier: AstrodomeScienceCloudHigh}
	evaluate := func(_ context.Context, pathM float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
		if pathM <= certificate.OpenSafeStartPathM || pathM >= certificate.OpenSafeEndPathM {
			t.Fatalf("removed short-panel rule sampled outside open safe interval at %.17g", pathM)
		}
		x := 2*(pathM-interval.startM)/(interval.endM-interval.startM) - 1
		value := 2 + x*x*x*x*x
		return astrodomeScienceEvaluation{
			values:   astrodomeScienceVector{value, value, value, value, value},
			blockKey: block, regime: "hmnsp99-troposphere",
		}, nil
	}
	_, err = astrodomeScienceGuardConstrained5_3(context.Background(), evaluate, interval)
	if !errors.Is(err, ErrAstrodomeScienceNonConvergence) {
		t.Fatalf("certified sub-GL2 interval error = %v", err)
	}
}

func TestAstrodomeScienceCertifiedSubGL2PanelPublishesLimitedApproximation(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	setAstrodomeScienceConstantAtmosphere(volume, 80000, 270, 0.005, 0, 0, 0, 0, 0, 0)
	reconstructor, ray, path, site, calibration := astrodomeScienceFixture(t, volume, validAt, 90)
	startM := 250.0
	endM := startM + 0.75e-3
	startEvent := AstrodomeScienceRootEvidence{
		RepresentativePathM: startM, LeftPathM: startM, RightPathM: startM,
		EventID: "boundary/hhl/test", Kind: "physical", Exact: true,
	}
	endEvent := AstrodomeScienceRootEvidence{
		RepresentativePathM: endM, LeftPathM: endM, RightPathM: endM,
		EventID: "longitude/test", Kind: "horizontal", Exact: true,
	}
	observer := AstrodomeScienceRootEvidence{
		RepresentativePathM: 0, LeftPathM: 0, RightPathM: 0,
		EventID: "ray/observer-aperture", Kind: "path-endpoint", Exact: true,
	}
	topM := path.Cells[0].EndPathM
	top := AstrodomeScienceRootEvidence{
		RepresentativePathM: topM, LeftPathM: topM, RightPathM: topM,
		EventID: "ray/model-top", Kind: "path-endpoint", Exact: true,
	}
	middleM := path.Cells[0].BreakpointsPathM[0]
	middle := AstrodomeScienceRootEvidence{
		RepresentativePathM: middleM, LeftPathM: middleM, RightPathM: middleM,
		EventID: "boundary/existing", Kind: "physical", Exact: true,
	}
	certificate := AstrodomeScienceCertifiedShortInterval{
		CertificateID: "record-all-represented-nodes", HorizontalCellID: "test-cell",
		Start: startEvent, End: endEvent,
		OpenSafeStartPathM: startM + 1e-6,
		OpenSafeEndPathM:   endM - 1e-6,
	}
	path.Cells[0].StartEvidence = &observer
	path.Cells[0].EndEvidence = &top
	path.Cells[0].BreakpointsPathM = []float64{startM, endM, middleM}
	path.Cells[0].BreakpointEvidence = []AstrodomeScienceRootEvidence{startEvent, endEvent, middle}
	path.Cells[0].CertifiedShortIntervals = []AstrodomeScienceCertifiedShortInterval{certificate}
	recorder := &astrodomeScienceRecordingShortVerifier{}
	path.ShortIntervalVerifier = recorder
	node, err := ComputeAstrodomeScienceNode(
		context.Background(), reconstructor, ray, validAt, path, site, calibration,
	)
	if err != nil || !node.Available || node.State != AstrodomeScienceNodeAvailable ||
		node.Quality.Category != AstrodomeScienceQualityLimited || node.Quality.QuadratureConverged ||
		math.Abs(node.Quality.ApproximationLengthM-(endM-startM)) > 1e-15 || len(recorder.paths) != 3 {
		t.Fatalf("certified sub-GL2 result = %+v, err=%v, evaluated=%d", node, err, len(recorder.paths))
	}
}

func TestAstrodomeScienceCertifiedShortPanelRejectsClusterAndMalformedProof(t *testing.T) {
	t.Parallel()

	pathFor := func(gapM float64, verifier AstrodomeScienceShortIntervalVerifier) AstrodomeSciencePath {
		start := AstrodomeScienceRootEvidence{
			RepresentativePathM: 0, LeftPathM: 0, RightPathM: 0,
			EventID: "start", Kind: "physical", Exact: true,
		}
		end := AstrodomeScienceRootEvidence{
			RepresentativePathM: gapM, LeftPathM: gapM, RightPathM: gapM,
			EventID: "end", Kind: "horizontal", Exact: true,
		}
		certificate := AstrodomeScienceCertifiedShortInterval{
			CertificateID: "short", HorizontalCellID: "cell",
			Start: start, End: end,
			OpenSafeStartPathM: math.Nextafter(0, math.Inf(1)),
			OpenSafeEndPathM:   math.Nextafter(gapM, math.Inf(-1)),
		}
		return AstrodomeSciencePath{
			NativeContext: &astrodomeScienceTestNativeResolver{}, ShortIntervalVerifier: verifier,
			Availability: AstrodomeSciencePathAvailability{
				Geometry: true, Turbulence: true, Cloud: true, TemporalBrackets: true, Terrain: true,
			},
			Cells: []AstrodomeSciencePathCell{{
				StartPathM: 0, EndPathM: gapM, HorizontalCellID: "cell",
				StartEvidence: &start, EndEvidence: &end,
				CertifiedShortIntervals:          []AstrodomeScienceCertifiedShortInterval{certificate},
				NativeVerticalPredicatesIsolated: true, TropopausePredicatesIsolated: true,
				TerrainState: AstrodomeScienceTerrainClear,
			}},
			TopClosed: true, GeometryCoverage: 1, TurbulencePathCoverage: 1,
			CloudPathCoverage: 1, TemporalResolutionHours: 1,
		}
	}
	_, err := prepareAstrodomeSciencePath(pathFor(
		AstrodomeScienceRootMergeToleranceM, astrodomeScienceTestShortVerifier{},
	))
	if !errors.Is(err, ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("exact cluster-threshold certificate error = %v", err)
	}
	_, err = prepareAstrodomeSciencePath(pathFor(0.75e-3, nil))
	if !errors.Is(err, ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("certificate without verifier error = %v", err)
	}
	malformed := pathFor(0.75e-3, astrodomeScienceTestShortVerifier{})
	malformed.Cells[0].CertifiedShortIntervals[0].OpenSafeStartPathM = 0.5e-3
	malformed.Cells[0].CertifiedShortIntervals[0].OpenSafeEndPathM = 0.4e-3
	_, err = prepareAstrodomeSciencePath(malformed)
	if !errors.Is(err, ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("reversed safe interval error = %v", err)
	}
	mutatedEvidence := pathFor(0.75e-3, astrodomeScienceTestShortVerifier{})
	mutatedEvidence.Cells[0].CertifiedShortIntervals[0].Start.EventID = "mutated"
	_, err = prepareAstrodomeSciencePath(mutatedEvidence)
	if !errors.Is(err, ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("mutated endpoint evidence error = %v", err)
	}
	equalBound := pathFor(0.75e-3, astrodomeScienceTestShortVerifier{})
	equalBound.Cells[0].CertifiedShortIntervals[0].OpenSafeStartPathM =
		equalBound.Cells[0].CertifiedShortIntervals[0].Start.RightPathM
	_, err = prepareAstrodomeSciencePath(equalBound)
	if !errors.Is(err, ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("non-open safe bound error = %v", err)
	}
	duplicate := pathFor(0.75e-3, astrodomeScienceTestShortVerifier{})
	duplicate.Cells[0].CertifiedShortIntervals = append(
		duplicate.Cells[0].CertifiedShortIntervals,
		duplicate.Cells[0].CertifiedShortIntervals[0],
	)
	_, err = prepareAstrodomeSciencePath(duplicate)
	if !errors.Is(err, ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("duplicate certificate error = %v", err)
	}
}

func TestAstrodomeScienceBreakpointCompactionUsesSharedRootCluster(t *testing.T) {
	t.Parallel()

	duplicates, err := compactAstrodomeScienceBreakpoints([]float64{0, 0, 1})
	if err != nil || len(duplicates) != 2 || duplicates[0] != 0 || duplicates[1] != 1 {
		t.Fatalf("bit-identical breakpoint duplicates = %v, %v", duplicates, err)
	}
	clusterOffset := 0.75 * AstrodomeScienceRootMergeToleranceM
	_, err = compactAstrodomeScienceBreakpoints([]float64{0, clusterOffset, 1})
	if !errors.Is(err, ErrAstrodomeScienceIncompletePartition) {
		t.Fatalf("anonymous %.9g m root cluster error = %v", clusterOffset, err)
	}
	distinctOffset := 1.25 * AstrodomeScienceRootMergeToleranceM
	distinct, err := compactAstrodomeScienceBreakpoints([]float64{0, distinctOffset, 1})
	if err != nil || len(distinct) != 3 || distinct[1] != distinctOffset {
		t.Fatalf("%.9g m distinct roots = %v, %v", distinctOffset, distinct, err)
	}
}

func TestAstrodomeScienceStrictContractAndGuardDerivedFloors(t *testing.T) {
	t.Parallel()

	if AstrodomeScienceVersion != "astrodome-science-kernel-v34-native-mh-glo30-informational-skyline" ||
		AstrodomeSciencePathContractVersion != "astrodome-science-path-v24-native-mh" {
		t.Fatalf("strict science versions = %q, %q",
			AstrodomeScienceVersion, AstrodomeSciencePathContractVersion)
	}
	if AstrodomeScienceMaximumApproximatePathLengthM != 1 {
		t.Fatalf("approximate path ceiling = %.12g m", AstrodomeScienceMaximumApproximatePathLengthM)
	}
	if AstrodomeScienceRootToleranceM != 0.0002 ||
		AstrodomeScienceRootMergeToleranceM != 0.0004 ||
		AstrodomeScienceCompoundRootSideGuardM != 0.0005 ||
		AstrodomeScienceCompoundRootSideGuardM <= AstrodomeScienceRootToleranceM {
		t.Fatalf("strict root radius %.12g m, merge %.12g m, and side guard %.12g m are inconsistent",
			AstrodomeScienceRootToleranceM, AstrodomeScienceRootMergeToleranceM,
			AstrodomeScienceCompoundRootSideGuardM)
	}
	const wantConstrainedFloorM = 0.001000000000001
	if math.Abs(AstrodomeScienceMinimumEventIntervalLengthM-wantConstrainedFloorM) > 2e-17 {
		t.Fatalf("constrained-rule guard-derived floor = %.17g m; want %.17g m",
			AstrodomeScienceMinimumEventIntervalLengthM, wantConstrainedFloorM)
	}
	if !(AstrodomeScienceMinimumEventIntervalLengthM < AstrodomeScienceGL2MinimumEventIntervalLengthM &&
		AstrodomeScienceGL2MinimumEventIntervalLengthM < AstrodomeScienceGL3MinimumEventIntervalLengthM &&
		AstrodomeScienceGL3MinimumEventIntervalLengthM < AstrodomeScienceGL5MinimumEventIntervalLengthM &&
		AstrodomeScienceGL5MinimumEventIntervalLengthM < AstrodomeScienceGK15MinimumEventIntervalLengthM) {
		t.Fatalf("quadrature floors are not strictly ordered: constrained=%.12g GL2=%.12g GL3=%.12g GL5=%.12g K15=%.12g",
			AstrodomeScienceMinimumEventIntervalLengthM,
			AstrodomeScienceGL2MinimumEventIntervalLengthM,
			AstrodomeScienceGL3MinimumEventIntervalLengthM,
			AstrodomeScienceGL5MinimumEventIntervalLengthM,
			AstrodomeScienceGK15MinimumEventIntervalLengthM)
	}
}

func TestAstrodomeScienceProductionUsesOneEmbeddedPassAndReferenceRepeats(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	setAstrodomeScienceConstantAtmosphere(volume, 80000, 270, 0.005, 0, 0, 0, 8, -2, 0)
	reconstructor, ray, path, site, productionCalibration := astrodomeScienceFixture(t, volume, validAt, 30)
	if productionCalibration.IntegrationVerification != AstrodomeScienceVerificationEmbedded {
		t.Fatalf("production verification = %q", productionCalibration.IntegrationVerification)
	}
	production, err := ComputeAstrodomeScienceNode(
		context.Background(), reconstructor, ray, validAt, path, site, productionCalibration,
	)
	if err != nil {
		t.Fatal(err)
	}

	referenceCalibration := ReferenceAstrodomeScienceCalibration()
	if referenceCalibration.IntegrationVerification != AstrodomeScienceVerificationIndependentRepeat {
		t.Fatalf("reference verification = %q", referenceCalibration.IntegrationVerification)
	}
	reference, err := ComputeAstrodomeScienceNode(
		context.Background(), reconstructor, ray, validAt, path, site, referenceCalibration,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !production.Available || !reference.Available || production.NumericalError == nil || reference.NumericalError == nil {
		t.Fatalf("production/reference nodes are incomplete: production=%+v reference=%+v", production, reference)
	}
	if reference.QuadratureEvaluations <= production.QuadratureEvaluations ||
		reference.QuadratureSubdivisions <= production.QuadratureSubdivisions {
		t.Fatalf("reference did not perform the independent repeat: evaluations=%d/%d subdivisions=%d/%d",
			production.QuadratureEvaluations, reference.QuadratureEvaluations,
			production.QuadratureSubdivisions, reference.QuadratureSubdivisions)
	}
	if !strings.Contains(production.Attribution.NumericalMethod, "single joint adaptive") ||
		strings.Contains(production.Attribution.NumericalMethod, "reference verification") ||
		!strings.Contains(reference.Attribution.NumericalMethod, "reference verification") {
		t.Fatalf("numerical attribution is inconsistent: production=%q reference=%q",
			production.Attribution.NumericalMethod, reference.Attribution.NumericalMethod)
	}
	assertAstrodomeScienceClose(t, "production/reference J", production.IntegratedCn2.Value, reference.IntegratedCn2.Value, 2e-12)
	assertAstrodomeScienceClose(t, "production/reference JV", production.WindWeightedCn2.Value, reference.WindWeightedCn2.Value, 2e-12)
	if production.NumericalError.OverallAbsolute < 0 || production.NumericalError.CloudTransmissionAbsolute < 0 {
		t.Fatalf("embedded numerical diagnostics are invalid: %+v", production.NumericalError)
	}

	invalid := productionCalibration
	invalid.IntegrationVerification = "unknown"
	if err := invalid.Validate(); err == nil {
		t.Fatal("unknown integration verification was accepted")
	}
}

func BenchmarkAstrodomeScienceProductionAndReferenceVerification(b *testing.B) {
	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	setAstrodomeScienceConstantAtmosphere(volume, 80000, 270, 0.005, 0, 0, 0, 8, -2, 0)
	reconstructor, ray, path, site, _ := astrodomeScienceFixture(b, volume, validAt, 30)
	benchmarks := []struct {
		name        string
		calibration AstrodomeScienceCalibration
	}{
		{name: "production-embedded", calibration: DefaultAstrodomeScienceCalibration()},
		{name: "reference-repeat", calibration: ReferenceAstrodomeScienceCalibration()},
	}
	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				node, err := ComputeAstrodomeScienceNode(
					context.Background(), reconstructor, ray, validAt, path, site, benchmark.calibration,
				)
				if err != nil || !node.Available {
					b.Fatalf("compute node: available=%v err=%v", node.Available, err)
				}
			}
		})
	}
}

func TestAstrodomeScienceQuadraturePublishesLimitedMidpointBelowBoundaryResolution(t *testing.T) {
	t.Parallel()

	calibration := DefaultAstrodomeScienceCalibration()
	calibration.RelativeTolerance = 1e-15
	calibration.AbsoluteTolerance = AstrodomeScienceIntegralTolerances{}
	intervalLengthM := 1.5 * AstrodomeScienceMinimumEventIntervalLengthM
	interval := astrodomeScienceAtomicInterval{startM: 0, endM: intervalLengthM, cellID: "boundary-floor"}
	evaluate := func(_ context.Context, pathM float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
		value := 0.0
		if pathM >= 0.501*intervalLengthM {
			value = 1
		}
		return astrodomeScienceEvaluation{values: astrodomeScienceVector{value, value, value, value, value}}, nil
	}
	pass, err := integrateAstrodomeSciencePath(context.Background(), evaluate,
		[]astrodomeScienceAtomicInterval{interval}, calibration, 1)
	if err != nil || pass.shortPanelApproximationCount != 1 ||
		math.Abs(pass.approximationLengthM-intervalLengthM) > 1e-15 {
		t.Fatalf("limited sub-centimetre panel = %+v, err=%v", pass, err)
	}
}

func TestAstrodomeScienceShortEventIntervalUsesOneShotGaussLegendre5_3(t *testing.T) {
	t.Parallel()

	calibration := DefaultAstrodomeScienceCalibration()
	lengthM := 1.01 * AstrodomeScienceGL5MinimumEventIntervalLengthM
	interval := astrodomeScienceAtomicInterval{startM: 200_000, endM: 200_000 + lengthM, cellID: "short-large-coordinate"}
	block := astrodomeScienceCloudBlockKey{cellID: interval.cellID, tier: AstrodomeScienceCloudMiddle}
	var sampled []float64
	evaluate := func(_ context.Context, pathM float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
		if pathM-interval.startM <= AstrodomeScienceCompoundRootSideGuardM ||
			interval.endM-pathM <= AstrodomeScienceCompoundRootSideGuardM {
			t.Fatalf("large-coordinate GL5/GL3 sampled inside compound-root envelope at %.15g", pathM)
		}
		sampled = append(sampled, pathM)
		value := 3 + 2*(pathM-interval.startM)
		return astrodomeScienceEvaluation{
			values: astrodomeScienceVector{value, value, value, value, value}, cloudFractionNominal: 0.3,
			cloudFractionConservativeUpper: 0.4, blockKey: block, regime: "hmnsp99-troposphere",
		}, nil
	}
	pass, err := integrateAstrodomeSciencePath(
		context.Background(), evaluate, []astrodomeScienceAtomicInterval{interval}, calibration, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	lengthM = interval.endM - interval.startM
	want := 3*lengthM + lengthM*lengthM
	for component, got := range pass.values {
		if math.Abs(got-want) > 1e-11*math.Max(1, math.Abs(want)) {
			t.Fatalf("micro component %d = %.15g; want %.15g", component, got, want)
		}
	}
	// GL5 and GL3 share only their centre: seven integration nodes plus the two
	// one-sided probes enforce both convergence and partition ownership.
	if len(sampled) != 9 || pass.subdivisions != 1 {
		t.Fatalf("one-shot GL5/GL3 used %d samples and %d panels; want 9 and 1", len(sampled), pass.subdivisions)
	}
}

func TestAstrodomeScienceShortEventIntervalCarriesFullMagnitudeAllowance(t *testing.T) {
	t.Parallel()

	calibration := DefaultAstrodomeScienceCalibration()
	calibration.RelativeTolerance = 1e-15
	calibration.AbsoluteTolerance = AstrodomeScienceIntegralTolerances{}
	intervalLengthM := 1.01 * AstrodomeScienceMinimumEventIntervalLengthM
	interval := astrodomeScienceAtomicInterval{startM: 0, endM: intervalLengthM, cellID: "short-nonconvergence"}
	evaluate := func(_ context.Context, pathM float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
		value := 0.0
		if pathM > 0.46913578*intervalLengthM {
			value = 1
		}
		return astrodomeScienceEvaluation{values: astrodomeScienceVector{value, value, value, value, value}}, nil
	}
	pass, err := integrateAstrodomeSciencePath(
		context.Background(), evaluate, []astrodomeScienceAtomicInterval{interval}, calibration, 1,
	)
	if err != nil || pass.shortPanelApproximationCount != 1 ||
		pass.errors[0] < math.Abs(pass.values[0]) {
		t.Fatalf("unresolved micro-interval allowance = %+v, err=%v", pass, err)
	}
}

func TestAstrodomeScienceShortGaussLegendre5SamplesOutsideCompoundRootEnvelope(t *testing.T) {
	t.Parallel()

	lengthM := AstrodomeScienceGL5MinimumEventIntervalLengthM * 1.01
	interval := astrodomeScienceAtomicInterval{startM: 10, endM: 10 + lengthM, cellID: "near-root-cluster"}
	minimumClearanceM := AstrodomeScienceGL5EndpointFraction * lengthM
	compoundRootGuardM := AstrodomeScienceCompoundRootSideGuardM
	if minimumClearanceM <= compoundRootGuardM {
		t.Fatalf("micro-node clearance %.12g m does not exceed compound-root envelope %.12g m",
			minimumClearanceM, compoundRootGuardM)
	}
	block := astrodomeScienceCloudBlockKey{cellID: interval.cellID, tier: AstrodomeScienceCloudHigh}
	evaluate := func(_ context.Context, pathM float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
		if pathM-interval.startM <= compoundRootGuardM ||
			interval.endM-pathM <= compoundRootGuardM {
			t.Fatalf("micro rule sampled inside endpoint root uncertainty at %.15g", pathM)
		}
		return astrodomeScienceEvaluation{blockKey: block, regime: "hmnsp99-troposphere"}, nil
	}
	if _, err := astrodomeScienceGaussLegendre5_3(context.Background(), evaluate, interval); err != nil {
		t.Fatal(err)
	}
}

func TestAstrodomeScienceProductionPhysicalRootGapUsesLimitedMidpointBelowGL2(t *testing.T) {
	t.Parallel()

	productionGapM := 0.5 * (AstrodomeScienceMinimumEventIntervalLengthM +
		AstrodomeScienceGL2MinimumEventIntervalLengthM)
	if productionGapM <= AstrodomeScienceMinimumEventIntervalLengthM ||
		productionGapM >= AstrodomeScienceGL2MinimumEventIntervalLengthM {
		t.Fatalf("production gap %.12g m is outside the removed extrapolatory range", productionGapM)
	}
	interval := astrodomeScienceAtomicInterval{
		startM: 200_000,
		endM:   200_000 + productionGapM,
		cellID: "cell-lat0377-lon0973",
	}
	block := astrodomeScienceCloudBlockKey{cellID: interval.cellID, tier: AstrodomeScienceCloudMiddle}
	var sampled int
	evaluate := func(_ context.Context, pathM float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
		if pathM-interval.startM <= AstrodomeScienceCompoundRootSideGuardM ||
			interval.endM-pathM <= AstrodomeScienceCompoundRootSideGuardM {
			t.Fatalf("short rule sampled inside the physical-root envelope at %.15g", pathM)
		}
		sampled++
		value := 2 + 0.5*(pathM-interval.startM)
		return astrodomeScienceEvaluation{
			values:   astrodomeScienceVector{value, value, value, value, value},
			blockKey: block, regime: "hmnsp99-troposphere",
		}, nil
	}
	calibration := DefaultAstrodomeScienceCalibration()
	pass, err := integrateAstrodomeSciencePath(
		context.Background(), evaluate, []astrodomeScienceAtomicInterval{interval}, calibration, 1,
	)
	if err != nil || sampled != 3 || pass.shortPanelApproximationCount != 1 ||
		math.Abs(pass.approximationLengthM-(interval.endM-interval.startM)) > 1e-15 {
		t.Fatalf("sub-GL2 production gap pass=%+v error=%v samples=%d", pass, err, sampled)
	}
}

func TestAstrodomeSciencePositiveShortRuleSelectionAtBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		lengthM     float64
		wantSamples int
	}{
		{name: "below-GL2", lengthM: math.Nextafter(AstrodomeScienceGL2MinimumEventIntervalLengthM, math.Inf(-1)), wantSamples: 3},
		{name: "equal-GL2", lengthM: AstrodomeScienceGL2MinimumEventIntervalLengthM, wantSamples: 3},
		{name: "above-GL2", lengthM: math.Nextafter(AstrodomeScienceGL2MinimumEventIntervalLengthM, math.Inf(1)), wantSamples: 5},
		{name: "below-GL3", lengthM: math.Nextafter(AstrodomeScienceGL3MinimumEventIntervalLengthM, math.Inf(-1)), wantSamples: 5},
		{name: "equal-GL3", lengthM: AstrodomeScienceGL3MinimumEventIntervalLengthM, wantSamples: 5},
		{name: "above-GL3", lengthM: math.Nextafter(AstrodomeScienceGL3MinimumEventIntervalLengthM, math.Inf(1)), wantSamples: 7},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			interval := astrodomeScienceAtomicInterval{startM: 0, endM: test.lengthM, cellID: test.name}
			sampled := 0
			evaluate := func(_ context.Context, _ float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
				sampled++
				return astrodomeScienceEvaluation{values: astrodomeScienceVector{1, 1, 1, 1, 1}}, nil
			}
			_, err := integrateAstrodomeSciencePath(
				context.Background(), evaluate, []astrodomeScienceAtomicInterval{interval}, DefaultAstrodomeScienceCalibration(), 1,
			)
			if err != nil || sampled != test.wantSamples {
				t.Fatalf("boundary selection error=%v samples=%d; want %d", err, sampled, test.wantSamples)
			}
		})
	}
}

func TestAstrodomeScienceProductionShortIntervalsPublishLimitedWithoutExtrapolation(t *testing.T) {
	t.Parallel()

	// These lengths reproduce physical-event gaps that are too short for the
	// embedded GL2/GL1 rule. Three positive interior probes establish ownership;
	// only the midpoint forms the point estimate and the panel stays limited.
	for _, intervalLengthM := range []float64{0.00153107653, 0.0019401} {
		intervalLengthM := intervalLengthM
		t.Run(fmt.Sprintf("length-%.10f", intervalLengthM), func(t *testing.T) {
			interval := astrodomeScienceAtomicInterval{
				startM: 200_000,
				endM:   200_000 + intervalLengthM,
				cellID: "cell-lat0386-lon0973",
			}
			block := astrodomeScienceCloudBlockKey{cellID: interval.cellID, tier: AstrodomeScienceCloudMiddle}
			scales := astrodomeScienceVector{1e-13, 1e-12, 0.01, 1e-5, 1e-5}
			var sampled int
			evaluate := func(_ context.Context, pathM float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
				if pathM-interval.startM <= AstrodomeScienceCompoundRootSideGuardM ||
					interval.endM-pathM <= AstrodomeScienceCompoundRootSideGuardM {
					t.Fatalf("constrained production rule sampled inside endpoint guard at %.15g", pathM)
				}
				sampled++
				x := 2*(pathM-interval.startM)/intervalLengthM - 1
				factor := 1 + 4*x*x
				values := astrodomeScienceVector{}
				for component := range values {
					values[component] = scales[component] * factor
				}
				return astrodomeScienceEvaluation{
					values: values, cloudFractionNominal: 0.3,
					cloudFractionConservativeUpper: 0.4, blockKey: block, regime: "hmnsp99-troposphere",
				}, nil
			}
			pass, err := integrateAstrodomeSciencePath(
				context.Background(), evaluate, []astrodomeScienceAtomicInterval{interval},
				DefaultAstrodomeScienceCalibration(), 1,
			)
			if err != nil || sampled != 3 || pass.shortPanelApproximationCount != 1 ||
				pass.errors[astrodomeScienceWaterIndex] < math.Abs(pass.values[astrodomeScienceWaterIndex]) {
				t.Fatalf("sub-GL2 panel=%+v error=%v samples=%d", pass, err, sampled)
			}
		})
	}
}

func TestAstrodomeScienceFineRepeatSplitsConstrainedPhysicalPanel(t *testing.T) {
	t.Parallel()

	interval := astrodomeScienceAtomicInterval{
		startM: 200_000, endM: 200_000 + 0.128379445,
		cellID: "cell-lat0386-lon0973",
	}
	block := astrodomeScienceCloudBlockKey{cellID: interval.cellID, tier: AstrodomeScienceCloudMiddle}
	run := func(toleranceScale float64) (astrodomeSciencePass, map[uint64]struct{}) {
		paths := make(map[uint64]struct{})
		evaluate := func(_ context.Context, pathM float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
			paths[math.Float64bits(pathM)] = struct{}{}
			x := 2*(pathM-interval.startM)/(interval.endM-interval.startM) - 1
			value := 1 + 4*x*x
			return astrodomeScienceEvaluation{
				values:   astrodomeScienceVector{value, value, value, value, value},
				blockKey: block, regime: "hmnsp99-troposphere",
			}, nil
		}
		pass, err := integrateAstrodomeSciencePath(
			context.Background(), evaluate, []astrodomeScienceAtomicInterval{interval},
			DefaultAstrodomeScienceCalibration(), toleranceScale,
		)
		if err != nil {
			t.Fatal(err)
		}
		return pass, paths
	}
	coarse, coarsePaths := run(1)
	fine, finePaths := run(0.5)
	if coarse.subdivisions != 1 || fine.subdivisions != 2 {
		t.Fatalf("constrained repeat subdivisions coarse/fine = %d/%d; want 1/2",
			coarse.subdivisions, fine.subdivisions)
	}
	independentNode := false
	for path := range finePaths {
		if _, shared := coarsePaths[path]; !shared {
			independentNode = true
			break
		}
	}
	if !independentNode {
		t.Fatal("fine constrained repeat reused only coarse quadrature nodes")
	}
	for component := range coarse.values {
		if math.Abs(coarse.values[component]-fine.values[component]) > 1e-6*math.Abs(coarse.values[component]) {
			t.Fatalf("component %d coarse/fine = %.15g/%.15g", component, coarse.values[component], fine.values[component])
		}
	}
}

func TestAstrodomeScienceLimitedMidpointDoesNotClaimEmbeddedConvergence(t *testing.T) {
	t.Parallel()

	lengthM := 1.0001 * AstrodomeScienceMinimumEventIntervalLengthM
	interval := astrodomeScienceAtomicInterval{startM: 0, endM: lengthM, cellID: "ill-conditioned-safe-sliver"}
	evaluate := func(_ context.Context, _ float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
		return astrodomeScienceEvaluation{values: astrodomeScienceVector{1, 1, 1, 1, 1}}, nil
	}
	calibration := DefaultAstrodomeScienceCalibration()
	pass, err := integrateAstrodomeSciencePath(
		context.Background(), evaluate, []astrodomeScienceAtomicInterval{interval}, calibration, 1,
	)
	if err != nil || pass.shortPanelApproximationCount != 1 || pass.approximationLengthM != lengthM {
		t.Fatalf("limited midpoint pass = %+v, err=%v", pass, err)
	}
}

func TestAstrodomeScienceProductionPhysicalRootGapFailsClosedAtGuardDerivedFloor(t *testing.T) {
	t.Parallel()

	productionGapM := 2.5 * AstrodomeScienceGL5MinimumEventIntervalLengthM
	interval := astrodomeScienceAtomicInterval{
		startM: 200_000,
		endM:   200_000 + productionGapM,
		cellID: "cell-lat0377-lon0973",
	}
	calibration := DefaultAstrodomeScienceCalibration()
	calibration.RelativeTolerance = 1e-15
	calibration.AbsoluteTolerance = AstrodomeScienceIntegralTolerances{}
	var sampled int
	minimumPanelLengthM := math.Inf(1)
	evaluate := func(_ context.Context, pathM float64, panel astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
		minimumPanelLengthM = math.Min(minimumPanelLengthM, panel.endM-panel.startM)
		if (!panel.startNumericalBoundary && pathM-panel.startM <= AstrodomeScienceCompoundRootSideGuardM) ||
			(!panel.endNumericalBoundary && panel.endM-pathM <= AstrodomeScienceCompoundRootSideGuardM) {
			t.Fatalf("adaptive short rule sampled inside the physical-root envelope at %.15g", pathM)
		}
		sampled++
		value := 0.0
		// Keep an unresolved variation inside every proposed numerical child so
		// the test exercises the rule-specific floor rather than depending on an
		// accidental alignment of one global discontinuity with quadrature nodes.
		if pathM > panel.startM+0.47*(panel.endM-panel.startM) {
			value = 1
		}
		return astrodomeScienceEvaluation{values: astrodomeScienceVector{value, value, value, value, value}}, nil
	}
	_, err := integrateAstrodomeSciencePath(
		context.Background(), evaluate, []astrodomeScienceAtomicInterval{interval}, calibration, 1,
	)
	if !errors.Is(err, ErrAstrodomeScienceNonConvergence) {
		t.Fatalf("production-gap nonconvergence error = %v", err)
	}
	// The unregistered discontinuity must trigger guarded refinement, but it may
	// never force a child through the constrained-rule endpoint-envelope floor.
	if sampled <= 9 || minimumPanelLengthM <= AstrodomeScienceMinimumPartitionLengthM {
		t.Fatalf("failed production gap used %d samples with minimum panel %.15g m; want refinement strictly above %.15g m",
			sampled, minimumPanelLengthM, AstrodomeScienceMinimumPartitionLengthM)
	}
}

func TestAstrodomeScienceProductionShortIntervalConvergesAfterGuardedSubdivision(t *testing.T) {
	t.Parallel()

	for _, productionIntervalM := range []float64{
		3 * AstrodomeScienceGL5MinimumEventIntervalLengthM,
		4 * AstrodomeScienceGL5MinimumEventIntervalLengthM,
	} {
		productionIntervalM := productionIntervalM
		t.Run(fmt.Sprintf("length-%.10f", productionIntervalM), func(t *testing.T) {
			if productionIntervalM <= 2*AstrodomeScienceMinimumEventIntervalLengthM ||
				productionIntervalM >= AstrodomeScienceGK15MinimumEventIntervalLengthM {
				t.Fatalf("production interval %.12g m is outside the adaptive short-rule range", productionIntervalM)
			}
			interval := astrodomeScienceAtomicInterval{
				startM: 200_000,
				endM:   200_000 + productionIntervalM,
				cellID: "cell-lat0386-lon0973",
			}
			block := astrodomeScienceCloudBlockKey{cellID: interval.cellID, tier: AstrodomeScienceCloudMiddle}
			var sampled int
			minimumPanelLengthM := math.Inf(1)
			evaluate := func(_ context.Context, pathM float64, panel astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
				minimumPanelLengthM = math.Min(minimumPanelLengthM, panel.endM-panel.startM)
				if (!panel.startNumericalBoundary && pathM-panel.startM <= AstrodomeScienceCompoundRootSideGuardM) ||
					(!panel.endNumericalBoundary && panel.endM-pathM <= AstrodomeScienceCompoundRootSideGuardM) {
					t.Fatalf("adaptive short rule sampled inside the physical-root envelope at %.15g", pathM)
				}
				sampled++
				normalized := 2*(pathM-interval.startM)/(interval.endM-interval.startM) - 1
				value := math.Exp(3 * normalized)
				return astrodomeScienceEvaluation{
					values:   astrodomeScienceVector{value, 1, 1, 1, 1},
					blockKey: block, regime: "hmnsp99-troposphere",
				}, nil
			}
			calibration := DefaultAstrodomeScienceCalibration()
			pass, err := integrateAstrodomeSciencePath(
				context.Background(), evaluate, []astrodomeScienceAtomicInterval{interval}, calibration, 1,
			)
			if err != nil {
				t.Fatal(err)
			}
			want := productionIntervalM * math.Sinh(3) / 3
			for component, got := range pass.values {
				componentWant := productionIntervalM
				if component == 0 {
					componentWant = want
				}
				if math.Abs(got-componentWant) > 5e-8*componentWant {
					t.Fatalf("adaptive production component %d = %.15g; want %.15g", component, got, componentWant)
				}
			}
			if sampled != 27 || pass.subdivisions != 3 {
				t.Fatalf("adaptive production interval used %d samples and %d panels; want 27 and 3", sampled, pass.subdivisions)
			}
			if minimumPanelLengthM <= AstrodomeScienceMinimumEventIntervalLengthM ||
				minimumPanelLengthM >= productionIntervalM {
				t.Fatalf("minimum adaptive panel length = %.15g m; want strictly between %.15g m and %.15g m",
					minimumPanelLengthM, AstrodomeScienceMinimumEventIntervalLengthM, productionIntervalM)
			}
		})
	}
}

func TestAstrodomeScienceAdaptiveMidpointIsNotACompoundRootEnvelope(t *testing.T) {
	t.Parallel()

	intervalLengthM := 0.5 * (AstrodomeScienceGL5MinimumEventIntervalLengthM +
		AstrodomeScienceGK15MinimumEventIntervalLengthM)
	interval := astrodomeScienceAtomicInterval{startM: 0, endM: intervalLengthM, cellID: "adaptive-numerical-boundary"}
	block := astrodomeScienceCloudBlockKey{cellID: interval.cellID, tier: AstrodomeScienceCloudLow}
	sawSampleAtNumericalBoundary := false
	evaluate := func(_ context.Context, pathM float64, panel astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
		if (!panel.startNumericalBoundary && pathM-panel.startM <= AstrodomeScienceCompoundRootSideGuardM) ||
			(!panel.endNumericalBoundary && panel.endM-pathM <= AstrodomeScienceCompoundRootSideGuardM) {
			t.Fatalf("adaptive rule sampled inside a physical compound-root guard at %.15g in %+v", pathM, panel)
		}
		if (panel.startNumericalBoundary && pathM-panel.startM <= AstrodomeScienceCompoundRootSideGuardM) ||
			(panel.endNumericalBoundary && panel.endM-pathM <= AstrodomeScienceCompoundRootSideGuardM) {
			sawSampleAtNumericalBoundary = true
		}
		x := 2*(pathM-interval.startM)/(interval.endM-interval.startM) - 1
		value := 1 + math.Pow(x, 6)
		return astrodomeScienceEvaluation{
			values:   astrodomeScienceVector{value, value, value, value, value},
			blockKey: block, regime: "hmnsp99-troposphere",
		}, nil
	}
	pass, err := integrateAstrodomeSciencePath(
		context.Background(), evaluate, []astrodomeScienceAtomicInterval{interval},
		DefaultAstrodomeScienceCalibration(), 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if pass.subdivisions <= 1 || !sawSampleAtNumericalBoundary {
		t.Fatalf("adaptive midpoint remained a fictitious root guard: subdivisions=%d sampled=%v",
			pass.subdivisions, sawSampleAtNumericalBoundary)
	}
}

func TestAstrodomeScienceOneSidedSubGL5IntervalFailsClosed(t *testing.T) {
	t.Parallel()

	lengthM := 1.5 * AstrodomeScienceCompoundRootSideGuardM
	interval := astrodomeScienceAtomicInterval{
		startM: 10, endM: 10 + lengthM, cellID: "one-sided-short",
		endNumericalBoundary: true,
	}
	block := astrodomeScienceCloudBlockKey{cellID: interval.cellID, tier: AstrodomeScienceCloudLow}
	sampled := false
	evaluate := func(_ context.Context, pathM float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
		if pathM-interval.startM <= AstrodomeScienceCompoundRootSideGuardM {
			t.Fatalf("one-sided constrained rule sampled inside physical guard at %.15g", pathM)
		}
		sampled = true
		return astrodomeScienceEvaluation{
			values: astrodomeScienceVector{1, 1, 1, 1, 1}, blockKey: block, regime: "hmnsp99-troposphere",
		}, nil
	}
	if _, err := astrodomeScienceGuardConstrained5_3(context.Background(), evaluate, interval); !errors.Is(err, ErrAstrodomeScienceNonConvergence) || sampled {
		t.Fatalf("one-sided sub-GL2 interval error=%v sampled=%v", err, sampled)
	}
}

func TestAstrodomeScienceGaussLegendre5_3PolynomialOrders(t *testing.T) {
	t.Parallel()

	interval := astrodomeScienceAtomicInterval{startM: -1, endM: 1, cellID: "polynomial-reference"}
	for degree := 0; degree <= 9; degree++ {
		degree := degree
		t.Run(fmt.Sprintf("degree-%d", degree), func(t *testing.T) {
			evaluate := func(_ context.Context, pathM float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
				value := math.Pow(pathM, float64(degree))
				return astrodomeScienceEvaluation{
					values: astrodomeScienceVector{value, value, value, value, value},
				}, nil
			}
			panel, err := astrodomeScienceGaussLegendre5_3(context.Background(), evaluate, interval)
			if err != nil {
				t.Fatal(err)
			}
			want := 0.0
			if degree%2 == 0 {
				want = 2 / float64(degree+1)
			}
			if math.Abs(panel.values[0]-want) > 2e-14 {
				t.Fatalf("G5 x^%d integral = %.17g; want %.17g", degree, panel.values[0], want)
			}
			if degree <= 5 && panel.errors[0] > 2e-14 {
				t.Fatalf("GL5/GL3 x^%d exact-order difference = %.17g", degree, panel.errors[0])
			}
		})
	}
}

func TestAstrodomeScienceGaussLegendre3_2PolynomialOrders(t *testing.T) {
	t.Parallel()

	interval := astrodomeScienceAtomicInterval{startM: -1, endM: 1, cellID: "polynomial-reference"}
	for degree := 0; degree <= 5; degree++ {
		degree := degree
		t.Run(fmt.Sprintf("degree-%d", degree), func(t *testing.T) {
			evaluate := func(_ context.Context, pathM float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
				value := math.Pow(pathM, float64(degree))
				return astrodomeScienceEvaluation{
					values: astrodomeScienceVector{value, value, value, value, value},
				}, nil
			}
			panel, err := astrodomeScienceGaussLegendre3_2(context.Background(), evaluate, interval)
			if err != nil {
				t.Fatal(err)
			}
			want := 0.0
			if degree%2 == 0 {
				want = 2 / float64(degree+1)
			}
			if math.Abs(panel.values[0]-want) > 2e-14 {
				t.Fatalf("GL3 x^%d integral = %.17g; want %.17g", degree, panel.values[0], want)
			}
			if degree <= 3 && panel.errors[0] > 2e-14 {
				t.Fatalf("GL3/GL2 x^%d exact-order difference = %.17g", degree, panel.errors[0])
			}
		})
	}
}

func TestAstrodomeScienceGaussLegendre2_1PolynomialOrders(t *testing.T) {
	t.Parallel()

	interval := astrodomeScienceAtomicInterval{startM: -1, endM: 1, cellID: "polynomial-reference"}
	for degree := 0; degree <= 3; degree++ {
		degree := degree
		t.Run(fmt.Sprintf("degree-%d", degree), func(t *testing.T) {
			evaluate := func(_ context.Context, pathM float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
				value := math.Pow(pathM, float64(degree))
				return astrodomeScienceEvaluation{
					values: astrodomeScienceVector{value, value, value, value, value},
				}, nil
			}
			panel, err := astrodomeScienceGaussLegendre2_1(context.Background(), evaluate, interval)
			if err != nil {
				t.Fatal(err)
			}
			want := 0.0
			if degree%2 == 0 {
				want = 2 / float64(degree+1)
			}
			if math.Abs(panel.values[0]-want) > 2e-14 {
				t.Fatalf("GL2 x^%d integral = %.17g; want %.17g", degree, panel.values[0], want)
			}
			if degree <= 1 && panel.errors[0] > 2e-14 {
				t.Fatalf("GL2/GL1 x^%d exact-order difference = %.17g", degree, panel.errors[0])
			}
		})
	}
}

func TestAstrodomeSciencePositiveShortRulesClearPhysicalEndpointGuard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		lengthM   float64
		integrate func(context.Context, astrodomeScienceEvaluationFunction, astrodomeScienceAtomicInterval) (astrodomeSciencePanel, error)
	}{
		{name: "GL3-GL2", lengthM: math.Nextafter(AstrodomeScienceGL3MinimumEventIntervalLengthM, math.Inf(1)), integrate: astrodomeScienceGaussLegendre3_2},
		{name: "GL2-GL1", lengthM: math.Nextafter(AstrodomeScienceGL2MinimumEventIntervalLengthM, math.Inf(1)), integrate: astrodomeScienceGaussLegendre2_1},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			interval := astrodomeScienceAtomicInterval{startM: 0, endM: test.lengthM, cellID: test.name}
			evaluate := func(_ context.Context, pathM float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
				if pathM-interval.startM <= AstrodomeScienceCompoundRootSideGuardM ||
					interval.endM-pathM <= AstrodomeScienceCompoundRootSideGuardM {
					t.Fatalf("%s sampled %.17g inside physical endpoint guard", test.name, pathM)
				}
				return astrodomeScienceEvaluation{values: astrodomeScienceVector{1, 1, 1, 1, 1}}, nil
			}
			panel, err := test.integrate(context.Background(), evaluate, interval)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(panel.values[0]-test.lengthM) > 4*astrodomeScienceFloatUnitRoundoff*test.lengthM {
				t.Fatalf("%s constant integral = %.17g; want %.17g", test.name, panel.values[0], test.lengthM)
			}
		})
	}
}

func TestAstrodomeSciencePositiveShortRuleWeightsAreNonNegativeAndNormalized(t *testing.T) {
	t.Parallel()

	weights := []float64{
		astrodomeScienceGaussLegendre3PairWeight,
		astrodomeScienceGaussLegendre3CentreWeight,
		astrodomeScienceGaussLegendre2PairWeight,
		astrodomeScienceGaussLegendre1CentreWeight,
	}
	for index, weight := range weights {
		if !finite(weight) || weight <= 0 {
			t.Fatalf("short-rule weight %d = %.17g", index, weight)
		}
	}
	if math.Abs(2*astrodomeScienceGaussLegendre3PairWeight+astrodomeScienceGaussLegendre3CentreWeight-2) > 2e-15 ||
		math.Abs(2*astrodomeScienceGaussLegendre2PairWeight-2) > 2e-15 ||
		math.Abs(astrodomeScienceGaussLegendre1CentreWeight-2) > 2e-15 {
		t.Fatal("positive short-rule weights do not integrate a constant over [-1,1]")
	}
}

func TestAstrodomeScienceRemovedConstrainedRuleAlwaysFailsClosed(t *testing.T) {
	t.Parallel()

	interval := astrodomeScienceAtomicInterval{startM: -1, endM: 1, cellID: "polynomial-reference"}
	sampled := false
	evaluate := func(_ context.Context, _ float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
		sampled = true
		return astrodomeScienceEvaluation{}, nil
	}
	_, err := astrodomeScienceGuardConstrained5_3(context.Background(), evaluate, interval)
	if !errors.Is(err, ErrAstrodomeScienceNonConvergence) || sampled {
		t.Fatalf("removed constrained rule error=%v sampled=%v", err, sampled)
	}
}

func TestAstrodomeScienceRemovedConstrainedRuleFailsClosedAtLargeOrigin(t *testing.T) {
	t.Parallel()

	lengthM := 1.01 * AstrodomeScienceMinimumEventIntervalLengthM
	interval := astrodomeScienceAtomicInterval{
		startM: 200_000,
		endM:   200_000 + lengthM,
		cellID: "represented-large-origin",
	}
	_, err := astrodomeScienceGuardConstrained5_3(
		context.Background(),
		func(_ context.Context, _ float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
			t.Fatal("removed constrained rule evaluated a node")
			return astrodomeScienceEvaluation{}, nil
		},
		interval,
	)
	if !errors.Is(err, ErrAstrodomeScienceNonConvergence) {
		t.Fatalf("large-origin removed-rule error = %v", err)
	}
}

func TestAstrodomeScienceRemovedConstrainedRuleSamplesNothing(t *testing.T) {
	t.Parallel()

	lengthM := 1.01 * AstrodomeScienceMinimumEventIntervalLengthM
	interval := astrodomeScienceAtomicInterval{startM: 200_000, endM: 200_000 + lengthM, cellID: "short-constrained"}
	block := astrodomeScienceCloudBlockKey{cellID: interval.cellID, tier: AstrodomeScienceCloudLow}
	var sampled int
	evaluate := func(_ context.Context, pathM float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
		if pathM-interval.startM <= AstrodomeScienceCompoundRootSideGuardM ||
			interval.endM-pathM <= AstrodomeScienceCompoundRootSideGuardM {
			t.Fatalf("removed short-panel rule sampled inside the endpoint root guard at %.15g", pathM)
		}
		sampled++
		return astrodomeScienceEvaluation{blockKey: block, regime: "hmnsp99-troposphere"}, nil
	}
	if _, err := astrodomeScienceGuardConstrained5_3(context.Background(), evaluate, interval); !errors.Is(err, ErrAstrodomeScienceNonConvergence) || sampled != 0 {
		t.Fatalf("removed constrained rule error=%v samples=%d", err, sampled)
	}
}

func TestAstrodomeScienceFinePassSplitsEveryOriginalPhysicalPanelExactlyOnce(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		length float64
	}{
		{name: "GL5-GL3", length: 0.5 * (AstrodomeScienceGL5MinimumEventIntervalLengthM + AstrodomeScienceGK15MinimumEventIntervalLengthM)},
		{name: "G7-K15", length: 3 * AstrodomeScienceGK15MinimumEventIntervalLengthM},
	} {
		t.Run(test.name, func(t *testing.T) {
			interval := astrodomeScienceAtomicInterval{startM: 1000, endM: 1000 + test.length, cellID: test.name}
			rootPanelSeen := false
			grandchildSeen := false
			evaluate := func(_ context.Context, _ float64, panel astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
				if panel.startM == interval.startM && panel.endM == interval.endM {
					rootPanelSeen = true
				}
				if panel.startM != interval.startM && panel.endM != interval.endM {
					grandchildSeen = true
				}
				return astrodomeScienceEvaluation{values: astrodomeScienceVector{1, 1, 1, 1, 1}}, nil
			}
			pass, err := integrateAstrodomeSciencePath(
				context.Background(), evaluate, []astrodomeScienceAtomicInterval{interval},
				DefaultAstrodomeScienceCalibration(), 0.5,
			)
			if err != nil {
				t.Fatal(err)
			}
			if rootPanelSeen || grandchildSeen || pass.subdivisions != 2 {
				t.Fatalf("fine independent split root=%v grandchild=%v panels=%d; want exactly two children",
					rootPanelSeen, grandchildSeen, pass.subdivisions)
			}
		})
	}
}

func TestAstrodomeScienceQuadratureFailsClosedOnUnpartitionedWMOTransition(t *testing.T) {
	t.Parallel()

	calibration := DefaultAstrodomeScienceCalibration()
	interval := astrodomeScienceAtomicInterval{startM: 0, endM: 100, cellID: "wmo-transition"}
	block := astrodomeScienceCloudBlockKey{cellID: interval.cellID, tier: AstrodomeScienceCloudHigh}
	evaluate := func(_ context.Context, pathM float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
		value, regime := 1.0, "hmnsp99-troposphere"
		if pathM >= 40 {
			value, regime = 3, "hmnsp99-stratosphere"
		}
		return astrodomeScienceEvaluation{
			values:   astrodomeScienceVector{value, value, value, value, value},
			blockKey: block,
			regime:   regime,
		}, nil
	}
	_, err := integrateAstrodomeSciencePath(
		context.Background(), evaluate, []astrodomeScienceAtomicInterval{interval}, calibration, 1,
	)
	if !errors.Is(err, ErrAstrodomeScienceIncompletePartition) ||
		!strings.Contains(err.Error(), "provider must isolate every raw WMO/cloud predicate") {
		t.Fatalf("unpartitioned WMO transition error = %v", err)
	}
}

func TestAstrodomeSciencePathRequiresRawWMOPredicateCertificate(t *testing.T) {
	t.Parallel()

	path := AstrodomeSciencePath{
		NativeContext: &astrodomeScienceTestNativeResolver{},
		Cells: []AstrodomeSciencePathCell{{
			StartPathM: 0, EndPathM: 100, HorizontalCellID: "uncertified-wmo",
			TerrainState: AstrodomeScienceTerrainClear,
		}},
		Availability: AstrodomeSciencePathAvailability{
			Geometry: true, Turbulence: true, Cloud: true, TemporalBrackets: true, Terrain: true,
		},
		TopClosed: true, GeometryCoverage: 1, TurbulencePathCoverage: 1,
		CloudPathCoverage: 1, TemporalResolutionHours: 1,
	}
	_, err := prepareAstrodomeSciencePath(path)
	if !errors.Is(err, ErrAstrodomeScienceIncompletePartition) ||
		!strings.Contains(err.Error(), "raw WMO tropopause predicate") {
		t.Fatalf("uncertified WMO path error = %v", err)
	}
	path.Cells[0].TropopausePredicatesIsolated = true
	_, err = prepareAstrodomeSciencePath(path)
	if !errors.Is(err, ErrAstrodomeScienceIncompletePartition) ||
		!strings.Contains(err.Error(), "native full-level cloud support predicate") {
		t.Fatalf("uncertified cloud-support path error = %v", err)
	}
}

func TestAstrodomeScienceTightenedQuadratureConvergesIndependently(t *testing.T) {
	t.Parallel()

	calibration := DefaultAstrodomeScienceCalibration()
	calibration.AbsoluteTolerance = AstrodomeScienceIntegralTolerances{
		IntegratedCn2: 1e-7, WindWeightedCn2: 1e-7, SlantWaterKgM2: 1e-7,
		LiquidOpticalDepth: 1e-7, IceOpticalDepth: 1e-7,
	}
	calibration.RelativeTolerance = 1e-6
	interval := astrodomeScienceAtomicInterval{startM: 0, endM: 1000, cellID: "oscillatory"}
	evaluate := func(_ context.Context, pathM float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
		value := math.Exp(pathM/1000) * math.Pow(math.Sin(0.037*pathM), 2)
		return astrodomeScienceEvaluation{values: astrodomeScienceVector{value, value, value, value, value}}, nil
	}
	coarse, err := integrateAstrodomeSciencePath(context.Background(), evaluate, []astrodomeScienceAtomicInterval{interval}, calibration, 1)
	if err != nil {
		t.Fatal(err)
	}
	fine, err := integrateAstrodomeSciencePath(context.Background(), evaluate, []astrodomeScienceAtomicInterval{interval}, calibration, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if fine.subdivisions < coarse.subdivisions {
		t.Fatalf("half-tolerance repeat used fewer panels: coarse=%d fine=%d", coarse.subdivisions, fine.subdivisions)
	}
	allowed := calibration.AbsoluteTolerance.IntegratedCn2 + calibration.RelativeTolerance*math.Abs(fine.values[0])
	if math.Abs(fine.values[0]-coarse.values[0]) > allowed {
		t.Fatalf("independent repeats differ by %g > %g", math.Abs(fine.values[0]-coarse.values[0]), allowed)
	}
}

func TestAstrodomeScienceClearAndOpaqueCloudClosures(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	clearVolume := newAstrodomeTestVolume([]time.Time{validAt})
	setAstrodomeScienceCloud(clearVolume, 0, 0, 0)
	clearReconstructor, clearRay, clearPath, site, calibration := astrodomeScienceFixture(t, clearVolume, validAt, 90)
	clearNode, err := ComputeAstrodomeScienceNode(context.Background(), clearReconstructor, clearRay, validAt, clearPath, site, calibration)
	if err != nil {
		t.Fatal(err)
	}

	opaqueVolume := newAstrodomeTestVolume([]time.Time{validAt})
	setAstrodomeScienceCloud(opaqueVolume, 0.002, 0.001, 1)
	opaqueReconstructor, opaqueRay, opaquePath, opaqueSite, opaqueCalibration := astrodomeScienceFixture(t, opaqueVolume, validAt, 90)
	opaqueNode, err := ComputeAstrodomeScienceNode(context.Background(), opaqueReconstructor, opaqueRay, validAt, opaquePath, opaqueSite, opaqueCalibration)
	if err != nil {
		t.Fatal(err)
	}
	if clearNode.CloudTransmission == nil || *clearNode.CloudTransmission != 1 {
		t.Fatalf("clear transmission = %v", clearNode.CloudTransmission)
	}
	if opaqueNode.CloudTransmission == nil || *opaqueNode.CloudTransmission > 1e-6 {
		t.Fatalf("opaque transmission = %v", opaqueNode.CloudTransmission)
	}
	if clearNode.Overall == nil || opaqueNode.Overall == nil || !(*opaqueNode.Overall < *clearNode.Overall) {
		t.Fatalf("cloud closure did not reduce Overall: clear=%v opaque=%v", clearNode.Overall, opaqueNode.Overall)
	}
	if len(opaqueNode.CloudBlocks) == 0 || opaqueNode.CloudBlocks[0].LiquidOpticalDepthConservative <= 0 || opaqueNode.CloudBlocks[0].IceOpticalDepthConservative <= 0 {
		t.Fatalf("phase optical depths were not retained: %+v", opaqueNode.CloudBlocks)
	}
}

func TestAstrodomeScienceEmbeddedCloudBoundsPropagatePerBlockPanelError(t *testing.T) {
	t.Parallel()

	calibration := DefaultAstrodomeScienceCalibration()
	key := astrodomeScienceCloudBlockKey{cellID: "embedded-error", tier: AstrodomeScienceCloudLow}
	blocks := map[astrodomeScienceCloudBlockKey]astrodomeScienceCloudAccumulator{
		key: {
			liquid:                           astrodomeScienceCompensatedSum{sum: 2},
			liquidError:                      astrodomeScienceCompensatedSum{sum: 0.2},
			maximumCloudFractionNominal:      1,
			maximumCloudFractionConservative: 1,
		},
	}
	closure := astrodomeScienceCloudClosure(blocks, calibration)
	nominal := closure.nominalTransmission
	minimum, maximum, err := astrodomeScienceEmbeddedCloudTransmissionBounds(blocks, calibration)
	if err != nil {
		t.Fatal(err)
	}
	if !(minimum < nominal && nominal < maximum) {
		t.Fatalf("embedded cloud bounds do not enclose nominal: minimum=%g nominal=%g maximum=%g", minimum, nominal, maximum)
	}
	assertAstrodomeScienceClose(t, "minimum embedded cloud transmission", minimum, math.Exp(-2.2), 2e-14)
	assertAstrodomeScienceClose(t, "maximum embedded cloud transmission", maximum, math.Exp(-1.8), 2e-14)
}

func TestAstrodomeScienceCloudBlockTransmissionIsMonotone(t *testing.T) {
	t.Parallel()

	for _, opticalDepth := range []float64{0, 5e-10, 1e-9, 1e-8, 1e-5, 0.1, 1, 10} {
		previous := 1.0
		for _, cover := range []float64{0, 1e-9, 9e-7, 1e-6, 1.1e-6, 1e-4, 0.009, 0.01, 0.1, 0.5, 1} {
			transmission := astrodomeScienceCloudBlockTransmission(cover, opticalDepth)
			if transmission > previous+2e-15 {
				t.Fatalf("transmission improved as cover increased at tau=%g: cover=%g T=%g after %g",
					opticalDepth, cover, transmission, previous)
			}
			previous = transmission
		}
	}
	for _, cover := range []float64{0, 1e-9, 9e-7, 1e-6, 0.001, 0.01, 0.1, 0.5, 1} {
		previous := 1.0
		for _, opticalDepth := range []float64{0, 5e-10, 1e-9, 1.1e-9, 1e-8, 1e-5, 0.1, 1, 10} {
			transmission := astrodomeScienceCloudBlockTransmission(cover, opticalDepth)
			if transmission > previous+2e-15 {
				t.Fatalf("transmission improved as optical depth increased at cover=%g: tau=%g T=%g after %g",
					cover, opticalDepth, transmission, previous)
			}
			previous = transmission
		}
	}
}

func TestAstrodomeScienceCloudOpticalDepthErrorOnlyLowersConservativeOverall(t *testing.T) {
	t.Parallel()

	calibration := DefaultAstrodomeScienceCalibration()
	key := astrodomeScienceCloudBlockKey{cellID: "tau-error", tier: AstrodomeScienceCloudLow}
	base := astrodomeSciencePass{blocks: map[astrodomeScienceCloudBlockKey]astrodomeScienceCloudAccumulator{
		key: {
			liquid:                           astrodomeScienceCompensatedSum{sum: 0.4},
			maximumCloudFractionNominal:      0.5,
			maximumCloudFractionConservative: 0.5,
		},
	}}
	withoutError, err := deriveAstrodomeSciencePass(base, AstrodomeScienceSiteInputs{}, calibration)
	if err != nil {
		t.Fatal(err)
	}
	withErrorInput := base
	withErrorInput.blocks = map[astrodomeScienceCloudBlockKey]astrodomeScienceCloudAccumulator{
		key: {
			liquid:                           astrodomeScienceCompensatedSum{sum: 0.4},
			liquidError:                      astrodomeScienceCompensatedSum{sum: 0.2},
			maximumCloudFractionNominal:      0.5,
			maximumCloudFractionConservative: 0.5,
		},
	}
	withError, err := deriveAstrodomeSciencePass(withErrorInput, AstrodomeScienceSiteInputs{}, calibration)
	if err != nil {
		t.Fatal(err)
	}
	if withError.cloudTransmissionNominal != withoutError.cloudTransmissionNominal ||
		withError.cloudTransmissionConservative >= withoutError.cloudTransmissionConservative ||
		withError.overall >= withoutError.overall {
		t.Fatalf("tau error did not propagate only into the conservative result:\nwithout=%+v\nwith=%+v",
			withoutError, withError)
	}
}

func TestAstrodomeScienceCloudClosureUsesCertifiedActiveNativeSupportEnvelope(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	setAstrodomeScienceCloud(volume, 0, 0, 0.2)
	spikeSupport := volume.stencil.Supports[0]
	spikeColumn := volume.columns[spikeSupport.ColumnID]
	spikeColumn.Frames[0].FullLevels[0].CloudFraction = 0.95
	volume.columns[spikeColumn.ColumnID] = spikeColumn
	reconstructor, ray, path, site, calibration := astrodomeScienceFixture(t, volume, validAt, 90)

	bottomState, err := reconstructor.Reconstruct(context.Background(), AstrodomeReconstructionQuery{
		ValidAt: validAt, Location: Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"}, HeightM: 1100,
	})
	if err != nil {
		t.Fatal(err)
	}
	bottomEnvelope, err := reconstructor.certifiedCloudFractionUpperEnvelope(
		context.Background(), validAt, bottomState.HorizontalStencil, bottomState.cloudFractionSupport,
	)
	if err != nil {
		t.Fatal(err)
	}
	if bottomEnvelope.upperBound < 0.2 || bottomEnvelope.upperBound > 0.200000000001 {
		t.Fatalf("constant-bottom envelope = %.17g; unrelated upper-level spike leaked into it", bottomEnvelope.upperBound)
	}

	pairState, err := reconstructor.Reconstruct(context.Background(), AstrodomeReconstructionQuery{
		ValidAt: validAt, Location: Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"}, HeightM: 2000,
	})
	if err != nil {
		t.Fatal(err)
	}
	pairEnvelope, err := reconstructor.certifiedCloudFractionUpperEnvelope(
		context.Background(), validAt, pairState.HorizontalStencil, pairState.cloudFractionSupport,
	)
	if err != nil {
		t.Fatal(err)
	}
	if pairEnvelope.upperBound < 0.95 || pairEnvelope.upperBound > 0.950000000001 {
		t.Fatalf("active-pair envelope = %.17g; want outward bound of raw 0.95 support", pairEnvelope.upperBound)
	}

	node, err := ComputeAstrodomeScienceNode(context.Background(), reconstructor, ray, validAt, path, site, calibration)
	if err != nil {
		t.Fatal(err)
	}
	if len(node.CloudBlocks) != 1 || node.CloudBlocks[0].CloudFractionConservative < 0.95 {
		t.Fatalf("certified raw-support cloud envelope did not reach cloud closure: %+v", node.CloudBlocks)
	}
}

func TestAstrodomeScienceQuadratureFailsClosedWhenCloudVerticalSupportChanges(t *testing.T) {
	t.Parallel()

	calibration := DefaultAstrodomeScienceCalibration()
	interval := astrodomeScienceAtomicInterval{startM: 0, endM: 100, cellID: "cloud-support-transition"}
	block := astrodomeScienceCloudBlockKey{cellID: interval.cellID, tier: AstrodomeScienceCloudLow}
	evaluate := func(_ context.Context, pathM float64, _ astrodomeScienceAtomicInterval) (astrodomeScienceEvaluation, error) {
		support := astrodomeCloudFractionVerticalSupport{
			lowerLevelIndex: 2, upperLevelIndex: 1, mode: astrodomeCloudFractionSupportPair,
		}
		if pathM >= 40 {
			support = astrodomeCloudFractionVerticalSupport{
				lowerLevelIndex: 1, upperLevelIndex: 0, mode: astrodomeCloudFractionSupportPair,
			}
		}
		return astrodomeScienceEvaluation{
			values: astrodomeScienceVector{1, 1, 1, 1, 1}, blockKey: block,
			cloudVerticalSupport: support, regime: "hmnsp99-troposphere",
		}, nil
	}
	_, err := integrateAstrodomeSciencePath(
		context.Background(), evaluate, []astrodomeScienceAtomicInterval{interval}, calibration, 1,
	)
	if !errors.Is(err, ErrAstrodomeScienceIncompletePartition) || !strings.Contains(err.Error(), "cloud-support") {
		t.Fatalf("unpartitioned cloud vertical support error = %v", err)
	}
}

func TestAstrodomeSciencePrecipitationIsAnOperationalVeto(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	setAstrodomeScienceCloud(volume, 0, 0, 0)
	reconstructor, ray, path, site, calibration := astrodomeScienceFixture(t, volume, validAt, 90)
	site.PrecipitationRateMMPerHour = calibration.Overall.PrecipitationDetectMM
	node, err := ComputeAstrodomeScienceNode(context.Background(), reconstructor, ray, validAt, path, site, calibration)
	if err != nil {
		t.Fatal(err)
	}
	if node.Overall == nil || *node.Overall != 1 || node.Factors == nil || node.Factors.Precipitation != 0 {
		t.Fatalf("precipitation veto result: Overall=%v factors=%+v", node.Overall, node.Factors)
	}
	if node.IntegratedCn2 == nil || node.CloudTransmission == nil {
		t.Fatal("precipitation veto erased physical diagnostics")
	}
}

func TestAstrodomeScienceUsesTransverseECEFWindAndCalmLimit(t *testing.T) {
	t.Parallel()

	observer := Location{Latitude: 0, Longitude: 0, TimeZone: "UTC"}
	ray, err := NewAstrodomeRay(observer, 1000, 90, nil)
	if err != nil {
		t.Fatal(err)
	}
	calibration := DefaultAstrodomeScienceCalibration()
	native := AstrodomeScienceNativeContext{HorizontalCellID: "direct", SurfaceHeightM: 0, MixedLayerDepthM: 2000}
	state := AstrodomeReconstructedAtmosphere{
		Location: observer, HeightM: 1200, PressurePa: 90000, TemperatureK: 280,
		SpecificHumidityKgKg: 0.004, CloudLiquidKgKg: 0, CloudIceKgKg: 0,
		CloudFraction: 0, TKEJkg: 0.5,
		VerticalDerivatives: AstrodomeReconstructedVerticalDerivatives{
			PressurePaPerM: -10, TemperatureKPerM: -0.006,
		},
	}
	// At (lat,lon)=(0,0), local Up is ECEF +X and East is +Y.
	state.WindECEF = AstrodomeECEFVector{X: 12}
	parallel, _, err := astrodomeScienceIntegrand(state, ray.DirectionECEF, native, math.NaN(), calibration)
	if err != nil {
		t.Fatal(err)
	}
	if parallel[astrodomeScienceCn2Index] <= 0 || parallel[astrodomeScienceWindCn2Index] != 0 {
		t.Fatalf("ray-parallel wind moments J=%g JV=%g", parallel[0], parallel[1])
	}
	state.WindECEF = AstrodomeECEFVector{Y: 12}
	transverse, _, err := astrodomeScienceIntegrand(state, ray.DirectionECEF, native, math.NaN(), calibration)
	if err != nil {
		t.Fatal(err)
	}
	want := transverse[astrodomeScienceCn2Index] * math.Pow(12, 5.0/3.0)
	assertAstrodomeScienceClose(t, "pointwise transverse wind moment", transverse[astrodomeScienceWindCn2Index], want, 2e-14)
}

func TestAstrodomeScienceTropopauseReturnsGeometric200HPAFallback(t *testing.T) {
	t.Parallel()

	profile := []AstrodomeScienceThermalPrimitive{
		{HeightM: 10_000, PressurePa: 30_000, TemperatureK: 300},
		{HeightM: 14_000, PressurePa: 10_000, TemperatureK: 260},
	}
	heightM, method, err := astrodomeScienceTropopause(profile)
	if err != nil {
		t.Fatal(err)
	}
	wantFraction := (math.Log(20_000.0) - math.Log(30_000.0)) / (math.Log(10_000.0) - math.Log(30_000.0))
	wantHeightM := 10_000 + wantFraction*4_000
	if math.Abs(heightM-wantHeightM) > 1e-9 || !strings.Contains(method, "200-hPa") {
		t.Fatalf("200-hPa fallback = %.12g m, %q; want %.12g m", heightM, method, wantHeightM)
	}
	boundary, err := ResolveAstrodomeScienceBoundaryHeights(
		AstrodomeScienceNativeContext{SurfaceHeightM: 0, MixedLayerDepthM: 500, ThermalProfile: profile},
		DefaultAstrodomeScienceCalibration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if boundary.TropopauseBoundaryKind != AstrodomeScienceTropopauseBoundaryPressureFallback ||
		boundary.TropopauseLowerLevelIndex != 0 || boundary.TropopauseUpperLevelIndex != 1 {
		t.Fatalf("200-hPa fallback identity = %+v", boundary)
	}
}

func TestAstrodomeSciencePressureBoundaryUsesStableNarrowLogBracket(t *testing.T) {
	t.Parallel()

	profile := []AstrodomeScienceThermalPrimitive{
		{HeightM: 10_000, PressurePa: 20_000.0000001, TemperatureK: 220},
		{HeightM: 12_000, PressurePa: 19_999.9999999, TemperatureK: 218},
	}
	heightM, lowerLevel := astrodomeSciencePressureBoundary(profile, 20_000)
	if lowerLevel != 0 || !finite(heightM) {
		t.Fatalf("narrow 200-hPa bracket = %.12g m at %d", heightM, lowerLevel)
	}
	const oracleHeightM = 10_999.9999999975
	if math.Abs(heightM-oracleHeightM) > 1e-8 {
		t.Fatalf("stable narrow-bracket height = %.12g m; want %.12g m", heightM, oracleHeightM)
	}
}

func TestAstrodomeScienceTropopauseIdentifiesSelectedNativeLevel(t *testing.T) {
	t.Parallel()

	profile := []AstrodomeScienceThermalPrimitive{
		{HeightM: 5000, PressurePa: 55_000, TemperatureK: 270},
		{HeightM: 6000, PressurePa: 45_000, TemperatureK: 269},
		{HeightM: 7500, PressurePa: 35_000, TemperatureK: 268},
	}
	boundary, err := ResolveAstrodomeScienceBoundaryHeights(
		AstrodomeScienceNativeContext{SurfaceHeightM: 0, MixedLayerDepthM: 500, ThermalProfile: profile},
		DefaultAstrodomeScienceCalibration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if boundary.TropopauseBoundaryKind != AstrodomeScienceTropopauseBoundaryWMOLevel ||
		boundary.TropopauseLowerLevelIndex != 0 || boundary.TropopauseUpperLevelIndex != 0 ||
		boundary.TropopauseHeightM != profile[0].HeightM {
		t.Fatalf("WMO native-level identity = %+v", boundary)
	}
}

func TestThermalTropopauseRequiresEveryMeanLapseWithinTwoKilometres(t *testing.T) {
	t.Parallel()

	levels := []VerticalLevel{
		{HeightM: 5000, TemperatureK: 270},
		{HeightM: 5500, TemperatureK: 270},
		// The endpoint at +2 km has an acceptable 1.5 K/km mean, but this
		// intervening level has a 3 K/km mean and invalidates the candidate.
		{HeightM: 6000, TemperatureK: 267},
		{HeightM: 7000, TemperatureK: 267},
	}
	if index := thermalTropopauseLevelIndex(levels); index != -1 {
		t.Fatalf("WMO candidate with an intervening mean-lapse violation selected level %d", index)
	}
}

func TestThermalTropopauseRequiresCompleteTwoKilometreProfile(t *testing.T) {
	t.Parallel()

	levels := []VerticalLevel{
		{HeightM: 5000, TemperatureK: 270},
		{HeightM: 6000, TemperatureK: 269},
		{HeightM: 6500, TemperatureK: 269},
	}
	if index := thermalTropopauseLevelIndex(levels); index != -1 {
		t.Fatalf("WMO candidate with only 1.5 km above it selected level %d", index)
	}
}

func TestAstrodomeScienceRefractionModeUsesTracedTrajectoryContract(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	setAstrodomeScienceConstantAtmosphere(volume, 80000, 270, 0.005, 0, 0, 0, 8, -2, 0)
	reconstructor, straight, path, site, calibration := astrodomeScienceFixture(t, volume, validAt, 30)
	straightNode, err := ComputeAstrodomeScienceNode(context.Background(), reconstructor, straight, validAt, path, site, calibration)
	if err != nil {
		t.Fatal(err)
	}
	field := astrodomeAnalyticRefractionField{
		refractiveIndex: func(AstrodomeECEFVector) float64 { return 1.00027 },
		gradient:        func(AstrodomeECEFVector) AstrodomeECEFVector { return AstrodomeECEFVector{} },
		surfaceDistance: func(position AstrodomeECEFVector) float64 {
			return position.Norm() - (AstrodomeICONSphereRadiusM + 1000)
		},
		topDistance: func(position AstrodomeECEFVector) float64 {
			return position.Norm() - (AstrodomeICONSphereRadiusM + 2500)
		},
	}
	refracted, err := TraceAstrodomeRefractedRay(context.Background(), field, straight, DefaultAstrodomeRefractionCalibration())
	if err != nil {
		t.Fatal(err)
	}
	refractedPath := path
	refractedPath.GeometryMode = AstrodomeScienceGeometryRefractionFull
	refractedPath.Cells = append([]AstrodomeSciencePathCell(nil), path.Cells...)
	refractedPath.Cells[0].EndPathM = refracted.PathLengthM
	refractedNode, err := ComputeAstrodomeScienceNodeRefracted(
		context.Background(), reconstructor, refracted, validAt, refractedPath, site, calibration,
	)
	if err != nil {
		t.Fatalf("ComputeAstrodomeScienceNodeRefracted: %v", err)
	}
	if refractedNode.GeometryMode != AstrodomeScienceGeometryRefractionFull ||
		refractedNode.DirectionAtModelTopECEF == nil {
		t.Fatalf("refraction geometry identity is incomplete: %+v", refractedNode)
	}
	assertAstrodomeScienceClose(t, "constant-n refraction J", refractedNode.IntegratedCn2.Value, straightNode.IntegratedCn2.Value, 1e-10)
	assertAstrodomeScienceClose(t, "constant-n refraction JV", refractedNode.WindWeightedCn2.Value, straightNode.WindWeightedCn2.Value, 1e-10)
}

func TestAstrodomeScienceKeepsTerrainBlockedDistinctFromOverallOne(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	reconstructor, ray, path, site, calibration := astrodomeScienceFixture(t, volume, validAt, 30)
	path.Cells[0].TerrainState = AstrodomeScienceTerrainBlocked
	node, err := ComputeAstrodomeScienceNode(context.Background(), reconstructor, ray, validAt, path, site, calibration)
	if err != nil {
		t.Fatal(err)
	}
	if node.Available || node.State != AstrodomeScienceNodeTerrainBlocked || node.Overall != nil {
		t.Fatalf("terrain state collapsed into a score or generic failure: %+v", node)
	}
}

func TestAstrodomeScienceRequiresRunBoundMandatoryPanels(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	reconstructor, ray, path, site, calibration := astrodomeScienceFixture(t, volume, validAt, 90)
	path.Availability.Cloud = false
	if node, err := ComputeAstrodomeScienceNode(context.Background(), reconstructor, ray, validAt, path, site, calibration); err == nil || node.Available || node.Overall != nil {
		t.Fatalf("missing mandatory cloud panels were published: node=%+v err=%v", node, err)
	}
	path.Availability.Cloud = true
	path.SourceIdentity.RunID = "another-run"
	if node, err := ComputeAstrodomeScienceNode(context.Background(), reconstructor, ray, validAt, path, site, calibration); err == nil || node.Available {
		t.Fatalf("mixed-run path was accepted: node=%+v err=%v", node, err)
	}
}

func TestAstrodomeScienceRejectsUnsplitLocalPBLInsteadOfUsingCellProxy(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	reconstructor, ray, path, site, calibration := astrodomeScienceFixture(t, volume, validAt, 90)
	// The declared breakpoints are 0/2000m-HHL/top, while the exact local PBL
	// boundary is at absolute 1800m. A midpoint/cell proxy could hide this; the
	// joint evaluator must detect both physics regimes in one panel and reject.
	resolver := path.NativeContext.(*astrodomeScienceTestNativeResolver)
	resolver.resolve = func(_ AstrodomeRayPoint) AstrodomeScienceNativeContext {
		return AstrodomeScienceNativeContext{
			HorizontalCellID: "test-cell", SurfaceHeightM: 1000, MixedLayerDepthM: 800,
		}
	}
	node, err := ComputeAstrodomeScienceNode(context.Background(), reconstructor, ray, validAt, path, site, calibration)
	if err == nil || !errors.Is(err, ErrAstrodomeScienceIncompletePartition) || node.Available {
		t.Fatalf("unsplit exact local PBL was approximated: node=%+v err=%v", node, err)
	}
}

func TestAstrodomeScienceInterpolatesRawTKEThenRecomputesCn2(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	volume := newAstrodomeTestVolume([]time.Time{start, end})
	setAstrodomeScienceCloud(volume, 0, 0, 0)
	for _, support := range volume.stencil.Supports {
		column := volume.columns[support.ColumnID]
		for levelIndex := range column.Frames[0].HalfLevels {
			column.Frames[0].HalfLevels[levelIndex].TKEJkg = 0.1
			column.Frames[1].HalfLevels[levelIndex].TKEJkg = 0.9
		}
		volume.columns[column.ColumnID] = column
	}
	reconstructor, ray, path, site, calibration := astrodomeScienceFixture(t, volume, start, 90)
	calibration.RelativeTolerance = 1e-2
	path.NativeContext.(*astrodomeScienceTestNativeResolver).context.MixedLayerDepthM = 2000 // complete tested path uses the native-TKE branch

	computeJ := func(validAt time.Time) float64 {
		localSite := site
		localSite.ValidAt = validAt
		localSite.PrecipitationIntervalStart = validAt.Add(-time.Hour)
		localSite.PrecipitationIntervalEnd = validAt
		localPath := path
		localPath.ValidAt = validAt
		node, err := ComputeAstrodomeScienceNode(context.Background(), reconstructor, ray, validAt, localPath, localSite, calibration)
		if err != nil {
			t.Fatalf("ComputeAstrodomeScienceNode(%s): %v", validAt, err)
		}
		return node.IntegratedCn2.Value
	}
	left := computeJ(start)
	middle := computeJ(start.Add(time.Hour))
	right := computeJ(end)
	interpolatedDerived := (left + right) / 2
	if math.Abs(middle-interpolatedDerived) <= math.Max(1e-22, math.Abs(middle)*1e-4) {
		t.Fatalf("nonlinear ordering collapsed to interpolated derived Cn2: left=%g middle=%g right=%g", left, middle, right)
	}
	// For unchanged thermodynamics, J is proportional to TKE^(2/3). The
	// midpoint therefore follows F(I(TKE)) with I(TKE)=0.5, not I(F(TKE)).
	wantRatio := math.Pow(0.5/0.1, 2.0/3.0)
	assertAstrodomeScienceClose(t, "raw-TKE-first nonlinear ratio", middle/left, wantRatio, 2e-9)
}

func TestAstrodomeScienceRejectsNonConvergentBudgetInsteadOfPublishingLastEstimate(t *testing.T) {
	t.Parallel()

	validAt := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	volume := newAstrodomeTestVolume([]time.Time{validAt})
	reconstructor, ray, path, site, calibration := astrodomeScienceFixture(t, volume, validAt, 30)
	calibration.MaximumSubdivisions = 16
	calibration.MaximumDepth = 4
	calibration.RelativeTolerance = 1e-14
	calibration.AbsoluteTolerance = AstrodomeScienceIntegralTolerances{
		IntegratedCn2: 1e-30, WindWeightedCn2: 1e-30, SlantWaterKgM2: 1e-15,
		LiquidOpticalDepth: 1e-15, IceOpticalDepth: 1e-15,
	}
	node, err := ComputeAstrodomeScienceNode(context.Background(), reconstructor, ray, validAt, path, site, calibration)
	if err == nil || !errors.Is(err, ErrAstrodomeScienceNonConvergence) {
		t.Fatalf("expected typed non-convergence, got node=%+v err=%v", node, err)
	}
	if node.Available || node.Overall != nil {
		t.Fatalf("last unconverged estimate was published: %+v", node)
	}
}

func astrodomeScienceFixture(t testing.TB, volume *astrodomeTestVolume, validAt time.Time, elevationDegrees float64) (*AstrodomePrimitiveReconstructor, AstrodomeRay, AstrodomeSciencePath, AstrodomeScienceSiteInputs, AstrodomeScienceCalibration) {
	t.Helper()
	reconstructor, err := NewAstrodomePrimitiveReconstructor(volume)
	if err != nil {
		t.Fatal(err)
	}
	observer := Location{Latitude: 50.5, Longitude: 30.5, TimeZone: "UTC"}
	var azimuth *float64
	if elevationDegrees < 90 {
		value := 67.5
		azimuth = &value
	}
	ray, err := NewAstrodomeRay(observer, 1500, elevationDegrees, azimuth)
	if err != nil {
		t.Fatal(err)
	}
	top, err := ray.IntersectAltitude(2500)
	if err != nil {
		t.Fatal(err)
	}
	middle, err := ray.IntersectAltitude(2000)
	if err != nil {
		t.Fatal(err)
	}
	path := AstrodomeSciencePath{
		ContractVersion: AstrodomeSciencePathContractVersion,
		SourceIdentity:  volume.identity, ValidAt: validAt,
		GeometryMode: AstrodomeScienceGeometryStraight,
		NativeContext: &astrodomeScienceTestNativeResolver{context: AstrodomeScienceNativeContext{
			HorizontalCellID: "test-cell", SurfaceHeightM: 1000, MixedLayerDepthM: 500,
		}},
		Availability: AstrodomeSciencePathAvailability{
			Geometry: true, Turbulence: true, Cloud: true, Humidity: true,
			TemporalBrackets: true, Terrain: true,
		},
		Cells: []AstrodomeSciencePathCell{{
			StartPathM: 0, EndPathM: top.PathLengthM, HorizontalCellID: "test-cell",
			BreakpointsPathM: []float64{middle.PathLengthM}, NativeVerticalPredicatesIsolated: true,
			TropopausePredicatesIsolated: true,
			TerrainState:                 AstrodomeScienceTerrainClear,
		}},
		TopClosed: true, GeometryCoverage: 1, TurbulencePathCoverage: 1,
		CloudPathCoverage: 1, HumidityPathCoverage: astrodomeScienceFloatPointer(1), TemporalResolutionHours: 1,
	}
	site := AstrodomeScienceSiteInputs{
		SourceIdentity: volume.identity, ValidAt: validAt,
		WindSpeed10MMS: 2, WindGust10MMS: 3, FogHeuristic: AstrodomeScienceFogNone,
		PrecipitationRateMMPerHour: 0,
		PrecipitationIntervalStart: validAt.Add(-time.Hour), PrecipitationIntervalEnd: validAt,
		ForecastLeadHours: 12,
	}
	return reconstructor, ray, path, site, DefaultAstrodomeScienceCalibration()
}

func astrodomeScienceFloatPointer(value float64) *float64 {
	return &value
}

type astrodomeScienceTestNativeResolver struct {
	context AstrodomeScienceNativeContext
	resolve func(AstrodomeRayPoint) AstrodomeScienceNativeContext
}

func (resolver *astrodomeScienceTestNativeResolver) ResolveAstrodomeScienceNativeContext(
	_ context.Context,
	_ time.Time,
	point AstrodomeRayPoint,
	_ AstrodomeHorizontalStencil,
) (AstrodomeScienceNativeContext, error) {
	if resolver.resolve != nil {
		return resolver.resolve(point), nil
	}
	return resolver.context, nil
}

func setAstrodomeScienceConstantAtmosphere(volume *astrodomeTestVolume, pressurePa, temperatureK, qv, ql, qi, cloudFraction, east, north, up float64) {
	for _, support := range volume.stencil.Supports {
		column := volume.columns[support.ColumnID]
		for frameIndex := range column.Frames {
			for levelIndex := range column.Frames[frameIndex].FullLevels {
				level := &column.Frames[frameIndex].FullLevels[levelIndex]
				// A mathematically constant pressure profile violates the physical
				// dP/dz<0 contract. Use the smallest symmetric, representable
				// top-to-bottom increase; its logarithmic mean differs from pressurePa
				// far below the analytic test tolerance.
				pressureStep := math.Max(math.Abs(pressurePa)*1e-12, 1e-6)
				level.PressurePa = pressurePa + (2*float64(levelIndex)-1)*pressureStep
				level.TemperatureK = temperatureK
				level.SpecificHumidityKgKg = qv
				level.CloudLiquidKgKg = ql
				level.CloudIceKgKg = qi
				level.CloudFraction = cloudFraction
				level.EastwardWindMS = east
				level.NorthwardWindMS = north
			}
			for levelIndex := range column.Frames[frameIndex].HalfLevels {
				level := &column.Frames[frameIndex].HalfLevels[levelIndex]
				level.VerticalWindMS = up
				level.TKEJkg = 0
			}
		}
		volume.columns[column.ColumnID] = column
	}
}

func setAstrodomeScienceCloud(volume *astrodomeTestVolume, liquid, ice, fraction float64) {
	for _, support := range volume.stencil.Supports {
		column := volume.columns[support.ColumnID]
		for frameIndex := range column.Frames {
			for levelIndex := range column.Frames[frameIndex].FullLevels {
				column.Frames[frameIndex].FullLevels[levelIndex].CloudLiquidKgKg = liquid
				column.Frames[frameIndex].FullLevels[levelIndex].CloudIceKgKg = ice
				column.Frames[frameIndex].FullLevels[levelIndex].CloudFraction = fraction
			}
		}
		volume.columns[column.ColumnID] = column
	}
}

func assertAstrodomeScienceClose(t *testing.T, name string, got, want, relativeTolerance float64) {
	t.Helper()
	scale := math.Max(1, math.Max(math.Abs(got), math.Abs(want)))
	if math.Abs(got-want) > relativeTolerance*scale {
		t.Fatalf("%s = %.17g, want %.17g (relative tolerance %.3g)", name, got, want, relativeTolerance)
	}
}
