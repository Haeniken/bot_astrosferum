package iconeu

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
)

const (
	// Horizontal and physical crossings use the same certified localization
	// radius. The evidence midpoint is the partition representative; no second,
	// nominal refinement is allowed to outrun the retained proof interval.
	domeAstrodomeHorizontalRootToleranceM  = forecast.AstrodomeScienceRootToleranceM
	domeAstrodomePhysicalRootToleranceM    = forecast.AstrodomeScienceRootToleranceM
	domeAstrodomeHorizontalMergeToleranceM = forecast.AstrodomeScienceRootMergeToleranceM
	domeAstrodomePhysicalMergeToleranceM   = forecast.AstrodomeScienceRootMergeToleranceM
	domeAstrodomePhysicalRoundoffULPs      = 64
	// The dynamically computed dense-output, Cartesian-to-spherical, libm,
	// normalized-grid and grid-snap error must fit inside this hard envelope.
	// Residual intervals use the smaller, actually evaluated error for the
	// relevant native fields; the envelope is an acceptance ceiling, not an
	// uncertainty assigned to every sample.
	domeAstrodomePositionCoordinateEnvelopeM = 1e-3
	// Same-sign cells adjacent to an already localized root may need a few
	// additional bisections before a conservative Lipschitz bound proves them
	// empty. This is an absence-proof scale only; root publication keeps the
	// separate declared horizontal and physical localization radii.
	domeAstrodomeRootProofToleranceM = forecast.AstrodomeScienceRootToleranceM
	// Root isolation is a provider path-planning concern, not a Gauss--Kronrod
	// quadrature subdivision. Each independent scalar field gets this complete
	// budget so thousands of irrelevant WMO predicates cannot consume the
	// allowance needed by a later physical surface.
	domeAstrodomeRootIsolationBudgetPerField = 32768
	domeAstrodomeFallbackPressurePa          = 20000.0
)

type domeAstrodomeCellInterval struct {
	startM        float64
	endM          float64
	cellID        string
	stencil       forecast.AstrodomeHorizontalStencil
	startEvidence domeAstrodomePathEvent
	endEvidence   domeAstrodomePathEvent
}

type domeAstrodomeBoundary struct {
	id         string
	kind       string
	levelIndex int
}

type domeAstrodomePhysicalRootCandidate struct {
	// pathM is retained for narrow planner tests and compatibility helpers.
	// Production candidates always carry the complete evidence below.
	pathM    float64
	evidence domeAstrodomeRootEvidence
	eventID  string
	checks   []domeAstrodomeShortPanelResidualCheck
}

type domeAstrodomePBLDecisionRoot struct {
	evidence domeAstrodomeRootEvidence
	eventID  string
	checks   []domeAstrodomeShortPanelResidualCheck
}

type domeAstrodomeHorizontalRootCandidate struct {
	pathM   float64
	leftM   float64
	rightM  float64
	eventID string
	exact   bool
}

type domeAstrodomeHorizontalAxisCandidateRange struct {
	name      string
	longitude bool
	minimum   float64
	increment float64
	first     int
	last      int
}

// domeAstrodomeScalarField is one continuous predicate reconstructed from
// the four native supports of a horizontal cell. WMO thermal-tropopause
// candidate selection is discrete, but every decision entering that
// selection is a sign test of one of these raw HHL/temperature predicates.
// Partitioning their zeros therefore keeps the selected native candidate
// fixed without interpolating a diagnosed tropopause or a derived science
// quantity.
type domeAstrodomeScalarField struct {
	id                   string
	corners              [4]float64
	roundoffOperandScale float64
	// evaluate reconstructs the same native primitives and applies the same
	// provider-neutral sign predicate as the science kernel. A nil evaluator
	// denotes an ordinary bilinear scalar field.
	evaluate func([4]float64) float64
}

func (field domeAstrodomeScalarField) value(weights [4]float64) float64 {
	if field.evaluate != nil {
		return field.evaluate(weights)
	}
	return domeWeighted4(field.corners, weights)
}

type domeAstrodomeRootEvidence struct {
	pathM  float64
	leftM  float64
	rightM float64
	exact  bool
}

type domeAstrodomePathEvent struct {
	evidence domeAstrodomeRootEvidence
	eventID  string
	kind     string
	checks   []domeAstrodomeShortPanelResidualCheck
}

type domeAstrodomeShortPanelResidualCheck struct {
	eventID       string
	roundoffScale float64
	valueAt       domeAstrodomeResidualValue
	expectedSign  int
}

type domeAstrodomeShortPanelProof struct {
	certificate forecast.AstrodomeScienceCertifiedShortInterval
	checks      []domeAstrodomeShortPanelResidualCheck
}

type domeAstrodomePhysicalBreakpoints struct {
	candidates   []domeAstrodomePhysicalRootCandidate
	certificates []forecast.AstrodomeScienceCertifiedShortInterval
}

// domeAstrodomeShortPanelVerifier is bound to one immutable volume/ray/hour.
// It is deliberately a small path-owned verifier rather than another service
// layer: certified panels are rare and every node is checked synchronously
// before the provider-neutral science integrand is evaluated.
type domeAstrodomeShortPanelVerifier struct {
	volume  *DomeVolume
	ray     forecast.AstrodomeRefractedRay
	validAt time.Time
	proofs  map[string]domeAstrodomeShortPanelProof
}

func domePhysicalCandidateEvent(candidate domeAstrodomePhysicalRootCandidate) domeAstrodomePathEvent {
	return domeAstrodomePathEvent{
		evidence: candidate.evidence,
		eventID:  candidate.eventID,
		kind:     "physical",
		checks:   candidate.checks,
	}
}

func domeForecastRootEvidence(event domeAstrodomePathEvent) forecast.AstrodomeScienceRootEvidence {
	return forecast.AstrodomeScienceRootEvidence{
		RepresentativePathM: event.evidence.pathM,
		LeftPathM:           event.evidence.leftM,
		RightPathM:          event.evidence.rightM,
		EventID:             event.eventID,
		Kind:                event.kind,
		Exact:               event.evidence.exact,
	}
}

type domeAstrodomeTropopauseProfile struct {
	heights        [][4]float64
	pressures      [][4]float64
	decisionFields []domeAstrodomeScalarField
}

type domeAstrodomeMetricBounds struct {
	pathDerivativeNorm float64
	radiusMinimumM     float64
	longitudeRadiusM   float64
	incrementRadians   float64
}

type domeAstrodomePhysicalSample struct {
	point                    forecast.AstrodomeRefractedRayPoint
	positionEvaluationErrorM float64
	coordinateBounds         domeAstrodomeCoordinateEvaluationBounds
	stencil                  forecast.AstrodomeHorizontalStencil
	weights                  [4]float64
	native                   forecast.AstrodomeScienceNativeContext
	heights                  forecast.AstrodomeScienceBoundaryHeights
	nativeReady              bool
}

// domeAstrodomeResidualSample is the proof value of a physical predicate at
// one path coordinate. evaluationError is an absolute, non-negative enclosure
// for evaluation at the accepted numerical trajectory point, excluding the
// scalar arithmetic roundoff that domeAstrodomeResidualSignInterval adds from
// the field's native operand scale.
type domeAstrodomeResidualSample struct {
	value           float64
	evaluationError float64
}

type domeAstrodomeResidualValue func(float64) (domeAstrodomeResidualSample, error)

type domeAstrodomeCoordinateEvaluationBounds struct {
	equivalentPositionM          float64
	latitudeFractionUncertainty  float64
	longitudeFractionUncertainty float64
}

func domeAstrodomeResidualWithEvaluationError(value, evaluationError float64) (domeAstrodomeResidualSample, error) {
	if !finiteDomeVolume(value) || !finiteDomeVolume(evaluationError) || evaluationError < 0 {
		return domeAstrodomeResidualSample{}, errors.New("invalid Astrodome evaluated residual")
	}
	return domeAstrodomeResidualSample{value: value, evaluationError: evaluationError}, nil
}

type domeAstrodomePhysicalSampler struct {
	ctx            context.Context
	volume         *DomeVolume
	ray            forecast.AstrodomeRefractedRay
	validAt        time.Time
	cellID         string
	calibration    forecast.AstrodomeScienceCalibration
	cache          map[uint64]*domeAstrodomePhysicalSample
	approximations *domeAstrodomePathApproximation
}

type domeAstrodomeApproximationSpan struct {
	startM float64
	endM   float64
}

// domeAstrodomePathApproximation records the union of sub-metre path spans
// whose physical-root absence could not be certified. Multiple predicates at
// one horizontal-cell endpoint must not count the same geometric sliver more
// than once.
type domeAstrodomePathApproximation struct {
	spans []domeAstrodomeApproximationSpan
}

func (approximation *domeAstrodomePathApproximation) add(startM, endM float64) error {
	if approximation == nil || !finiteDomeVolume(startM) || !finiteDomeVolume(endM) || endM <= startM {
		return errors.New("invalid ICON-EU Astrodome approximation span")
	}
	approximation.spans = append(approximation.spans, domeAstrodomeApproximationSpan{startM: startM, endM: endM})
	if approximation.lengthM() > forecast.AstrodomeScienceMaximumApproximatePathLengthM {
		return fmt.Errorf(
			"%w: ICON-EU unresolved endpoint-sliver union exceeds the %.9g m publication ceiling",
			forecast.ErrAstrodomeScienceIncompletePartition,
			forecast.AstrodomeScienceMaximumApproximatePathLengthM,
		)
	}
	return nil
}

func (approximation *domeAstrodomePathApproximation) lengthM() float64 {
	if approximation == nil || len(approximation.spans) == 0 {
		return 0
	}
	spans := append([]domeAstrodomeApproximationSpan(nil), approximation.spans...)
	sort.Slice(spans, func(left, right int) bool {
		if spans[left].startM == spans[right].startM {
			return spans[left].endM < spans[right].endM
		}
		return spans[left].startM < spans[right].startM
	})
	total := 0.0
	startM, endM := spans[0].startM, spans[0].endM
	for _, span := range spans[1:] {
		if span.startM <= endM {
			endM = math.Max(endM, span.endM)
			continue
		}
		total += endM - startM
		startM, endM = span.startM, span.endM
	}
	return total + endM - startM
}

const (
	domeAstrodomeBoundaryHHL    = "hhl"
	domeAstrodomeBoundaryFull   = "full"
	domeAstrodomeBoundaryPBL    = "pbl"
	domeAstrodomeBoundaryLowTop = "cloud-low-top"
	domeAstrodomeBoundaryMidTop = "cloud-middle-top"
)

// BuildAstrodomeSciencePath solves native horizontal-cell, HHL interpolation,
// PBL, WMO tropopause, and cloud-tier events on the accepted curved ray. It
// partitions raw primitive reconstruction only; no seeing, tau0,
// transmission, Overall, or quality value is interpolated.
func (volume *DomeVolume) BuildAstrodomeSciencePath(
	ctx context.Context,
	ray forecast.AstrodomeRefractedRay,
	validAt time.Time,
	calibration forecast.AstrodomeScienceCalibration,
) (forecast.AstrodomeSciencePath, error) {
	path := forecast.AstrodomeSciencePath{
		ContractVersion: forecast.AstrodomeSciencePathContractVersion,
		SourceIdentity:  volume.Identity(), ValidAt: validAt.UTC(),
		GeometryMode:  forecast.AstrodomeScienceGeometryRefractionFull,
		NativeContext: volume,
	}
	if volume == nil {
		return path, errors.New("ICON-EU Astrodome volume is required")
	}
	if err := ctx.Err(); err != nil {
		return path, err
	}
	if err := calibration.Validate(); err != nil {
		return path, err
	}
	if !validAt.Equal(validAt.UTC()) || validAt.Minute() != 0 || validAt.Second() != 0 || validAt.Nanosecond() != 0 {
		return path, errors.New("ICON-EU Astrodome science-path time must be a whole UTC hour")
	}
	if ray.GeometryVersion != forecast.AstrodomeRefractionGeometryVersion {
		return path, errors.New("ICON-EU Astrodome science path needs a full-refraction ray")
	}
	shortVerifier := &domeAstrodomeShortPanelVerifier{
		volume:  volume,
		ray:     ray,
		validAt: validAt.UTC(),
		proofs:  make(map[string]domeAstrodomeShortPanelProof),
	}
	approximations := &domeAstrodomePathApproximation{}
	path.ShortIntervalVerifier = shortVerifier
	integrationIntervals, err := ray.IntegrationIntervals()
	if err != nil {
		return path, err
	}
	horizontalCandidates := make([]domeAstrodomeHorizontalRootCandidate, 0, len(integrationIntervals)*2)
	horizontalRootCache := make(map[string][]domeAstrodomeRootEvidence)
	for _, interval := range integrationIntervals {
		breaks, breakErr := volume.domeHorizontalBreakpointCandidatesWithCache(
			ctx, ray, interval.StartPathM, interval.EndPathM, horizontalRootCache,
		)
		if breakErr != nil {
			return path, breakErr
		}
		horizontalCandidates = append(horizontalCandidates, breaks...)
	}
	horizontalEvents, err := compactDomeHorizontalRootCandidates(horizontalCandidates)
	if err != nil {
		return path, err
	}
	pathEvents := make([]domeAstrodomePathEvent, 0, len(horizontalEvents)+2)
	pathEvents = append(pathEvents, domeAstrodomePathEvent{
		evidence: domeAstrodomeRootEvidence{pathM: 0, leftM: 0, rightM: 0, exact: true},
		eventID:  "ray/observer-aperture", kind: "path-endpoint",
	})
	for _, event := range horizontalEvents {
		if event.pathM > domeAstrodomeHorizontalMergeToleranceM &&
			event.pathM < ray.PathLengthM-domeAstrodomeHorizontalMergeToleranceM {
			pathEvents = append(pathEvents, domeAstrodomePathEvent{
				evidence: domeAstrodomeRootEvidence{
					pathM: event.pathM, leftM: event.leftM, rightM: event.rightM, exact: event.exact,
				},
				eventID: event.eventID, kind: "horizontal",
			})
		}
	}
	pathEvents = append(pathEvents, domeAstrodomePathEvent{
		evidence: domeAstrodomeRootEvidence{
			pathM: ray.PathLengthM, leftM: ray.PathLengthM, rightM: ray.PathLengthM, exact: true,
		},
		eventID: "ray/model-top", kind: "path-endpoint",
	})
	cellIntervals := make([]domeAstrodomeCellInterval, 0, len(pathEvents)-1)
	for index := 0; index+1 < len(pathEvents); index++ {
		startM, endM := pathEvents[index].evidence.pathM, pathEvents[index+1].evidence.pathM
		midpoint, pointErr := ray.PointAtPathLength((startM + endM) / 2)
		if pointErr != nil {
			return path, pointErr
		}
		stencil, stencilErr := volume.HorizontalStencil(ctx, midpoint.Location)
		if stencilErr != nil {
			return path, stencilErr
		}
		cellID, cellErr := volume.HorizontalCellID(stencil)
		if cellErr != nil {
			return path, cellErr
		}
		cellIntervals = append(cellIntervals, domeAstrodomeCellInterval{
			startM: startM, endM: endM, cellID: cellID, stencil: stencil,
			startEvidence: pathEvents[index], endEvidence: pathEvents[index+1],
		})
	}
	if len(cellIntervals) == 0 {
		return path, errors.New("ICON-EU Astrodome curved path contains no horizontal cell")
	}
	for _, interval := range cellIntervals {
		if interval.endM-interval.startM <= forecast.AstrodomeScienceMinimumEventIntervalLengthM {
			return path, fmt.Errorf("%w: ICON-EU horizontal cell interval does not clear the %.9g m compound-root side guard",
				forecast.ErrAstrodomeScienceIncompletePartition, forecast.AstrodomeScienceMinimumEventIntervalLengthM)
		}
	}

	path.Cells = make([]forecast.AstrodomeSciencePathCell, 0, len(cellIntervals))
	for _, interval := range cellIntervals {
		physical, boundaryErr := volume.domePhysicalBreakpointsWithEvidence(
			ctx, ray, validAt, interval, calibration, shortVerifier, approximations,
		)
		if boundaryErr != nil {
			return path, boundaryErr
		}
		breakpointPaths := domePhysicalRootCandidatePaths(physical.candidates)
		breakpointEvidence := make([]forecast.AstrodomeScienceRootEvidence, len(physical.candidates))
		for index := range physical.candidates {
			breakpointEvidence[index] = domeForecastRootEvidence(domePhysicalCandidateEvent(physical.candidates[index]))
		}
		startEvidence := domeForecastRootEvidence(interval.startEvidence)
		endEvidence := domeForecastRootEvidence(interval.endEvidence)
		path.Cells = append(path.Cells, forecast.AstrodomeSciencePathCell{
			StartPathM: interval.startM, EndPathM: interval.endM,
			HorizontalCellID: interval.cellID, BreakpointsPathM: breakpointPaths,
			StartEvidence: &startEvidence, EndEvidence: &endEvidence,
			BreakpointEvidence: breakpointEvidence, CertifiedShortIntervals: physical.certificates,
			NativeVerticalPredicatesIsolated: true,
			TropopausePredicatesIsolated:     true,
			TerrainState:                     forecast.AstrodomeScienceTerrainClear,
		})
	}
	one := 1.0
	path.Availability = forecast.AstrodomeSciencePathAvailability{
		Geometry: true, Turbulence: true, Cloud: true, Humidity: true,
		TemporalBrackets: true, Terrain: true,
	}
	path.TopClosed = true
	path.GeometryCoverage, path.TurbulencePathCoverage, path.CloudPathCoverage = 1, 1, 1
	path.HumidityPathCoverage = &one
	path.TemporalResolutionHours = 1
	path.ApproximationLengthM = approximations.lengthM()
	return path, nil
}

func (volume *DomeVolume) domeHorizontalBreakpoints(
	ctx context.Context,
	ray forecast.AstrodomeRefractedRay,
	startM, endM float64,
) ([]float64, error) {
	candidates, err := volume.domeHorizontalBreakpointCandidates(ctx, ray, startM, endM)
	if err != nil {
		return nil, err
	}
	compacted, err := compactDomeHorizontalRootCandidates(candidates)
	if err != nil {
		return nil, err
	}
	result := make([]float64, len(compacted))
	for index := range compacted {
		result[index] = compacted[index].pathM
	}
	return result, nil
}

func (volume *DomeVolume) domeHorizontalBreakpointCandidates(
	ctx context.Context,
	ray forecast.AstrodomeRefractedRay,
	startM, endM float64,
) ([]domeAstrodomeHorizontalRootCandidate, error) {
	return volume.domeHorizontalBreakpointCandidatesWithCache(ctx, ray, startM, endM, nil)
}

func (volume *DomeVolume) domeHorizontalBreakpointCandidatesWithCache(
	ctx context.Context,
	ray forecast.AstrodomeRefractedRay,
	startM, endM float64,
	rootCache map[string][]domeAstrodomeRootEvidence,
) ([]domeAstrodomeHorizontalRootCandidate, error) {
	axisRanges, err := volume.domeAstrodomeHorizontalAxisCandidateRanges(ray, startM, endM)
	if err != nil {
		return nil, err
	}
	result := make([]domeAstrodomeHorizontalRootCandidate, 0, 2)
	for _, axis := range axisRanges {
		for gridIndex := axis.first; gridIndex <= axis.last; gridIndex++ {
			boundary := axis.minimum + float64(gridIndex)*axis.increment
			eventID := fmt.Sprintf("%s/%d", axis.name, gridIndex)
			roots, cached := rootCache[eventID]
			if !cached {
				var rootErr error
				roots, rootErr = volume.domeAstrodomeHorizontalPredicateRoots(
					ctx, ray, startM, endM, boundary, axis.longitude,
				)
				if rootErr != nil {
					return nil, rootErr
				}
				if rootCache != nil {
					rootCache[eventID] = append([]domeAstrodomeRootEvidence(nil), roots...)
				}
			}
			for _, root := range roots {
				if root.pathM < startM-domeAstrodomeHorizontalMergeToleranceM ||
					root.pathM > endM+domeAstrodomeHorizontalMergeToleranceM {
					continue
				}
				result = append(result, domeAstrodomeHorizontalRootCandidate{
					pathM: root.pathM, leftM: root.leftM, rightM: root.rightM,
					eventID: eventID, exact: root.exact,
				})
			}
		}
	}
	return result, nil
}

// domeAstrodomeHorizontalAxisCandidateRanges returns every native latitude or
// longitude grid line that the accepted dense-output trajectory can reach on
// one DOPRI interval. Endpoint and midpoint signs are deliberately irrelevant:
// a tangency or two crossings can have equal endpoint signs.
//
// Let U bound |dr/ds|, h be the outward half-width of the interval, rho_min a
// lower bound for |r|, and p_min a lower bound for hypot(x,y). Relative to the
// midpoint, every coordinate lies inside
//
//	|Delta latitude|  <= (U*h + e_pos)/rho_min
//	|Delta longitude| <= (U*h + e_pos)/p_min,
//
// where e_pos is the common 1-mm position/coordinate envelope. The separately
// computed coordinate fractions add dense-evaluation, Cartesian-to-spherical,
// libm, normalized-grid, and grid-snap uncertainty. All positive arithmetic is
// rounded outward. If the resulting enclosure cannot be proved to stay inside
// the regular-grid domain and its longitude cut, the path fails closed.
func (volume *DomeVolume) domeAstrodomeHorizontalAxisCandidateRanges(
	ray forecast.AstrodomeRefractedRay,
	startM, endM float64,
) ([2]domeAstrodomeHorizontalAxisCandidateRange, error) {
	result := [2]domeAstrodomeHorizontalAxisCandidateRange{}
	if !finiteDomeVolume(startM) || !finiteDomeVolume(endM) || startM < 0 ||
		endM <= startM || endM > ray.PathLengthM {
		return result, errors.New("invalid ICON-EU Astrodome horizontal candidate interval")
	}
	grid := volume.manifest.Grid
	latitudeMaximum, err := domeAstrodomeCertifiedGridLineMaximum(
		grid.MinLat, grid.MaxLat, grid.Increment,
	)
	if err != nil {
		return result, err
	}
	longitudeMaximum, err := domeAstrodomeCertifiedGridLineMaximum(
		grid.MinLon, grid.MaxLon, grid.Increment,
	)
	if err != nil {
		return result, err
	}
	if grid.MinLat <= -90 || grid.MaxLat >= 90 || grid.MinLon <= -180 || grid.MaxLon >= 180 {
		return result, fmt.Errorf("%w: ICON-EU Astrodome horizontal grid touches a spherical coordinate cut",
			forecast.ErrAstrodomeScienceIncompletePartition)
	}

	middleM := startM + (endM-startM)/2
	middle, err := ray.PointAtPathLength(middleM)
	if err != nil {
		return result, err
	}
	middlePositionErrorM, err := ray.PositionEvaluationErrorUpperBound(middleM)
	if err != nil {
		return result, err
	}
	coordinateBounds, err := domeAstrodomeCoordinateEvaluationErrorBounds(
		middle, middlePositionErrorM, grid, false,
	)
	if err != nil {
		return result, err
	}
	if coordinateBounds.equivalentPositionM > domeAstrodomePositionCoordinateEnvelopeM {
		return result, fmt.Errorf("%w: ICON-EU Astrodome horizontal enumeration bound %.9g m exceeds the %.9g m contract",
			forecast.ErrAstrodomeScienceIncompletePartition,
			coordinateBounds.equivalentPositionM, domeAstrodomePositionCoordinateEnvelopeM)
	}

	pathDerivativeNorm, err := ray.PositionDerivativeNormUpperBound(startM, endM)
	if err != nil {
		return result, err
	}
	pathDerivativeNorm = math.Max(1, pathDerivativeNorm)
	pathScale := math.Max(1, math.Max(math.Abs(startM), math.Abs(endM)))
	spanUpperM := math.Nextafter(
		endM-startM+domeAstrodomeClearanceRoundoff(0, 0, pathScale), math.Inf(1),
	)
	halfSpanUpperM := domeAstrodomePositiveDivUpper(spanUpperM, 2)
	positionReachM := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveMulUpper(pathDerivativeNorm, halfSpanUpperM),
		domeAstrodomePositionCoordinateEnvelopeM,
	)
	radiusMinimumM := domeAstrodomePositiveSubLower(
		middle.ECEF.Norm(), positionReachM, domeAstrodomePhysicalHeightOperandScaleM(),
	)
	horizontalRadiusMinimumM := domeAstrodomePositiveSubLower(
		math.Hypot(middle.ECEF.X, middle.ECEF.Y), positionReachM,
		domeAstrodomePhysicalHeightOperandScaleM(),
	)
	if !finiteDomeVolume(radiusMinimumM) || radiusMinimumM <= 0 ||
		!finiteDomeVolume(horizontalRadiusMinimumM) || horizontalRadiusMinimumM <= 0 {
		return result, fmt.Errorf("%w: ICON-EU Astrodome horizontal reach has no positive angular denominator",
			forecast.ErrAstrodomeScienceIncompletePartition)
	}
	incrementRadians := math.Nextafter(
		grid.Increment*math.Pi/180-
			domeAstrodomeClearanceRoundoff(grid.Increment*math.Pi/180, 0, 1),
		0,
	)
	if !finiteDomeVolume(incrementRadians) || incrementRadians <= 0 {
		return result, fmt.Errorf("%w: ICON-EU Astrodome horizontal grid has no positive angular increment",
			forecast.ErrAstrodomeScienceIncompletePartition)
	}
	latitudeReachFraction := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveDivUpper(
			domeAstrodomePositiveDivUpper(positionReachM, radiusMinimumM), incrementRadians,
		),
		coordinateBounds.latitudeFractionUncertainty,
	)
	longitudeReachFraction := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveDivUpper(
			domeAstrodomePositiveDivUpper(positionReachM, horizontalRadiusMinimumM), incrementRadians,
		),
		coordinateBounds.longitudeFractionUncertainty,
	)

	for index, axis := range [...]struct {
		name                    string
		longitude               bool
		coordinate, minimum     float64
		maximumIndex            int
		coordinateReachFraction float64
	}{
		{name: "latitude", coordinate: middle.Location.Latitude, minimum: grid.MinLat,
			maximumIndex: latitudeMaximum, coordinateReachFraction: latitudeReachFraction},
		{name: "longitude", longitude: true, coordinate: middle.Location.Longitude, minimum: grid.MinLon,
			maximumIndex: longitudeMaximum, coordinateReachFraction: longitudeReachFraction},
	} {
		centerFraction := (axis.coordinate - axis.minimum) / grid.Increment
		first, last, rangeErr := domeAstrodomeCertifiedGridLineCandidateRange(
			centerFraction, axis.coordinateReachFraction, axis.maximumIndex,
		)
		if rangeErr != nil {
			return result, fmt.Errorf("%w: ICON-EU Astrodome %s candidate range is not certified: %w",
				forecast.ErrAstrodomeScienceIncompletePartition, axis.name, rangeErr)
		}
		result[index] = domeAstrodomeHorizontalAxisCandidateRange{
			name: axis.name, longitude: axis.longitude, minimum: axis.minimum,
			increment: grid.Increment, first: first, last: last,
		}
	}
	return result, nil
}

func domeAstrodomeCertifiedGridLineMaximum(minimum, maximum, increment float64) (int, error) {
	if !finiteDomeVolume(minimum) || !finiteDomeVolume(maximum) ||
		!finiteDomeVolume(increment) || increment <= 0 || maximum <= minimum {
		return 0, fmt.Errorf("%w: invalid ICON-EU Astrodome regular grid",
			forecast.ErrAstrodomeScienceIncompletePartition)
	}
	span := (maximum - minimum) / increment
	nearest := math.Round(span)
	uncertainty := domeAstrodomeClearanceRoundoff(span, 0, math.Max(1, math.Abs(span)))
	if !finiteDomeVolume(span) || nearest < 1 || nearest > float64(math.MaxInt) ||
		math.Abs(span-nearest) > uncertainty {
		return 0, fmt.Errorf("%w: ICON-EU Astrodome regular-grid extent is not an integer number of cells",
			forecast.ErrAstrodomeScienceIncompletePartition)
	}
	return int(nearest), nil
}

func domeAstrodomeCertifiedGridLineCandidateRange(
	centerFraction, reachFraction float64,
	maximumIndex int,
) (int, int, error) {
	if !finiteDomeVolume(centerFraction) || !finiteDomeVolume(reachFraction) ||
		reachFraction < 0 || maximumIndex < 1 {
		return 0, -1, errors.New("invalid grid-line reach")
	}
	lower := math.Nextafter(centerFraction-reachFraction, math.Inf(-1))
	upper := math.Nextafter(centerFraction+reachFraction, math.Inf(1))
	if !finiteDomeVolume(lower) || !finiteDomeVolume(upper) || lower < 0 || upper > float64(maximumIndex) {
		return 0, -1, errors.New("coordinate enclosure leaves the model footprint or coordinate cut")
	}
	first := int(math.Ceil(lower))
	last := int(math.Floor(upper))
	if first < 0 || last > maximumIndex {
		return 0, -1, errors.New("grid-line candidate indices leave the model footprint")
	}
	return first, last, nil
}

// domeAstrodomeHorizontalPredicateRoots isolates a native latitude/longitude
// boundary as an ECEF scalar predicate. It never relies on a nominal
// atan2/asin coordinate being exactly on a grid line:
//
//	longitude: y*cos(lambda)-x*sin(lambda) = 0
//	latitude:  z*cos(phi)-hypot(x,y)*sin(phi) = 0
//
// Both predicates have unit spatial gradient. The common one-millimetre
// evaluation envelope therefore enters their residual interval directly.
func (volume *DomeVolume) domeAstrodomeHorizontalPredicateRoots(
	ctx context.Context,
	ray forecast.AstrodomeRefractedRay,
	startM, endM, boundaryDegrees float64,
	longitude bool,
) ([]domeAstrodomeRootEvidence, error) {
	if endM <= startM || !finiteDomeVolume(boundaryDegrees) {
		return nil, errors.New("invalid ICON-EU Astrodome horizontal predicate interval")
	}
	// Solve each event on one deterministic whole-ray interval. Adjacent dense
	// segments can discover the same grid boundary; identical proof bounds make
	// their root evidence bit-identical instead of manufacturing two nearby
	// representatives. A grid line through either ray endpoint is not an
	// interior partition and is excluded by the common side guard.
	leftM := forecast.AstrodomeScienceCompoundRootSideGuardM
	rightM := ray.PathLengthM - forecast.AstrodomeScienceCompoundRootSideGuardM
	if rightM-leftM <= 2*domeAstrodomeRootProofToleranceM {
		return nil, fmt.Errorf("%w: horizontal predicate interval has no certified interior",
			forecast.ErrAstrodomeScienceIncompletePartition)
	}

	pathDerivativeNorm, err := ray.PositionDerivativeNormUpperBound(leftM, rightM)
	if err != nil {
		return nil, err
	}
	pathDerivativeNorm = math.Max(1, pathDerivativeNorm)
	acceleration, err := ray.PositionAccelerationNormUpperBound(leftM, rightM)
	if err != nil {
		return nil, err
	}
	angle := boundaryDegrees * math.Pi / 180
	sine, cosine := math.Sin(angle), math.Cos(angle)
	if !longitude && boundaryDegrees == 0 {
		identicallyZero, zeroErr := ray.PositionComponentIdenticallyZero(leftM, rightM, 2)
		if zeroErr != nil {
			return nil, zeroErr
		}
		if identicallyZero {
			// At latitude zero the ECEF predicate is exactly z. A bit-exact zero
			// dense polynomial remains in domeLowerGridIndex's deterministic
			// north-cell ownership for the whole interval and is not a crossing.
			return nil, nil
		}
	}
	valueAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
		if err := ctx.Err(); err != nil {
			return domeAstrodomeResidualSample{}, err
		}
		point, pointErr := ray.PointAtPathLength(pathM)
		if pointErr != nil {
			return domeAstrodomeResidualSample{}, pointErr
		}
		positionErrorM, pointErr := ray.PositionEvaluationErrorUpperBound(pathM)
		if pointErr != nil {
			return domeAstrodomeResidualSample{}, pointErr
		}
		if positionErrorM > domeAstrodomePositionCoordinateEnvelopeM {
			return domeAstrodomeResidualSample{}, fmt.Errorf("%w: ICON-EU Astrodome horizontal dense-position bound %.9g m exceeds the %.9g m contract",
				forecast.ErrAstrodomeScienceIncompletePartition,
				positionErrorM, domeAstrodomePositionCoordinateEnvelopeM)
		}
		value := point.ECEF.Z*cosine - math.Hypot(point.ECEF.X, point.ECEF.Y)*sine
		if longitude {
			value = point.ECEF.Y*cosine - point.ECEF.X*sine
		}
		return domeAstrodomeResidualWithEvaluationError(value, positionErrorM)
	}

	secondDerivative := acceleration
	if !longitude {
		middleM := leftM + (rightM-leftM)/2
		middle, pointErr := ray.PointAtPathLength(middleM)
		if pointErr != nil {
			return nil, pointErr
		}
		pathSpan := math.Nextafter(rightM-leftM, math.Inf(1))
		halfSpan := domeAstrodomePositiveDivUpper(pathSpan, 2)
		horizontalCenter := math.Hypot(middle.ECEF.X, middle.ECEF.Y)
		horizontalMinimum := domeAstrodomePositiveSubLower(
			math.Nextafter(horizontalCenter-domeAstrodomePositionCoordinateEnvelopeM, math.Inf(-1)),
			domeAstrodomePositiveMulUpper(pathDerivativeNorm, halfSpan),
			domeAstrodomePhysicalHeightOperandScaleM(),
		)
		if horizontalMinimum <= 0 || !finiteDomeVolume(horizontalMinimum) {
			return nil, fmt.Errorf("%w: horizontal latitude predicate has no positive cylindrical radius",
				forecast.ErrAstrodomeScienceIncompletePartition)
		}
		secondDerivative = domeAstrodomePositiveAddUpper(
			secondDerivative,
			domeAstrodomePositiveDivUpper(
				domeAstrodomePositiveMulUpper(pathDerivativeNorm, pathDerivativeNorm), horizontalMinimum,
			),
		)
	}
	slopeBounds := func(
		localLeftM, localRightM float64,
		leftSample, rightSample domeAstrodomeResidualSample,
	) (float64, float64, error) {
		lower, upper, slopeErr := domeAstrodomeSecantSlopeBoundsResiduals(
			localLeftM, localRightM, leftSample, rightSample,
			domeAstrodomePhysicalHeightOperandScaleM(), secondDerivative,
		)
		if slopeErr != nil {
			return 0, 0, slopeErr
		}
		return math.Max(-pathDerivativeNorm, lower), math.Min(pathDerivativeNorm, upper), nil
	}
	leftSample, err := valueAt(leftM)
	if err != nil {
		return nil, err
	}
	rightSample, err := valueAt(rightM)
	if err != nil {
		return nil, err
	}
	remaining := domeAstrodomeRootIsolationBudgetPerField
	result, err := domeAstrodomeIsolatePathRootsRecursive(
		leftM, rightM, leftSample, rightSample, pathDerivativeNorm,
		domeAstrodomePhysicalHeightOperandScaleM(), domeAstrodomeHorizontalRootToleranceM,
		slopeBounds, &remaining, valueAt,
	)
	if err != nil {
		return nil, err
	}
	return domeAstrodomeResolveRootIsolationEvidence(result, rightM)
}

func (volume *DomeVolume) domePhysicalBreakpointsWithEvidence(
	ctx context.Context,
	ray forecast.AstrodomeRefractedRay,
	validAt time.Time,
	interval domeAstrodomeCellInterval,
	calibration forecast.AstrodomeScienceCalibration,
	shortVerifier *domeAstrodomeShortPanelVerifier,
	approximations *domeAstrodomePathApproximation,
) (*domeAstrodomePhysicalBreakpoints, error) {
	columns, err := volume.domeStencilColumns(ctx, interval.stencil)
	if err != nil {
		return nil, err
	}
	metric, err := volume.domeAstrodomeMetricBoundsForInterval(ray, interval)
	if err != nil {
		return nil, err
	}
	boundaries := make([]domeAstrodomeBoundary, 0, domeHalfLevelCount-2+domeFullLevelCount+3)
	for levelIndex := 1; levelIndex+1 < domeHalfLevelCount; levelIndex++ {
		boundaries = append(boundaries, domeAstrodomeBoundary{
			id: fmt.Sprintf("hhl/%03d", levelIndex+1), kind: domeAstrodomeBoundaryHHL, levelIndex: levelIndex,
		})
	}
	for levelIndex := 0; levelIndex < domeFullLevelCount; levelIndex++ {
		boundaries = append(boundaries, domeAstrodomeBoundary{
			id: fmt.Sprintf("full-mid/%03d", levelIndex+1), kind: domeAstrodomeBoundaryFull, levelIndex: levelIndex,
		})
	}
	mixedLayerDepths, err := volume.domeAstrodomeMixedLayerCornerValues(columns, validAt)
	if err != nil {
		return nil, err
	}
	boundaries = append(boundaries,
		domeAstrodomeBoundary{id: "cloud-low-top", kind: domeAstrodomeBoundaryLowTop},
		domeAstrodomeBoundary{id: "cloud-middle-top", kind: domeAstrodomeBoundaryMidTop},
	)
	probePaths, err := domeAstrodomePhysicalProbePaths(interval)
	if err != nil {
		return nil, err
	}
	sampler := domeAstrodomePhysicalSampler{
		ctx: ctx, volume: volume, ray: ray, validAt: validAt, cellID: interval.cellID,
		calibration: calibration, cache: make(map[uint64]*domeAstrodomePhysicalSample, len(probePaths)*2),
		approximations: approximations,
	}
	resultCandidates := make([]domeAstrodomePhysicalRootCandidate, 0, 16)
	pblCandidates, err := volume.domeAstrodomePBLBreakpoints(
		ray, interval, probePaths, metric, &sampler, columns, mixedLayerDepths, calibration,
	)
	if err != nil {
		return nil, err
	}
	resultCandidates = append(resultCandidates, pblCandidates...)
	tropopauseProfile, err := volume.domeAstrodomeTropopauseProfile(columns, validAt)
	if err != nil {
		return nil, err
	}
	decisionCandidates := make([]domeAstrodomePhysicalRootCandidate, 0, 16)
	for _, field := range tropopauseProfile.decisionFields {
		lipschitz, lipschitzErr := domeAstrodomeScalarFieldLipschitz(
			metric, field.corners, field.roundoffOperandScale,
		)
		if lipschitzErr != nil {
			return nil, fmt.Errorf("bound ICON-EU Astrodome tropopause predicate %s: %w", field.id, lipschitzErr)
		}
		valueAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
			sample, sampleErr := sampler.sample(pathM, false)
			if sampleErr != nil {
				return domeAstrodomeResidualSample{}, sampleErr
			}
			return domeAstrodomeScalarFieldResidualAtSample(sample, field)
		}
		slopeBounds, slopeErr := domeAstrodomeBilinearScalarSlopeBounds(
			ray, metric, field, lipschitz, interval.startM, interval.endM,
		)
		if slopeErr != nil {
			return nil, fmt.Errorf("bound ICON-EU Astrodome tropopause predicate slope %s: %w", field.id, slopeErr)
		}
		roots, isolateErr := domeAstrodomeIsolateFieldAcrossProbesWithSlopeBoundsResidualEvidence(
			interval, probePaths, lipschitz, field.roundoffOperandScale, slopeBounds, nil,
			sampler.approximations, valueAt,
		)
		if isolateErr != nil {
			return nil, fmt.Errorf("isolate ICON-EU Astrodome tropopause predicate %s: %w", field.id, isolateErr)
		}
		for _, root := range domeAstrodomeInteriorRootEvidence(interval, roots) {
			eventID := "tropopause-decision/" + field.id
			decisionCandidates = append(decisionCandidates, domeAstrodomePhysicalRootCandidate{
				evidence: root, eventID: eventID,
				checks: []domeAstrodomeShortPanelResidualCheck{{
					eventID: eventID, roundoffScale: field.roundoffOperandScale, valueAt: valueAt,
				}},
			})
		}
	}
	decisionCandidates, err = compactDomePhysicalRootCandidates(decisionCandidates)
	if err != nil {
		return nil, err
	}
	decisionRoots := domePhysicalRootCandidatePaths(decisionCandidates)
	resultCandidates = append(resultCandidates, decisionCandidates...)

	for _, boundary := range boundaries {
		nominalValueAt := func(pathM float64) (float64, error) {
			sample, sampleErr := sampler.sample(pathM, boundary.kind != domeAstrodomeBoundaryHHL && boundary.kind != domeAstrodomeBoundaryFull)
			if sampleErr != nil {
				return 0, sampleErr
			}
			if boundary.kind == domeAstrodomeBoundaryHHL || boundary.kind == domeAstrodomeBoundaryFull {
				boundaryHeight, boundaryErr := domeAstrodomeNativeBoundaryHeight(columns, sample.weights, boundary)
				if boundaryErr != nil {
					return 0, boundaryErr
				}
				return sample.point.HeightM - boundaryHeight, nil
			}
			switch boundary.kind {
			case domeAstrodomeBoundaryLowTop:
				return sample.point.HeightM - sample.native.SurfaceHeightM - forecast.AstrodomeScienceCloudLowTopAGLM, nil
			case domeAstrodomeBoundaryMidTop:
				return sample.point.HeightM - sample.native.SurfaceHeightM - forecast.AstrodomeScienceCloudMiddleTopAGLM, nil
			default:
				return 0, fmt.Errorf("unsupported Astrodome physical boundary %q", boundary.id)
			}
		}
		boundaryFields, smoothBoundary, fieldsErr := volume.domePhysicalBoundaryFields(
			validAt, columns, boundary, calibration,
		)
		if fieldsErr != nil {
			return nil, fieldsErr
		}
		if !smoothBoundary {
			return nil, fmt.Errorf("%w: non-smooth boundary %s reached the ordinary physical solver",
				forecast.ErrAstrodomeScienceIncompletePartition, boundary.id)
		}
		lipschitz, boundarySlope, certifiedLipschitz, lipschitzErr := domePhysicalBoundaryLipschitz(
			boundaryFields, metric,
		)
		if lipschitzErr != nil {
			return nil, lipschitzErr
		}
		if !certifiedLipschitz {
			return nil, fmt.Errorf("%w: ICON-EU boundary %s has no certified path bound",
				forecast.ErrAstrodomeScienceIncompletePartition, boundary.id)
		}
		valueAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
			value, valueErr := nominalValueAt(pathM)
			if valueErr != nil {
				return domeAstrodomeResidualSample{}, valueErr
			}
			sample, sampleErr := sampler.sample(
				pathM, boundary.kind != domeAstrodomeBoundaryHHL && boundary.kind != domeAstrodomeBoundaryFull,
			)
			if sampleErr != nil {
				return domeAstrodomeResidualSample{}, sampleErr
			}
			evaluationError := sample.positionEvaluationErrorM
			for _, field := range boundaryFields {
				fieldError, fieldErr := domeAstrodomeBilinearCoordinateUncertaintyFromBounds(
					sample.coordinateBounds, field, domeAstrodomeCornerOperandScale(field),
				)
				if fieldErr != nil {
					return domeAstrodomeResidualSample{}, fieldErr
				}
				evaluationError = domeAstrodomePositiveAddUpper(evaluationError, fieldError)
			}
			return domeAstrodomeResidualWithEvaluationError(value, evaluationError)
		}
		slopeBounds := func(
			leftM, rightM float64,
			leftSample, rightSample domeAstrodomeResidualSample,
		) (float64, float64, error) {
			lower, upper, boundsErr := ray.HeightDerivativeBounds(leftM, rightM)
			if boundsErr != nil {
				return 0, 0, boundsErr
			}
			lower = math.Nextafter(lower-boundarySlope, math.Inf(-1))
			upper = math.Nextafter(upper+boundarySlope, math.Inf(1))
			secondDerivative, derivativeErr := domeAstrodomePhysicalBoundarySecondDerivativeBound(
				ray, metric, boundaryFields, leftM, rightM,
			)
			if derivativeErr != nil {
				return 0, 0, derivativeErr
			}
			secantLower, secantUpper, secantErr := domeAstrodomeSecantSlopeBoundsResiduals(
				leftM, rightM, leftSample, rightSample,
				domeAstrodomePhysicalHeightOperandScaleM(), secondDerivative,
			)
			if secantErr != nil {
				return 0, 0, secantErr
			}
			lower = math.Max(lower, secantLower)
			upper = math.Min(upper, secantUpper)
			if lower > upper {
				return 0, 0, fmt.Errorf("%w: Astrodome physical slope enclosures do not intersect",
					forecast.ErrAstrodomeScienceIncompletePartition)
			}
			return lower, upper, nil
		}
		probeValues := [5]domeAstrodomeResidualSample{}
		for index, pathM := range probePaths {
			probeValues[index], err = valueAt(pathM)
			if err != nil {
				return nil, err
			}
		}
		secondDerivative, err := domeAstrodomePhysicalBoundarySecondDerivativeBound(
			ray, metric, boundaryFields, interval.startM, interval.endM,
		)
		if err != nil {
			return nil, fmt.Errorf("bound ICON-EU Astrodome boundary endpoint slope %s: %w", boundary.id, err)
		}
		endpointSlopeBounds, err := domeAstrodomePhysicalEndpointSlopeBoundsFromInterior(
			boundary.kind, ray, boundarySlope, probePaths, probeValues, secondDerivative,
		)
		if err != nil {
			return nil, fmt.Errorf("bound ICON-EU Astrodome boundary endpoint slope %s: %w", boundary.id, err)
		}
		roots, isolateErr := domeAstrodomeIsolateFieldAcrossProbesWithSlopeBoundsResidualEvidence(
			interval, probePaths, lipschitz, domeAstrodomePhysicalHeightOperandScaleM(),
			slopeBounds, endpointSlopeBounds, sampler.approximations, valueAt,
		)
		if isolateErr != nil {
			return nil, fmt.Errorf("isolate ICON-EU Astrodome boundary %s: %w", boundary.id, isolateErr)
		}
		for _, root := range domeAstrodomeInteriorRootEvidence(interval, roots) {
			eventID := "boundary/" + boundary.id
			resultCandidates = append(resultCandidates, domeAstrodomePhysicalRootCandidate{
				evidence: root, eventID: eventID,
				checks: []domeAstrodomeShortPanelResidualCheck{{
					eventID:       eventID,
					roundoffScale: domeAstrodomePhysicalHeightOperandScaleM(),
					valueAt:       valueAt,
				}},
			})
		}
	}

	// A WMO-selected tropopause is exactly one native full-level midpoint;
	// those surfaces have already been solved above. Raw decision roots delimit
	// every place where the selected level can change. Only the documented
	// 200-hPa fallback needs an additional nonlinear isobaric surface.
	decisionBreakpoints := append([]float64{interval.startM}, decisionRoots...)
	decisionBreakpoints = append(decisionBreakpoints, interval.endM)
	for part := 0; part+1 < len(decisionBreakpoints); part++ {
		partStartM, partEndM := decisionBreakpoints[part], decisionBreakpoints[part+1]
		if partEndM-partStartM <= forecast.AstrodomeScienceMinimumEventIntervalLengthM {
			return nil, fmt.Errorf("%w: tropopause decisions in cell %q do not clear the %.9g m compound-root side guard",
				forecast.ErrAstrodomeScienceIncompletePartition, interval.cellID,
				forecast.AstrodomeScienceMinimumEventIntervalLengthM)
		}
		partProbes, probeErr := domeAstrodomePhysicalProbePaths(domeAstrodomeCellInterval{
			startM: partStartM, endM: partEndM, cellID: interval.cellID, stencil: interval.stencil,
		})
		if probeErr != nil {
			return nil, probeErr
		}
		middle, sampleErr := sampler.sample(partProbes[2], true)
		if sampleErr != nil {
			return nil, sampleErr
		}
		selection := middle.heights
		switch selection.TropopauseBoundaryKind {
		case forecast.AstrodomeScienceTropopauseBoundaryWMOLevel,
			forecast.AstrodomeScienceTropopauseBoundaryNone:
			continue
		case forecast.AstrodomeScienceTropopauseBoundaryPressureFallback:
		default:
			return nil, fmt.Errorf("unsupported ICON-EU tropopause boundary kind %q", selection.TropopauseBoundaryKind)
		}
		lipschitz, secondDerivative, fallbackEvaluationError, boundErr := domeAstrodomeFallbackBoundaryDerivativeBounds(
			ray, metric, tropopauseProfile, selection.TropopauseLowerLevelIndex,
			partStartM, partEndM,
		)
		if boundErr != nil {
			return nil, boundErr
		}
		valueAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
			sample, valueErr := sampler.sample(pathM, true)
			if valueErr != nil {
				return domeAstrodomeResidualSample{}, valueErr
			}
			if sample.heights.TropopauseBoundaryKind != selection.TropopauseBoundaryKind ||
				sample.heights.TropopauseLowerLevelIndex != selection.TropopauseLowerLevelIndex ||
				sample.heights.TropopauseUpperLevelIndex != selection.TropopauseUpperLevelIndex {
				return domeAstrodomeResidualSample{}, fmt.Errorf("%w: ICON-EU tropopause candidate changed inside a raw-decision partition",
					forecast.ErrAstrodomeScienceIncompletePartition)
			}
			coordinateError, residualErr := domeAstrodomeFallbackBoundaryCoordinateError(
				sample, tropopauseProfile,
				selection.TropopauseLowerLevelIndex,
			)
			if residualErr != nil {
				return domeAstrodomeResidualSample{}, residualErr
			}
			evaluationError := domeAstrodomePositiveAddUpper(
				sample.positionEvaluationErrorM,
				domeAstrodomePositiveAddUpper(coordinateError, fallbackEvaluationError),
			)
			return domeAstrodomeResidualWithEvaluationError(
				sample.point.HeightM-sample.heights.TropopauseHeightM, evaluationError,
			)
		}
		slopeBounds := func(
			leftM, rightM float64,
			leftSample, rightSample domeAstrodomeResidualSample,
		) (float64, float64, error) {
			lower, upper, slopeErr := domeAstrodomeSecantSlopeBoundsResiduals(
				leftM, rightM, leftSample, rightSample,
				domeAstrodomePhysicalHeightOperandScaleM(), secondDerivative,
			)
			if slopeErr != nil {
				return 0, 0, slopeErr
			}
			lower = math.Max(math.Nextafter(-lipschitz, math.Inf(-1)), lower)
			upper = math.Min(math.Nextafter(lipschitz, math.Inf(1)), upper)
			if lower > upper {
				return 0, 0, fmt.Errorf("%w: Astrodome 200-hPa slope enclosures do not intersect",
					forecast.ErrAstrodomeScienceIncompletePartition)
			}
			return lower, upper, nil
		}
		roots, isolateErr := domeAstrodomeIsolateFieldAcrossProbesWithSlopeBoundsResidualEvidence(
			domeAstrodomeCellInterval{startM: partStartM, endM: partEndM, cellID: interval.cellID, stencil: interval.stencil},
			partProbes, lipschitz, domeAstrodomePhysicalHeightOperandScaleM(), slopeBounds, nil,
			sampler.approximations, valueAt,
		)
		if isolateErr != nil {
			return nil, fmt.Errorf("isolate ICON-EU Astrodome 200-hPa fallback boundary: %w", isolateErr)
		}
		for _, root := range domeAstrodomeInteriorRootEvidence(interval, roots) {
			eventID := fmt.Sprintf("tropopause-fallback/%03d/%03d",
				selection.TropopauseLowerLevelIndex, selection.TropopauseUpperLevelIndex)
			resultCandidates = append(resultCandidates, domeAstrodomePhysicalRootCandidate{
				evidence: root, eventID: eventID,
				checks: []domeAstrodomeShortPanelResidualCheck{{
					eventID:       eventID,
					roundoffScale: domeAstrodomePhysicalHeightOperandScaleM(),
					valueAt:       valueAt,
				}},
			})
		}
	}
	resultCandidates, err = compactDomePhysicalRootCandidates(resultCandidates)
	if err != nil {
		return nil, err
	}
	certificates, err := domeValidatePhysicalBreakpointsWithEvidence(
		interval, resultCandidates, &sampler, shortVerifier,
	)
	if err != nil {
		return nil, err
	}
	return &domeAstrodomePhysicalBreakpoints{
		candidates:   resultCandidates,
		certificates: certificates,
	}, nil
}

func (volume *DomeVolume) domePhysicalBreakpoints(
	ctx context.Context,
	ray forecast.AstrodomeRefractedRay,
	validAt time.Time,
	interval domeAstrodomeCellInterval,
	calibration forecast.AstrodomeScienceCalibration,
) ([]float64, error) {
	verifier := &domeAstrodomeShortPanelVerifier{
		volume: volume, ray: ray, validAt: validAt.UTC(),
		proofs: make(map[string]domeAstrodomeShortPanelProof),
	}
	result, err := volume.domePhysicalBreakpointsWithEvidence(
		ctx, ray, validAt, interval, calibration, verifier, nil,
	)
	if err != nil {
		return nil, err
	}
	return domePhysicalRootCandidatePaths(result.candidates), nil
}

func (volume *DomeVolume) domeAstrodomePBLBreakpoints(
	ray forecast.AstrodomeRefractedRay,
	interval domeAstrodomeCellInterval,
	probePaths [5]float64,
	metric domeAstrodomeMetricBounds,
	sampler *domeAstrodomePhysicalSampler,
	columns [4]forecast.AstrodomePrimitiveColumn,
	mixedLayerDepths [4]float64,
	calibration forecast.AstrodomeScienceCalibration,
) ([]domeAstrodomePhysicalRootCandidate, error) {
	minimumDepthM := calibration.Overall.BoundaryLayerMinM
	maximumDepthM := calibration.Overall.BoundaryLayerTopM
	cellBranch, err := domeClassifyAstrodomePBLClampBranch(
		mixedLayerDepths, minimumDepthM, maximumDepthM,
	)
	if err != nil {
		return nil, err
	}
	surfaceHeights := domeAstrodomeSurfaceCornerValues(columns)
	if cellBranch == domeAstrodomePBLClampUpper {
		// On the certified upper branch PBL=HSURF+maximumDepthM. The science
		// calibration uses the same maximum as the low-cloud top, so that
		// surface is already solved once under boundary/cloud-low-top.
		if maximumDepthM == forecast.AstrodomeScienceCloudLowTopAGLM {
			return nil, nil
		}
		return domeAstrodomeIsolatePBLBranchBoundary(
			ray, interval, probePaths, metric, sampler, surfaceHeights, mixedLayerDepths,
			cellBranch, minimumDepthM, maximumDepthM, 0,
		)
	}
	if cellBranch == domeAstrodomePBLClampLower || cellBranch == domeAstrodomePBLClampIdentity {
		return domeAstrodomeIsolatePBLBranchBoundary(
			ray, interval, probePaths, metric, sampler, surfaceHeights, mixedLayerDepths,
			cellBranch, minimumDepthM, maximumDepthM, 0,
		)
	}

	minimumField := domeAstrodomePBLDecisionField(
		"pbl-clamp/minimum", mixedLayerDepths, minimumDepthM,
	)
	maximumField := domeAstrodomePBLDecisionField(
		"pbl-clamp/maximum", mixedLayerDepths, maximumDepthM,
	)
	decisionRoots := make([]domeAstrodomePBLDecisionRoot, 0, 4)
	for _, field := range []domeAstrodomeScalarField{minimumField, maximumField} {
		roots, rootErr := domeAstrodomePBLDecisionFieldRoots(
			ray, interval, probePaths, metric, sampler, field,
		)
		if rootErr != nil {
			return nil, rootErr
		}
		decisionRoots = append(decisionRoots, roots...)
	}
	decisionRoots, err = compactDomeAstrodomePBLDecisionRoots(decisionRoots, ray.PathLengthM)
	if err != nil {
		return nil, err
	}
	result := make([]domeAstrodomePhysicalRootCandidate, 0, len(decisionRoots)+2)
	partitionPaths := make([]float64, 0, len(decisionRoots)+2)
	partitionPaths = append(partitionPaths, interval.startM)
	for _, root := range decisionRoots {
		partitionPaths = append(partitionPaths, root.evidence.pathM)
		result = append(result, domeAstrodomePhysicalRootCandidate{
			evidence: root.evidence, eventID: root.eventID, checks: root.checks,
		})
	}
	partitionPaths = append(partitionPaths, interval.endM)

	// The complete raw-MH root isolation above proves that no clamp threshold
	// changes sign away from these evidence intervals. Each physical solver is
	// therefore restricted to one certified smooth branch. The root evidence
	// must also remain strictly outside every branch-sensitive probe.
	globalProofFields := [][4]float64{surfaceHeights, mixedLayerDepths}
	globalLipschitz, _, certified, err := domePhysicalBoundaryLipschitz(globalProofFields, metric)
	if err != nil {
		return nil, err
	}
	if !certified {
		return nil, fmt.Errorf("%w: ICON-EU mixed-cell PBL has no global path bound",
			forecast.ErrAstrodomeScienceIncompletePartition)
	}
	for part := 0; part+1 < len(partitionPaths); part++ {
		partInterval := domeAstrodomeCellInterval{
			startM: partitionPaths[part], endM: partitionPaths[part+1],
			cellID: interval.cellID, stencil: interval.stencil,
		}
		if partInterval.endM-partInterval.startM <= forecast.AstrodomeScienceMinimumEventIntervalLengthM {
			return nil, fmt.Errorf("%w: PBL decisions in cell %q do not clear the %.9g m compound-root side guard",
				forecast.ErrAstrodomeScienceIncompletePartition, interval.cellID,
				forecast.AstrodomeScienceMinimumEventIntervalLengthM)
		}
		partProbes, probeErr := domeAstrodomePhysicalProbePaths(partInterval)
		if probeErr != nil {
			return nil, probeErr
		}
		if part > 0 && decisionRoots[part-1].evidence.rightM >= partProbes[0] {
			return nil, fmt.Errorf("%w: PBL decision evidence enters the following branch probe span",
				forecast.ErrAstrodomeScienceIncompletePartition)
		}
		if part < len(decisionRoots) && decisionRoots[part].evidence.leftM <= partProbes[len(partProbes)-1] {
			return nil, fmt.Errorf("%w: PBL decision evidence enters the preceding branch probe span",
				forecast.ErrAstrodomeScienceIncompletePartition)
		}
		branch, branchErr := domeAstrodomeCertifiedPBLBranchAtProbes(
			sampler, partProbes, minimumField, maximumField,
		)
		if branchErr != nil {
			return nil, branchErr
		}
		switch branch {
		case domeAstrodomePBLClampUpper:
			if maximumDepthM == forecast.AstrodomeScienceCloudLowTopAGLM {
				continue
			}
		case domeAstrodomePBLClampLower, domeAstrodomePBLClampIdentity:
		default:
			return nil, fmt.Errorf("%w: ICON-EU PBL branch is not certified inside a raw-MH partition",
				forecast.ErrAstrodomeScienceIncompletePartition)
		}
		roots, isolateErr := domeAstrodomeIsolatePBLBranchBoundary(
			ray, partInterval, partProbes, metric, sampler, surfaceHeights, mixedLayerDepths,
			branch, minimumDepthM, maximumDepthM, globalLipschitz,
		)
		if isolateErr != nil {
			return nil, isolateErr
		}
		result = append(result, roots...)
	}
	return result, nil
}

func domeAstrodomePBLDecisionField(
	id string,
	mixedLayerDepths [4]float64,
	thresholdM float64,
) domeAstrodomeScalarField {
	corners := [4]float64{}
	operandScale := math.Max(1, math.Abs(thresholdM))
	for corner := range corners {
		corners[corner] = forecast.CompensatedDifferenceResidual(
			mixedLayerDepths[corner], 0, thresholdM,
		)
		operandScale = math.Max(operandScale, math.Abs(mixedLayerDepths[corner]))
	}
	return domeAstrodomeScalarField{
		id: id, corners: corners, roundoffOperandScale: operandScale,
		evaluate: func(weights [4]float64) float64 {
			return forecast.CompensatedDifferenceResidual(
				domeWeighted4(mixedLayerDepths, weights), 0, thresholdM,
			)
		},
	}
}

func domeAstrodomeScalarFieldResidualAtSample(
	sample *domeAstrodomePhysicalSample,
	field domeAstrodomeScalarField,
) (domeAstrodomeResidualSample, error) {
	if sample == nil {
		return domeAstrodomeResidualSample{}, errors.New("ICON-EU Astrodome scalar sample is required")
	}
	evaluationError, err := domeAstrodomeBilinearCoordinateUncertaintyFromBounds(
		sample.coordinateBounds, field.corners, field.roundoffOperandScale,
	)
	if err != nil {
		return domeAstrodomeResidualSample{}, err
	}
	return domeAstrodomeResidualWithEvaluationError(field.value(sample.weights), evaluationError)
}

func domeAstrodomePBLDecisionFieldRoots(
	ray forecast.AstrodomeRefractedRay,
	interval domeAstrodomeCellInterval,
	probePaths [5]float64,
	metric domeAstrodomeMetricBounds,
	sampler *domeAstrodomePhysicalSampler,
	field domeAstrodomeScalarField,
) ([]domeAstrodomePBLDecisionRoot, error) {
	minimum, maximum := domeAstrodomeCornerRange(field.corners)
	cornerError := domeAstrodomeClearanceRoundoff(0, 0, field.roundoffOperandScale)
	if minimum > cornerError || maximum < -cornerError {
		return nil, nil
	}
	lipschitz, err := domeAstrodomeScalarFieldLipschitz(
		metric, field.corners, field.roundoffOperandScale,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: raw ICON-EU PBL predicate %s has no certified path bound: %w",
			forecast.ErrAstrodomeScienceIncompletePartition, field.id, err)
	}
	valueAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
		sample, sampleErr := sampler.sample(pathM, false)
		if sampleErr != nil {
			return domeAstrodomeResidualSample{}, sampleErr
		}
		return domeAstrodomeScalarFieldResidualAtSample(sample, field)
	}
	slopeBounds, err := domeAstrodomeBilinearScalarSlopeBounds(
		ray, metric, field, lipschitz, interval.startM, interval.endM,
	)
	if err != nil {
		return nil, fmt.Errorf("bound raw ICON-EU PBL predicate slope %s: %w", field.id, err)
	}
	probeValues := [5]domeAstrodomeResidualSample{}
	for index, pathM := range probePaths {
		probeValues[index], err = valueAt(pathM)
		if err != nil {
			return nil, err
		}
	}
	secondDerivative, err := domeAstrodomeBilinearScalarSecondDerivativeBound(
		ray, metric, field, interval.startM, interval.endM,
	)
	if err != nil {
		return nil, fmt.Errorf("bound raw ICON-EU PBL predicate endpoint slope %s: %w", field.id, err)
	}
	endpointSlopeBounds, err := domeAstrodomeEndpointSlopeBoundsFromInterior(
		probePaths, probeValues, field.roundoffOperandScale, secondDerivative,
	)
	if err != nil {
		return nil, fmt.Errorf("bound raw ICON-EU PBL predicate endpoint slope %s: %w", field.id, err)
	}
	evidence, err := domeAstrodomeIsolateFieldAcrossProbesWithSlopeBoundsResidualEvidence(
		interval, probePaths, lipschitz, field.roundoffOperandScale,
		slopeBounds, endpointSlopeBounds, sampler.approximations, valueAt,
	)
	if err != nil {
		return nil, fmt.Errorf("isolate raw ICON-EU PBL predicate %s: %w", field.id, err)
	}
	result := make([]domeAstrodomePBLDecisionRoot, 0, len(evidence))
	for _, root := range evidence {
		if root.pathM > interval.startM+domeAstrodomePhysicalMergeToleranceM &&
			root.pathM < interval.endM-domeAstrodomePhysicalMergeToleranceM {
			result = append(result, domeAstrodomePBLDecisionRoot{
				evidence: root, eventID: "pbl-decision/" + field.id,
				checks: []domeAstrodomeShortPanelResidualCheck{{
					eventID:       "pbl-decision/" + field.id,
					roundoffScale: field.roundoffOperandScale,
					valueAt:       valueAt,
				}},
			})
		}
	}
	return result, nil
}

func compactDomeAstrodomePBLDecisionRoots(
	values []domeAstrodomePBLDecisionRoot,
	pathEndM float64,
) ([]domeAstrodomePBLDecisionRoot, error) {
	sort.Slice(values, func(left, right int) bool {
		if values[left].evidence.pathM == values[right].evidence.pathM {
			return values[left].eventID < values[right].eventID
		}
		return values[left].evidence.pathM < values[right].evidence.pathM
	})
	result := values[:0]
	for _, root := range values {
		evidence := root.evidence
		if strings.TrimSpace(root.eventID) == "" || !finiteDomeVolume(evidence.pathM) ||
			!finiteDomeVolume(evidence.leftM) || !finiteDomeVolume(evidence.rightM) ||
			evidence.leftM < 0 || evidence.rightM > pathEndM || evidence.leftM > evidence.pathM ||
			evidence.pathM > evidence.rightM ||
			math.Max(evidence.pathM-evidence.leftM, evidence.rightM-evidence.pathM) >
				domeAstrodomePhysicalRootToleranceM {
			return nil, fmt.Errorf("%w: invalid ICON-EU PBL decision-root evidence",
				forecast.ErrAstrodomeScienceIncompletePartition)
		}
		if len(result) == 0 || evidence.pathM-result[len(result)-1].evidence.pathM > domeAstrodomePhysicalMergeToleranceM {
			result = append(result, root)
			continue
		}
		previous := result[len(result)-1]
		if root.eventID == previous.eventID && evidence.pathM == previous.evidence.pathM &&
			evidence.leftM == previous.evidence.leftM && evidence.rightM == previous.evidence.rightM &&
			evidence.exact == previous.evidence.exact {
			continue
		}
		return nil, fmt.Errorf("%w: distinct ICON-EU PBL decisions %q and %q are %.9g m apart inside the %.9g m root cluster",
			forecast.ErrAstrodomeScienceIncompletePartition, previous.eventID, root.eventID,
			evidence.pathM-previous.evidence.pathM, domeAstrodomePhysicalMergeToleranceM)
	}
	return result, nil
}

func domeAstrodomeCertifiedPBLBranchAtProbes(
	sampler *domeAstrodomePhysicalSampler,
	probePaths [5]float64,
	minimumField, maximumField domeAstrodomeScalarField,
) (domeAstrodomePBLClampBranch, error) {
	branch := domeAstrodomePBLClampCrossing
	for _, pathM := range probePaths {
		sample, err := sampler.sample(pathM, false)
		if err != nil {
			return domeAstrodomePBLClampCrossing, err
		}
		minimumResidual, err := domeAstrodomeScalarFieldResidualAtSample(sample, minimumField)
		if err != nil {
			return domeAstrodomePBLClampCrossing, err
		}
		maximumResidual, err := domeAstrodomeScalarFieldResidualAtSample(sample, maximumField)
		if err != nil {
			return domeAstrodomePBLClampCrossing, err
		}
		minimumSign, _, _ := domeAstrodomeResidualSignInterval(
			minimumResidual, minimumField.roundoffOperandScale,
		)
		maximumSign, _, _ := domeAstrodomeResidualSignInterval(
			maximumResidual, maximumField.roundoffOperandScale,
		)
		var probeBranch domeAstrodomePBLClampBranch
		switch {
		case minimumSign < 0 && maximumSign < 0:
			probeBranch = domeAstrodomePBLClampLower
		case minimumSign > 0 && maximumSign < 0:
			probeBranch = domeAstrodomePBLClampIdentity
		case minimumSign > 0 && maximumSign > 0:
			probeBranch = domeAstrodomePBLClampUpper
		default:
			return domeAstrodomePBLClampCrossing, fmt.Errorf("%w: raw ICON-EU PBL branch is numerically indeterminate at %.12g m",
				forecast.ErrAstrodomeScienceIncompletePartition, pathM)
		}
		if branch == domeAstrodomePBLClampCrossing {
			branch = probeBranch
		} else if branch != probeBranch {
			return domeAstrodomePBLClampCrossing, fmt.Errorf("%w: raw ICON-EU PBL branch changes inside a certified partition",
				forecast.ErrAstrodomeScienceIncompletePartition)
		}
	}
	return branch, nil
}

func domeAstrodomeIsolatePBLBranchBoundary(
	ray forecast.AstrodomeRefractedRay,
	interval domeAstrodomeCellInterval,
	probePaths [5]float64,
	metric domeAstrodomeMetricBounds,
	sampler *domeAstrodomePhysicalSampler,
	surfaceHeights, mixedLayerDepths [4]float64,
	branch domeAstrodomePBLClampBranch,
	minimumDepthM, maximumDepthM, proofLipschitz float64,
) ([]domeAstrodomePhysicalRootCandidate, error) {
	fields := [][4]float64{surfaceHeights}
	offsetM := minimumDepthM
	switch branch {
	case domeAstrodomePBLClampLower:
	case domeAstrodomePBLClampIdentity:
		fields = append(fields, mixedLayerDepths)
		offsetM = 0
	case domeAstrodomePBLClampUpper:
		offsetM = maximumDepthM
	default:
		return nil, fmt.Errorf("%w: cannot solve an uncertified ICON-EU PBL branch",
			forecast.ErrAstrodomeScienceIncompletePartition)
	}
	branchLipschitz, boundarySlope, certified, err := domePhysicalBoundaryLipschitz(fields, metric)
	if err != nil {
		return nil, err
	}
	if !certified {
		return nil, fmt.Errorf("%w: ICON-EU PBL branch has no certified path bound",
			forecast.ErrAstrodomeScienceIncompletePartition)
	}
	if proofLipschitz <= 0 {
		proofLipschitz = branchLipschitz
	} else if !finiteDomeVolume(proofLipschitz) || proofLipschitz < branchLipschitz {
		return nil, errors.New("invalid ICON-EU PBL proof Lipschitz bound")
	}
	valueAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
		sample, sampleErr := sampler.sample(pathM, false)
		if sampleErr != nil {
			return domeAstrodomeResidualSample{}, sampleErr
		}
		value := sample.point.HeightM - domeWeighted4(surfaceHeights, sample.weights) - offsetM
		if branch == domeAstrodomePBLClampIdentity {
			value -= domeWeighted4(mixedLayerDepths, sample.weights)
		}
		evaluationError := sample.positionEvaluationErrorM
		for _, field := range fields {
			fieldError, fieldErr := domeAstrodomeBilinearCoordinateUncertaintyFromBounds(
				sample.coordinateBounds, field, domeAstrodomeCornerOperandScale(field),
			)
			if fieldErr != nil {
				return domeAstrodomeResidualSample{}, fieldErr
			}
			evaluationError = domeAstrodomePositiveAddUpper(evaluationError, fieldError)
		}
		return domeAstrodomeResidualWithEvaluationError(value, evaluationError)
	}
	slopeBounds := func(
		leftM, rightM float64,
		leftSample, rightSample domeAstrodomeResidualSample,
	) (float64, float64, error) {
		lower, upper, boundsErr := ray.HeightDerivativeBounds(leftM, rightM)
		if boundsErr != nil {
			return 0, 0, boundsErr
		}
		lower = math.Nextafter(lower-boundarySlope, math.Inf(-1))
		upper = math.Nextafter(upper+boundarySlope, math.Inf(1))
		secondDerivative, derivativeErr := domeAstrodomePhysicalBoundarySecondDerivativeBound(
			ray, metric, fields, leftM, rightM,
		)
		if derivativeErr != nil {
			return 0, 0, derivativeErr
		}
		secantLower, secantUpper, secantErr := domeAstrodomeSecantSlopeBoundsResiduals(
			leftM, rightM, leftSample, rightSample,
			domeAstrodomePhysicalHeightOperandScaleM(), secondDerivative,
		)
		if secantErr != nil {
			return 0, 0, secantErr
		}
		lower = math.Max(lower, secantLower)
		upper = math.Min(upper, secantUpper)
		if lower > upper {
			return 0, 0, fmt.Errorf("%w: Astrodome PBL branch slope enclosures do not intersect",
				forecast.ErrAstrodomeScienceIncompletePartition)
		}
		return lower, upper, nil
	}
	roots, err := domeAstrodomeIsolateFieldAcrossProbesWithSlopeBoundsResidualEvidence(
		interval, probePaths, proofLipschitz, domeAstrodomePhysicalHeightOperandScaleM(),
		slopeBounds, domeAstrodomePhysicalEndpointSlopeBounds(domeAstrodomeBoundaryPBL, ray, boundarySlope),
		sampler.approximations, valueAt,
	)
	if err != nil {
		return nil, fmt.Errorf("isolate ICON-EU Astrodome PBL %v branch: %w", branch, err)
	}
	result := make([]domeAstrodomePhysicalRootCandidate, 0, len(roots))
	for _, root := range domeAstrodomeInteriorRootEvidence(interval, roots) {
		result = append(result, domeAstrodomePhysicalRootCandidate{
			evidence: root, eventID: "boundary/pbl",
			checks: []domeAstrodomeShortPanelResidualCheck{{
				eventID:       "boundary/pbl",
				roundoffScale: domeAstrodomePhysicalHeightOperandScaleM(),
				valueAt:       valueAt,
			}},
		})
	}
	return result, nil
}

func (volume *DomeVolume) domeAstrodomeTropopauseProfile(
	columns [4]forecast.AstrodomePrimitiveColumn,
	validAt time.Time,
) (domeAstrodomeTropopauseProfile, error) {
	bracket, err := domeVolumeBracket(volume.fieldTimes[forecast.AstrodomePrimitivePressure], validAt)
	if err != nil {
		return domeAstrodomeTropopauseProfile{}, err
	}
	heights := make([][4]float64, domeFullLevelCount)
	upperHalfHeights := make([][4]float64, domeFullLevelCount)
	lowerHalfHeights := make([][4]float64, domeFullLevelCount)
	pressures := make([][4]float64, domeFullLevelCount)
	temperatures := make([][4]float64, domeFullLevelCount)
	for thermalIndex := range domeFullLevelCount {
		modelIndex := domeFullLevelCount - 1 - thermalIndex
		for corner, column := range columns {
			if modelIndex+1 >= len(column.HalfLevelGeometry) ||
				bracket.leftIndex >= len(column.Frames) || bracket.rightIndex >= len(column.Frames) ||
				modelIndex >= len(column.Frames[bracket.leftIndex].FullLevels) ||
				modelIndex >= len(column.Frames[bracket.rightIndex].FullLevels) {
				return domeAstrodomeTropopauseProfile{}, fmt.Errorf("ICON-EU WMO predicate level %d is unavailable", modelIndex+1)
			}
			left := column.Frames[bracket.leftIndex].FullLevels[modelIndex]
			right := column.Frames[bracket.rightIndex].FullLevels[modelIndex]
			if !left.Available.Has(forecast.AstrodomePrimitivePressure) ||
				!right.Available.Has(forecast.AstrodomePrimitivePressure) ||
				!left.Available.Has(forecast.AstrodomePrimitiveTemperature) ||
				!right.Available.Has(forecast.AstrodomePrimitiveTemperature) {
				return domeAstrodomeTropopauseProfile{}, fmt.Errorf("ICON-EU WMO predicate P/T level %d is unavailable", modelIndex+1)
			}
			upperHalfHeights[thermalIndex][corner] = column.HalfLevelGeometry[modelIndex].HeightM
			lowerHalfHeights[thermalIndex][corner] = column.HalfLevelGeometry[modelIndex+1].HeightM
			heights[thermalIndex][corner] = (upperHalfHeights[thermalIndex][corner] +
				lowerHalfHeights[thermalIndex][corner]) / 2
			pressures[thermalIndex][corner] = domeLinear(
				left.PressurePa, right.PressurePa, bracket.fraction,
			)
			temperatures[thermalIndex][corner] = domeLinear(
				left.TemperatureK, right.TemperatureK, bracket.fraction,
			)
			if !finiteDomeVolume(heights[thermalIndex][corner]) ||
				!finiteDomeVolume(pressures[thermalIndex][corner]) || pressures[thermalIndex][corner] <= 0 ||
				!finiteDomeVolume(temperatures[thermalIndex][corner]) ||
				temperatures[thermalIndex][corner] < 150 || temperatures[thermalIndex][corner] > 350 {
				return domeAstrodomeTropopauseProfile{}, fmt.Errorf("ICON-EU WMO predicate level %d has invalid HHL/P/T", modelIndex+1)
			}
			if thermalIndex > 0 && heights[thermalIndex][corner] <= heights[thermalIndex-1][corner] {
				return domeAstrodomeTropopauseProfile{}, errors.New("ICON-EU WMO predicate geometry is not bottom-to-top ordered")
			}
			if thermalIndex > 0 && pressures[thermalIndex][corner] >= pressures[thermalIndex-1][corner] {
				return domeAstrodomeTropopauseProfile{}, errors.New("ICON-EU WMO predicate pressure is not bottom-to-top ordered")
			}
		}
	}
	heightAt := func(level int, weights [4]float64) float64 {
		// Keep the provider-neutral primitive order: interpolate both native
		// HHL surfaces first, then reconstruct their local full-level midpoint.
		return (domeWeighted4(upperHalfHeights[level], weights) +
			domeWeighted4(lowerHalfHeights[level], weights)) / 2
	}
	fields, wmoGuaranteed, err := domeAstrodomeWMOPredicateFieldsFromCornersWithHeightEvaluator(
		heights, temperatures, heightAt,
	)
	if err != nil {
		return domeAstrodomeTropopauseProfile{}, err
	}
	for level := range pressures {
		if wmoGuaranteed {
			break
		}
		corners := [4]float64{}
		operandScale := domeAstrodomeFallbackPressurePa
		for corner := range corners {
			corners[corner] = forecast.CompensatedDifferenceResidual(
				pressures[level][corner], 0, domeAstrodomeFallbackPressurePa,
			)
			operandScale = math.Max(operandScale, math.Abs(pressures[level][corner]))
		}
		pressureLevel := level
		domeAppendAstrodomeEvaluatedCrossingField(&fields,
			fmt.Sprintf("pressure-fallback/level-%03d/200hpa", level), corners, operandScale,
			func(weights [4]float64) float64 {
				return forecast.CompensatedDifferenceResidual(
					domeWeighted4(pressures[pressureLevel], weights), 0, domeAstrodomeFallbackPressurePa,
				)
			},
		)
	}
	return domeAstrodomeTropopauseProfile{
		heights: heights, pressures: pressures, decisionFields: fields,
	}, nil
}

func domeAstrodomeWMOPredicateFieldsFromCorners(
	heights, temperatures [][4]float64,
) ([]domeAstrodomeScalarField, bool, error) {
	return domeAstrodomeWMOPredicateFieldsFromCornersWithHeightEvaluator(heights, temperatures, nil)
}

func domeAstrodomeWMOPredicateFieldsFromCornersWithHeightEvaluator(
	heights, temperatures [][4]float64,
	heightAt func(int, [4]float64) float64,
) ([]domeAstrodomeScalarField, bool, error) {
	if len(heights) < 2 || len(heights) != len(temperatures) {
		return nil, false, errors.New("ICON-EU WMO predicate profile dimensions are invalid")
	}
	for level := range heights {
		for corner := range heights[level] {
			if !finiteDomeVolume(heights[level][corner]) ||
				!finiteDomeVolume(temperatures[level][corner]) ||
				temperatures[level][corner] < 150 || temperatures[level][corner] > 350 ||
				(level > 0 && heights[level][corner] <= heights[level-1][corner]) {
				return nil, false, errors.New("ICON-EU WMO predicate corner profile is invalid")
			}
		}
	}
	if heightAt == nil {
		heightAt = func(level int, weights [4]float64) float64 {
			return domeWeighted4(heights[level], weights)
		}
	}
	fields := make([]domeAstrodomeScalarField, 0, len(heights))
	wmoGuaranteed := false
	difference := func(left, right [4]float64, offset float64) ([4]float64, float64) {
		result := [4]float64{}
		operandScale := math.Max(1, math.Abs(offset))
		for corner := range result {
			result[corner] = forecast.CompensatedDifferenceResidual(right[corner], left[corner], offset)
			operandScale = math.Max(operandScale, math.Max(math.Abs(left[corner]), math.Abs(right[corner])))
		}
		return result, operandScale
	}
	lapsePredicate := func(lower, upper int) ([4]float64, float64) {
		result := [4]float64{}
		operandScale := 1.0
		for corner := range result {
			result[corner] = forecast.WMOThermalLapseResidual(
				heights[lower][corner], heights[upper][corner],
				temperatures[lower][corner], temperatures[upper][corner],
			)
			operandScale = math.Max(operandScale, math.Max(
				math.Max(math.Abs(heights[lower][corner]), math.Abs(heights[upper][corner])),
				math.Max(500*math.Abs(temperatures[lower][corner]), 500*math.Abs(temperatures[upper][corner])),
			))
		}
		return result, operandScale
	}
	for lower := 0; lower+1 < len(heights); lower++ {
		aboveFiveKM := [4]float64{}
		aboveFiveKMOperandScale := 5000.0
		for corner := range aboveFiveKM {
			aboveFiveKM[corner] = forecast.CompensatedDifferenceResidual(heights[lower][corner], 0, 5000)
			aboveFiveKMOperandScale = math.Max(aboveFiveKMOperandScale, math.Abs(heights[lower][corner]))
		}
		aboveFiveKMMinimum, aboveFiveKMMaximum := domeAstrodomeCornerRange(aboveFiveKM)
		aboveFiveKMUncertainty := domeAstrodomeClearanceRoundoff(0, 0, aboveFiveKMOperandScale)
		possiblyEligible := aboveFiveKMMaximum >= -aboveFiveKMUncertainty
		candidateCertifiedEverywhere := aboveFiveKMMinimum > aboveFiveKMUncertainty
		lowerLevel := lower
		domeAppendAstrodomeEvaluatedCrossingField(&fields,
			fmt.Sprintf("wmo/level-%03d/height-5000m", lower), aboveFiveKM, aboveFiveKMOperandScale,
			func(weights [4]float64) float64 {
				return forecast.CompensatedDifferenceResidual(
					heightAt(lowerLevel, weights), 0, 5000,
				)
			},
		)
		if !possiblyEligible {
			continue
		}
		instantLapse, instantLapseScale := lapsePredicate(lower, lower+1)
		instantMinimum, instantMaximum := domeAstrodomeCornerRange(instantLapse)
		instantUncertainty := domeAstrodomeClearanceRoundoff(0, 0, instantLapseScale)
		candidateCertifiedEverywhere = candidateCertifiedEverywhere &&
			instantMinimum > instantUncertainty
		instantUpper := lower + 1
		domeAppendAstrodomeEvaluatedCrossingField(&fields, fmt.Sprintf("wmo/level-%03d/instant-lapse", lower),
			instantLapse, instantLapseScale, func(weights [4]float64) float64 {
				return forecast.WMOThermalLapseResidual(
					heightAt(lowerLevel, weights), heightAt(instantUpper, weights),
					domeWeighted4(temperatures[lowerLevel], weights), domeWeighted4(temperatures[instantUpper], weights),
				)
			})
		// Every height-eligible point must evaluate the instantaneous lapse
		// first. If it is strictly negative throughout the complete bilinear
		// cell, this candidate can never reach a span or mean-lapse test.
		if instantMaximum < -instantUncertainty {
			continue
		}
		certifiedTwoKilometreTop := false
		for upper := lower + 1; upper < len(heights); upper++ {
			upperLevel := upper
			span, spanScale := difference(heights[lower], heights[upper], 2000)
			domeAppendAstrodomeEvaluatedCrossingField(&fields, fmt.Sprintf("wmo/level-%03d/top-%03d/span-2000m", lower, upper),
				span, spanScale, func(weights [4]float64) float64 {
					return forecast.CompensatedDifferenceResidual(
						heightAt(upperLevel, weights), heightAt(lowerLevel, weights), 2000,
					)
				})
			meanLapse, meanLapseScale := lapsePredicate(lower, upper)
			meanLapseMinimum, meanLapseMaximum := domeAstrodomeCornerRange(meanLapse)
			meanLapseUncertainty := domeAstrodomeClearanceRoundoff(0, 0, meanLapseScale)
			candidateCertifiedEverywhere = candidateCertifiedEverywhere &&
				meanLapseMinimum > meanLapseUncertainty
			// For the first upper level this WMO mean-lapse predicate is
			// algebraically identical to the instantaneous lower/next-level
			// predicate registered above. The decision is still evaluated here,
			// but registering the same zero set under a second event identity
			// would falsely look like two unresolved physical boundaries.
			if upper != lower+1 {
				domeAppendAstrodomeEvaluatedCrossingField(&fields, fmt.Sprintf("wmo/level-%03d/top-%03d/mean-lapse", lower, upper),
					meanLapse, meanLapseScale, func(weights [4]float64) float64 {
						return forecast.WMOThermalLapseResidual(
							heightAt(lowerLevel, weights), heightAt(upperLevel, weights),
							domeWeighted4(temperatures[lowerLevel], weights), domeWeighted4(temperatures[upperLevel], weights),
						)
					})
			}
			// A point that does not reach this upper level has already stopped at
			// an earlier 2-km top. Every point that does reach it fails here when
			// the complete field is strictly negative, so no later upper-level
			// predicate is reachable anywhere in the cell.
			if meanLapseMaximum < -meanLapseUncertainty {
				break
			}

			// thermalTropopauseLevelIndex evaluates mean lapse only through
			// the first native level at least 2 km above the candidate. A
			// bilinear field is a convex combination of its four corners, so
			// once this upper level is strictly above 2 km at every corner by
			// more than the unit-aware cancellation enclosure, no later upper
			// level can be selected anywhere in the cell. If any corner is at
			// or inside the enclosure, keep later predicates: the identity of
			// the first qualifying top may still vary inside the cell.
			minimumSpan := span[0]
			for _, value := range span[1:] {
				minimumSpan = math.Min(minimumSpan, value)
			}
			if minimumSpan > domeAstrodomeClearanceRoundoff(0, 0, spanScale) {
				certifiedTwoKilometreTop = true
				break
			}
		}
		// WMO returns the first qualifying lower level. If this candidate is
		// strictly certified at every native corner, convex bilinear
		// reconstruction proves that it qualifies throughout the cell. Later
		// candidates are then unreachable and their algebraic zeros must not
		// become artificial physical breakpoints.
		if candidateCertifiedEverywhere && certifiedTwoKilometreTop {
			wmoGuaranteed = true
			break
		}
	}
	return fields, wmoGuaranteed, nil
}

func domeAppendAstrodomeCrossingField(
	fields *[]domeAstrodomeScalarField,
	id string,
	corners [4]float64,
	roundoffOperandScale float64,
) {
	domeAppendAstrodomeEvaluatedCrossingField(fields, id, corners, roundoffOperandScale, nil)
}

func domeAppendAstrodomeEvaluatedCrossingField(
	fields *[]domeAstrodomeScalarField,
	id string,
	corners [4]float64,
	roundoffOperandScale float64,
	evaluate func([4]float64) float64,
) {
	minimum, maximum := corners[0], corners[0]
	for _, value := range corners[1:] {
		minimum = math.Min(minimum, value)
		maximum = math.Max(maximum, value)
	}
	uncertainty := domeAstrodomeClearanceRoundoff(0, 0, roundoffOperandScale)
	// Bilinear reconstruction is a convex combination inside one native cell.
	// A predicate is removable only when every corner is separated from zero
	// by more than its unit-aware cancellation enclosure. Uncertain same-sign
	// corners are retained and passed to the fail-closed root isolator.
	if minimum <= uncertainty && maximum >= -uncertainty {
		*fields = append(*fields, domeAstrodomeScalarField{
			id: id, corners: corners, roundoffOperandScale: roundoffOperandScale, evaluate: evaluate,
		})
	}
}

func compactDomePhysicalRootCandidates(
	values []domeAstrodomePhysicalRootCandidate,
) ([]domeAstrodomePhysicalRootCandidate, error) {
	for index := range values {
		if values[index].evidence.pathM == 0 && values[index].evidence.leftM == 0 &&
			values[index].evidence.rightM == 0 && values[index].pathM != 0 {
			values[index].evidence = domeAstrodomeRootEvidence{
				pathM:  values[index].pathM,
				leftM:  values[index].pathM,
				rightM: values[index].pathM,
				exact:  true,
			}
		}
	}
	sort.Slice(values, func(left, right int) bool {
		if values[left].evidence.pathM == values[right].evidence.pathM {
			return values[left].eventID < values[right].eventID
		}
		return values[left].evidence.pathM < values[right].evidence.pathM
	})
	result := values[:0]
	for _, candidate := range values {
		evidence := candidate.evidence
		if !finiteDomeVolume(evidence.pathM) || !finiteDomeVolume(evidence.leftM) ||
			!finiteDomeVolume(evidence.rightM) || evidence.leftM > evidence.pathM ||
			evidence.pathM > evidence.rightM || strings.TrimSpace(candidate.eventID) == "" ||
			(!evidence.exact && math.Max(evidence.pathM-evidence.leftM, evidence.rightM-evidence.pathM) >
				domeAstrodomePhysicalRootToleranceM) {
			return nil, fmt.Errorf("%w: invalid ICON-EU physical event candidate",
				forecast.ErrAstrodomeScienceIncompletePartition)
		}
		if len(result) == 0 || evidence.pathM-result[len(result)-1].evidence.pathM > domeAstrodomePhysicalMergeToleranceM {
			result = append(result, candidate)
			continue
		}
		previous := &result[len(result)-1]
		if evidence.pathM == previous.evidence.pathM && evidence.leftM == previous.evidence.leftM &&
			evidence.rightM == previous.evidence.rightM && evidence.exact == previous.evidence.exact {
			// Only identical retained enclosures can describe one geometric
			// section. Bit-distinct representatives are never merged.
			previous.eventID = compoundDomePhysicalEventIDs(previous.eventID, candidate.eventID)
			previous.checks = append(previous.checks, candidate.checks...)
			continue
		}
		return nil, fmt.Errorf("%w: distinct ICON-EU physical events %q and %q are %.9g m apart inside the %.9g m root cluster",
			forecast.ErrAstrodomeScienceIncompletePartition, previous.eventID, candidate.eventID,
			evidence.pathM-previous.evidence.pathM, domeAstrodomePhysicalMergeToleranceM)
	}
	return result, nil
}

func compoundDomePhysicalEventIDs(left, right string) string {
	seen := make(map[string]struct{}, 2)
	for _, value := range append(strings.Split(left, "|"), strings.Split(right, "|")...) {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	values := make([]string, 0, len(seen))
	for value := range seen {
		values = append(values, value)
	}
	sort.Strings(values)
	return strings.Join(values, "|")
}

func domePhysicalRootCandidatePaths(values []domeAstrodomePhysicalRootCandidate) []float64 {
	result := make([]float64, len(values))
	for index := range values {
		result[index] = values[index].evidence.pathM
	}
	return result
}

func domeValidatePhysicalBreakpointsWithEvidence(
	interval domeAstrodomeCellInterval,
	breakpoints []domeAstrodomePhysicalRootCandidate,
	sampler *domeAstrodomePhysicalSampler,
	verifier *domeAstrodomeShortPanelVerifier,
) ([]forecast.AstrodomeScienceCertifiedShortInterval, error) {
	start := interval.startEvidence
	if strings.TrimSpace(start.eventID) == "" {
		start = domeAstrodomePathEvent{
			evidence: domeAstrodomeRootEvidence{
				pathM: interval.startM, leftM: interval.startM, rightM: interval.startM, exact: true,
			},
			eventID: "horizontal-cell/start", kind: "horizontal",
		}
	}
	end := interval.endEvidence
	if strings.TrimSpace(end.eventID) == "" {
		end = domeAstrodomePathEvent{
			evidence: domeAstrodomeRootEvidence{
				pathM: interval.endM, leftM: interval.endM, rightM: interval.endM, exact: true,
			},
			eventID: "horizontal-cell/end", kind: "horizontal",
		}
	}
	events := make([]domeAstrodomePathEvent, 0, len(breakpoints)+2)
	events = append(events, start)
	for _, breakpoint := range breakpoints {
		events = append(events, domePhysicalCandidateEvent(breakpoint))
	}
	events = append(events, end)
	certificates := make([]forecast.AstrodomeScienceCertifiedShortInterval, 0, 1)
	for index := 0; index+1 < len(events); index++ {
		left, right := events[index], events[index+1]
		if err := domeValidateAstrodomePathEvent(left, interval.startM, interval.endM); err != nil {
			return nil, err
		}
		if err := domeValidateAstrodomePathEvent(right, interval.startM, interval.endM); err != nil {
			return nil, err
		}
		gapM := right.evidence.pathM - left.evidence.pathM
		if gapM <= domeAstrodomePhysicalMergeToleranceM {
			return nil, fmt.Errorf("%w: ICON-EU events %q and %q are %.9g m apart inside the %.9g m root cluster",
				forecast.ErrAstrodomeScienceIncompletePartition, left.eventID, right.eventID,
				gapM, domeAstrodomePhysicalMergeToleranceM)
		}
		if gapM > forecast.AstrodomeScienceMinimumEventIntervalLengthM {
			continue
		}
		certificate, err := domeBuildAstrodomeShortPanelCertificate(
			interval, left, right, sampler, verifier,
		)
		if err != nil {
			return nil, err
		}
		certificates = append(certificates, certificate)
	}
	return certificates, nil
}

func domeValidatePhysicalBreakpoints(
	interval domeAstrodomeCellInterval,
	breakpoints []domeAstrodomePhysicalRootCandidate,
) error {
	previousM := interval.startM
	previousID := "horizontal-cell/start"
	for index := 0; index <= len(breakpoints); index++ {
		pathM, eventID := interval.endM, "horizontal-cell/end"
		if index < len(breakpoints) {
			pathM, eventID = breakpoints[index].pathM, breakpoints[index].eventID
			if pathM == 0 {
				pathM = breakpoints[index].evidence.pathM
			}
		}
		gapM := pathM - previousM
		if gapM <= forecast.AstrodomeScienceMinimumEventIntervalLengthM {
			return fmt.Errorf("%w: ICON-EU physical-root gap %.9g m between %q and %q does not clear the ordinary guard",
				forecast.ErrAstrodomeScienceIncompletePartition, gapM, previousID, eventID)
		}
		previousM, previousID = pathM, eventID
	}
	return nil
}

func domeValidateAstrodomePathEvent(event domeAstrodomePathEvent, startM, endM float64) error {
	evidence := event.evidence
	if strings.TrimSpace(event.eventID) == "" || strings.TrimSpace(event.kind) == "" ||
		!finiteDomeVolume(evidence.pathM) || !finiteDomeVolume(evidence.leftM) ||
		!finiteDomeVolume(evidence.rightM) || evidence.pathM < startM || evidence.pathM > endM ||
		evidence.leftM > evidence.pathM || evidence.pathM > evidence.rightM {
		return fmt.Errorf("%w: invalid retained ICON-EU path event",
			forecast.ErrAstrodomeScienceIncompletePartition)
	}
	if evidence.exact {
		if evidence.leftM != evidence.pathM || evidence.rightM != evidence.pathM {
			return fmt.Errorf("%w: exact ICON-EU event %q has a nonzero evidence enclosure",
				forecast.ErrAstrodomeScienceIncompletePartition, event.eventID)
		}
	} else if math.Max(evidence.pathM-evidence.leftM, evidence.rightM-evidence.pathM) >
		domeAstrodomePhysicalRootToleranceM {
		return fmt.Errorf("%w: ICON-EU event %q exceeds the %.9g m evidence radius",
			forecast.ErrAstrodomeScienceIncompletePartition, event.eventID,
			domeAstrodomePhysicalRootToleranceM)
	}
	return nil
}

func domeBuildAstrodomeShortPanelCertificate(
	interval domeAstrodomeCellInterval,
	start, end domeAstrodomePathEvent,
	sampler *domeAstrodomePhysicalSampler,
	verifier *domeAstrodomeShortPanelVerifier,
) (forecast.AstrodomeScienceCertifiedShortInterval, error) {
	if sampler == nil || verifier == nil || verifier.volume == nil ||
		end.evidence.leftM <= start.evidence.rightM {
		return forecast.AstrodomeScienceCertifiedShortInterval{}, fmt.Errorf(
			"%w: short interval %q..%q has no separated evidence enclosures",
			forecast.ErrAstrodomeScienceIncompletePartition, start.eventID, end.eventID,
		)
	}
	for _, endpoint := range []domeAstrodomePathEvent{start, end} {
		switch endpoint.kind {
		case "physical":
			if len(endpoint.checks) == 0 {
				return forecast.AstrodomeScienceCertifiedShortInterval{}, fmt.Errorf(
					"%w: physical short-panel endpoint %q has no residual-sign proof",
					forecast.ErrAstrodomeScienceIncompletePartition, endpoint.eventID,
				)
			}
		case "horizontal", "path-endpoint":
		default:
			return forecast.AstrodomeScienceCertifiedShortInterval{}, fmt.Errorf(
				"%w: unsupported short-panel endpoint kind %q",
				forecast.ErrAstrodomeScienceIncompletePartition, endpoint.kind,
			)
		}
	}
	middleM := start.evidence.rightM + (end.evidence.leftM-start.evidence.rightM)/2
	startUncertaintyM, err := verifier.domeAstrodomeNodePathUncertainty(start.evidence.rightM, false)
	if err != nil {
		return forecast.AstrodomeScienceCertifiedShortInterval{}, err
	}
	middleUncertaintyM, err := verifier.domeAstrodomeNodePathUncertainty(middleM, false)
	if err != nil {
		return forecast.AstrodomeScienceCertifiedShortInterval{}, err
	}
	endUncertaintyM, err := verifier.domeAstrodomeNodePathUncertainty(end.evidence.leftM, false)
	if err != nil {
		return forecast.AstrodomeScienceCertifiedShortInterval{}, err
	}
	startUncertaintyM = math.Nextafter(math.Max(startUncertaintyM, middleUncertaintyM), math.Inf(1))
	endUncertaintyM = math.Nextafter(math.Max(endUncertaintyM, middleUncertaintyM), math.Inf(1))
	openStartM := math.Nextafter(start.evidence.rightM+startUncertaintyM, math.Inf(1))
	openEndM := math.Nextafter(end.evidence.leftM-endUncertaintyM, math.Inf(-1))
	leftNodeM := math.Nextafter(openStartM, math.Inf(1))
	rightNodeM := math.Nextafter(openEndM, math.Inf(-1))
	if !finiteDomeVolume(openStartM) || !finiteDomeVolume(openEndM) ||
		openStartM >= openEndM || domeAstrodomeRepresentablePathCapacity(leftNodeM, rightNodeM) < 2 {
		return forecast.AstrodomeScienceCertifiedShortInterval{}, fmt.Errorf(
			"%w: evidence-aware interval %q..%q has no representable open safe domain",
			forecast.ErrAstrodomeScienceIncompletePartition, start.eventID, end.eventID,
		)
	}
	referenceM := leftNodeM + (rightNodeM-leftNodeM)/2
	checks := append([]domeAstrodomeShortPanelResidualCheck(nil), start.checks...)
	checks = append(checks, end.checks...)
	for index := range checks {
		if strings.TrimSpace(checks[index].eventID) == "" || checks[index].valueAt == nil ||
			!finiteDomeVolume(checks[index].roundoffScale) || checks[index].roundoffScale <= 0 {
			return forecast.AstrodomeScienceCertifiedShortInterval{}, fmt.Errorf(
				"%w: short interval contains a malformed physical residual verifier",
				forecast.ErrAstrodomeScienceIncompletePartition,
			)
		}
		residual, residualErr := checks[index].valueAt(referenceM)
		if residualErr != nil {
			return forecast.AstrodomeScienceCertifiedShortInterval{}, residualErr
		}
		sign, _, _ := domeAstrodomeResidualSignInterval(residual, checks[index].roundoffScale)
		if sign == 0 {
			return forecast.AstrodomeScienceCertifiedShortInterval{}, fmt.Errorf(
				"%w: physical residual %q is indeterminate in the short-panel reference",
				forecast.ErrAstrodomeScienceIncompletePartition, checks[index].eventID,
			)
		}
		checks[index].expectedSign = sign
	}
	certificate := forecast.AstrodomeScienceCertifiedShortInterval{
		CertificateID: fmt.Sprintf("%s/%016x-%016x", interval.cellID,
			math.Float64bits(start.evidence.pathM), math.Float64bits(end.evidence.pathM)),
		HorizontalCellID:   interval.cellID,
		Start:              domeForecastRootEvidence(start),
		End:                domeForecastRootEvidence(end),
		OpenSafeStartPathM: openStartM,
		OpenSafeEndPathM:   openEndM,
	}
	if _, exists := verifier.proofs[certificate.CertificateID]; exists {
		return forecast.AstrodomeScienceCertifiedShortInterval{}, fmt.Errorf(
			"%w: duplicate short-panel proof identity %q",
			forecast.ErrAstrodomeScienceIncompletePartition, certificate.CertificateID,
		)
	}
	proof := domeAstrodomeShortPanelProof{certificate: certificate, checks: checks}
	verifier.proofs[certificate.CertificateID] = proof
	if err := verifier.VerifyAstrodomeScienceShortIntervalNode(
		sampler.ctx, sampler.validAt, referenceM, certificate,
	); err != nil {
		delete(verifier.proofs, certificate.CertificateID)
		return forecast.AstrodomeScienceCertifiedShortInterval{}, err
	}
	return certificate, nil
}

func (verifier *domeAstrodomeShortPanelVerifier) domeAstrodomeNodePathUncertainty(
	pathM float64,
	checkCell bool,
) (float64, error) {
	if verifier == nil || verifier.volume == nil || !finiteDomeVolume(pathM) {
		return 0, errors.New("invalid ICON-EU short-panel uncertainty request")
	}
	point, err := verifier.ray.PointAtPathLength(pathM)
	if err != nil {
		return 0, err
	}
	positionErrorM, err := verifier.ray.PositionEvaluationErrorUpperBound(pathM)
	if err != nil {
		return 0, err
	}
	coordinateBounds, err := domeAstrodomeCoordinateEvaluationErrorBounds(
		point, positionErrorM, verifier.volume.manifest.Grid, checkCell,
	)
	if err != nil {
		return 0, err
	}
	uncertaintyM := math.Nextafter(
		math.Max(positionErrorM, coordinateBounds.equivalentPositionM), math.Inf(1),
	)
	if !finiteDomeVolume(uncertaintyM) || uncertaintyM < 0 ||
		uncertaintyM > domeAstrodomePositionCoordinateEnvelopeM {
		return 0, fmt.Errorf("%w: ICON-EU short-panel path/coordinate uncertainty %.9g m is invalid",
			forecast.ErrAstrodomeScienceIncompletePartition, uncertaintyM)
	}
	return uncertaintyM, nil
}

func (verifier *domeAstrodomeShortPanelVerifier) VerifyAstrodomeScienceShortIntervalNode(
	ctx context.Context,
	validAt time.Time,
	pathM float64,
	certificate forecast.AstrodomeScienceCertifiedShortInterval,
) error {
	if verifier == nil || verifier.volume == nil || errContext(ctx) != nil {
		if err := errContext(ctx); err != nil {
			return err
		}
		return errors.New("ICON-EU short-panel verifier is unavailable")
	}
	if !validAt.Equal(verifier.validAt) {
		return errors.New("ICON-EU short-panel verifier hour changed")
	}
	proof, ok := verifier.proofs[certificate.CertificateID]
	if !ok || !domeAstrodomeShortCertificateEqual(proof.certificate, certificate) {
		return errors.New("ICON-EU short-panel certificate is unknown or mutated")
	}
	if !finiteDomeVolume(pathM) || pathM <= certificate.OpenSafeStartPathM ||
		pathM >= certificate.OpenSafeEndPathM {
		return errors.New("ICON-EU short-panel node escaped the declared open safe interval")
	}
	uncertaintyM, err := verifier.domeAstrodomeNodePathUncertainty(pathM, true)
	if err != nil {
		return err
	}
	lowerM := math.Nextafter(pathM-uncertaintyM, math.Inf(-1))
	upperM := math.Nextafter(pathM+uncertaintyM, math.Inf(1))
	if lowerM <= certificate.Start.RightPathM || upperM >= certificate.End.LeftPathM {
		return errors.New("ICON-EU short-panel node uncertainty intersects a root enclosure")
	}
	point, err := verifier.ray.PointAtPathLength(pathM)
	if err != nil {
		return err
	}
	stencil, err := verifier.volume.HorizontalStencil(ctx, point.Location)
	if err != nil {
		return err
	}
	cellID, err := verifier.volume.HorizontalCellID(stencil)
	if err != nil {
		return err
	}
	if cellID != certificate.HorizontalCellID {
		return fmt.Errorf("ICON-EU short-panel node changed horizontal cell to %q", cellID)
	}
	return domeVerifyAstrodomeShortPanelResidualChecks(pathM, proof.checks)
}

func domeVerifyAstrodomeShortPanelResidualChecks(
	pathM float64,
	checks []domeAstrodomeShortPanelResidualCheck,
) error {
	for _, check := range checks {
		if check.valueAt == nil || check.expectedSign == 0 ||
			!finiteDomeVolume(check.roundoffScale) || check.roundoffScale <= 0 {
			return errors.New("ICON-EU short-panel residual proof is malformed")
		}
		residual, residualErr := check.valueAt(pathM)
		if residualErr != nil {
			return residualErr
		}
		sign, _, _ := domeAstrodomeResidualSignInterval(residual, check.roundoffScale)
		if sign == 0 {
			return fmt.Errorf("ICON-EU short-panel residual %q is numerically indeterminate", check.eventID)
		}
		if sign != check.expectedSign {
			return fmt.Errorf("ICON-EU short-panel residual %q changed branch sign", check.eventID)
		}
	}
	return nil
}

func domeAstrodomeShortCertificateEqual(
	left, right forecast.AstrodomeScienceCertifiedShortInterval,
) bool {
	return left.CertificateID == right.CertificateID &&
		left.HorizontalCellID == right.HorizontalCellID &&
		domeAstrodomeForecastRootEvidenceEqual(left.Start, right.Start) &&
		domeAstrodomeForecastRootEvidenceEqual(left.End, right.End) &&
		math.Float64bits(left.OpenSafeStartPathM) == math.Float64bits(right.OpenSafeStartPathM) &&
		math.Float64bits(left.OpenSafeEndPathM) == math.Float64bits(right.OpenSafeEndPathM)
}

func domeAstrodomeForecastRootEvidenceEqual(
	left, right forecast.AstrodomeScienceRootEvidence,
) bool {
	return math.Float64bits(left.RepresentativePathM) == math.Float64bits(right.RepresentativePathM) &&
		math.Float64bits(left.LeftPathM) == math.Float64bits(right.LeftPathM) &&
		math.Float64bits(left.RightPathM) == math.Float64bits(right.RightPathM) &&
		left.EventID == right.EventID && left.Kind == right.Kind && left.Exact == right.Exact
}

func domeAstrodomeRepresentablePathCapacity(leftPathM, rightPathM float64) uint64 {
	if !finiteDomeVolume(leftPathM) || !finiteDomeVolume(rightPathM) || rightPathM < leftPathM {
		return 0
	}
	leftKey := domeAstrodomeOrderedFloatKey(leftPathM)
	rightKey := domeAstrodomeOrderedFloatKey(rightPathM)
	if rightKey < leftKey {
		return 0
	}
	distance := rightKey - leftKey
	if distance == ^uint64(0) {
		return distance
	}
	return distance + 1
}

func domeAstrodomeOrderedFloatKey(value float64) uint64 {
	bits := math.Float64bits(value)
	if bits&(uint64(1)<<63) != 0 {
		return ^bits
	}
	return bits | (uint64(1) << 63)
}

func errContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("ICON-EU short-panel verifier context is nil")
	}
	return ctx.Err()
}

func (sampler *domeAstrodomePhysicalSampler) sample(pathM float64, needNative bool) (*domeAstrodomePhysicalSample, error) {
	if err := sampler.ctx.Err(); err != nil {
		return nil, err
	}
	key := math.Float64bits(pathM)
	sample, ok := sampler.cache[key]
	if !ok {
		point, err := sampler.ray.PointAtPathLength(pathM)
		if err != nil {
			return nil, err
		}
		positionEvaluationErrorM, err := sampler.ray.PositionEvaluationErrorUpperBound(pathM)
		if err != nil {
			return nil, err
		}
		coordinateBounds, err := domeAstrodomeCoordinateEvaluationErrorBounds(
			point, positionEvaluationErrorM, sampler.volume.manifest.Grid, true,
		)
		if err != nil {
			return nil, err
		}
		if coordinateBounds.equivalentPositionM > domeAstrodomePositionCoordinateEnvelopeM {
			return nil, fmt.Errorf("%w: ICON-EU Astrodome position/coordinate evaluation bound %.9g m exceeds the %.9g m contract",
				forecast.ErrAstrodomeScienceIncompletePartition,
				coordinateBounds.equivalentPositionM, domeAstrodomePositionCoordinateEnvelopeM)
		}
		stencil, err := sampler.volume.HorizontalStencil(sampler.ctx, point.Location)
		if err != nil {
			return nil, err
		}
		cellID, err := sampler.volume.HorizontalCellID(stencil)
		if err != nil {
			return nil, err
		}
		if cellID != sampler.cellID {
			return nil, errors.New("ICON-EU Astrodome physical root escaped its horizontal cell")
		}
		sample = &domeAstrodomePhysicalSample{
			point: point, positionEvaluationErrorM: positionEvaluationErrorM,
			coordinateBounds: coordinateBounds, stencil: stencil, weights: domeStencilWeights(stencil),
		}
		sampler.cache[key] = sample
	}
	if needNative && !sample.nativeReady {
		native, err := sampler.volume.ResolveAstrodomeScienceNativeContext(
			sampler.ctx, sampler.validAt, sample.point.AstrodomeRayPoint, sample.stencil,
		)
		if err != nil {
			return nil, err
		}
		heights, err := forecast.ResolveAstrodomeScienceBoundaryHeights(native, sampler.calibration)
		if err != nil {
			return nil, err
		}
		sample.native, sample.heights, sample.nativeReady = native, heights, true
	}
	return sample, nil
}

// domeAstrodomeIsolatePathRoots recursively covers the complete interval; it
// never skips a neighbourhood around a root. The supplied Lipschitz constant
// bounds |df/ds|, so midpoint clearance greater than L*h certifies the whole
// half-width h as root-free. At the family-specific published localization
// radius and 0.2-mm recursive proof floor, an unresolved
// interval fails closed: neither proximity to nor overlap with evidenced root
// brackets proves uniqueness, so it may still contain a tangency or paired
// roots and must not be silently absorbed.
func domeAstrodomeIsolatePathRoots(
	leftM, rightM, leftValue, rightValue, lipschitz, roundoffOperandScale float64,
	remaining *int,
	value func(float64) (float64, error),
) ([]float64, error) {
	residualValue := func(pathM float64) (domeAstrodomeResidualSample, error) {
		result, err := value(pathM)
		return domeAstrodomeResidualSample{value: result}, err
	}
	return domeAstrodomeIsolatePathRootsWithSlopeBoundsResiduals(
		leftM, rightM,
		domeAstrodomeResidualSample{value: leftValue},
		domeAstrodomeResidualSample{value: rightValue},
		lipschitz, roundoffOperandScale, nil, remaining, residualValue,
	)
}

func domeAstrodomeIsolatePathRootsWithSlopeBoundsResiduals(
	leftM, rightM float64,
	leftSample, rightSample domeAstrodomeResidualSample,
	lipschitz, roundoffOperandScale float64,
	slopeBounds domeAstrodomeResidualSlopeBounds,
	remaining *int,
	value domeAstrodomeResidualValue,
) ([]float64, error) {
	result, err := domeAstrodomeIsolatePathRootsRecursive(
		leftM, rightM, leftSample, rightSample, lipschitz, roundoffOperandScale,
		domeAstrodomePhysicalRootToleranceM, slopeBounds, remaining, value,
	)
	if err != nil {
		return nil, err
	}
	return domeAstrodomeResolveRootIsolation(result, rightM)
}

func domeAstrodomeIsolatePathRootsWithSlopeBounds(
	leftM, rightM, leftValue, rightValue, lipschitz, roundoffOperandScale float64,
	slopeBounds domeAstrodomeSlopeBounds,
	remaining *int,
	value func(float64) (float64, error),
) ([]float64, error) {
	residualValue := func(pathM float64) (domeAstrodomeResidualSample, error) {
		result, err := value(pathM)
		return domeAstrodomeResidualSample{value: result}, err
	}
	var residualSlope domeAstrodomeResidualSlopeBounds
	if slopeBounds != nil {
		residualSlope = func(
			localLeftM, localRightM float64,
			leftSample, rightSample domeAstrodomeResidualSample,
		) (float64, float64, error) {
			return slopeBounds(localLeftM, localRightM, leftSample.value, rightSample.value)
		}
	}
	return domeAstrodomeIsolatePathRootsWithSlopeBoundsResiduals(
		leftM, rightM,
		domeAstrodomeResidualSample{value: leftValue},
		domeAstrodomeResidualSample{value: rightValue},
		lipschitz, roundoffOperandScale, residualSlope, remaining, residualValue,
	)
}

func domeAstrodomeResolveRootIsolation(
	result domeAstrodomeRootIsolationResult,
	pathEndM float64,
) ([]float64, error) {
	rootEvidence, err := domeAstrodomeResolveRootIsolationEvidence(result, pathEndM)
	if err != nil {
		return nil, err
	}
	roots := make([]float64, len(rootEvidence))
	for index := range rootEvidence {
		roots[index] = rootEvidence[index].pathM
	}
	return roots, nil
}

func domeAstrodomeResolveRootIsolationEvidence(
	result domeAstrodomeRootIsolationResult,
	pathEndM float64,
) ([]domeAstrodomeRootEvidence, error) {
	rootEvidence, err := compactDomeSameEventRoots(result.roots, pathEndM)
	if err != nil {
		return nil, err
	}
	if len(result.unresolved) > 0 {
		interval := result.unresolved[0]
		return nil, fmt.Errorf("%w: physical boundary interval [%.12g, %.12g] m remains unresolved inside the %.9g m proof tolerance",
			forecast.ErrAstrodomeScienceIncompletePartition, interval.leftM, interval.rightM,
			domeAstrodomeRootProofToleranceM)
	}
	rootEvidence, err = compactDomeSameEventRoots(rootEvidence, pathEndM)
	if err != nil {
		return nil, err
	}
	return rootEvidence, nil
}

func compactDomeSameEventRoots(
	values []domeAstrodomeRootEvidence,
	pathEndM float64,
) ([]domeAstrodomeRootEvidence, error) {
	sort.Slice(values, func(left, right int) bool {
		if values[left].pathM != values[right].pathM {
			return values[left].pathM < values[right].pathM
		}
		if values[left].leftM != values[right].leftM {
			return values[left].leftM < values[right].leftM
		}
		return values[left].rightM < values[right].rightM
	})
	result := values[:0]
	for _, evidence := range values {
		if !finiteDomeVolume(evidence.pathM) || !finiteDomeVolume(evidence.leftM) ||
			!finiteDomeVolume(evidence.rightM) || evidence.pathM < 0 || evidence.pathM > pathEndM ||
			evidence.leftM < 0 || evidence.rightM > pathEndM || evidence.leftM > evidence.pathM ||
			evidence.pathM > evidence.rightM {
			return nil, fmt.Errorf("%w: invalid ICON-EU physical root evidence",
				forecast.ErrAstrodomeScienceIncompletePartition)
		}
		if len(result) == 0 || evidence.pathM-result[len(result)-1].pathM > domeAstrodomePhysicalMergeToleranceM {
			result = append(result, evidence)
			continue
		}
		previous := result[len(result)-1]
		if evidence.pathM == previous.pathM && evidence.leftM == previous.leftM &&
			evidence.rightM == previous.rightM && evidence.exact == previous.exact {
			// Only bit-identical evidence from an identical recursive traversal
			// is a duplicate. Equal representatives with different brackets are
			// not enough to prove that a second same-event root is absent.
			continue
		}
		return nil, fmt.Errorf("%w: one ICON-EU scalar field has non-identical roots %.9g m apart inside the %.9g m root cluster",
			forecast.ErrAstrodomeScienceIncompletePartition, evidence.pathM-previous.pathM,
			domeAstrodomePhysicalMergeToleranceM)
	}
	return result, nil
}

type domeAstrodomeRootIsolationInterval struct {
	leftM  float64
	rightM float64
}

type domeAstrodomeRootIsolationResult struct {
	roots      []domeAstrodomeRootEvidence
	unresolved []domeAstrodomeRootIsolationInterval
}

// domeAstrodomeSlopeBounds encloses every derivative value of one scalar field
// on [leftM,rightM]. A strictly positive or negative enclosure is therefore a
// complete monotonicity certificate; it is deliberately optional because a
// plain Lipschitz upper bound cannot prove uniqueness around a root.
type domeAstrodomeResidualSlopeBounds func(
	leftM, rightM float64,
	leftSample, rightSample domeAstrodomeResidualSample,
) (lower, upper float64, err error)

type domeAstrodomeSlopeBounds func(
	leftM, rightM, leftValue, rightValue float64,
) (lower, upper float64, err error)

// domeAstrodomeEndpointSlopeBounds encloses every derivative of one scalar
// field on an omitted one-sided probe sliver. It deliberately does not need a
// value at the horizontal-cell endpoint: that endpoint has ambiguous native
// ownership inside the horizontal root's localization enclosure. A strict
// derivative direction can nevertheless prove that the already certified
// interior sample moves away from zero all the way to the endpoint.
type domeAstrodomeEndpointSlopeBounds func(
	leftM, rightM float64,
) (lower, upper float64, err error)

func domeAstrodomeResolveMonotoneRootInterval(
	leftM, rightM float64,
	leftSample, rightSample domeAstrodomeResidualSample,
	roundoffOperandScale float64,
	rootToleranceM float64,
	slopeBounds domeAstrodomeResidualSlopeBounds,
) (domeAstrodomeRootIsolationResult, bool, error) {
	lowerSlope, upperSlope, err := slopeBounds(leftM, rightM, leftSample, rightSample)
	if err != nil {
		return domeAstrodomeRootIsolationResult{}, false, err
	}
	if !finiteDomeVolume(lowerSlope) || !finiteDomeVolume(upperSlope) || lowerSlope > upperSlope {
		return domeAstrodomeRootIsolationResult{}, false, fmt.Errorf(
			"%w: invalid Astrodome monotonic slope enclosure",
			forecast.ErrAstrodomeScienceIncompletePartition,
		)
	}
	direction := 0
	if lowerSlope > 0 {
		direction = 1
	} else if upperSlope < 0 {
		direction = -1
	} else {
		return domeAstrodomeRootIsolationResult{}, false, nil
	}
	leftSign, _, _ := domeAstrodomeResidualSignInterval(leftSample, roundoffOperandScale)
	rightSign, _, _ := domeAstrodomeResidualSignInterval(rightSample, roundoffOperandScale)
	if leftSign == 0 || rightSign == 0 {
		return domeAstrodomeRootIsolationResult{}, false, nil
	}
	if leftSign == rightSign {
		return domeAstrodomeRootIsolationResult{}, true, nil
	}
	if (direction > 0 && (leftSign > 0 || rightSign < 0)) ||
		(direction < 0 && (leftSign < 0 || rightSign > 0)) {
		return domeAstrodomeRootIsolationResult{}, false, fmt.Errorf(
			"%w: endpoint signs contradict the certified Astrodome slope",
			forecast.ErrAstrodomeScienceIncompletePartition,
		)
	}
	middleM := leftM + (rightM-leftM)/2
	if math.Max(middleM-leftM, rightM-middleM) > rootToleranceM {
		return domeAstrodomeRootIsolationResult{}, false, nil
	}
	return domeAstrodomeRootIsolationResult{roots: []domeAstrodomeRootEvidence{{
		pathM: middleM, leftM: leftM, rightM: rightM,
	}}}, true, nil
}

func domeAstrodomeResidualInterval(
	sample domeAstrodomeResidualSample,
	roundoffOperandScale, lipschitzVariation float64,
) (float64, float64) {
	roundoff := domeAstrodomeClearanceRoundoff(sample.value, lipschitzVariation, roundoffOperandScale)
	totalError := domeAstrodomePositiveAddUpper(sample.evaluationError, roundoff)
	lower := math.Nextafter(sample.value-totalError, math.Inf(-1))
	upper := math.Nextafter(sample.value+totalError, math.Inf(1))
	return lower, upper
}

func domeAstrodomeResidualSignInterval(
	sample domeAstrodomeResidualSample,
	roundoffOperandScale float64,
) (int, float64, float64) {
	lower, upper := domeAstrodomeResidualInterval(sample, roundoffOperandScale, 0)
	if lower > 0 {
		return 1, lower, upper
	}
	if upper < 0 {
		return -1, lower, upper
	}
	return 0, lower, upper
}

// domeAstrodomeLocalizeCertifiedMonotoneRoot refines a root whose existence
// and uniqueness were already proved on an ancestor interval by strict,
// opposite endpoint signs and a derivative enclosure excluding zero. That
// proof must not be discarded merely because a later micrometre-scale sample
// interval contains zero after floating-point error is enclosed.
//
// Interval-valued evaluations are refined with safeguarded interval Newton.
// When one midpoint remains indeterminate, two interior probes retain ordinary
// monotone bisection evidence on both sides. A nominal near-zero value is never
// promoted to an exact root, and refinement fails closed if the evaluation
// information floor remains wider than the caller's localization diameter.
func domeAstrodomeLocalizeCertifiedMonotoneRoot(
	leftM, rightM, leftValue, rightValue, lowerSlope, upperSlope, roundoffOperandScale float64,
	remaining *int,
	value func(float64) (float64, error),
) (domeAstrodomeRootIsolationResult, error) {
	residualValue := func(pathM float64) (domeAstrodomeResidualSample, error) {
		result, err := value(pathM)
		return domeAstrodomeResidualSample{value: result}, err
	}
	return domeAstrodomeLocalizeCertifiedMonotoneRootResiduals(
		leftM, rightM,
		domeAstrodomeResidualSample{value: leftValue},
		domeAstrodomeResidualSample{value: rightValue},
		lowerSlope, upperSlope, roundoffOperandScale, domeAstrodomePhysicalRootToleranceM,
		remaining, residualValue,
	)
}

func domeAstrodomeLocalizeCertifiedMonotoneRootResiduals(
	leftM, rightM float64,
	leftSample, rightSample domeAstrodomeResidualSample,
	lowerSlope, upperSlope, roundoffOperandScale float64,
	rootToleranceM float64,
	remaining *int,
	value domeAstrodomeResidualValue,
) (domeAstrodomeRootIsolationResult, error) {
	certificateLeftM, certificateRightM := leftM, rightM
	localizationDiameterM := 2 * rootToleranceM
	direction := 0
	minimumSlopeMagnitude := 0.0
	if lowerSlope > 0 {
		direction = 1
		minimumSlopeMagnitude = lowerSlope
	} else if upperSlope < 0 {
		direction = -1
		minimumSlopeMagnitude = -upperSlope
	}
	leftSign, _, _ := domeAstrodomeResidualSignInterval(leftSample, roundoffOperandScale)
	rightSign, _, _ := domeAstrodomeResidualSignInterval(rightSample, roundoffOperandScale)
	if !finiteDomeVolume(leftM) || !finiteDomeVolume(rightM) || rightM <= leftM ||
		!finiteDomeVolume(leftSample.value) || !finiteDomeVolume(rightSample.value) ||
		!finiteDomeVolume(leftSample.evaluationError) || leftSample.evaluationError < 0 ||
		!finiteDomeVolume(rightSample.evaluationError) || rightSample.evaluationError < 0 ||
		!finiteDomeVolume(lowerSlope) || !finiteDomeVolume(upperSlope) || lowerSlope > upperSlope ||
		!finiteDomeVolume(roundoffOperandScale) || roundoffOperandScale <= 0 ||
		!finiteDomeVolume(rootToleranceM) || rootToleranceM <= 0 ||
		!finiteDomeVolume(localizationDiameterM) || localizationDiameterM <= 0 ||
		remaining == nil || *remaining < 0 || value == nil ||
		direction == 0 || !finiteDomeVolume(minimumSlopeMagnitude) || minimumSlopeMagnitude <= 0 ||
		leftSign == 0 || rightSign == 0 || leftSign == rightSign ||
		(direction > 0 && (leftSign > 0 || rightSign < 0)) ||
		(direction < 0 && (leftSign < 0 || rightSign > 0)) {
		return domeAstrodomeRootIsolationResult{}, fmt.Errorf(
			"%w: invalid inherited monotone root certificate",
			forecast.ErrAstrodomeScienceIncompletePartition,
		)
	}

	// The strict endpoint signs and derivative enclosure above prove existence
	// and uniqueness once, on the complete ancestor interval. Refinement keeps
	// that proof while intersecting a separate root enclosure. Its endpoints do
	// not themselves need to retain point-evaluable signs: every interval-Newton
	// image below is a necessary enclosure for the same already-proved root.
	rootLeftM, rootRightM := leftM, rightM
	lastMaximumResidual := 0.0
	lastSampleValue, lastEvaluationError := 0.0, 0.0
	lastResidualLower, lastResidualUpper := 0.0, 0.0
	lastRoundoff := 0.0
	contractAt := func(pathM float64, sample domeAstrodomeResidualSample) (bool, error) {
		if !finiteDomeVolume(pathM) || pathM < certificateLeftM || pathM > certificateRightM ||
			!finiteDomeVolume(sample.value) || !finiteDomeVolume(sample.evaluationError) ||
			sample.evaluationError < 0 {
			return false, errors.New("non-finite Astrodome physical boundary value")
		}
		residualSign, residualLower, residualUpper := domeAstrodomeResidualSignInterval(
			sample, roundoffOperandScale,
		)
		lastSampleValue, lastEvaluationError = sample.value, sample.evaluationError
		lastResidualLower, lastResidualUpper = residualLower, residualUpper
		lastRoundoff = domeAstrodomeClearanceRoundoff(sample.value, 0, roundoffOperandScale)
		lastMaximumResidual = math.Max(math.Abs(residualLower), math.Abs(residualUpper))
		previousLeftM, previousRightM := rootLeftM, rootRightM

		// A strict interval sign is ordinary monotone bisection evidence. Do not
		// use the nominal sign when the residual interval contains zero.
		if residualSign != 0 {
			rootLiesRight := (direction > 0 && residualSign < 0) ||
				(direction < 0 && residualSign > 0)
			if rootLiesRight {
				rootLeftM = math.Max(rootLeftM, pathM)
			} else {
				rootRightM = math.Min(rootRightM, pathM)
			}
		}

		// Interval Newton remains valid with an interval-valued point
		// evaluation. For the unique root r and any sample x, the mean-value
		// theorem gives r=x-F(x)/f'(xi). Enclose all four endpoint quotients of
		// F(x)/D, round outwards, and intersect with the retained root enclosure.
		quotientLower, quotientUpper := math.Inf(1), math.Inf(-1)
		for _, residual := range [...]float64{residualLower, residualUpper} {
			for _, slope := range [...]float64{lowerSlope, upperSlope} {
				quotient := residual / slope
				if !finiteDomeVolume(quotient) {
					return false, fmt.Errorf(
						"%w: monotone interval-Newton quotient is invalid",
						forecast.ErrAstrodomeScienceIncompletePartition,
					)
				}
				quotientLower = math.Min(quotientLower, math.Nextafter(quotient, math.Inf(-1)))
				quotientUpper = math.Max(quotientUpper, math.Nextafter(quotient, math.Inf(1)))
			}
		}
		newtonLeftM := math.Nextafter(pathM-quotientUpper, math.Inf(-1))
		newtonRightM := math.Nextafter(pathM-quotientLower, math.Inf(1))
		rootLeftM = math.Max(rootLeftM, newtonLeftM)
		rootRightM = math.Min(rootRightM, newtonRightM)
		if !finiteDomeVolume(rootLeftM) || !finiteDomeVolume(rootRightM) || rootLeftM > rootRightM {
			return false, fmt.Errorf(
				"%w: monotone interval contraction contradicts the inherited root certificate",
				forecast.ErrAstrodomeScienceIncompletePartition,
			)
		}
		return rootLeftM > previousLeftM || rootRightM < previousRightM, nil
	}
	evaluateAt := func(pathM float64) (domeAstrodomeResidualSample, error) {
		if remaining == nil || *remaining <= 0 {
			return domeAstrodomeResidualSample{}, fmt.Errorf(
				"%w: physical root-isolation budget exhausted",
				forecast.ErrAstrodomeScienceIncompletePartition,
			)
		}
		(*remaining)--
		return value(pathM)
	}

	// Endpoint interval-Newton images use quantitative information which the
	// former sign-only initialization discarded. Both samples belong to the
	// ancestor derivative enclosure even if the first contraction places one
	// endpoint outside the subsequently smaller root enclosure.
	if _, err := contractAt(leftM, leftSample); err != nil {
		return domeAstrodomeRootIsolationResult{}, err
	}
	if _, err := contractAt(rightM, rightSample); err != nil {
		return domeAstrodomeRootIsolationResult{}, err
	}

	rootWidthUpper := func() float64 {
		return math.Nextafter(rootRightM-rootLeftM, math.Inf(1))
	}
	rootGeometry := func() (middleM, leftRadiusUpperM, rightRadiusUpperM float64) {
		middleM = rootLeftM + (rootRightM-rootLeftM)/2
		leftRadiusUpperM = middleM - rootLeftM
		if leftRadiusUpperM > 0 {
			leftRadiusUpperM = math.Nextafter(leftRadiusUpperM, math.Inf(1))
		}
		rightRadiusUpperM = rootRightM - middleM
		if rightRadiusUpperM > 0 {
			rightRadiusUpperM = math.Nextafter(rightRadiusUpperM, math.Inf(1))
		}
		return middleM, leftRadiusUpperM, rightRadiusUpperM
	}
	rootLocalized := func() bool {
		_, leftRadiusUpperM, rightRadiusUpperM := rootGeometry()
		return finiteDomeVolume(leftRadiusUpperM) && finiteDomeVolume(rightRadiusUpperM) &&
			leftRadiusUpperM <= rootToleranceM && rightRadiusUpperM <= rootToleranceM
	}
	for !rootLocalized() {
		middleM := rootLeftM + (rootRightM-rootLeftM)/2
		middleSample, err := evaluateAt(middleM)
		if err != nil {
			return domeAstrodomeRootIsolationResult{}, err
		}
		_, err = contractAt(middleM, middleSample)
		if err != nil {
			return domeAstrodomeRootIsolationResult{}, err
		}
		if rootLocalized() {
			break
		}

		// Any midpoint contraction that is still wider than the target is followed
		// by both sides of a central window. This prevents arbitrarily small but
		// nonzero Newton contractions from repeatedly postponing the off-centre
		// evidence until the complete isolation budget is exhausted. Strict signs
		// move the two root bounds independently; interval-valued probes receive
		// the same Newton contraction. Halve the enclosure per successful round. For
		// the final round request the preceding representable width below the
		// public diameter, so the outward width check cannot reject a window only
		// because its subtraction rounded to the target value.
		widthM := rootWidthUpper()
		finalWindowM := math.Nextafter(localizationDiameterM, 0)
		nextWidthM := widthM / 2
		if widthM > localizationDiameterM {
			nextWidthM = math.Max(finalWindowM, nextWidthM)
		}
		marginM := (widthM - nextWidthM) / 2
		if !finiteDomeVolume(marginM) || marginM <= 0 {
			return domeAstrodomeRootIsolationResult{}, fmt.Errorf(
				"%w: monotone root uncertainty %.9g m exceeds the %.9g m localization diameter (residual enclosure %.9g, minimum slope %.9g; last sample %.17g, evaluation error %.17g, scalar roundoff %.17g, interval [%.17g, %.17g])",
				forecast.ErrAstrodomeScienceIncompletePartition, widthM,
				localizationDiameterM, lastMaximumResidual, minimumSlopeMagnitude,
				lastSampleValue, lastEvaluationError, lastRoundoff, lastResidualLower, lastResidualUpper,
			)
		}
		probePaths := [2]float64{
			rootLeftM + marginM,
			rootRightM - marginM,
		}
		pairedContracted := false
		for _, probeM := range probePaths {
			if probeM <= rootLeftM || probeM >= rootRightM {
				continue
			}
			probeSample, probeErr := evaluateAt(probeM)
			if probeErr != nil {
				return domeAstrodomeRootIsolationResult{}, probeErr
			}
			probeContracted, probeErr := contractAt(probeM, probeSample)
			if probeErr != nil {
				return domeAstrodomeRootIsolationResult{}, probeErr
			}
			pairedContracted = pairedContracted || probeContracted
			if rootLocalized() {
				break
			}
		}
		if !pairedContracted && !rootLocalized() {
			return domeAstrodomeRootIsolationResult{}, fmt.Errorf(
				"%w: monotone root uncertainty %.9g m exceeds the %.9g m localization diameter (residual enclosure %.9g, minimum slope %.9g; last sample %.17g, evaluation error %.17g, scalar roundoff %.17g, interval [%.17g, %.17g])",
				forecast.ErrAstrodomeScienceIncompletePartition, rootWidthUpper(),
				localizationDiameterM, lastMaximumResidual, minimumSlopeMagnitude,
				lastSampleValue, lastEvaluationError, lastRoundoff, lastResidualLower, lastResidualUpper,
			)
		}
	}
	middleM, leftRadiusUpperM, rightRadiusUpperM := rootGeometry()
	if !finiteDomeVolume(middleM) || !finiteDomeVolume(leftRadiusUpperM) || leftRadiusUpperM < 0 ||
		!finiteDomeVolume(rightRadiusUpperM) || rightRadiusUpperM < 0 ||
		leftRadiusUpperM > rootToleranceM || rightRadiusUpperM > rootToleranceM {
		return domeAstrodomeRootIsolationResult{}, fmt.Errorf(
			"%w: monotone root uncertainty radii %.9g/%.9g m exceed the %.9g m localization radius",
			forecast.ErrAstrodomeScienceIncompletePartition,
			leftRadiusUpperM, rightRadiusUpperM, rootToleranceM,
		)
	}
	return domeAstrodomeRootIsolationResult{roots: []domeAstrodomeRootEvidence{{
		pathM: middleM, leftM: rootLeftM, rightM: rootRightM,
	}}}, nil
}

func domeAstrodomeIsolatePathRootsRecursive(
	leftM, rightM float64,
	leftSample, rightSample domeAstrodomeResidualSample,
	lipschitz, roundoffOperandScale, rootToleranceM float64,
	slopeBounds domeAstrodomeResidualSlopeBounds,
	remaining *int,
	value domeAstrodomeResidualValue,
) (domeAstrodomeRootIsolationResult, error) {
	if !finiteDomeVolume(leftM) || !finiteDomeVolume(rightM) || rightM <= leftM ||
		!finiteDomeVolume(leftSample.value) || !finiteDomeVolume(rightSample.value) ||
		!finiteDomeVolume(leftSample.evaluationError) || leftSample.evaluationError < 0 ||
		!finiteDomeVolume(rightSample.evaluationError) || rightSample.evaluationError < 0 ||
		!finiteDomeVolume(lipschitz) || lipschitz <= 0 ||
		!finiteDomeVolume(roundoffOperandScale) || roundoffOperandScale <= 0 || remaining == nil {
		return domeAstrodomeRootIsolationResult{}, errors.New("invalid Astrodome root-isolation interval")
	}
	if *remaining <= 0 {
		return domeAstrodomeRootIsolationResult{}, fmt.Errorf("%w: physical root-isolation budget exhausted",
			forecast.ErrAstrodomeScienceIncompletePartition)
	}
	(*remaining)--
	if slopeBounds != nil {
		leftSign, _, _ := domeAstrodomeResidualSignInterval(leftSample, roundoffOperandScale)
		rightSign, _, _ := domeAstrodomeResidualSignInterval(rightSample, roundoffOperandScale)
		if leftSign != 0 && rightSign != 0 && leftSign != rightSign {
			lowerSlope, upperSlope, boundsErr := slopeBounds(
				leftM, rightM, leftSample, rightSample,
			)
			if boundsErr != nil {
				return domeAstrodomeRootIsolationResult{}, boundsErr
			}
			if !finiteDomeVolume(lowerSlope) || !finiteDomeVolume(upperSlope) || lowerSlope > upperSlope {
				return domeAstrodomeRootIsolationResult{}, fmt.Errorf(
					"%w: invalid Astrodome monotonic slope enclosure",
					forecast.ErrAstrodomeScienceIncompletePartition,
				)
			}
			if lowerSlope > 0 || upperSlope < 0 {
				return domeAstrodomeLocalizeCertifiedMonotoneRootResiduals(
					leftM, rightM, leftSample, rightSample, lowerSlope, upperSlope,
					roundoffOperandScale, rootToleranceM, remaining, value,
				)
			}
		}
	}
	if slopeBounds != nil && rightM-leftM <= 2*rootToleranceM {
		result, resolved, err := domeAstrodomeResolveMonotoneRootInterval(
			leftM, rightM, leftSample, rightSample, roundoffOperandScale,
			rootToleranceM, slopeBounds,
		)
		if err != nil {
			return domeAstrodomeRootIsolationResult{}, err
		}
		if resolved {
			return result, nil
		}
	}
	middleM := (leftM + rightM) / 2
	middleSample, err := value(middleM)
	if err != nil {
		return domeAstrodomeRootIsolationResult{}, err
	}
	if !finiteDomeVolume(middleSample.value) || !finiteDomeVolume(middleSample.evaluationError) ||
		middleSample.evaluationError < 0 {
		return domeAstrodomeRootIsolationResult{}, errors.New("non-finite Astrodome physical boundary value")
	}
	halfWidthM := (rightM - leftM) / 2
	pathVariation := math.Nextafter(lipschitz*halfWidthM, math.Inf(1))
	middleLower, middleUpper := domeAstrodomeResidualInterval(
		middleSample, roundoffOperandScale, pathVariation,
	)
	leftSign, _, _ := domeAstrodomeResidualSignInterval(leftSample, roundoffOperandScale)
	middleSign, _, _ := domeAstrodomeResidualSignInterval(middleSample, roundoffOperandScale)
	rightSign, _, _ := domeAstrodomeResidualSignInterval(rightSample, roundoffOperandScale)
	if leftSign != 0 && leftSign == middleSign && middleSign == rightSign &&
		((middleSign > 0 && middleLower > pathVariation) ||
			(middleSign < 0 && middleUpper < -pathVariation)) {
		return domeAstrodomeRootIsolationResult{}, nil
	}
	if rightM-leftM <= domeAstrodomeRootProofToleranceM {
		if *remaining < 2 {
			return domeAstrodomeRootIsolationResult{}, fmt.Errorf("%w: physical root-isolation budget exhausted",
				forecast.ErrAstrodomeScienceIncompletePartition)
		}
		*remaining -= 2
		leftResult, leftErr := domeAstrodomeClassifyRootProofHalf(
			leftM, middleM, leftSample, middleSample, lipschitz, roundoffOperandScale,
			rootToleranceM, slopeBounds, value,
		)
		if leftErr != nil {
			return domeAstrodomeRootIsolationResult{}, leftErr
		}
		rightResult, rightErr := domeAstrodomeClassifyRootProofHalf(
			middleM, rightM, middleSample, rightSample, lipschitz, roundoffOperandScale,
			rootToleranceM, slopeBounds, value,
		)
		if rightErr != nil {
			return domeAstrodomeRootIsolationResult{}, rightErr
		}
		leftResult.roots = append(leftResult.roots, rightResult.roots...)
		leftResult.unresolved = append(leftResult.unresolved, rightResult.unresolved...)
		return leftResult, nil
	}
	leftResult, err := domeAstrodomeIsolatePathRootsRecursive(
		leftM, middleM, leftSample, middleSample, lipschitz, roundoffOperandScale,
		rootToleranceM, slopeBounds, remaining, value,
	)
	if err != nil {
		return domeAstrodomeRootIsolationResult{}, err
	}
	rightResult, err := domeAstrodomeIsolatePathRootsRecursive(
		middleM, rightM, middleSample, rightSample, lipschitz, roundoffOperandScale,
		rootToleranceM, slopeBounds, remaining, value,
	)
	if err != nil {
		return domeAstrodomeRootIsolationResult{}, err
	}
	leftResult.roots = append(leftResult.roots, rightResult.roots...)
	leftResult.unresolved = append(leftResult.unresolved, rightResult.unresolved...)
	return leftResult, nil
}

func domeAstrodomeClassifyRootProofHalf(
	leftM, rightM float64,
	leftSample, rightSample domeAstrodomeResidualSample,
	lipschitz, roundoffOperandScale, rootToleranceM float64,
	slopeBounds domeAstrodomeResidualSlopeBounds,
	value domeAstrodomeResidualValue,
) (domeAstrodomeRootIsolationResult, error) {
	if slopeBounds != nil {
		result, resolved, err := domeAstrodomeResolveMonotoneRootInterval(
			leftM, rightM, leftSample, rightSample, roundoffOperandScale,
			rootToleranceM, slopeBounds,
		)
		if err != nil {
			return domeAstrodomeRootIsolationResult{}, err
		}
		if resolved {
			return result, nil
		}
	}
	middleM := (leftM + rightM) / 2
	middleSample, err := value(middleM)
	if err != nil {
		return domeAstrodomeRootIsolationResult{}, err
	}
	if !finiteDomeVolume(middleSample.value) || !finiteDomeVolume(middleSample.evaluationError) ||
		middleSample.evaluationError < 0 {
		return domeAstrodomeRootIsolationResult{}, errors.New("non-finite Astrodome physical boundary value")
	}
	halfWidthM := (rightM - leftM) / 2
	pathVariation := math.Nextafter(lipschitz*halfWidthM, math.Inf(1))
	middleLower, middleUpper := domeAstrodomeResidualInterval(
		middleSample, roundoffOperandScale, pathVariation,
	)
	leftSign, _, _ := domeAstrodomeResidualSignInterval(leftSample, roundoffOperandScale)
	middleSign, _, _ := domeAstrodomeResidualSignInterval(middleSample, roundoffOperandScale)
	rightSign, _, _ := domeAstrodomeResidualSignInterval(rightSample, roundoffOperandScale)
	if leftSign != 0 && leftSign == middleSign && middleSign == rightSign &&
		((middleSign > 0 && middleLower > pathVariation) ||
			(middleSign < 0 && middleUpper < -pathVariation)) {
		return domeAstrodomeRootIsolationResult{}, nil
	}

	result := domeAstrodomeRootIsolationResult{roots: make([]domeAstrodomeRootEvidence, 0, 2)}
	for _, interval := range [...]struct {
		leftM, rightM           float64
		leftSample, rightSample domeAstrodomeResidualSample
	}{
		{leftM: leftM, rightM: middleM, leftSample: leftSample, rightSample: middleSample},
		{leftM: middleM, rightM: rightM, leftSample: middleSample, rightSample: rightSample},
	} {
		// A sign change inside one proof sub-half is finite cluster evidence for
		// that sub-half only. Its sibling is still classified independently.
		leftSign, _, _ := domeAstrodomeResidualSignInterval(interval.leftSample, roundoffOperandScale)
		rightSign, _, _ := domeAstrodomeResidualSignInterval(interval.rightSample, roundoffOperandScale)
		if leftSign != 0 && rightSign != 0 && leftSign != rightSign {
			result.roots = append(result.roots, domeAstrodomeRootEvidence{
				pathM:  (interval.leftM + interval.rightM) / 2,
				leftM:  interval.leftM,
				rightM: interval.rightM,
			})
			continue
		}

		// A nominal zero or any interval containing zero is uncertain evidence,
		// never an exact root. Without monotonicity it leaves the complete sub-half
		// unresolved so paired crossings or a tangency cannot be hidden.
		if leftSign == 0 || rightSign == 0 {
			result.unresolved = append(result.unresolved, domeAstrodomeRootIsolationInterval{
				leftM: interval.leftM, rightM: interval.rightM,
			})
			continue
		}

		widthM := interval.rightM - interval.leftM
		endpointClears := func(sample domeAstrodomeResidualSample) bool {
			variation := math.Nextafter(lipschitz*widthM, math.Inf(1))
			lower, upper := domeAstrodomeResidualInterval(sample, roundoffOperandScale, variation)
			return lower > variation || upper < -variation
		}
		leftClears := endpointClears(interval.leftSample)
		rightClears := endpointClears(interval.rightSample)
		if leftClears || rightClears {
			continue
		}
		result.unresolved = append(result.unresolved, domeAstrodomeRootIsolationInterval{
			leftM: interval.leftM, rightM: interval.rightM,
		})
	}
	return result, nil
}

func (volume *DomeVolume) domeAstrodomeMetricBoundsForInterval(
	ray forecast.AstrodomeRefractedRay,
	interval domeAstrodomeCellInterval,
) (domeAstrodomeMetricBounds, error) {
	pathDerivativeNorm, err := ray.PositionDerivativeNormUpperBound(interval.startM, interval.endM)
	if err != nil {
		return domeAstrodomeMetricBounds{}, err
	}
	pathDerivativeNorm = math.Max(1, pathDerivativeNorm)
	middle, err := ray.PointAtPathLength((interval.startM + interval.endM) / 2)
	if err != nil {
		return domeAstrodomeMetricBounds{}, err
	}
	middlePathM := (interval.startM + interval.endM) / 2
	middlePositionErrorM, err := ray.PositionEvaluationErrorUpperBound(middlePathM)
	if err != nil {
		return domeAstrodomeMetricBounds{}, err
	}
	middleCoordinateBounds, err := domeAstrodomeCoordinateEvaluationErrorBounds(
		middle, middlePositionErrorM, volume.manifest.Grid, true,
	)
	if err != nil {
		return domeAstrodomeMetricBounds{}, err
	}
	if middleCoordinateBounds.equivalentPositionM > domeAstrodomePositionCoordinateEnvelopeM {
		return domeAstrodomeMetricBounds{}, fmt.Errorf("%w: ICON-EU Astrodome metric position/coordinate bound %.9g m exceeds the %.9g m contract",
			forecast.ErrAstrodomeScienceIncompletePartition,
			middleCoordinateBounds.equivalentPositionM, domeAstrodomePositionCoordinateEnvelopeM)
	}
	pathOperandScale := math.Max(1, math.Max(math.Abs(interval.startM), math.Abs(interval.endM)))
	pathSpanUpper := math.Nextafter(
		interval.endM-interval.startM+
			domeAstrodomeClearanceRoundoff(0, 0, pathOperandScale),
		math.Inf(1),
	)
	halfPathSpanUpper := domeAstrodomePositiveDivUpper(pathSpanUpper, 2)
	radiusCenterNominal := forecast.AstrodomeICONSphereRadiusM + middle.HeightM
	radiusCenterLower := math.Nextafter(
		radiusCenterNominal-domeAstrodomePositionCoordinateEnvelopeM-
			domeAstrodomeClearanceRoundoff(radiusCenterNominal, 0, domeAstrodomePhysicalHeightOperandScaleM()),
		math.Inf(-1),
	)
	radiusDropUpper := domeAstrodomePositiveMulUpper(pathDerivativeNorm, halfPathSpanUpper)
	radiusMinimumM := domeAstrodomePositiveSubLower(
		radiusCenterLower, radiusDropUpper, domeAstrodomePhysicalHeightOperandScaleM(),
	)
	incrementNominal := volume.manifest.Grid.Increment * math.Pi / 180
	incrementRadians := math.Nextafter(
		incrementNominal-domeAstrodomeClearanceRoundoff(incrementNominal, 0, 1), 0,
	)
	horizontalCenterNominalM := math.Hypot(middle.ECEF.X, middle.ECEF.Y)
	horizontalCenterLowerM := math.Nextafter(
		horizontalCenterNominalM-domeAstrodomePositionCoordinateEnvelopeM-
			domeAstrodomeClearanceRoundoff(horizontalCenterNominalM, 0, domeAstrodomePhysicalHeightOperandScaleM()),
		math.Inf(-1),
	)
	longitudeRadiusM := domeAstrodomePositiveSubLower(
		horizontalCenterLowerM, radiusDropUpper, domeAstrodomePhysicalHeightOperandScaleM(),
	)
	if !finiteDomeVolume(radiusMinimumM) || radiusMinimumM <= 0 ||
		!finiteDomeVolume(incrementRadians) || incrementRadians <= 0 ||
		!finiteDomeVolume(longitudeRadiusM) || longitudeRadiusM <= 0 {
		return domeAstrodomeMetricBounds{}, errors.New("ICON-EU Astrodome path has an invalid metric slope scale")
	}
	return domeAstrodomeMetricBounds{
		pathDerivativeNorm: pathDerivativeNorm, radiusMinimumM: radiusMinimumM,
		longitudeRadiusM: longitudeRadiusM, incrementRadians: incrementRadians,
	}, nil
}

func domeAstrodomeScalarFieldLipschitz(
	metric domeAstrodomeMetricBounds,
	values [4]float64,
	roundoffOperandScale float64,
) (float64, error) {
	if !finiteDomeVolume(roundoffOperandScale) || roundoffOperandScale <= 0 {
		return 0, errors.New("ICON-EU Astrodome scalar field has an invalid roundoff scale")
	}
	// Each reconstructed corner may itself be a cancellation residual. Add an
	// absolute, unit-aware enclosure before forming the slope, then round every
	// positive arithmetic stage outward. This avoids relying on one final
	// Nextafter after potentially cancelling subtractions.
	differenceRoundoff := domeAstrodomeClearanceRoundoff(0, 0, roundoffOperandScale)
	latitudeDifference := math.Nextafter(
		math.Max(math.Abs(values[2]-values[0]), math.Abs(values[3]-values[1]))+2*differenceRoundoff,
		math.Inf(1),
	)
	longitudeDifference := math.Nextafter(
		math.Max(math.Abs(values[1]-values[0]), math.Abs(values[3]-values[2]))+2*differenceRoundoff,
		math.Inf(1),
	)
	latitudeDenominator := math.Nextafter(metric.radiusMinimumM*metric.incrementRadians, 0)
	longitudeDenominator := math.Nextafter(metric.longitudeRadiusM*metric.incrementRadians, 0)
	if latitudeDenominator <= 0 || longitudeDenominator <= 0 {
		return 0, errors.New("ICON-EU Astrodome scalar field has an invalid metric denominator")
	}
	latitudeSlope := math.Nextafter(latitudeDifference/latitudeDenominator, math.Inf(1))
	longitudeSlope := math.Nextafter(longitudeDifference/longitudeDenominator, math.Inf(1))
	slopeSum := math.Nextafter(latitudeSlope+longitudeSlope, math.Inf(1))
	lipschitz := math.Nextafter(metric.pathDerivativeNorm*slopeSum, math.Inf(1))
	if !finiteDomeVolume(lipschitz) || lipschitz <= 0 {
		return 0, errors.New("ICON-EU Astrodome scalar field has no positive Lipschitz bound")
	}
	return lipschitz, nil
}

func domePhysicalBoundaryLipschitz(
	fields [][4]float64,
	metric domeAstrodomeMetricBounds,
) (float64, float64, bool, error) {
	boundarySlope := 0.0
	for _, values := range fields {
		fieldLipschitz, fieldErr := domeAstrodomeScalarFieldLipschitz(
			metric, values, domeAstrodomePhysicalHeightOperandScaleM(),
		)
		if fieldErr != nil {
			// A constant field has zero path derivative and remains a valid
			// physical surface contribution.
			if domeAstrodomeScalarFieldConstant(values) {
				continue
			}
			return 0, 0, false, fieldErr
		}
		boundarySlope = domeAstrodomePositiveAddUpper(boundarySlope, fieldLipschitz)
	}
	lipschitz := domeAstrodomePositiveAddUpper(metric.pathDerivativeNorm, boundarySlope)
	if !finiteDomeVolume(lipschitz) || lipschitz <= 0 {
		return 0, 0, false, errors.New("ICON-EU Astrodome physical boundary has an invalid Lipschitz bound")
	}
	if !finiteDomeVolume(boundarySlope) || boundarySlope < 0 {
		return 0, 0, false, errors.New("ICON-EU Astrodome physical boundary has an invalid slope bound")
	}
	return lipschitz, boundarySlope, true, nil
}

func domeAstrodomePhysicalEndpointSlopeBounds(
	boundaryKind string,
	ray forecast.AstrodomeRefractedRay,
	boundarySlope float64,
) domeAstrodomeEndpointSlopeBounds {
	return func(leftM, rightM float64) (float64, float64, error) {
		switch boundaryKind {
		case domeAstrodomeBoundaryHHL, domeAstrodomeBoundaryFull,
			domeAstrodomeBoundaryLowTop, domeAstrodomeBoundaryMidTop,
			domeAstrodomeBoundaryPBL:
		default:
			return 0, 0, fmt.Errorf("unsupported ICON-EU physical endpoint boundary %q", boundaryKind)
		}
		if !finiteDomeVolume(boundarySlope) || boundarySlope < 0 ||
			!finiteDomeVolume(leftM) || !finiteDomeVolume(rightM) || rightM <= leftM {
			return 0, 0, errors.New("invalid ICON-EU physical endpoint-slope request")
		}
		lower, upper, err := ray.HeightDerivativeBounds(leftM, rightM)
		if err != nil {
			return 0, 0, err
		}
		lower = math.Nextafter(lower-boundarySlope, math.Inf(-1))
		upper = math.Nextafter(upper+boundarySlope, math.Inf(1))
		if !finiteDomeVolume(lower) || !finiteDomeVolume(upper) || lower > upper {
			return 0, 0, fmt.Errorf("%w: invalid ICON-EU physical endpoint-slope enclosure",
				forecast.ErrAstrodomeScienceIncompletePartition)
		}
		return lower, upper, nil
	}
}

func domeAstrodomePhysicalEndpointSlopeBoundsFromInterior(
	boundaryKind string,
	ray forecast.AstrodomeRefractedRay,
	boundarySlope float64,
	probePaths [5]float64,
	probeValues [5]domeAstrodomeResidualSample,
	secondDerivativeBound float64,
) (domeAstrodomeEndpointSlopeBounds, error) {
	localBounds, err := domeAstrodomeEndpointSlopeBoundsFromInterior(
		probePaths, probeValues, domeAstrodomePhysicalHeightOperandScaleM(), secondDerivativeBound,
	)
	if err != nil {
		return nil, err
	}
	globalBounds := domeAstrodomePhysicalEndpointSlopeBounds(boundaryKind, ray, boundarySlope)
	return func(leftM, rightM float64) (float64, float64, error) {
		globalLower, globalUpper, globalErr := globalBounds(leftM, rightM)
		if globalErr != nil {
			return 0, 0, globalErr
		}
		localLower, localUpper, localErr := localBounds(leftM, rightM)
		if localErr != nil {
			return 0, 0, localErr
		}
		lower := math.Max(globalLower, localLower)
		upper := math.Min(globalUpper, localUpper)
		if lower > upper {
			return 0, 0, fmt.Errorf("%w: Astrodome physical endpoint-slope enclosures do not intersect",
				forecast.ErrAstrodomeScienceIncompletePartition)
		}
		return lower, upper, nil
	}, nil
}

func (volume *DomeVolume) domePhysicalBoundaryFields(
	validAt time.Time,
	columns [4]forecast.AstrodomePrimitiveColumn,
	boundary domeAstrodomeBoundary,
	calibration forecast.AstrodomeScienceCalibration,
) ([][4]float64, bool, error) {
	fields := make([][4]float64, 0, 2)
	smooth := true
	switch boundary.kind {
	case domeAstrodomeBoundaryHHL:
		values, err := domeAstrodomeHHLCornerValues(columns, boundary.levelIndex)
		if err != nil {
			return nil, false, err
		}
		fields = append(fields, values)
	case domeAstrodomeBoundaryFull:
		upper, err := domeAstrodomeHHLCornerValues(columns, boundary.levelIndex)
		if err != nil {
			return nil, false, err
		}
		lower, err := domeAstrodomeHHLCornerValues(columns, boundary.levelIndex+1)
		if err != nil {
			return nil, false, err
		}
		values := [4]float64{}
		for index := range values {
			values[index] = (upper[index] + lower[index]) / 2
		}
		fields = append(fields, values)
	case domeAstrodomeBoundaryLowTop, domeAstrodomeBoundaryMidTop:
		fields = append(fields, domeAstrodomeSurfaceCornerValues(columns))
	case domeAstrodomeBoundaryPBL:
		mixedLayerDepths, err := volume.domeAstrodomeMixedLayerCornerValues(columns, validAt)
		if err != nil {
			return nil, false, err
		}
		return domeAstrodomePBLFieldsForCornerRange(
			domeAstrodomeSurfaceCornerValues(columns), mixedLayerDepths,
			calibration.Overall.BoundaryLayerMinM,
			calibration.Overall.BoundaryLayerTopM,
		)
	default:
		return nil, false, fmt.Errorf("unsupported Astrodome physical boundary %q", boundary.id)
	}
	return fields, smooth, nil
}

func domeAstrodomePBLFieldsForCornerRange(
	surfaceHeights, mixedLayerDepths [4]float64,
	minimumDepthM, maximumDepthM float64,
) ([][4]float64, bool, error) {
	branch, err := domeClassifyAstrodomePBLClampBranch(mixedLayerDepths, minimumDepthM, maximumDepthM)
	if err != nil {
		return nil, false, err
	}
	switch branch {
	case domeAstrodomePBLClampLower, domeAstrodomePBLClampUpper:
		return [][4]float64{surfaceHeights}, true, nil
	case domeAstrodomePBLClampIdentity:
		return [][4]float64{surfaceHeights, mixedLayerDepths}, true, nil
	}

	minimumValue, maximumValue := domeAstrodomeCornerRange(mixedLayerDepths)
	operandScale := domeAstrodomeCornerOperandScale(mixedLayerDepths)
	roundoff := domeAstrodomeClearanceRoundoff(0, 0, operandScale)
	minimumValue = math.Nextafter(minimumValue-roundoff, math.Inf(-1))
	maximumValue = math.Nextafter(maximumValue+roundoff, math.Inf(1))
	return domeAstrodomePBLFieldsForEnclosedRange(
		surfaceHeights, mixedLayerDepths, minimumValue, maximumValue,
		minimumDepthM, maximumDepthM,
	)
}

type domeAstrodomePBLClampBranch uint8

const (
	domeAstrodomePBLClampCrossing domeAstrodomePBLClampBranch = iota
	domeAstrodomePBLClampLower
	domeAstrodomePBLClampIdentity
	domeAstrodomePBLClampUpper
)

// domeAstrodomePBLClampBranch classifies the complete bilinear MH field using
// an outward binary64 enclosure. A non-crossing result therefore proves the
// same clamp branch at every point in the native horizontal cell.
func domeClassifyAstrodomePBLClampBranch(
	mixedLayerDepths [4]float64,
	minimumDepthM, maximumDepthM float64,
) (domeAstrodomePBLClampBranch, error) {
	if !finiteDomeVolume(minimumDepthM) || !finiteDomeVolume(maximumDepthM) ||
		minimumDepthM >= maximumDepthM {
		return domeAstrodomePBLClampCrossing, errors.New("invalid ICON-EU Astrodome PBL clamp limits")
	}
	minimumValue, maximumValue := domeAstrodomeCornerRange(mixedLayerDepths)
	operandScale := domeAstrodomeCornerOperandScale(mixedLayerDepths)
	roundoff := domeAstrodomeClearanceRoundoff(0, 0, operandScale)
	minimumValue = math.Nextafter(minimumValue-roundoff, math.Inf(-1))
	maximumValue = math.Nextafter(maximumValue+roundoff, math.Inf(1))
	switch {
	case maximumValue <= minimumDepthM:
		return domeAstrodomePBLClampLower, nil
	case minimumValue >= maximumDepthM:
		return domeAstrodomePBLClampUpper, nil
	case minimumValue >= minimumDepthM && maximumValue <= maximumDepthM:
		return domeAstrodomePBLClampIdentity, nil
	default:
		return domeAstrodomePBLClampCrossing, nil
	}
}

func domeAstrodomePBLIntervalFields(
	sampler *domeAstrodomePhysicalSampler,
	surfaceHeights, mixedLayerDepths [4]float64,
	mixedLayerLipschitz, mixedLayerOperandScale,
	leftM, rightM, minimumDepthM, maximumDepthM float64,
) ([][4]float64, bool, error) {
	if !finiteDomeVolume(mixedLayerLipschitz) || mixedLayerLipschitz <= 0 ||
		!finiteDomeVolume(mixedLayerOperandScale) || mixedLayerOperandScale <= 0 ||
		!finiteDomeVolume(leftM) || !finiteDomeVolume(rightM) || rightM <= leftM {
		return nil, false, errors.New("invalid ICON-EU Astrodome PBL interval certificate")
	}
	middleM := leftM + (rightM-leftM)/2
	middleSample, err := sampler.sample(middleM, false)
	if err != nil {
		return nil, false, err
	}
	middleDepthM := domeWeighted4(mixedLayerDepths, middleSample.weights)
	coordinateUncertaintyM, err := domeAstrodomeBilinearCoordinateUncertainty(
		middleSample.point, middleSample.positionEvaluationErrorM,
		sampler.volume.manifest.Grid, mixedLayerDepths, mixedLayerOperandScale,
	)
	if err != nil {
		return nil, false, err
	}
	halfWidthM := math.Max(middleM-leftM, rightM-middleM)
	return domeAstrodomePBLFieldsAroundSample(
		surfaceHeights, mixedLayerDepths, middleDepthM, mixedLayerLipschitz,
		mixedLayerOperandScale, coordinateUncertaintyM,
		halfWidthM, minimumDepthM, maximumDepthM,
	)
}

func domeAstrodomePBLFieldsAroundSample(
	surfaceHeights, mixedLayerDepths [4]float64,
	middleDepthM, mixedLayerLipschitz, mixedLayerOperandScale,
	middleEvaluationUncertaintyM, halfWidthM, minimumDepthM, maximumDepthM float64,
) ([][4]float64, bool, error) {
	if !finiteDomeVolume(middleDepthM) || !finiteDomeVolume(mixedLayerLipschitz) ||
		mixedLayerLipschitz < 0 || !finiteDomeVolume(mixedLayerOperandScale) ||
		mixedLayerOperandScale <= 0 || !finiteDomeVolume(middleEvaluationUncertaintyM) ||
		middleEvaluationUncertaintyM < 0 || !finiteDomeVolume(halfWidthM) || halfWidthM < 0 {
		return nil, false, errors.New("invalid ICON-EU Astrodome local PBL enclosure")
	}
	variationM := domeAstrodomePositiveMulUpper(mixedLayerLipschitz, halfWidthM)
	variationM = domeAstrodomePositiveAddUpper(variationM, middleEvaluationUncertaintyM)
	variationM = domeAstrodomePositiveAddUpper(
		variationM,
		domeAstrodomeClearanceRoundoff(middleDepthM, variationM, mixedLayerOperandScale),
	)
	minimumValue := math.Nextafter(middleDepthM-variationM, math.Inf(-1))
	maximumValue := math.Nextafter(middleDepthM+variationM, math.Inf(1))
	return domeAstrodomePBLFieldsForEnclosedRange(
		surfaceHeights, mixedLayerDepths, minimumValue, maximumValue,
		minimumDepthM, maximumDepthM,
	)
}

// domeAstrodomeBilinearCoordinateUncertainty encloses the value error caused
// by evaluating one stored dense-output ECEF point as spherical
// latitude/longitude, mapping those coordinates to regular-grid fractions,
// and constructing bilinear weights. The position error is the rigorous
// formation-sum bound exported by AstrodomeRefractedRay. The independent
// latitude and longitude angular enclosures retain the smaller projected
// radius at high latitude.
//
// The ordinary coordinate-arithmetic allowance is applied to the full
// normalized operand (180 degrees) before division by the native grid
// increment; applying it only to the final MH value misses that division's
// amplification. The explicit snap term covers domeLowerGridIndex's finite
// grid-line assignment.
//
// Inside one certified horizontal cell, a bilinear scalar's coordinate
// sensitivities are bounded by the greatest north-south and east-west edge
// differences. Every positive arithmetic stage is rounded outward. Ordinary
// weight/product/summation roundoff remains covered separately by the scalar
// clearance added by domeAstrodomePBLFieldsAroundSample.
func domeAstrodomeCoordinateEvaluationErrorBounds(
	point forecast.AstrodomeRefractedRayPoint,
	positionEvaluationErrorM float64,
	grid model.Coverage,
	checkCell bool,
) (domeAstrodomeCoordinateEvaluationBounds, error) {
	if !finiteDomeVolume(positionEvaluationErrorM) || positionEvaluationErrorM < 0 ||
		!finiteDomeVolume(grid.Increment) || grid.Increment <= 0 ||
		grid.MinLat <= -90 || grid.MaxLat >= 90 || grid.MinLat >= grid.MaxLat ||
		grid.MinLon <= -180 || grid.MaxLon >= 180 || grid.MinLon >= grid.MaxLon {
		return domeAstrodomeCoordinateEvaluationBounds{}, errors.New("invalid ICON-EU Astrodome coordinate uncertainty request")
	}

	cell, err := domeGridCell(grid, point.Location)
	if err != nil {
		return domeAstrodomeCoordinateEvaluationBounds{}, err
	}
	radiusM := point.ECEF.Norm()
	horizontalRadiusM := math.Hypot(point.ECEF.X, point.ECEF.Y)
	cartesianArithmeticM := domeAstrodomeClearanceRoundoff(
		0, 0, domeAstrodomePhysicalHeightOperandScaleM(),
	)
	positionUncertaintyM := domeAstrodomePositiveAddUpper(
		positionEvaluationErrorM, cartesianArithmeticM,
	)
	radiusLowerM := math.Nextafter(radiusM-positionUncertaintyM, 0)
	horizontalRadiusLowerM := math.Nextafter(horizontalRadiusM-positionUncertaintyM, 0)
	if !finiteDomeVolume(radiusLowerM) || radiusLowerM <= 0 ||
		!finiteDomeVolume(horizontalRadiusLowerM) || horizontalRadiusLowerM <= 0 ||
		positionUncertaintyM >= radiusLowerM || positionUncertaintyM >= horizontalRadiusLowerM {
		return domeAstrodomeCoordinateEvaluationBounds{}, fmt.Errorf("%w: ICON-EU Astrodome coordinate projection has no positive radius enclosure",
			forecast.ErrAstrodomeScienceIncompletePartition)
	}

	angularUpper := func(denominatorM float64) (float64, error) {
		ratio := domeAstrodomePositiveDivUpper(positionUncertaintyM, denominatorM)
		if !finiteDomeVolume(ratio) || ratio < 0 || ratio >= 1 {
			return 0, fmt.Errorf("%w: ICON-EU Astrodome coordinate projection has no angular enclosure",
				forecast.ErrAstrodomeScienceIncompletePartition)
		}
		angle := math.Asin(ratio)
		angle = domeAstrodomePositiveAddUpper(
			angle, domeAstrodomeClearanceRoundoff(angle, 0, math.Pi),
		)
		if !finiteDomeVolume(angle) || angle < 0 {
			return 0, errors.New("ICON-EU Astrodome coordinate angular uncertainty is invalid")
		}
		return angle, nil
	}
	latitudeAngle, err := angularUpper(radiusLowerM)
	if err != nil {
		return domeAstrodomeCoordinateEvaluationBounds{}, err
	}
	longitudeAngle, err := angularUpper(horizontalRadiusLowerM)
	if err != nil {
		return domeAstrodomeCoordinateEvaluationBounds{}, err
	}

	radiansToDegrees := domeAstrodomePositiveDivUpper(180, math.Pi)
	coordinateDegrees := domeAstrodomeClearanceRoundoff(0, 0, 180)
	coordinateFraction := domeAstrodomePositiveDivUpper(coordinateDegrees, grid.Increment)
	fractionUncertainty := func(angle float64) float64 {
		angleDegrees := domeAstrodomePositiveMulUpper(angle, radiansToDegrees)
		positionFraction := domeAstrodomePositiveDivUpper(angleDegrees, grid.Increment)
		result := domeAstrodomePositiveAddUpper(positionFraction, coordinateFraction)
		return domeAstrodomePositiveAddUpper(result, domeGridLineSnapTolerance)
	}
	latitudeFractionUncertainty := fractionUncertainty(latitudeAngle)
	longitudeFractionUncertainty := fractionUncertainty(longitudeAngle)
	if checkCell {
		latitudeBoundaryDistance := math.Min(
			math.Nextafter(cell.latitudeFraction, 0),
			math.Nextafter(1-cell.latitudeFraction, 0),
		)
		longitudeBoundaryDistance := math.Min(
			math.Nextafter(cell.longitudeFraction, 0),
			math.Nextafter(1-cell.longitudeFraction, 0),
		)
		if latitudeBoundaryDistance <= latitudeFractionUncertainty ||
			longitudeBoundaryDistance <= longitudeFractionUncertainty {
			return domeAstrodomeCoordinateEvaluationBounds{}, fmt.Errorf("%w: ICON-EU Astrodome coordinate uncertainty crosses a horizontal cell boundary",
				forecast.ErrAstrodomeScienceIncompletePartition)
		}
	}

	incrementRadians := grid.Increment * math.Pi / 180
	radiusUpperM := math.Nextafter(radiusM+positionUncertaintyM, math.Inf(1))
	horizontalRadiusUpperM := math.Nextafter(horizontalRadiusM+positionUncertaintyM, math.Inf(1))
	latitudeEquivalentM := domeAstrodomePositiveMulUpper(
		domeAstrodomePositiveMulUpper(latitudeFractionUncertainty, incrementRadians), radiusUpperM,
	)
	longitudeEquivalentM := domeAstrodomePositiveMulUpper(
		domeAstrodomePositiveMulUpper(longitudeFractionUncertainty, incrementRadians), horizontalRadiusUpperM,
	)
	equivalentPositionM := math.Max(positionUncertaintyM, math.Max(latitudeEquivalentM, longitudeEquivalentM))
	if !finiteDomeVolume(equivalentPositionM) || equivalentPositionM < 0 ||
		!finiteDomeVolume(latitudeFractionUncertainty) || latitudeFractionUncertainty < 0 ||
		!finiteDomeVolume(longitudeFractionUncertainty) || longitudeFractionUncertainty < 0 {
		return domeAstrodomeCoordinateEvaluationBounds{}, errors.New("ICON-EU Astrodome coordinate uncertainty is invalid")
	}
	return domeAstrodomeCoordinateEvaluationBounds{
		equivalentPositionM:          equivalentPositionM,
		latitudeFractionUncertainty:  latitudeFractionUncertainty,
		longitudeFractionUncertainty: longitudeFractionUncertainty,
	}, nil
}

func domeAstrodomeBilinearCoordinateUncertainty(
	point forecast.AstrodomeRefractedRayPoint,
	positionEvaluationErrorM float64,
	grid model.Coverage,
	values [4]float64,
	roundoffOperandScale float64,
) (float64, error) {
	if !finiteDomeVolume(roundoffOperandScale) || roundoffOperandScale <= 0 {
		return 0, errors.New("invalid ICON-EU Astrodome bilinear coordinate uncertainty request")
	}
	for _, value := range values {
		if !finiteDomeVolume(value) {
			return 0, errors.New("invalid ICON-EU Astrodome bilinear coordinate field")
		}
	}
	coordinateBounds, err := domeAstrodomeCoordinateEvaluationErrorBounds(
		point, positionEvaluationErrorM, grid, true,
	)
	if err != nil {
		return 0, err
	}
	return domeAstrodomeBilinearCoordinateUncertaintyFromBounds(
		coordinateBounds, values, roundoffOperandScale,
	)
}

func domeAstrodomeBilinearCoordinateUncertaintyFromBounds(
	coordinateBounds domeAstrodomeCoordinateEvaluationBounds,
	values [4]float64,
	roundoffOperandScale float64,
) (float64, error) {
	if !finiteDomeVolume(roundoffOperandScale) || roundoffOperandScale <= 0 ||
		!finiteDomeVolume(coordinateBounds.equivalentPositionM) || coordinateBounds.equivalentPositionM < 0 ||
		!finiteDomeVolume(coordinateBounds.latitudeFractionUncertainty) || coordinateBounds.latitudeFractionUncertainty < 0 ||
		!finiteDomeVolume(coordinateBounds.longitudeFractionUncertainty) || coordinateBounds.longitudeFractionUncertainty < 0 {
		return 0, errors.New("invalid ICON-EU Astrodome bilinear coordinate uncertainty request")
	}
	for _, value := range values {
		if !finiteDomeVolume(value) {
			return 0, errors.New("invalid ICON-EU Astrodome bilinear coordinate field")
		}
	}
	differenceRoundoff := domeAstrodomeClearanceRoundoff(0, 0, roundoffOperandScale)
	differenceAllowance := domeAstrodomePositiveMulUpper(2, differenceRoundoff)
	latitudeDifference := domeAstrodomePositiveAddUpper(
		math.Max(math.Abs(values[2]-values[0]), math.Abs(values[3]-values[1])),
		differenceAllowance,
	)
	longitudeDifference := domeAstrodomePositiveAddUpper(
		math.Max(math.Abs(values[1]-values[0]), math.Abs(values[3]-values[2])),
		differenceAllowance,
	)
	uncertainty := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveMulUpper(latitudeDifference, coordinateBounds.latitudeFractionUncertainty),
		domeAstrodomePositiveMulUpper(longitudeDifference, coordinateBounds.longitudeFractionUncertainty),
	)
	if !finiteDomeVolume(uncertainty) || uncertainty < 0 {
		return 0, errors.New("ICON-EU Astrodome bilinear coordinate uncertainty is invalid")
	}
	return uncertainty, nil
}

// domeAstrodomeFallbackBoundaryCoordinateError encloses only the change in
// the reconstructed 200-hPa height caused by the accepted horizontal
// coordinate uncertainty. Arithmetic uncertainty in the four native fields,
// logarithms, quotient, and affine height expression is enclosed separately
// by domeAstrodomeFallbackBoundaryDerivativeBounds.
func domeAstrodomeFallbackBoundaryCoordinateError(
	sample *domeAstrodomePhysicalSample,
	profile domeAstrodomeTropopauseProfile,
	lowerLevel int,
) (float64, error) {
	upperLevel := lowerLevel + 1
	if sample == nil || lowerLevel < 0 || upperLevel >= len(profile.heights) ||
		len(profile.heights) != len(profile.pressures) {
		return 0, errors.New("invalid ICON-EU 200-hPa coordinate-error bracket")
	}
	lowerHeight, upperHeight := profile.heights[lowerLevel], profile.heights[upperLevel]
	lowerPressure, upperPressure := profile.pressures[lowerLevel], profile.pressures[upperLevel]
	coordinateError := func(values [4]float64) (float64, error) {
		return domeAstrodomeBilinearCoordinateUncertaintyFromBounds(
			sample.coordinateBounds, values, domeAstrodomeCornerOperandScale(values),
		)
	}
	lowerHeightError, err := coordinateError(lowerHeight)
	if err != nil {
		return 0, err
	}
	upperHeightError, err := coordinateError(upperHeight)
	if err != nil {
		return 0, err
	}
	lowerPressureError, err := coordinateError(lowerPressure)
	if err != nil {
		return 0, err
	}
	upperPressureError, err := coordinateError(upperPressure)
	if err != nil {
		return 0, err
	}

	lowerHeightValue := domeWeighted4(lowerHeight, sample.weights)
	upperHeightValue := domeWeighted4(upperHeight, sample.weights)
	lowerPressureValue := domeWeighted4(lowerPressure, sample.weights)
	upperPressureValue := domeWeighted4(upperPressure, sample.weights)
	lowerHeightMinimum := math.Nextafter(lowerHeightValue-lowerHeightError, math.Inf(-1))
	lowerHeightMaximum := math.Nextafter(lowerHeightValue+lowerHeightError, math.Inf(1))
	upperHeightMinimum := math.Nextafter(upperHeightValue-upperHeightError, math.Inf(-1))
	upperHeightMaximum := math.Nextafter(upperHeightValue+upperHeightError, math.Inf(1))
	lowerPressureMinimum := math.Nextafter(lowerPressureValue-lowerPressureError, math.Inf(-1))
	lowerPressureMaximum := math.Nextafter(lowerPressureValue+lowerPressureError, math.Inf(1))
	upperPressureMinimum := math.Nextafter(upperPressureValue-upperPressureError, math.Inf(-1))
	upperPressureMaximum := math.Nextafter(upperPressureValue+upperPressureError, math.Inf(1))
	if lowerHeightMaximum >= upperHeightMinimum ||
		upperPressureMinimum <= 0 || lowerPressureMinimum <= upperPressureMaximum ||
		lowerPressureMinimum <= domeAstrodomeFallbackPressurePa ||
		upperPressureMaximum >= domeAstrodomeFallbackPressurePa {
		return 0, fmt.Errorf("%w: ICON-EU 200-hPa native coordinate enclosure does not preserve its physical bracket",
			forecast.ErrAstrodomeScienceIncompletePartition)
	}

	pressureFraction := func(lower, upper float64) (float64, error) {
		numerator := math.Log1p(
			(lower - domeAstrodomeFallbackPressurePa) / domeAstrodomeFallbackPressurePa,
		)
		denominator := math.Log1p((lower - upper) / upper)
		fraction := numerator / denominator
		if denominator <= 0 || !finiteDomeVolume(fraction) || fraction < 0 || fraction > 1 {
			return 0, errors.New("ICON-EU 200-hPa coordinate enclosure has an invalid logarithmic fraction")
		}
		return fraction, nil
	}
	// For p_u < P200 < p_l, alpha=ln(p_l/P200)/ln(p_l/p_u) is
	// increasing in both native pressures. With h_u>h_l, the affine height
	// is independently increasing in h_l, h_u, and alpha. These two corner
	// evaluations therefore enclose the complete four-field interval without
	// linearizing the logarithmic pressure relation.
	fractionMinimum, err := pressureFraction(lowerPressureMinimum, upperPressureMinimum)
	if err != nil {
		return 0, err
	}
	fractionMaximum, err := pressureFraction(lowerPressureMaximum, upperPressureMaximum)
	if err != nil {
		return 0, err
	}
	fractionMinimum = math.Nextafter(fractionMinimum, math.Inf(-1))
	fractionMaximum = math.Nextafter(fractionMaximum, math.Inf(1))
	heightMinimum := math.Nextafter(
		lowerHeightMinimum+fractionMinimum*(upperHeightMinimum-lowerHeightMinimum),
		math.Inf(-1),
	)
	heightMaximum := math.Nextafter(
		lowerHeightMaximum+fractionMaximum*(upperHeightMaximum-lowerHeightMaximum),
		math.Inf(1),
	)
	nominalHeight := sample.heights.TropopauseHeightM
	errorM := math.Nextafter(
		math.Max(math.Abs(nominalHeight-heightMinimum), math.Abs(heightMaximum-nominalHeight)),
		math.Inf(1),
	)
	if !finiteDomeVolume(errorM) || errorM < 0 {
		return 0, errors.New("ICON-EU 200-hPa coordinate-error enclosure is invalid")
	}
	return errorM, nil
}

func domeAstrodomePBLFieldsForEnclosedRange(
	surfaceHeights, mixedLayerDepths [4]float64,
	minimumValue, maximumValue, minimumDepthM, maximumDepthM float64,
) ([][4]float64, bool, error) {
	if !finiteDomeVolume(minimumValue) || !finiteDomeVolume(maximumValue) ||
		minimumValue > maximumValue || !finiteDomeVolume(minimumDepthM) ||
		!finiteDomeVolume(maximumDepthM) || minimumDepthM >= maximumDepthM {
		return nil, false, errors.New("invalid ICON-EU Astrodome PBL clamp enclosure")
	}
	if maximumValue <= minimumDepthM || minimumValue >= maximumDepthM {
		// On either saturated branch the clamped mixed-layer contribution is
		// constant, so only the bilinear surface height contributes curvature.
		return [][4]float64{surfaceHeights}, true, nil
	}
	if minimumValue >= minimumDepthM && maximumValue <= maximumDepthM {
		// In the identity branch the physical boundary is exactly HSURF+MH.
		return [][4]float64{surfaceHeights, mixedLayerDepths}, true, nil
	}
	// The interval enclosure intersects a clamp kink. Keep the complete
	// Lipschitz bound, but do not claim a smooth derivative certificate.
	return [][4]float64{surfaceHeights, mixedLayerDepths}, false, nil
}

type domeAstrodomeAngularDerivativeBounds struct {
	accelerationMPerS2 float64
	speedSquared       float64
	radiusM            float64
	latitudeFirst      float64
	longitudeFirst     float64
	latitudeSecond     float64
	longitudeSecond    float64
}

func domeAstrodomeAngularBounds(
	ray forecast.AstrodomeRefractedRay,
	metric domeAstrodomeMetricBounds,
	startM, endM float64,
) (domeAstrodomeAngularDerivativeBounds, error) {
	acceleration, err := ray.PositionAccelerationNormUpperBound(startM, endM)
	if err != nil {
		return domeAstrodomeAngularDerivativeBounds{}, err
	}
	speed := metric.pathDerivativeNorm
	radius := metric.radiusMinimumM
	cosineLower := math.Nextafter(metric.longitudeRadiusM/radius, 0)
	if !finiteDomeVolume(acceleration) || acceleration < 0 ||
		!finiteDomeVolume(speed) || speed <= 0 || !finiteDomeVolume(radius) || radius <= 0 ||
		!finiteDomeVolume(cosineLower) || cosineLower <= 0 || cosineLower > 1 ||
		!finiteDomeVolume(metric.incrementRadians) || metric.incrementRadians <= 0 {
		return domeAstrodomeAngularDerivativeBounds{}, errors.New(
			"ICON-EU Astrodome scalar field has invalid angular-derivative scales",
		)
	}

	speedSquared := domeAstrodomePositiveMulUpper(speed, speed)
	radiusSquaredLower := math.Nextafter(radius*radius, 0)
	longitudeRadiusSquaredLower := math.Nextafter(metric.longitudeRadiusM*metric.longitudeRadiusM, 0)
	secantLatitudeFactor := domeAstrodomePositiveDivUpper(radius, metric.longitudeRadiusM)
	latitudeAngularFirst := domeAstrodomePositiveDivUpper(speed, radius)
	longitudeAngularFirst := domeAstrodomePositiveDivUpper(speed, metric.longitudeRadiusM)
	latitudeAngularSecond := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveDivUpper(acceleration, radius),
		domeAstrodomePositiveMulUpper(
			domeAstrodomePositiveDivUpper(speedSquared, radiusSquaredLower),
			secantLatitudeFactor,
		),
	)
	longitudeAngularSecond := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveDivUpper(acceleration, metric.longitudeRadiusM),
		domeAstrodomePositiveDivUpper(speedSquared, longitudeRadiusSquaredLower),
	)
	latitudeFirst := domeAstrodomePositiveDivUpper(latitudeAngularFirst, metric.incrementRadians)
	longitudeFirst := domeAstrodomePositiveDivUpper(
		longitudeAngularFirst, metric.incrementRadians,
	)
	latitudeSecond := domeAstrodomePositiveDivUpper(
		latitudeAngularSecond, metric.incrementRadians,
	)
	longitudeSecond := domeAstrodomePositiveDivUpper(
		longitudeAngularSecond, metric.incrementRadians,
	)
	return domeAstrodomeAngularDerivativeBounds{
		accelerationMPerS2: acceleration,
		speedSquared:       speedSquared,
		radiusM:            radius,
		latitudeFirst:      latitudeFirst,
		longitudeFirst:     longitudeFirst,
		latitudeSecond:     latitudeSecond,
		longitudeSecond:    longitudeSecond,
	}, nil
}

func domeAstrodomeBilinearFieldsSecondDerivativeBound(
	angular domeAstrodomeAngularDerivativeBounds,
	fields [][4]float64,
	roundoffOperandScale float64,
) (float64, error) {
	if !finiteDomeVolume(roundoffOperandScale) || roundoffOperandScale <= 0 || len(fields) == 0 {
		return 0, errors.New("ICON-EU Astrodome scalar field has an invalid second-derivative request")
	}

	boundarySecond := 0.0
	for _, values := range fields {
		if domeAstrodomeScalarFieldConstant(values) {
			continue
		}
		roundoff := domeAstrodomeClearanceRoundoff(0, 0, roundoffOperandScale)
		latitudeDifference := math.Nextafter(
			math.Max(math.Abs(values[2]-values[0]), math.Abs(values[3]-values[1]))+2*roundoff,
			math.Inf(1),
		)
		longitudeDifference := math.Nextafter(
			math.Max(math.Abs(values[1]-values[0]), math.Abs(values[3]-values[2]))+2*roundoff,
			math.Inf(1),
		)
		mixedDifference := math.Nextafter(
			math.Abs(values[3]-values[2]-values[1]+values[0])+4*roundoff,
			math.Inf(1),
		)
		fieldSecond := domeAstrodomePositiveAddUpper(
			domeAstrodomePositiveMulUpper(latitudeDifference, angular.latitudeSecond),
			domeAstrodomePositiveAddUpper(
				domeAstrodomePositiveMulUpper(longitudeDifference, angular.longitudeSecond),
				domeAstrodomePositiveMulUpper(
					domeAstrodomePositiveMulUpper(2, mixedDifference),
					domeAstrodomePositiveMulUpper(angular.latitudeFirst, angular.longitudeFirst),
				),
			),
		)
		boundarySecond = domeAstrodomePositiveAddUpper(boundarySecond, fieldSecond)
	}
	if !finiteDomeVolume(boundarySecond) || boundarySecond < 0 {
		return 0, errors.New("ICON-EU Astrodome scalar field has no finite second-derivative bound")
	}
	return boundarySecond, nil
}

func domeAstrodomePhysicalBoundarySecondDerivativeBound(
	ray forecast.AstrodomeRefractedRay,
	metric domeAstrodomeMetricBounds,
	fields [][4]float64,
	startM, endM float64,
) (float64, error) {
	angular, err := domeAstrodomeAngularBounds(ray, metric, startM, endM)
	if err != nil {
		return 0, err
	}
	boundarySecond, err := domeAstrodomeBilinearFieldsSecondDerivativeBound(
		angular, fields, domeAstrodomePhysicalHeightOperandScaleM(),
	)
	if err != nil {
		return 0, err
	}
	radialSecond := domeAstrodomePositiveAddUpper(
		angular.accelerationMPerS2,
		domeAstrodomePositiveDivUpper(angular.speedSquared, angular.radiusM),
	)
	total := domeAstrodomePositiveAddUpper(radialSecond, boundarySecond)
	if !finiteDomeVolume(total) || total <= 0 {
		return 0, errors.New("ICON-EU Astrodome physical boundary has no finite second-derivative bound")
	}
	return total, nil
}

// Every WMO candidate predicate is a linear combination of bilinearly
// reconstructed native HHL, temperature, or pressure fields. Along one smooth
// ray segment it is therefore a smooth bilinear scalar field, even though its
// evaluator deliberately preserves the science kernel's compensated operation
// order. A secant plus the complete spherical-coordinate curvature bound can
// certify monotonicity locally and retain an ancestor's strict sign-change
// proof when later micrometre-scale residuals are dominated by roundoff.
func domeAstrodomeBilinearScalarSlopeBounds(
	ray forecast.AstrodomeRefractedRay,
	metric domeAstrodomeMetricBounds,
	field domeAstrodomeScalarField,
	lipschitz, startM, endM float64,
) (domeAstrodomeResidualSlopeBounds, error) {
	if !finiteDomeVolume(lipschitz) || lipschitz <= 0 {
		return nil, errors.New("ICON-EU Astrodome scalar field has no positive slope bound")
	}
	secondDerivative, err := domeAstrodomeBilinearScalarSecondDerivativeBound(
		ray, metric, field, startM, endM,
	)
	if err != nil {
		return nil, err
	}
	return func(
		leftM, rightM float64,
		leftSample, rightSample domeAstrodomeResidualSample,
	) (float64, float64, error) {
		lower, upper, boundsErr := domeAstrodomeSecantSlopeBoundsResiduals(
			leftM, rightM, leftSample, rightSample,
			field.roundoffOperandScale, secondDerivative,
		)
		if boundsErr != nil {
			return 0, 0, boundsErr
		}
		lower = math.Max(math.Nextafter(-lipschitz, math.Inf(-1)), lower)
		upper = math.Min(math.Nextafter(lipschitz, math.Inf(1)), upper)
		if lower > upper {
			return 0, 0, fmt.Errorf("%w: Astrodome scalar slope enclosures do not intersect",
				forecast.ErrAstrodomeScienceIncompletePartition)
		}
		return lower, upper, nil
	}, nil
}

func domeAstrodomeBilinearScalarSecondDerivativeBound(
	ray forecast.AstrodomeRefractedRay,
	metric domeAstrodomeMetricBounds,
	field domeAstrodomeScalarField,
	startM, endM float64,
) (float64, error) {
	angular, err := domeAstrodomeAngularBounds(ray, metric, startM, endM)
	if err != nil {
		return 0, err
	}
	secondDerivative, err := domeAstrodomeBilinearFieldsSecondDerivativeBound(
		angular, [][4]float64{field.corners}, field.roundoffOperandScale,
	)
	if err != nil {
		return 0, err
	}
	return secondDerivative, nil
}

// domeAstrodomeEndpointSlopeBoundsFromInterior proves a derivative
// enclosure on each endpoint sliver without evaluating the ambiguously owned
// horizontal-cell endpoint. The secant/curvature enclosure on the adjacent
// fully interior probe interval contains f' at the shared probe. If |f”|<=M,
// every derivative in the omitted sliver differs from that endpoint
// derivative by at most M times the sliver width. Callers intersect this local
// enclosure with their independent global derivative bound. The helper is
// used only for smooth physical residuals and raw bilinear MH clamp
// predicates; it is not applied to WMO decision evaluators or the nonlinear
// 200-hPa fallback.
func domeAstrodomeEndpointSlopeBoundsFromInterior(
	probePaths [5]float64,
	probeValues [5]domeAstrodomeResidualSample,
	roundoffOperandScale, secondDerivativeBound float64,
) (domeAstrodomeEndpointSlopeBounds, error) {
	if !finiteDomeVolume(roundoffOperandScale) || roundoffOperandScale <= 0 ||
		!finiteDomeVolume(secondDerivativeBound) || secondDerivativeBound < 0 {
		return nil, errors.New("invalid ICON-EU interior endpoint-slope request")
	}
	for index := range probePaths {
		if !finiteDomeVolume(probePaths[index]) ||
			(index > 0 && probePaths[index] <= probePaths[index-1]) ||
			!finiteDomeVolume(probeValues[index].value) ||
			!finiteDomeVolume(probeValues[index].evaluationError) || probeValues[index].evaluationError < 0 {
			return nil, errors.New("invalid ICON-EU interior endpoint-slope probes")
		}
	}
	type anchor struct {
		leftM, rightM           float64
		leftSample, rightSample domeAstrodomeResidualSample
	}
	startAnchor := anchor{
		leftM: probePaths[0], rightM: probePaths[1],
		leftSample: probeValues[0], rightSample: probeValues[1],
	}
	endAnchor := anchor{
		leftM: probePaths[3], rightM: probePaths[4],
		leftSample: probeValues[3], rightSample: probeValues[4],
	}
	return func(leftM, rightM float64) (float64, float64, error) {
		var selected anchor
		switch {
		case rightM == probePaths[0] && leftM < rightM:
			selected = startAnchor
		case leftM == probePaths[4] && rightM > leftM:
			selected = endAnchor
		default:
			return 0, 0, errors.New("ICON-EU interior endpoint slope requested outside a probe sliver")
		}
		lower, upper, err := domeAstrodomeSecantSlopeBoundsResiduals(
			selected.leftM, selected.rightM, selected.leftSample, selected.rightSample,
			roundoffOperandScale, secondDerivativeBound,
		)
		if err != nil {
			return 0, 0, err
		}
		drift := domeAstrodomePositiveMulUpper(secondDerivativeBound, rightM-leftM)
		lower = math.Nextafter(lower-drift, math.Inf(-1))
		upper = math.Nextafter(upper+drift, math.Inf(1))
		if !finiteDomeVolume(lower) || !finiteDomeVolume(upper) || lower > upper {
			return 0, 0, errors.New("ICON-EU interior endpoint-slope enclosure is invalid")
		}
		return lower, upper, nil
	}, nil
}

func domeAstrodomeSecantSlopeBoundsResiduals(
	leftM, rightM float64,
	leftSample, rightSample domeAstrodomeResidualSample,
	roundoffOperandScale, secondDerivativeBound float64,
) (float64, float64, error) {
	if !finiteDomeVolume(leftSample.value) || !finiteDomeVolume(rightSample.value) ||
		!finiteDomeVolume(leftSample.evaluationError) || leftSample.evaluationError < 0 ||
		!finiteDomeVolume(rightSample.evaluationError) || rightSample.evaluationError < 0 ||
		!finiteDomeVolume(secondDerivativeBound) || secondDerivativeBound < 0 || rightM <= leftM {
		return 0, 0, errors.New("invalid Astrodome secant-slope certificate")
	}
	leftLower, leftUpper := domeAstrodomeResidualInterval(leftSample, roundoffOperandScale, 0)
	rightLower, rightUpper := domeAstrodomeResidualInterval(rightSample, roundoffOperandScale, 0)
	deltaLower := math.Nextafter(rightLower-leftUpper, math.Inf(-1))
	deltaUpper := math.Nextafter(rightUpper-leftLower, math.Inf(1))
	pathScale := math.Max(1, math.Max(math.Abs(leftM), math.Abs(rightM)))
	width := rightM - leftM
	widthError := domeAstrodomeClearanceRoundoff(width, 0, pathScale)
	widthLower := math.Nextafter(width-widthError, 0)
	widthUpper := math.Nextafter(width+widthError, math.Inf(1))
	if widthLower <= 0 || !finiteDomeVolume(widthUpper) {
		return 0, 0, errors.New("astrodome secant-slope interval has no positive width")
	}
	quotients := [...]float64{
		math.Nextafter(deltaLower/widthLower, math.Inf(-1)),
		math.Nextafter(deltaLower/widthUpper, math.Inf(-1)),
		math.Nextafter(deltaUpper/widthLower, math.Inf(1)),
		math.Nextafter(deltaUpper/widthUpper, math.Inf(1)),
	}
	secantLower, secantUpper := quotients[0], quotients[0]
	for _, quotient := range quotients[1:] {
		secantLower = math.Min(secantLower, quotient)
		secantUpper = math.Max(secantUpper, quotient)
	}
	secantLower = math.Nextafter(secantLower, math.Inf(-1))
	secantUpper = math.Nextafter(secantUpper, math.Inf(1))
	// The secant is the interval mean of f'. If |f''| <= M, every f'(x)
	// differs from that mean by at most M*w/2. This is the sharp endpoint
	// bound and is applied only to a smooth scalar boundary inside one
	// horizontal cell.
	drift := domeAstrodomePositiveDivUpper(
		domeAstrodomePositiveMulUpper(secondDerivativeBound, widthUpper), 2,
	)
	lower := math.Nextafter(secantLower-drift, math.Inf(-1))
	upper := math.Nextafter(secantUpper+drift, math.Inf(1))
	if !finiteDomeVolume(lower) || !finiteDomeVolume(upper) || lower > upper {
		return 0, 0, errors.New("astrodome secant-slope enclosure is invalid")
	}
	return lower, upper, nil
}

func domeAstrodomeSecantSlopeBounds(
	leftM, rightM, leftValue, rightValue, roundoffOperandScale, secondDerivativeBound float64,
) (float64, float64, error) {
	return domeAstrodomeSecantSlopeBoundsResiduals(
		leftM, rightM,
		domeAstrodomeResidualSample{value: leftValue},
		domeAstrodomeResidualSample{value: rightValue},
		roundoffOperandScale, secondDerivativeBound,
	)
}

func domeAstrodomeScalarFieldConstant(values [4]float64) bool {
	return values[0] == values[1] && values[0] == values[2] && values[0] == values[3]
}

func domeAstrodomeFallbackBoundaryLipschitz(
	metric domeAstrodomeMetricBounds,
	profile domeAstrodomeTropopauseProfile,
	lowerLevel int,
) (float64, error) {
	upperLevel := lowerLevel + 1
	if lowerLevel < 0 || upperLevel >= len(profile.heights) ||
		len(profile.heights) != len(profile.pressures) {
		return 0, errors.New("ICON-EU 200-hPa fallback bracket is invalid")
	}
	lowerHeight, upperHeight := profile.heights[lowerLevel], profile.heights[upperLevel]
	lowerPressure, upperPressure := profile.pressures[lowerLevel], profile.pressures[upperLevel]
	lowerPressureMin, lowerPressureMax := domeAstrodomeCornerRange(lowerPressure)
	upperPressureMin, _ := domeAstrodomeCornerRange(upperPressure)
	pressureOperandScale := math.Max(
		domeAstrodomeCornerOperandScale(lowerPressure), domeAstrodomeCornerOperandScale(upperPressure),
	)
	pressureRoundoff := domeAstrodomeClearanceRoundoff(0, 0, pressureOperandScale)
	heightRoundoff := domeAstrodomeClearanceRoundoff(0, 0, domeAstrodomePhysicalHeightOperandScaleM())
	minimumPressureGap, maximumHeightGap := math.Inf(1), 0.0
	for corner := range lowerPressure {
		gapLower := domeAstrodomePositiveSubLower(
			lowerPressure[corner], upperPressure[corner], pressureOperandScale,
		)
		minimumPressureGap = math.Min(minimumPressureGap, gapLower)
		heightGapUpper := math.Nextafter(
			math.Abs(upperHeight[corner]-lowerHeight[corner])+heightRoundoff, math.Inf(1),
		)
		maximumHeightGap = math.Max(maximumHeightGap, heightGapUpper)
	}
	lowerPressureMin = math.Nextafter(lowerPressureMin-pressureRoundoff, math.Inf(-1))
	upperPressureMin = math.Nextafter(upperPressureMin-pressureRoundoff, math.Inf(-1))
	lowerPressureMax = math.Nextafter(lowerPressureMax+pressureRoundoff, math.Inf(1))
	if lowerPressureMin <= 0 || upperPressureMin <= 0 || minimumPressureGap <= 0 ||
		maximumHeightGap <= 0 || !finiteDomeVolume(minimumPressureGap) {
		return 0, errors.New("ICON-EU 200-hPa fallback bracket has no positive native separation")
	}
	lowerPressureLipschitz, err := domeAstrodomeScalarFieldLipschitz(
		metric, lowerPressure, domeAstrodomeCornerOperandScale(lowerPressure),
	)
	if err != nil && !domeAstrodomeScalarFieldConstant(lowerPressure) {
		return 0, err
	}
	upperPressureLipschitz, err := domeAstrodomeScalarFieldLipschitz(
		metric, upperPressure, domeAstrodomeCornerOperandScale(upperPressure),
	)
	if err != nil && !domeAstrodomeScalarFieldConstant(upperPressure) {
		return 0, err
	}
	lowerHeightLipschitz, err := domeAstrodomeScalarFieldLipschitz(
		metric, lowerHeight, domeAstrodomePhysicalHeightOperandScaleM(),
	)
	if err != nil && !domeAstrodomeScalarFieldConstant(lowerHeight) {
		return 0, err
	}
	upperHeightLipschitz, err := domeAstrodomeScalarFieldLipschitz(
		metric, upperHeight, domeAstrodomePhysicalHeightOperandScaleM(),
	)
	if err != nil && !domeAstrodomeScalarFieldConstant(upperHeight) {
		return 0, err
	}

	// H_200 = h_l + a(h_u-h_l), where
	// a=(log(P_200)-log(p_l))/(log(p_u)-log(p_l)). Bound every
	// factor before applying the quotient rule. Bilinear interpolation preserves
	// the positive native p_l-p_u gap; d/log p_l/p_u is therefore separated
	// from zero throughout the complete cell.
	lowerLogLipschitz := domeAstrodomePositiveDivUpper(lowerPressureLipschitz, lowerPressureMin)
	upperLogLipschitz := domeAstrodomePositiveDivUpper(upperPressureLipschitz, upperPressureMin)
	denominatorMinimum := math.Nextafter(minimumPressureGap/lowerPressureMax, 0)
	targetLog := math.Log(domeAstrodomeFallbackPressurePa)
	numeratorMaximum := math.Max(
		math.Abs(targetLog-math.Log(lowerPressureMin)),
		math.Abs(targetLog-math.Log(lowerPressureMax)),
	)
	logOperandScale := math.Max(
		1, math.Max(math.Abs(targetLog), math.Max(math.Abs(math.Log(lowerPressureMin)), math.Abs(math.Log(lowerPressureMax)))),
	)
	numeratorMaximum = math.Nextafter(
		numeratorMaximum+2*domeAstrodomeClearanceRoundoff(0, 0, logOperandScale), math.Inf(1),
	)
	if denominatorMinimum <= 0 || !finiteDomeVolume(denominatorMinimum) {
		return 0, errors.New("ICON-EU 200-hPa fallback logarithmic bracket is invalid")
	}
	fractionMaximum := domeAstrodomePositiveDivUpper(numeratorMaximum, denominatorMinimum)
	logSum := domeAstrodomePositiveAddUpper(lowerLogLipschitz, upperLogLipschitz)
	denominatorSquaredLower := math.Nextafter(denominatorMinimum*denominatorMinimum, 0)
	fractionLipschitz := domeAstrodomePositiveAddUpper(
		domeAstrodomePositiveDivUpper(lowerLogLipschitz, denominatorMinimum),
		domeAstrodomePositiveDivUpper(
			domeAstrodomePositiveMulUpper(numeratorMaximum, logSum),
			denominatorSquaredLower,
		),
	)
	heightLipschitz := domeAstrodomePositiveAddUpper(
		lowerHeightLipschitz,
		domeAstrodomePositiveAddUpper(
			domeAstrodomePositiveMulUpper(fractionLipschitz, maximumHeightGap),
			domeAstrodomePositiveMulUpper(
				fractionMaximum, domeAstrodomePositiveAddUpper(lowerHeightLipschitz, upperHeightLipschitz),
			),
		),
	)
	bound := domeAstrodomePositiveAddUpper(metric.pathDerivativeNorm, heightLipschitz)
	if !finiteDomeVolume(bound) || bound <= 0 {
		return 0, errors.New("ICON-EU 200-hPa fallback has no finite path bound")
	}
	return bound, nil
}

func domeAstrodomePositiveAddUpper(left, right float64) float64 {
	return math.Nextafter(left+right, math.Inf(1))
}

func domeAstrodomePositiveMulUpper(left, right float64) float64 {
	return math.Nextafter(left*right, math.Inf(1))
}

func domeAstrodomePositiveSubLower(minuend, subtrahend, operandScale float64) float64 {
	nominal := minuend - subtrahend
	return math.Nextafter(
		nominal-domeAstrodomeClearanceRoundoff(nominal, subtrahend, operandScale),
		math.Inf(-1),
	)
}

func domeAstrodomePositiveMulLower(left, right, operandScale float64) float64 {
	nominal := left * right
	return math.Nextafter(
		nominal-domeAstrodomeClearanceRoundoff(nominal, 0, operandScale), 0,
	)
}

func domeAstrodomePositiveDivUpper(numerator, denominator float64) float64 {
	return math.Nextafter(numerator/math.Nextafter(denominator, 0), math.Inf(1))
}

func domeAstrodomeCornerRange(values [4]float64) (float64, float64) {
	minimum, maximum := values[0], values[0]
	for _, value := range values[1:] {
		minimum = math.Min(minimum, value)
		maximum = math.Max(maximum, value)
	}
	return minimum, maximum
}

func domeAstrodomeCornerOperandScale(values [4]float64) float64 {
	scale := 1.0
	for _, value := range values {
		scale = math.Max(scale, math.Abs(value))
	}
	return math.Nextafter(scale, math.Inf(1))
}

func domeAstrodomeIsolateFieldAcrossProbesResiduals(
	interval domeAstrodomeCellInterval,
	probePaths [5]float64,
	lipschitz, roundoffOperandScale float64,
	value domeAstrodomeResidualValue,
) ([]float64, error) {
	return domeAstrodomeIsolateFieldAcrossProbesWithSlopeBoundsResiduals(
		interval, probePaths, lipschitz, roundoffOperandScale, nil, value,
	)
}

func domeAstrodomeIsolateFieldAcrossProbes(
	interval domeAstrodomeCellInterval,
	probePaths [5]float64,
	lipschitz, roundoffOperandScale float64,
	value func(float64) (float64, error),
) ([]float64, error) {
	residualValue := func(pathM float64) (domeAstrodomeResidualSample, error) {
		result, err := value(pathM)
		return domeAstrodomeResidualSample{value: result}, err
	}
	return domeAstrodomeIsolateFieldAcrossProbesResiduals(
		interval, probePaths, lipschitz, roundoffOperandScale, residualValue,
	)
}

func domeAstrodomeIsolateFieldAcrossProbesWithSlopeBoundsResiduals(
	interval domeAstrodomeCellInterval,
	probePaths [5]float64,
	lipschitz, roundoffOperandScale float64,
	slopeBounds domeAstrodomeResidualSlopeBounds,
	value domeAstrodomeResidualValue,
) ([]float64, error) {
	return domeAstrodomeIsolateFieldAcrossProbesWithEndpointSlopeBoundsResiduals(
		interval, probePaths, lipschitz, roundoffOperandScale,
		slopeBounds, nil, value,
	)
}

func domeAstrodomeIsolateFieldAcrossProbesWithEndpointSlopeBoundsResiduals(
	interval domeAstrodomeCellInterval,
	probePaths [5]float64,
	lipschitz, roundoffOperandScale float64,
	slopeBounds domeAstrodomeResidualSlopeBounds,
	endpointSlopeBounds domeAstrodomeEndpointSlopeBounds,
	value domeAstrodomeResidualValue,
) ([]float64, error) {
	evidence, err := domeAstrodomeIsolateFieldAcrossProbesWithSlopeBoundsResidualEvidence(
		interval, probePaths, lipschitz, roundoffOperandScale,
		slopeBounds, endpointSlopeBounds, nil, value,
	)
	if err != nil {
		return nil, err
	}
	roots := make([]float64, len(evidence))
	for index := range evidence {
		roots[index] = evidence[index].pathM
	}
	return roots, nil
}

func domeAstrodomeIsolateFieldAcrossProbesWithSlopeBoundsResidualEvidence(
	interval domeAstrodomeCellInterval,
	probePaths [5]float64,
	lipschitz, roundoffOperandScale float64,
	slopeBounds domeAstrodomeResidualSlopeBounds,
	endpointSlopeBounds domeAstrodomeEndpointSlopeBounds,
	approximations *domeAstrodomePathApproximation,
	value domeAstrodomeResidualValue,
) ([]domeAstrodomeRootEvidence, error) {
	probeValues := [5]domeAstrodomeResidualSample{}
	for probe := range probePaths {
		var err error
		probeValues[probe], err = value(probePaths[probe])
		if err != nil {
			return nil, err
		}
	}
	if err := domeAstrodomeCertifyProbeEndpointSliversResidualsWithApproximation(
		interval, probePaths, probeValues, lipschitz, roundoffOperandScale,
		endpointSlopeBounds, approximations,
	); err != nil {
		return nil, err
	}
	remaining := domeAstrodomeRootIsolationBudgetPerField
	// Try the complete interior probe span before treating probe-to-probe
	// intervals independently. A real root can land exactly on an internal
	// probe (or its binary64 residual can become indeterminate there). Strict
	// opposite signs at the outer probes plus one derivative enclosure still
	// prove exactly one root across the complete span; preserving that ancestor
	// proof avoids turning the shared probe into two unresolved half-roots.
	if slopeBounds != nil {
		leftM, rightM := probePaths[0], probePaths[len(probePaths)-1]
		leftSample, rightSample := probeValues[0], probeValues[len(probeValues)-1]
		leftSign, _, _ := domeAstrodomeResidualSignInterval(leftSample, roundoffOperandScale)
		rightSign, _, _ := domeAstrodomeResidualSignInterval(rightSample, roundoffOperandScale)
		if leftSign != 0 && rightSign != 0 {
			lowerSlope, upperSlope, boundsErr := slopeBounds(leftM, rightM, leftSample, rightSample)
			if boundsErr != nil {
				return nil, boundsErr
			}
			if !finiteDomeVolume(lowerSlope) || !finiteDomeVolume(upperSlope) || lowerSlope > upperSlope {
				return nil, fmt.Errorf("%w: invalid Astrodome monotonic slope enclosure",
					forecast.ErrAstrodomeScienceIncompletePartition)
			}
			if lowerSlope > 0 || upperSlope < 0 {
				if leftSign == rightSign {
					return nil, nil
				}
				resolved, localizeErr := domeAstrodomeLocalizeCertifiedMonotoneRootResiduals(
					leftM, rightM, leftSample, rightSample, lowerSlope, upperSlope,
					roundoffOperandScale, domeAstrodomePhysicalRootToleranceM,
					&remaining, value,
				)
				if localizeErr != nil {
					return nil, localizeErr
				}
				return domeAstrodomeResolveRootIsolationEvidence(resolved, rightM)
			}
		}
	}
	result := domeAstrodomeRootIsolationResult{roots: make([]domeAstrodomeRootEvidence, 0, 2)}
	for probe := 0; probe+1 < len(probePaths); probe++ {
		part, err := domeAstrodomeIsolatePathRootsRecursive(
			probePaths[probe], probePaths[probe+1], probeValues[probe], probeValues[probe+1],
			lipschitz, roundoffOperandScale, domeAstrodomePhysicalRootToleranceM,
			slopeBounds, &remaining, value,
		)
		if err != nil {
			return nil, err
		}
		result.roots = append(result.roots, part.roots...)
		result.unresolved = append(result.unresolved, part.unresolved...)
	}
	return domeAstrodomeResolveRootIsolationEvidence(result, probePaths[len(probePaths)-1])
}

func domeAstrodomeIsolateFieldAcrossProbesWithSlopeBounds(
	interval domeAstrodomeCellInterval,
	probePaths [5]float64,
	lipschitz, roundoffOperandScale float64,
	slopeBounds domeAstrodomeSlopeBounds,
	value func(float64) (float64, error),
) ([]float64, error) {
	residualValue := func(pathM float64) (domeAstrodomeResidualSample, error) {
		result, err := value(pathM)
		return domeAstrodomeResidualSample{value: result}, err
	}
	var residualSlope domeAstrodomeResidualSlopeBounds
	if slopeBounds != nil {
		residualSlope = func(
			leftM, rightM float64,
			leftSample, rightSample domeAstrodomeResidualSample,
		) (float64, float64, error) {
			return slopeBounds(leftM, rightM, leftSample.value, rightSample.value)
		}
	}
	return domeAstrodomeIsolateFieldAcrossProbesWithSlopeBoundsResiduals(
		interval, probePaths, lipschitz, roundoffOperandScale,
		residualSlope, residualValue,
	)
}

func domeAstrodomeCertifyProbeEndpointSliversResiduals(
	interval domeAstrodomeCellInterval,
	probePaths [5]float64,
	probeValues [5]domeAstrodomeResidualSample,
	lipschitz, roundoffOperandScale float64,
	slopeBounds domeAstrodomeEndpointSlopeBounds,
) error {
	return domeAstrodomeCertifyProbeEndpointSliversResidualsWithApproximation(
		interval, probePaths, probeValues, lipschitz, roundoffOperandScale,
		slopeBounds, nil,
	)
}

func domeAstrodomeCertifyProbeEndpointSliversResidualsWithApproximation(
	interval domeAstrodomeCellInterval,
	probePaths [5]float64,
	probeValues [5]domeAstrodomeResidualSample,
	lipschitz, roundoffOperandScale float64,
	slopeBounds domeAstrodomeEndpointSlopeBounds,
	approximations *domeAstrodomePathApproximation,
) error {
	if !finiteDomeVolume(interval.startM) || !finiteDomeVolume(interval.endM) ||
		interval.endM <= interval.startM || !finiteDomeVolume(lipschitz) || lipschitz <= 0 ||
		!finiteDomeVolume(roundoffOperandScale) || roundoffOperandScale <= 0 ||
		probePaths[0] < interval.startM || probePaths[len(probePaths)-1] > interval.endM {
		return errors.New("invalid ICON-EU endpoint-sliver certification request")
	}
	for _, side := range []struct {
		name          string
		leftM, rightM float64
		sample        domeAstrodomeResidualSample
		start         bool
	}{
		{name: "start", leftM: interval.startM, rightM: probePaths[0], sample: probeValues[0], start: true},
		{name: "end", leftM: probePaths[len(probePaths)-1], rightM: interval.endM, sample: probeValues[len(probeValues)-1]},
	} {
		width := side.rightM - side.leftM
		if width == 0 {
			continue
		}
		if !finiteDomeVolume(width) || width < 0 ||
			!finiteDomeVolume(side.sample.value) || !finiteDomeVolume(side.sample.evaluationError) ||
			side.sample.evaluationError < 0 {
			return errors.New("invalid ICON-EU endpoint-sliver value")
		}
		maximumVariation := math.Nextafter(lipschitz*width, math.Inf(1))
		lower, upper := domeAstrodomeResidualInterval(
			side.sample, roundoffOperandScale, maximumVariation,
		)
		if lower > maximumVariation || upper < -maximumVariation {
			continue
		}
		lowerSlope, upperSlope := math.NaN(), math.NaN()
		directedLower, directedUpper := math.NaN(), math.NaN()

		// A one-sided derivative enclosure can be stronger than the symmetric
		// |f'| <= L clearance above. Integrate the complete signed derivative
		// interval from the unambiguously interior probe over the omitted
		// sliver. This proves both motion away from zero and motion toward zero
		// that is quantitatively too small to reach it. No value at the
		// ambiguously owned horizontal endpoint is evaluated.
		if slopeBounds != nil {
			var err error
			lowerSlope, upperSlope, err = slopeBounds(side.leftM, side.rightM)
			if err != nil {
				return err
			}
			if !finiteDomeVolume(lowerSlope) || !finiteDomeVolume(upperSlope) || lowerSlope > upperSlope {
				return fmt.Errorf("%w: invalid ICON-EU %s endpoint-sliver slope enclosure",
					forecast.ErrAstrodomeScienceIncompletePartition, side.name)
			}
			lowerSlope = math.Max(lowerSlope, math.Nextafter(-lipschitz, math.Inf(-1)))
			upperSlope = math.Min(upperSlope, math.Nextafter(lipschitz, math.Inf(1)))
			if lowerSlope > upperSlope {
				return fmt.Errorf("%w: ICON-EU %s endpoint-sliver slope enclosures do not intersect",
					forecast.ErrAstrodomeScienceIncompletePartition, side.name)
			}
			pathScale := math.Max(1, math.Max(math.Abs(side.leftM), math.Abs(side.rightM)))
			widthError := domeAstrodomeClearanceRoundoff(width, 0, pathScale)
			widthUpper := math.Nextafter(width+widthError, math.Inf(1))
			positiveSlopeMotion := domeAstrodomePositiveMulUpper(math.Max(0, upperSlope), widthUpper)
			negativeSlopeMotion := domeAstrodomePositiveMulUpper(math.Max(0, -lowerSlope), widthUpper)
			if side.start {
				directedLower = math.Nextafter(lower-positiveSlopeMotion, math.Inf(-1))
				directedUpper = math.Nextafter(upper+negativeSlopeMotion, math.Inf(1))
			} else {
				directedLower = math.Nextafter(lower-negativeSlopeMotion, math.Inf(-1))
				directedUpper = math.Nextafter(upper+positiveSlopeMotion, math.Inf(1))
			}
			if directedLower > 0 || directedUpper < 0 {
				continue
			}
		}
		if approximations != nil {
			if err := approximations.add(side.leftM, side.rightM); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("%w: ICON-EU %s endpoint sliver %.9g m lacks Lipschitz or one-sided derivative root clearance (probe residual [%.9g, %.9g], Lipschitz variation %.9g, endpoint slope [%.9g, %.9g], directed residual [%.9g, %.9g])",
			forecast.ErrAstrodomeScienceIncompletePartition, side.name, width,
			lower, upper, maximumVariation, lowerSlope, upperSlope, directedLower, directedUpper)
	}
	return nil
}

func domeAstrodomeCertifyProbeEndpointSlivers(
	interval domeAstrodomeCellInterval,
	probePaths [5]float64,
	probeValues [5]float64,
	lipschitz, roundoffOperandScale float64,
) error {
	residuals := [5]domeAstrodomeResidualSample{}
	for index, value := range probeValues {
		residuals[index] = domeAstrodomeResidualSample{value: value}
	}
	return domeAstrodomeCertifyProbeEndpointSliversResiduals(
		interval, probePaths, residuals, lipschitz, roundoffOperandScale, nil,
	)
}

func domeAstrodomePhysicalHeightOperandScaleM() float64 {
	// HeightM is obtained from an ECEF norm minus the ICON spherical Earth
	// radius before it is compared with native HHL/surface heights. The
	// residual can be micrometric while both operands are near 6.4e6 m, so its
	// floating-point enclosure must retain that native metre scale.
	return math.Nextafter(forecast.AstrodomeICONSphereRadiusM+200_000, math.Inf(1))
}

func domeAstrodomeClearanceRoundoff(value, lipschitzVariation, operandScale float64) float64 {
	scale := math.Max(1, math.Max(math.Abs(value), math.Abs(lipschitzVariation)))
	scale = math.Max(scale, math.Abs(operandScale))
	roundoff := domeAstrodomePhysicalRoundoffULPs * (math.Nextafter(1, 2) - 1) * scale
	return math.Nextafter(roundoff, math.Inf(1))
}

func domeAstrodomeInteriorRootEvidence(
	interval domeAstrodomeCellInterval,
	roots []domeAstrodomeRootEvidence,
) []domeAstrodomeRootEvidence {
	result := roots[:0]
	for _, root := range roots {
		if root.pathM > interval.startM+domeAstrodomePhysicalMergeToleranceM &&
			root.pathM < interval.endM-domeAstrodomePhysicalMergeToleranceM {
			result = append(result, root)
		}
	}
	return result
}

func domeAstrodomeHHLCornerValues(
	columns [4]forecast.AstrodomePrimitiveColumn,
	levelIndex int,
) ([4]float64, error) {
	values := [4]float64{}
	for index, column := range columns {
		if levelIndex < 0 || levelIndex >= len(column.HalfLevelGeometry) ||
			column.HalfLevelGeometry[levelIndex].ModelHalfLevel != levelIndex+1 ||
			!finiteDomeVolume(column.HalfLevelGeometry[levelIndex].HeightM) {
			return values, fmt.Errorf("ICON-EU Astrodome column %q has invalid HHL level %d",
				column.ColumnID, levelIndex+1)
		}
		values[index] = column.HalfLevelGeometry[levelIndex].HeightM
	}
	return values, nil
}

func domeAstrodomeSurfaceCornerValues(columns [4]forecast.AstrodomePrimitiveColumn) [4]float64 {
	values := [4]float64{}
	for index, column := range columns {
		values[index] = column.HSURFHeightM
	}
	return values
}

func (volume *DomeVolume) domeAstrodomeMixedLayerCornerValues(
	columns [4]forecast.AstrodomePrimitiveColumn,
	validAt time.Time,
) ([4]float64, error) {
	values := [4]float64{}
	bracket, err := domeVolumeBracket(volume.fieldTimes[forecast.AstrodomePrimitiveMixedLayerDepth], validAt)
	if err != nil {
		return values, err
	}
	for index, column := range columns {
		left := column.Frames[bracket.leftIndex].Surface
		right := column.Frames[bracket.rightIndex].Surface
		if !left.Available.Has(forecast.AstrodomePrimitiveMixedLayerDepth) ||
			!right.Available.Has(forecast.AstrodomePrimitiveMixedLayerDepth) {
			return values, errors.New("ICON-EU mixed-layer depth is unavailable at a native bracket")
		}
		values[index] = domeLinear(left.MixedLayerDepthM, right.MixedLayerDepthM, bracket.fraction)
		if !finiteDomeVolume(values[index]) || values[index] < 0 {
			return values, errors.New("ICON-EU mixed-layer depth corner is invalid")
		}
	}
	return values, nil
}

func domeAstrodomePhysicalProbePaths(interval domeAstrodomeCellInterval) ([5]float64, error) {
	paths := [5]float64{}
	if !finiteDomeVolume(interval.startM) || !finiteDomeVolume(interval.endM) ||
		interval.endM-interval.startM <= forecast.AstrodomeScienceMinimumEventIntervalLengthM {
		return paths, fmt.Errorf("ICON-EU Astrodome physical probe interval does not clear the %.9g m compound-root side guard",
			forecast.AstrodomeScienceMinimumEventIntervalLengthM)
	}
	// Cell ownership is ambiguous exactly on a horizontal boundary. Move every
	// probe strictly beyond the certified 0.5-mm compound-root envelope. Each
	// omitted endpoint sliver is then separately proved root-free with the
	// field's Lipschitz bound before root isolation proceeds.
	leftM := interval.startM + forecast.AstrodomeScienceCompoundRootSideGuardM
	for leftM-interval.startM <= forecast.AstrodomeScienceCompoundRootSideGuardM {
		leftM = math.Nextafter(leftM, math.Inf(1))
	}
	rightM := interval.endM - forecast.AstrodomeScienceCompoundRootSideGuardM
	for interval.endM-rightM <= forecast.AstrodomeScienceCompoundRootSideGuardM {
		rightM = math.Nextafter(rightM, math.Inf(-1))
	}
	if leftM >= rightM {
		return paths, fmt.Errorf("ICON-EU Astrodome physical probe interval has no open interior")
	}
	paths[0], paths[4] = leftM, rightM
	for probe := 1; probe < 4; probe++ {
		paths[probe] = leftM + float64(probe)*(rightM-leftM)/4
	}
	return paths, nil
}

func domeAstrodomeNativeBoundaryHeight(
	columns [4]forecast.AstrodomePrimitiveColumn,
	weights [4]float64,
	boundary domeAstrodomeBoundary,
) (float64, error) {
	switch boundary.kind {
	case domeAstrodomeBoundaryHHL:
		return domeInterpolatedHHLHeight(columns, weights, boundary.levelIndex)
	case domeAstrodomeBoundaryFull:
		upper, err := domeInterpolatedHHLHeight(columns, weights, boundary.levelIndex)
		if err != nil {
			return 0, err
		}
		lower, err := domeInterpolatedHHLHeight(columns, weights, boundary.levelIndex+1)
		if err != nil {
			return 0, err
		}
		height := (upper + lower) / 2
		if !finiteDomeVolume(height) || height <= lower || height >= upper {
			return 0, fmt.Errorf("bilinearly reconstructed ICON-EU full level %d is invalid", boundary.levelIndex+1)
		}
		return height, nil
	default:
		return 0, fmt.Errorf("astrodome boundary %q is not a native HHL/full-level surface", boundary.id)
	}
}

func domeBisectionPathRoot(
	leftM, rightM, toleranceM float64,
	value func(float64) (float64, error),
) (float64, error) {
	leftValue, err := value(leftM)
	if err != nil {
		return 0, err
	}
	rightValue, err := value(rightM)
	if err != nil {
		return 0, err
	}
	if leftValue == 0 {
		return leftM, nil
	}
	if rightValue == 0 {
		return rightM, nil
	}
	if (leftValue < 0) == (rightValue < 0) {
		return 0, errors.New("path root is not bracketed")
	}
	for iteration := 0; iteration < 96 && rightM-leftM > toleranceM; iteration++ {
		middleM := (leftM + rightM) / 2
		middleValue, middleErr := value(middleM)
		if middleErr != nil {
			return 0, middleErr
		}
		if middleValue == 0 {
			return middleM, nil
		}
		if (middleValue < 0) == (leftValue < 0) {
			leftM, leftValue = middleM, middleValue
		} else {
			rightM = middleM
		}
	}
	return (leftM + rightM) / 2, nil
}

func compactDomeHorizontalRootCandidates(
	values []domeAstrodomeHorizontalRootCandidate,
) ([]domeAstrodomeHorizontalRootCandidate, error) {
	sort.Slice(values, func(left, right int) bool {
		if values[left].pathM == values[right].pathM {
			return values[left].eventID < values[right].eventID
		}
		return values[left].pathM < values[right].pathM
	})
	result := values[:0]
	for _, candidate := range values {
		if !finiteDomeVolume(candidate.pathM) || strings.TrimSpace(candidate.eventID) == "" {
			return nil, fmt.Errorf("%w: invalid ICON-EU horizontal event candidate",
				forecast.ErrAstrodomeScienceIncompletePartition)
		}
		if !candidate.exact && (!finiteDomeVolume(candidate.leftM) || !finiteDomeVolume(candidate.rightM) ||
			candidate.leftM > candidate.pathM || candidate.pathM > candidate.rightM ||
			candidate.rightM-candidate.leftM > 2*domeAstrodomeHorizontalRootToleranceM) {
			return nil, fmt.Errorf("%w: uncertified ICON-EU horizontal event candidate %q",
				forecast.ErrAstrodomeScienceIncompletePartition, candidate.eventID)
		}
		if len(result) == 0 || candidate.pathM-result[len(result)-1].pathM > domeAstrodomeHorizontalMergeToleranceM {
			result = append(result, candidate)
			continue
		}
		previous := &result[len(result)-1]
		if candidate.pathM == previous.pathM && candidate.leftM == previous.leftM &&
			candidate.rightM == previous.rightM && candidate.eventID == previous.eventID &&
			candidate.exact == previous.exact {
			continue
		}
		if compoundID, ok := compoundDomeHorizontalGridCornerEventIDs(
			previous.eventID, candidate.eventID,
		); ok {
			coincidentExact := previous.exact && candidate.exact && previous.pathM == candidate.pathM
			if !coincidentExact {
				return nil, fmt.Errorf("%w: latitude/longitude events %q and %q have no certified coincident root enclosure",
					forecast.ErrAstrodomeScienceIncompletePartition, previous.eventID, candidate.eventID)
			}
			previous.leftM, previous.rightM = previous.pathM, previous.pathM
			previous.eventID = compoundID
			previous.exact = true
			continue
		}
		return nil, fmt.Errorf("%w: distinct ICON-EU horizontal events %q and %q are %.9g m apart inside the %.9g m root cluster",
			forecast.ErrAstrodomeScienceIncompletePartition, previous.eventID, candidate.eventID,
			candidate.pathM-previous.pathM, domeAstrodomeHorizontalMergeToleranceM)
	}
	return result, nil
}

func domeHorizontalRootCandidatePaths(values []domeAstrodomeHorizontalRootCandidate) []float64 {
	result := make([]float64, len(values))
	for index := range values {
		result[index] = values[index].pathM
	}
	return result
}

func compoundDomeHorizontalGridCornerEventIDs(left, right string) (string, bool) {
	seen := make(map[string]struct{}, 2)
	latitudeID, longitudeID := "", ""
	for _, value := range append(strings.Split(left, "|"), strings.Split(right, "|")...) {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		switch {
		case strings.HasPrefix(value, "latitude/") && len(value) > len("latitude/"):
			if latitudeID != "" {
				return "", false
			}
			latitudeID = value
		case strings.HasPrefix(value, "longitude/") && len(value) > len("longitude/"):
			if longitudeID != "" {
				return "", false
			}
			longitudeID = value
		default:
			return "", false
		}
	}
	if len(seen) != 2 || latitudeID == "" || longitudeID == "" {
		return "", false
	}
	return latitudeID + "|" + longitudeID, true
}
