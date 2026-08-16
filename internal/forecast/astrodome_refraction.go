package forecast

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	AstrodomeRefractionIntegratorVersion       = "dormand-prince-5-4-fsal-event-v4"
	astrodomeRefractionMaximumRefinementLevels = 7
)

type AstrodomeRefractionVerificationMode string

const (
	// AstrodomeRefractionVerificationForwardRepeat publishes only after two
	// independently integrated forward passes meet the declared endpoint,
	// direction, and optical-path repeat tolerances. This is the production
	// contract: the embedded DOPRI5(4) test remains active in every pass, while
	// the third, reverse trajectory is not computed.
	AstrodomeRefractionVerificationForwardRepeat AstrodomeRefractionVerificationMode = "forward_repeat"
	// AstrodomeRefractionVerificationReversible additionally integrates the
	// accepted fine trajectory backwards and applies the declared reversibility
	// tolerances. It is retained for strict calibration and regression work.
	AstrodomeRefractionVerificationReversible AstrodomeRefractionVerificationMode = "forward_repeat_reversible"
)

var (
	ErrAstrodomeRefractionNonConvergence = errors.New("astrodome refracted ray did not converge")
	ErrAstrodomeRefractionTerrain        = errors.New("astrodome refracted ray intersects model terrain")
)

// AstrodomeRefractionCalibration declares numerical error targets. Relative
// tolerance is applied to local step displacement, tangent, and optical-path
// increment, never to the approximately 6371 km ECEF coordinate magnitude.
type AstrodomeRefractionCalibration struct {
	Version                       string                              `json:"version"`
	VerificationMode              AstrodomeRefractionVerificationMode `json:"verification_mode"`
	RelativeTolerance             float64                             `json:"relative_tolerance"`
	PositionAbsoluteToleranceM    float64                             `json:"position_absolute_tolerance_m"`
	DirectionAbsoluteToleranceRad float64                             `json:"direction_absolute_tolerance_rad"`
	OpticalPathAbsoluteToleranceM float64                             `json:"optical_path_absolute_tolerance_m"`
	EventPathToleranceM           float64                             `json:"event_path_tolerance_m"`
	PartitionTransitionToleranceM float64                             `json:"partition_transition_tolerance_m"`
	TerrainDepartureClearanceM    float64                             `json:"terrain_departure_clearance_m"`
	InitialStepM                  float64                             `json:"initial_step_m"`
	MinimumStepM                  float64                             `json:"minimum_step_m"`
	MaximumStepM                  float64                             `json:"maximum_step_m"`
	MaximumPathLengthM            float64                             `json:"maximum_path_length_m"`
	MaximumSteps                  int                                 `json:"maximum_steps"`
	RepeatPositionToleranceM      float64                             `json:"repeat_position_tolerance_m"`
	RepeatDirectionToleranceRad   float64                             `json:"repeat_direction_tolerance_rad"`
	RepeatOpticalPathToleranceM   float64                             `json:"repeat_optical_path_tolerance_m"`
	ReversibilityPositionM        float64                             `json:"reversibility_position_m"`
	ReversibilityDirectionRad     float64                             `json:"reversibility_direction_rad"`
}

func DefaultAstrodomeRefractionCalibration() AstrodomeRefractionCalibration {
	return AstrodomeRefractionCalibration{
		Version:           AstrodomeRefractionIntegratorVersion,
		VerificationMode:  AstrodomeRefractionVerificationForwardRepeat,
		RelativeTolerance: 1e-9, PositionAbsoluteToleranceM: 1e-4,
		DirectionAbsoluteToleranceRad: 1e-11, OpticalPathAbsoluteToleranceM: 1e-4,
		EventPathToleranceM: 1e-2, PartitionTransitionToleranceM: 25,
		TerrainDepartureClearanceM: 0.1,
		InitialStepM:               25, MinimumStepM: 1e-4, MaximumStepM: 1000,
		MaximumPathLengthM: 500000, MaximumSteps: 1000000,
		// Ten nanoradians correspond to about 0.002 arcsec and at most 5 mm
		// transverse displacement over the complete 500 km guard path. This is
		// tighter than both the 1 mm physical-event bracket and the published
		// 0.01-arcsec seeing resolution while avoiding meaningless refinements.
		RepeatPositionToleranceM: 0.02, RepeatDirectionToleranceRad: 1e-8,
		RepeatOpticalPathToleranceM: 0.02,
		ReversibilityPositionM:      0.05, ReversibilityDirectionRad: 5e-9,
	}
}

func (calibration AstrodomeRefractionCalibration) Validate() error {
	if calibration.Version != AstrodomeRefractionIntegratorVersion {
		return fmt.Errorf("unsupported astrodome refraction integrator %q", calibration.Version)
	}
	values := []float64{
		calibration.RelativeTolerance, calibration.PositionAbsoluteToleranceM,
		calibration.DirectionAbsoluteToleranceRad, calibration.OpticalPathAbsoluteToleranceM,
		calibration.EventPathToleranceM, calibration.PartitionTransitionToleranceM,
		calibration.TerrainDepartureClearanceM,
		calibration.InitialStepM, calibration.MinimumStepM, calibration.MaximumStepM,
		calibration.MaximumPathLengthM, calibration.RepeatPositionToleranceM,
		calibration.RepeatDirectionToleranceRad, calibration.RepeatOpticalPathToleranceM,
		calibration.ReversibilityPositionM, calibration.ReversibilityDirectionRad,
	}
	for _, value := range values {
		if !finite(value) || value <= 0 {
			return fmt.Errorf("astrodome refraction calibration values must be finite and positive")
		}
	}
	if calibration.VerificationMode != AstrodomeRefractionVerificationForwardRepeat &&
		calibration.VerificationMode != AstrodomeRefractionVerificationReversible {
		return fmt.Errorf("unsupported astrodome refraction verification mode %q", calibration.VerificationMode)
	}
	if calibration.RelativeTolerance > 1e-3 || calibration.DirectionAbsoluteToleranceRad > 1e-5 ||
		calibration.MinimumStepM > calibration.InitialStepM || calibration.InitialStepM > calibration.MaximumStepM ||
		calibration.MaximumStepM >= calibration.MaximumPathLengthM ||
		calibration.EventPathToleranceM > calibration.PartitionTransitionToleranceM ||
		calibration.PartitionTransitionToleranceM > calibration.MaximumStepM ||
		calibration.MinimumStepM > math.Ldexp(calibration.PartitionTransitionToleranceM, -astrodomeRefractionMaximumRefinementLevels) {
		return fmt.Errorf("astrodome refraction calibration scales are inconsistent")
	}
	if calibration.MaximumSteps < 100 {
		return fmt.Errorf("astrodome refraction maximum steps are too small")
	}
	return nil
}

type AstrodomeRefractionNumericalError struct {
	EndpointPositionM         float64 `json:"endpoint_position_m"`
	DirectionAtICONTopRad     float64 `json:"direction_at_icon_top_rad"`
	OpticalPathM              float64 `json:"optical_path_m"`
	ReversibilityPositionM    float64 `json:"reversibility_position_m"`
	ReversibilityDirectionRad float64 `json:"reversibility_direction_rad"`
	ReversibilityChecked      bool    `json:"reversibility_checked"`
	MaximumAcceptedErrorRatio float64 `json:"maximum_accepted_error_ratio"`
	MaximumTangentNormError   float64 `json:"maximum_tangent_norm_error"`
	TopRootBracketM           float64 `json:"top_root_bracket_m"`
}

type AstrodomeRefractionDiagnostics struct {
	// AcceptedSteps through PartitionEvents describe the accepted published
	// forward pass. TotalFieldEvaluations includes discarded forward refinements
	// and the optional reverse verification pass.
	AcceptedSteps         int     `json:"accepted_steps"`
	RejectedSteps         int     `json:"rejected_steps"`
	FieldEvaluations      int     `json:"field_evaluations"`
	PartitionEvents       int     `json:"partition_events"`
	MaximumPartitionStepM float64 `json:"maximum_partition_step_m"`
	ForwardPasses         int     `json:"forward_passes"`
	ReversePasses         int     `json:"reverse_passes"`
	TotalFieldEvaluations int     `json:"total_field_evaluations"`
}

type AstrodomeRefractedRayPoint struct {
	AstrodomeRayPoint
	TangentECEF     AstrodomeECEFVector `json:"tangent_ecef"`
	OpticalPathM    float64             `json:"optical_path_m"`
	RefractiveIndex float64             `json:"refractive_index"`
}

// AstrodomeRefractedRay is parameterised by geometric arc length s. The
// direction at the endpoint is deliberately named ICON-top direction: the
// model top is not vacuum and residual stratospheric refraction remains.
type AstrodomeRefractedRay struct {
	GeometryVersion         string                              `json:"geometry_version"`
	IntegratorVersion       string                              `json:"integrator_version"`
	VerificationMode        AstrodomeRefractionVerificationMode `json:"verification_mode"`
	RefractivityVersion     string                              `json:"refractivity_version"`
	Observer                Location                            `json:"observer"`
	ObserverHeightM         float64                             `json:"observer_height_m"`
	VisibleElevationDegrees float64                             `json:"visible_elevation_deg"`
	VisibleAzimuthDegrees   *float64                            `json:"visible_azimuth_deg,omitempty"`
	InitialTangentECEF      AstrodomeECEFVector                 `json:"initial_tangent_ecef"`
	DirectionAtICONTopECEF  AstrodomeECEFVector                 `json:"direction_at_icon_top_ecef"`
	TopPoint                AstrodomeRefractedRayPoint          `json:"top_point"`
	PathLengthM             float64                             `json:"path_length_m"`
	OpticalPathM            float64                             `json:"optical_path_m"`
	NumericalError          AstrodomeRefractionNumericalError   `json:"numerical_error"`
	Diagnostics             AstrodomeRefractionDiagnostics      `json:"diagnostics"`

	segments []astrodomeRefractionDenseSegment
}

// AstrodomeRefractedPathInterval exposes only the accepted integration
// abscissae needed by a provider path planner. It does not expose RK stages
// and cannot be used to interpolate a finished science quantity.
type AstrodomeRefractedPathInterval struct {
	StartPathM float64 `json:"start_path_m"`
	EndPathM   float64 `json:"end_path_m"`
}

// IntegrationIntervals returns the accepted Dormand--Prince dense-output
// intervals. A path planner scans these bounded intervals and solves native
// cell, HHL, PBL, tropopause, and cloud-tier events on the curved trajectory.
func (ray AstrodomeRefractedRay) IntegrationIntervals() ([]AstrodomeRefractedPathInterval, error) {
	if err := ray.validate(); err != nil {
		return nil, err
	}
	result := make([]AstrodomeRefractedPathInterval, len(ray.segments))
	for index, segment := range ray.segments {
		result[index] = AstrodomeRefractedPathInterval{StartPathM: segment.startPathM, EndPathM: segment.endPathM}
	}
	return result, nil
}

func (ray AstrodomeRefractedRay) validate() error {
	if ray.GeometryVersion != AstrodomeRefractionGeometryVersion ||
		ray.IntegratorVersion != AstrodomeRefractionIntegratorVersion ||
		(ray.VerificationMode != AstrodomeRefractionVerificationForwardRepeat &&
			ray.VerificationMode != AstrodomeRefractionVerificationReversible) ||
		strings.TrimSpace(ray.RefractivityVersion) == "" {
		return fmt.Errorf("astrodome refracted ray version identity is incomplete")
	}
	if err := ValidateCoordinates(ray.Observer.Latitude, ray.Observer.Longitude); err != nil {
		return fmt.Errorf("astrodome refracted observer: %w", err)
	}
	if !finite(ray.ObserverHeightM) || !finite(ray.PathLengthM) || ray.PathLengthM <= 0 ||
		!finite(ray.OpticalPathM) || ray.OpticalPathM <= ray.PathLengthM || len(ray.segments) == 0 {
		return fmt.Errorf("astrodome refracted ray path is invalid")
	}
	if _, _, err := astrodomeENUUnitDirection(ray.VisibleElevationDegrees, ray.VisibleAzimuthDegrees); err != nil {
		return err
	}
	if math.Abs(ray.InitialTangentECEF.Norm()-1) > 1e-8 ||
		math.Abs(ray.DirectionAtICONTopECEF.Norm()-1) > 1e-8 {
		return fmt.Errorf("astrodome refracted ray endpoint tangents are not unit vectors")
	}
	if math.Abs(ray.TopPoint.PathLengthM-ray.PathLengthM) > 1e-6 ||
		astrodomeVectorDistance(ray.TopPoint.ECEF, astrodomeStatePosition(ray.segments[len(ray.segments)-1].evaluate(ray.PathLengthM))) > 1e-3 {
		return fmt.Errorf("astrodome refracted ray top closure is inconsistent")
	}
	return nil
}

// PointAtPathLength evaluates the Dormand--Prince continuous extension used
// by the accepted integration, rather than linearly interpolating a finished
// seeing/transmission/index product.
func (ray AstrodomeRefractedRay) PointAtPathLength(pathLengthM float64) (AstrodomeRefractedRayPoint, error) {
	if !finite(pathLengthM) || pathLengthM < 0 || pathLengthM > ray.PathLengthM || len(ray.segments) == 0 {
		return AstrodomeRefractedRayPoint{}, fmt.Errorf("refracted-ray path length is outside [0, %.6f] m", ray.PathLengthM)
	}
	index := ray.denseSegmentIndex(pathLengthM)
	state := ray.segments[index].evaluate(pathLengthM)
	point := astrodomeRefractedPoint(state, pathLengthM, ray.Observer)
	point.RefractiveIndex = ray.segments[index].refractiveIndex(pathLengthM)
	return point, nil
}

// PositionEvaluationErrorUpperBound returns the rigorous floating-point
// formation-sum bound for evaluating the stored Shampine dense position at
// pathLengthM. It describes evaluation of the accepted numerical trajectory,
// not the separate physical truncation error of the refraction integration.
// Provider path planners use it when a Cartesian point is transformed to a
// native-grid coordinate for a side-sensitive branch certificate.
func (ray AstrodomeRefractedRay) PositionEvaluationErrorUpperBound(pathLengthM float64) (float64, error) {
	if !finite(pathLengthM) || pathLengthM < 0 || pathLengthM > ray.PathLengthM || len(ray.segments) == 0 {
		return 0, fmt.Errorf("refracted-ray path length is outside [0, %.6f] m", ray.PathLengthM)
	}
	positionErrorM, _, err := ray.segments[ray.denseSegmentIndex(pathLengthM)].
		positionAndDerivativeEvaluationErrors(pathLengthM)
	if err != nil {
		return 0, err
	}
	if !finite(positionErrorM) || positionErrorM < 0 {
		return 0, fmt.Errorf("refracted-ray dense position evaluation error is invalid")
	}
	return math.Nextafter(positionErrorM, math.Inf(1)), nil
}

func (ray AstrodomeRefractedRay) denseSegmentIndex(pathLengthM float64) int {
	index := sort.Search(len(ray.segments), func(index int) bool {
		return ray.segments[index].endPathM >= pathLengthM
	})
	if index == len(ray.segments) {
		index--
	}
	return index
}

// PositionDerivativeNormUpperBound returns an upper bound for |dr/ds| on the
// stored Shampine continuous extension. It is intentionally derived from the
// dense polynomial itself: MaximumTangentNormError only describes accepted RK
// endpoint states and is not a bound for the polynomial between them.
//
// For every overlapping dense segment, dr/ds is a cubic vector polynomial.
// Its four Bernstein control vectors enclose the complete curve by the convex
// hull property, so the greatest control-vector norm bounds every interior
// derivative norm. A small outward floating-point allowance covers formation
// of the power and Bernstein coefficients.
func (ray AstrodomeRefractedRay) PositionDerivativeNormUpperBound(startPathM, endPathM float64) (float64, error) {
	if !finite(startPathM) || !finite(endPathM) || startPathM < 0 ||
		endPathM <= startPathM || endPathM > ray.PathLengthM || len(ray.segments) == 0 {
		return 0, fmt.Errorf("refracted-ray derivative interval is outside [0, %.6f] m", ray.PathLengthM)
	}
	maximum, coveredThrough := 0.0, startPathM
	for _, segment := range ray.segments {
		if segment.endPathM <= startPathM || segment.startPathM >= endPathM {
			continue
		}
		overlapStartM := math.Max(startPathM, segment.startPathM)
		overlapEndM := math.Min(endPathM, segment.endPathM)
		if !finite(segment.startPathM) || !finite(segment.endPathM) ||
			!finite(segment.baseStepM) || segment.baseStepM <= 0 ||
			segment.endPathM <= segment.startPathM || segment.startPathM > coveredThrough {
			return 0, fmt.Errorf("refracted-ray derivative interval is not covered by contiguous dense segments")
		}
		bound, err := segment.positionDerivativeNormUpperBound(overlapStartM, overlapEndM)
		if err != nil {
			return 0, err
		}
		maximum = math.Max(maximum, bound)
		coveredThrough = math.Max(coveredThrough, overlapEndM)
	}
	if coveredThrough < endPathM || !finite(maximum) || maximum <= 0 {
		return 0, fmt.Errorf("refracted-ray derivative interval has no finite dense segment")
	}
	return maximum, nil
}

// PositionAccelerationNormUpperBound returns an outward bound for |d2r/ds2|
// on the stored Shampine continuous extension. Each overlapping quadratic
// acceleration polynomial is affinely restricted to the requested path
// interval before its Bernstein convex hull is evaluated.
func (ray AstrodomeRefractedRay) PositionAccelerationNormUpperBound(startPathM, endPathM float64) (float64, error) {
	if !finite(startPathM) || !finite(endPathM) || startPathM < 0 ||
		endPathM <= startPathM || endPathM > ray.PathLengthM || len(ray.segments) == 0 {
		return 0, fmt.Errorf("refracted-ray acceleration interval is outside [0, %.6f] m", ray.PathLengthM)
	}
	maximum, coveredThrough := 0.0, startPathM
	for _, segment := range ray.segments {
		if segment.endPathM <= startPathM || segment.startPathM >= endPathM {
			continue
		}
		overlapStartM := math.Max(startPathM, segment.startPathM)
		overlapEndM := math.Min(endPathM, segment.endPathM)
		if !finite(segment.startPathM) || !finite(segment.endPathM) ||
			!finite(segment.baseStepM) || segment.baseStepM <= 0 ||
			segment.endPathM <= segment.startPathM || segment.startPathM > coveredThrough {
			return 0, fmt.Errorf("refracted-ray acceleration interval is not covered by contiguous dense segments")
		}
		bound, err := segment.positionAccelerationNormUpperBound(overlapStartM, overlapEndM)
		if err != nil {
			return 0, err
		}
		maximum = math.Max(maximum, bound)
		coveredThrough = math.Max(coveredThrough, overlapEndM)
	}
	if coveredThrough < endPathM || !finite(maximum) || maximum < 0 {
		return 0, fmt.Errorf("refracted-ray acceleration interval has no finite dense segment")
	}
	return math.Nextafter(maximum, math.Inf(1)), nil
}

// PositionComponentIdenticallyZero certifies the narrow, exact case in which
// one Cartesian component of the stored dense-output trajectory is the zero
// polynomial on an interval. This is stronger than sampling: every accepted
// segment's initial component and every DOPRI stage component must be the
// floating-point value zero. Provider path planners use the certificate to
// retain deterministic ownership when a ray lies exactly on a coordinate
// plane instead of misclassifying the continuum of zeros as crossings.
func (ray AstrodomeRefractedRay) PositionComponentIdenticallyZero(
	startPathM, endPathM float64,
	component int,
) (bool, error) {
	if err := ray.validate(); err != nil {
		return false, err
	}
	if !finite(startPathM) || !finite(endPathM) || startPathM < 0 ||
		endPathM <= startPathM || endPathM > ray.PathLengthM || component < 0 || component >= 3 {
		return false, fmt.Errorf("refracted-ray zero-component interval is invalid")
	}
	coveredThrough := startPathM
	for _, segment := range ray.segments {
		if segment.endPathM <= startPathM || segment.startPathM >= endPathM {
			continue
		}
		if !finite(segment.startPathM) || !finite(segment.endPathM) ||
			segment.startPathM > coveredThrough || segment.initial[component] != 0 {
			return false, nil
		}
		for stage := range segment.stages {
			if segment.stages[stage][component] != 0 {
				return false, nil
			}
		}
		coveredThrough = math.Max(coveredThrough, math.Min(endPathM, segment.endPathM))
	}
	if coveredThrough < endPathM {
		return false, fmt.Errorf("refracted-ray zero-component interval is not covered by contiguous dense segments")
	}
	return true, nil
}

// HeightDerivativeBounds returns outward lower and upper bounds for the
// radial ICON-sphere height derivative dh/ds on a stored path interval. The
// interval is split at every accepted dense-output segment; a finished
// height, seeing, transmission, or index is never interpolated.
//
// On each segment rho=|r| and dh/ds=r.r' / rho. Bernstein convex-hull bounds
// U>=|r'| and A>=|r”| give the certified Lipschitz bound
//
//	|d2 rho/ds2| <= A + U*U/rho_min.
//
// The returned bounds include outward floating-point allowances for dense
// polynomial formation and evaluation. They are intended for monotonicity
// proofs in provider path planners, not as a sampled estimate.
func (ray AstrodomeRefractedRay) HeightDerivativeBounds(startPathM, endPathM float64) (float64, float64, error) {
	if !finite(startPathM) || !finite(endPathM) || startPathM < 0 ||
		endPathM <= startPathM || endPathM > ray.PathLengthM || len(ray.segments) == 0 {
		return 0, 0, fmt.Errorf("refracted-ray height-derivative interval is outside [0, %.6f] m", ray.PathLengthM)
	}

	lower, upper, coveredThrough := math.Inf(1), math.Inf(-1), startPathM
	for _, segment := range ray.segments {
		if segment.endPathM <= startPathM || segment.startPathM >= endPathM {
			continue
		}
		overlapStartM := math.Max(startPathM, segment.startPathM)
		overlapEndM := math.Min(endPathM, segment.endPathM)
		if !finite(segment.startPathM) || !finite(segment.endPathM) ||
			!finite(segment.baseStepM) || segment.baseStepM <= 0 ||
			segment.endPathM <= segment.startPathM || segment.startPathM > coveredThrough {
			return 0, 0, fmt.Errorf("refracted-ray height-derivative interval is not covered by contiguous dense segments")
		}
		segmentLower, segmentUpper, err := segment.heightDerivativeBounds(overlapStartM, overlapEndM)
		if err != nil {
			return 0, 0, err
		}
		lower = math.Min(lower, segmentLower)
		upper = math.Max(upper, segmentUpper)
		coveredThrough = math.Max(coveredThrough, overlapEndM)
	}
	if coveredThrough < endPathM || !finite(lower) || !finite(upper) || lower > upper {
		return 0, 0, fmt.Errorf("refracted-ray height-derivative interval has no finite dense segment")
	}
	return math.Nextafter(lower, math.Inf(-1)), math.Nextafter(upper, math.Inf(1)), nil
}

func TraceAstrodomeRefractedRay(
	ctx context.Context,
	field AstrodomeRefractionField,
	initial AstrodomeRay,
	calibration AstrodomeRefractionCalibration,
) (AstrodomeRefractedRay, error) {
	if field == nil {
		return AstrodomeRefractedRay{}, fmt.Errorf("astrodome refraction field is required")
	}
	if err := initial.validate(); err != nil {
		return AstrodomeRefractedRay{}, err
	}
	if err := calibration.Validate(); err != nil {
		return AstrodomeRefractedRay{}, err
	}
	previous, err := astrodomeIntegrateRefractionToTop(ctx, field, initial, calibration, 1)
	if err != nil {
		return AstrodomeRefractedRay{}, err
	}

	var (
		fine                                              astrodomeRefractionPass
		positionDelta, directionDelta, opticalDelta       = math.Inf(1), math.Inf(1), math.Inf(1)
		reversePosition, reverseDirection, toleranceScale = 0.0, 0.0, 1.0
		converged, reverseChecked                         bool
		forwardPasses                                     = 1
		reversePasses                                     int
		totalFieldEvaluations                             = previous.diagnostics.FieldEvaluations
	)
	// DOPRI5(4) selects h proportionally to tolerance^(1/5). If the global
	// endpoint repeat rejects the nominal fine pass, halve the local tolerance.
	// Strict calibration additionally repeats the accepted candidate backwards.
	// Seven bounded refinements give a 128x stricter local target without weakening
	// an enabled numerical acceptance threshold or publishing non-convergence.
	for refinement := 1; refinement <= astrodomeRefractionMaximumRefinementLevels; refinement++ {
		toleranceScale = math.Ldexp(1, -refinement)
		candidate, integrateErr := astrodomeIntegrateRefractionToTop(ctx, field, initial, calibration, toleranceScale)
		if integrateErr != nil {
			return AstrodomeRefractedRay{}, integrateErr
		}
		forwardPasses++
		totalFieldEvaluations += candidate.diagnostics.FieldEvaluations
		positionDelta = astrodomeStatePosition(candidate.finalState).subtract(astrodomeStatePosition(previous.finalState)).Norm()
		directionDelta = astrodomeVectorAngle(astrodomeStateTangent(candidate.finalState), astrodomeStateTangent(previous.finalState))
		opticalDelta = math.Abs(candidate.finalState[6] - previous.finalState[6])
		repeatAccepted := positionDelta <= calibration.RepeatPositionToleranceM &&
			directionDelta <= calibration.RepeatDirectionToleranceRad &&
			opticalDelta <= calibration.RepeatOpticalPathToleranceM
		if !repeatAccepted {
			reverseChecked = false
			previous = candidate
			continue
		}
		if calibration.VerificationMode == AstrodomeRefractionVerificationForwardRepeat {
			fine = candidate
			converged = true
			break
		}

		reverseInitial := candidate.finalState
		reverseTangent := astrodomeStateTangent(reverseInitial).scale(-1)
		reverseInitial[3], reverseInitial[4], reverseInitial[5] = reverseTangent.X, reverseTangent.Y, reverseTangent.Z
		reverseInitial[6] = 0
		reversed, reverseErr := astrodomeIntegrateRefractionFixedLength(
			ctx, field, reverseInitial, candidate.pathLengthM, calibration, toleranceScale,
		)
		if reverseErr != nil {
			return AstrodomeRefractedRay{}, fmt.Errorf("refraction reversibility pass: %w", reverseErr)
		}
		reversePasses++
		totalFieldEvaluations += reversed.diagnostics.FieldEvaluations
		reversePosition = astrodomeStatePosition(reversed.finalState).subtract(initial.ObserverECEF).Norm()
		reverseDirection = astrodomeVectorAngle(astrodomeStateTangent(reversed.finalState), initial.DirectionECEF.scale(-1))
		reverseChecked = true

		reverseAccepted := reversePosition <= calibration.ReversibilityPositionM &&
			reverseDirection <= calibration.ReversibilityDirectionRad
		if reverseAccepted {
			fine = candidate
			converged = true
			break
		}
		previous = candidate
	}
	if !converged {
		if calibration.VerificationMode == AstrodomeRefractionVerificationForwardRepeat {
			return AstrodomeRefractedRay{}, fmt.Errorf(
				"%w: forward repeat through tolerance scale %.6g produced endpoint residuals %.6g m, %.6g rad, %.6g m OPL",
				ErrAstrodomeRefractionNonConvergence, toleranceScale,
				positionDelta, directionDelta, opticalDelta,
			)
		}
		if !reverseChecked {
			return AstrodomeRefractedRay{}, fmt.Errorf(
				"%w: refinement through tolerance scale %.6g produced endpoint residuals %.6g m, %.6g rad, %.6g m OPL; reversibility was not evaluated because the neighboring forward passes did not converge",
				ErrAstrodomeRefractionNonConvergence, toleranceScale,
				positionDelta, directionDelta, opticalDelta,
			)
		}
		return AstrodomeRefractedRay{}, fmt.Errorf(
			"%w: refinement through tolerance scale %.6g produced endpoint residuals %.6g m, %.6g rad, %.6g m OPL and reversibility residuals %.6g m, %.6g rad",
			ErrAstrodomeRefractionNonConvergence, toleranceScale,
			positionDelta, directionDelta, opticalDelta, reversePosition, reverseDirection,
		)
	}

	topPoint := astrodomeRefractedPoint(fine.finalState, fine.pathLengthM, initial.Observer)
	topPoint.RefractiveIndex = fine.finalRefractiveIndex
	diagnostics := fine.diagnostics
	diagnostics.ForwardPasses = forwardPasses
	diagnostics.ReversePasses = reversePasses
	diagnostics.TotalFieldEvaluations = totalFieldEvaluations
	return AstrodomeRefractedRay{
		GeometryVersion:     AstrodomeRefractionGeometryVersion,
		IntegratorVersion:   AstrodomeRefractionIntegratorVersion,
		VerificationMode:    calibration.VerificationMode,
		RefractivityVersion: fine.refractivityVersion,
		Observer:            initial.Observer, ObserverHeightM: initial.ObserverHeightM,
		VisibleElevationDegrees: initial.ElevationDegrees,
		VisibleAzimuthDegrees:   cloneAstrodomeFloatPointer(initial.AzimuthDegrees),
		InitialTangentECEF:      initial.DirectionECEF,
		DirectionAtICONTopECEF:  astrodomeStateTangent(fine.finalState),
		TopPoint:                topPoint, PathLengthM: fine.pathLengthM, OpticalPathM: fine.finalState[6],
		NumericalError: AstrodomeRefractionNumericalError{
			EndpointPositionM: positionDelta, DirectionAtICONTopRad: directionDelta,
			OpticalPathM: opticalDelta, ReversibilityPositionM: reversePosition,
			ReversibilityDirectionRad: reverseDirection, ReversibilityChecked: reverseChecked,
			MaximumAcceptedErrorRatio: fine.maximumAcceptedErrorRatio,
			MaximumTangentNormError:   fine.maximumTangentNormError,
			TopRootBracketM:           fine.topRootBracketM,
		},
		Diagnostics: diagnostics,
		segments:    fine.segments,
	}, nil
}

type astrodomeRefractionState [7]float64

type astrodomeRefractionDenseSegment struct {
	startPathM float64
	endPathM   float64
	baseStepM  float64
	initial    astrodomeRefractionState
	stages     [7]astrodomeRefractionState
}

// astrodomeDenseBoundFloatingPointOperationBudget is an audited ceiling for
// the longest straight-line binary64 evaluation covered by
// astrodomeDenseFloatingPointAllowance. It includes dense-coefficient
// formation, affine restriction, compensated accumulation, polynomial
// evaluation and the Cartesian norm staging. The actual per-output paths are
// shorter; keeping a 1024-operation ceiling leaves room for compiler-level
// reassociation while replacing the former dimensionally ambiguous
// "4096 ULP" multiplier with the standard gamma_n forward-error bound.
const astrodomeDenseBoundFloatingPointOperationBudget = 1024.0

// A point evaluation is substantially shorter than formation and affine
// restriction of every Bernstein range certificate above. The audited longest
// componentwise positive-scale path has 102 rounding-capable source operations
// (the derivative scale), so 128 retains a 26-operation margin. Keeping this
// budget separate avoids charging each Cartesian component for two unrelated
// components while the general derivative/acceleration bounds remain at 1024.
const astrodomeDensePointFloatingPointOperationBudget = 128.0

func (segment astrodomeRefractionDenseSegment) positionDerivativeNormUpperBound(startPathM, endPathM float64) (float64, error) {
	thetaStart, thetaEnd, err := segment.denseIntervalThetas(startPathM, endPathM)
	if err != nil {
		return 0, err
	}

	// power[component][degree] represents dr_component/ds in theta powers.
	var power [3][4]float64
	absoluteFormationSum := 0.0
	for component := 0; component < 3; component++ {
		for degree := 0; degree < 4; degree++ {
			sum, correction, absoluteTerms := 0.0, 0.0, 0.0
			for stage := range segment.stages {
				term := segment.stages[stage][component] * float64(degree+1) * astrodomeDOPRIDenseP[stage][degree]
				absoluteTerms += math.Abs(term)
				next := sum + term
				if math.Abs(sum) >= math.Abs(term) {
					correction += (sum - next) + term
				} else {
					correction += (term - next) + sum
				}
				sum = next
			}
			power[component][degree] = sum + correction
			absoluteFormationSum += absoluteTerms
		}
	}

	// Affinely restrict theta to the requested interval, then convert the
	// resulting cubic exactly from power to Bernstein form on t in [0,1].
	var controls [4]AstrodomeECEFVector
	thetaWidth := thetaEnd - thetaStart
	for component := 0; component < 3; component++ {
		c0, c1, c2, c3 := power[component][0], power[component][1], power[component][2], power[component][3]
		a := thetaStart
		d0 := c0 + c1*a + c2*a*a + c3*a*a*a
		d1 := thetaWidth * (c1 + 2*c2*a + 3*c3*a*a)
		d2 := thetaWidth * thetaWidth * (c2 + 3*c3*a)
		d3 := thetaWidth * thetaWidth * thetaWidth * c3
		values := [4]float64{
			d0,
			d0 + d1/3,
			d0 + 2*d1/3 + d2/3,
			d0 + d1 + d2 + d3,
		}
		absoluteFormationSum += math.Abs(c0) + math.Abs(c1*a) + math.Abs(c2*a*a) + math.Abs(c3*a*a*a) +
			math.Abs(d0) + math.Abs(d1) + math.Abs(d2) + math.Abs(d3)
		for control := range controls {
			absoluteFormationSum += math.Abs(values[control])
			switch component {
			case 0:
				controls[control].X = values[control]
			case 1:
				controls[control].Y = values[control]
			case 2:
				controls[control].Z = values[control]
			}
		}
	}
	maximum := 0.0
	for _, control := range controls {
		maximum = math.Max(maximum, control.Norm())
	}
	if !finite(maximum) || maximum <= 0 || !finite(absoluteFormationSum) {
		return 0, fmt.Errorf("refracted-ray dense derivative bound is invalid")
	}
	allowance := astrodomeDenseFloatingPointAllowance(absoluteFormationSum)
	return math.Nextafter(maximum+allowance, math.Inf(1)), nil
}

func (segment astrodomeRefractionDenseSegment) positionAccelerationNormUpperBound(startPathM, endPathM float64) (float64, error) {
	thetaStart, thetaEnd, err := segment.denseIntervalThetas(startPathM, endPathM)
	if err != nil {
		return 0, err
	}

	// power[component][degree] represents d2r_component/ds2 in theta powers.
	var power [3][3]float64
	absoluteFormationSum := 0.0
	for component := 0; component < 3; component++ {
		for degree := 0; degree < 3; degree++ {
			sum, correction, absoluteTerms := 0.0, 0.0, 0.0
			multiplier := float64((degree+1)*(degree+2)) / segment.baseStepM
			for stage := range segment.stages {
				term := segment.stages[stage][component] * multiplier * astrodomeDOPRIDenseP[stage][degree+1]
				absoluteTerms += math.Abs(term)
				next := sum + term
				if math.Abs(sum) >= math.Abs(term) {
					correction += (sum - next) + term
				} else {
					correction += (term - next) + sum
				}
				sum = next
			}
			power[component][degree] = sum + correction
			absoluteFormationSum += absoluteTerms
		}
	}

	// Affinely restrict theta and convert the quadratic to Bernstein form.
	var controls [3]AstrodomeECEFVector
	thetaWidth := thetaEnd - thetaStart
	for component := 0; component < 3; component++ {
		c0, c1, c2 := power[component][0], power[component][1], power[component][2]
		a := thetaStart
		d0 := c0 + c1*a + c2*a*a
		d1 := thetaWidth * (c1 + 2*c2*a)
		d2 := thetaWidth * thetaWidth * c2
		values := [3]float64{d0, d0 + d1/2, d0 + d1 + d2}
		absoluteFormationSum += math.Abs(c0) + math.Abs(c1*a) + math.Abs(c2*a*a) +
			math.Abs(d0) + math.Abs(d1) + math.Abs(d2)
		for control := range controls {
			absoluteFormationSum += math.Abs(values[control])
			switch component {
			case 0:
				controls[control].X = values[control]
			case 1:
				controls[control].Y = values[control]
			case 2:
				controls[control].Z = values[control]
			}
		}
	}
	maximum := 0.0
	for _, control := range controls {
		maximum = math.Max(maximum, control.Norm())
	}
	if !finite(maximum) || !finite(absoluteFormationSum) {
		return 0, fmt.Errorf("refracted-ray dense acceleration bound is invalid")
	}
	allowance := astrodomeDenseFloatingPointAllowance(absoluteFormationSum)
	return math.Nextafter(maximum+allowance, math.Inf(1)), nil
}

func (segment astrodomeRefractionDenseSegment) heightDerivativeBounds(startPathM, endPathM float64) (float64, float64, error) {
	if _, _, err := segment.denseIntervalThetas(startPathM, endPathM); err != nil {
		return 0, 0, err
	}
	middlePathM := startPathM + 0.5*(endPathM-startPathM)
	position := astrodomeStatePosition(segment.evaluate(middlePathM))
	velocity := segment.positionDerivative(middlePathM)
	radiusM := position.Norm()
	if !finite(radiusM) || radiusM <= 0 || !finite(velocity.X) || !finite(velocity.Y) || !finite(velocity.Z) {
		return 0, 0, fmt.Errorf("refracted-ray dense radial derivative is invalid")
	}

	speedBound, err := segment.positionDerivativeNormUpperBound(startPathM, endPathM)
	if err != nil {
		return 0, 0, err
	}
	accelerationBound, err := segment.positionAccelerationNormUpperBound(startPathM, endPathM)
	if err != nil {
		return 0, 0, err
	}
	positionErrorM, velocityError, err := segment.positionAndDerivativeEvaluationErrors(middlePathM)
	if err != nil {
		return 0, 0, err
	}

	halfWidthM := math.Max(middlePathM-startPathM, endPathM-middlePathM)
	radiusRoundoffM := astrodomeDenseFloatingPointAllowance(radiusM)
	pathReachM := math.Nextafter(speedBound*halfWidthM, math.Inf(1))
	radiusMinimumM := math.Nextafter(radiusM-positionErrorM, math.Inf(-1))
	radiusMinimumM = math.Nextafter(radiusMinimumM-radiusRoundoffM, math.Inf(-1))
	radiusMinimumM = math.Nextafter(radiusMinimumM-pathReachM, math.Inf(-1))
	if !finite(radiusMinimumM) || radiusMinimumM <= 0 {
		return 0, 0, fmt.Errorf("refracted-ray dense interval has no positive certified radius")
	}

	speedSquaredBound := math.Nextafter(speedBound*speedBound, math.Inf(1))
	curvatureBound := math.Nextafter(speedSquaredBound/radiusMinimumM, math.Inf(1))
	radialSecondDerivativeBound := math.Nextafter(accelerationBound+curvatureBound, math.Inf(1))
	drift := math.Nextafter(radialSecondDerivativeBound*halfWidthM, math.Inf(1))
	centerDerivative := position.dot(velocity) / radiusM
	centerArithmeticError := astrodomeDenseFloatingPointAllowance(
		math.Abs(centerDerivative) + speedBound + radiusM,
	)
	speedForEvaluationBound := math.Nextafter(speedBound+velocityError, math.Inf(1))
	centerEvaluationError := math.Nextafter(
		velocityError+2*speedForEvaluationBound*(positionErrorM+radiusRoundoffM)/radiusMinimumM+centerArithmeticError,
		math.Inf(1),
	)
	if !finite(centerDerivative) || !finite(drift) || !finite(centerEvaluationError) {
		return 0, 0, fmt.Errorf("refracted-ray dense height-derivative bound is invalid")
	}

	lower := math.Nextafter(centerDerivative-centerEvaluationError-drift, math.Inf(-1))
	upper := math.Nextafter(centerDerivative+centerEvaluationError+drift, math.Inf(1))
	if !finite(lower) || !finite(upper) || lower > upper {
		return 0, 0, fmt.Errorf("refracted-ray dense height-derivative bound is not finite")
	}
	return lower, upper, nil
}

func (segment astrodomeRefractionDenseSegment) denseIntervalThetas(startPathM, endPathM float64) (float64, float64, error) {
	if !finite(segment.startPathM) || !finite(segment.endPathM) || !finite(segment.baseStepM) ||
		segment.baseStepM <= 0 || segment.endPathM <= segment.startPathM ||
		!finite(startPathM) || !finite(endPathM) || startPathM < segment.startPathM ||
		endPathM > segment.endPathM || endPathM <= startPathM {
		return 0, 0, fmt.Errorf("refracted-ray dense derivative interval is invalid")
	}
	thetaStart := (startPathM - segment.startPathM) / segment.baseStepM
	thetaEnd := (endPathM - segment.startPathM) / segment.baseStepM
	maximumTheta := (segment.endPathM - segment.startPathM) / segment.baseStepM
	if !finite(thetaStart) || !finite(thetaEnd) || thetaStart < 0 || thetaEnd <= thetaStart || thetaEnd > maximumTheta {
		return 0, 0, fmt.Errorf("refracted-ray dense derivative interval has invalid normalized bounds")
	}
	return thetaStart, thetaEnd, nil
}

func (segment astrodomeRefractionDenseSegment) positionAndDerivativeEvaluationErrors(pathLengthM float64) (float64, float64, error) {
	if !finite(pathLengthM) || pathLengthM < segment.startPathM || pathLengthM > segment.endPathM ||
		!finite(segment.baseStepM) || segment.baseStepM <= 0 {
		return 0, 0, fmt.Errorf("refracted-ray dense evaluation point is invalid")
	}
	rawTheta := (pathLengthM - segment.startPathM) / segment.baseStepM
	maximumTheta := (segment.endPathM - segment.startPathM) / segment.baseStepM
	positionTheta := clampSurfaceValue(rawTheta, 0, maximumTheta)
	positionExact := pathLengthM <= segment.startPathM
	positionScales := [3]float64{}
	if !positionExact {
		positionScales = [3]float64{
			math.Abs(segment.initial[0]),
			math.Abs(segment.initial[1]),
			math.Abs(segment.initial[2]),
		}
	}
	derivativeScales := [3]float64{}
	positionPowers := [4]float64{
		positionTheta,
		positionTheta * positionTheta,
		positionTheta * positionTheta * positionTheta,
		positionTheta * positionTheta * positionTheta * positionTheta,
	}
	for stage := range segment.stages {
		// The scale must sum absolute monomials, not the absolute value of the
		// already summed coefficient: cancellation in the Shampine polynomial is
		// part of the rounding problem and cannot make its error envelope smaller.
		positionCoefficientAbsoluteSum := 0.0
		if !positionExact {
			for power := range positionPowers {
				positionCoefficientAbsoluteSum += math.Abs(astrodomeDOPRIDenseP[stage][power] * positionPowers[power])
			}
		}
		derivativeCoefficientAbsoluteSum := math.Abs(astrodomeDOPRIDenseP[stage][0]) +
			math.Abs(2*astrodomeDOPRIDenseP[stage][1]*rawTheta) +
			math.Abs(3*astrodomeDOPRIDenseP[stage][2]*rawTheta*rawTheta) +
			math.Abs(4*astrodomeDOPRIDenseP[stage][3]*rawTheta*rawTheta*rawTheta)
		for component := 0; component < 3; component++ {
			positionScales[component] += math.Abs(segment.baseStepM*segment.stages[stage][component]) * positionCoefficientAbsoluteSum
			derivativeScales[component] += math.Abs(segment.stages[stage][component]) * derivativeCoefficientAbsoluteSum
		}
	}
	for component := 0; component < 3; component++ {
		if !finite(positionScales[component]) || !finite(derivativeScales[component]) {
			return 0, 0, fmt.Errorf("refracted-ray dense evaluation error scale is invalid")
		}
	}
	positionError := 0.0
	if !positionExact {
		positionError = astrodomeDenseVectorFloatingPointAllowance(positionScales)
	}
	return positionError, astrodomeDenseVectorFloatingPointAllowance(derivativeScales), nil
}

// astrodomeDenseVectorFloatingPointAllowance converts independently proved
// Cartesian component bounds into one outward Euclidean envelope. Summing all
// component formation scales before applying gamma_n is valid but needlessly
// replaces ||e||_2 by ||e||_1. Componentwise gamma_n bounds preserve the
// actual dense-evaluation dependency graph; outward squares, additions and the
// final square root then prove ||delta r||_2 <= sqrt(e_x^2+e_y^2+e_z^2).
func astrodomeDenseVectorFloatingPointAllowance(componentScales [3]float64) float64 {
	sumSquares := 0.0
	for _, scale := range componentScales {
		componentError := astrodomeDensePointFloatingPointAllowance(scale)
		square := math.Nextafter(componentError*componentError, math.Inf(1))
		sumSquares = math.Nextafter(sumSquares+square, math.Inf(1))
	}
	return math.Nextafter(math.Sqrt(sumSquares), math.Inf(1))
}

func astrodomeDensePointFloatingPointAllowance(absoluteFormationSum float64) float64 {
	return astrodomeFloatingPointAllowance(
		absoluteFormationSum,
		astrodomeDensePointFloatingPointOperationBudget,
	)
}

func astrodomeDenseFloatingPointAllowance(absoluteFormationSum float64) float64 {
	return astrodomeFloatingPointAllowance(
		absoluteFormationSum,
		astrodomeDenseBoundFloatingPointOperationBudget,
	)
}

func astrodomeFloatingPointAllowance(absoluteFormationSum, operationBudget float64) float64 {
	// Higham's gamma_n = n*u/(1-n*u), with binary64 unit roundoff
	// u=2^-53. absoluteFormationSum is itself accumulated in binary64, so
	// divide its represented value by (1-gamma_n) before applying the bound;
	// this also encloses downward rounding while forming the positive sum.
	u := math.Ldexp(1, -53)
	nu := math.Nextafter(operationBudget*u, math.Inf(1))
	denominator := math.Nextafter(1-nu, 0)
	gamma := math.Nextafter(nu/denominator, math.Inf(1))
	formationDenominator := math.Nextafter(1-gamma, 0)
	scale := math.Nextafter(math.Max(1, absoluteFormationSum)/formationDenominator, math.Inf(1))
	return math.Nextafter(gamma*scale, math.Inf(1))
}

func (segment astrodomeRefractionDenseSegment) positionDerivative(pathLengthM float64) AstrodomeECEFVector {
	theta := (pathLengthM - segment.startPathM) / segment.baseStepM
	derivative := AstrodomeECEFVector{}
	for stage := range segment.stages {
		coefficient := astrodomeDOPRIDenseP[stage][0] +
			2*astrodomeDOPRIDenseP[stage][1]*theta +
			3*astrodomeDOPRIDenseP[stage][2]*theta*theta +
			4*astrodomeDOPRIDenseP[stage][3]*theta*theta*theta
		derivative.X += segment.stages[stage][0] * coefficient
		derivative.Y += segment.stages[stage][1] * coefficient
		derivative.Z += segment.stages[stage][2] * coefficient
	}
	return derivative
}

func (segment astrodomeRefractionDenseSegment) positionAcceleration(pathLengthM float64) AstrodomeECEFVector {
	theta := (pathLengthM - segment.startPathM) / segment.baseStepM
	acceleration := AstrodomeECEFVector{}
	for stage := range segment.stages {
		coefficient := (2*astrodomeDOPRIDenseP[stage][1] +
			6*astrodomeDOPRIDenseP[stage][2]*theta +
			12*astrodomeDOPRIDenseP[stage][3]*theta*theta) / segment.baseStepM
		acceleration.X += segment.stages[stage][0] * coefficient
		acceleration.Y += segment.stages[stage][1] * coefficient
		acceleration.Z += segment.stages[stage][2] * coefficient
	}
	return acceleration
}

func (segment astrodomeRefractionDenseSegment) evaluate(pathLengthM float64) astrodomeRefractionState {
	if pathLengthM <= segment.startPathM {
		return segment.initial
	}
	theta := (pathLengthM - segment.startPathM) / segment.baseStepM
	theta = clampSurfaceValue(theta, 0, (segment.endPathM-segment.startPathM)/segment.baseStepM)
	powers := [4]float64{theta, theta * theta, theta * theta * theta, theta * theta * theta * theta}
	result := segment.initial
	for component := range result {
		increment := 0.0
		for stage := range segment.stages {
			coefficient := 0.0
			for power := range powers {
				coefficient += astrodomeDOPRIDenseP[stage][power] * powers[power]
			}
			increment += coefficient * segment.stages[stage][component]
		}
		result[component] += segment.baseStepM * increment
	}
	return astrodomeNormaliseRefractionState(result)
}

func (segment astrodomeRefractionDenseSegment) refractiveIndex(pathLengthM float64) float64 {
	theta := clampSurfaceValue((pathLengthM-segment.startPathM)/segment.baseStepM, 0,
		(segment.endPathM-segment.startPathM)/segment.baseStepM)
	result := 0.0
	for stage := range segment.stages {
		coefficient := astrodomeDOPRIDenseP[stage][0] +
			2*astrodomeDOPRIDenseP[stage][1]*theta +
			3*astrodomeDOPRIDenseP[stage][2]*theta*theta +
			4*astrodomeDOPRIDenseP[stage][3]*theta*theta*theta
		result += coefficient * segment.stages[stage][6]
	}
	return result
}

type astrodomeRefractionPass struct {
	finalState                astrodomeRefractionState
	pathLengthM               float64
	segments                  []astrodomeRefractionDenseSegment
	diagnostics               AstrodomeRefractionDiagnostics
	refractivityVersion       string
	finalRefractiveIndex      float64
	maximumAcceptedErrorRatio float64
	maximumTangentNormError   float64
	topRootBracketM           float64
}

func astrodomeInitialRefractionState(initial AstrodomeRay) astrodomeRefractionState {
	return astrodomeRefractionState{
		initial.ObserverECEF.X, initial.ObserverECEF.Y, initial.ObserverECEF.Z,
		initial.DirectionECEF.X, initial.DirectionECEF.Y, initial.DirectionECEF.Z,
		0,
	}
}

func astrodomeStatePosition(state astrodomeRefractionState) AstrodomeECEFVector {
	return AstrodomeECEFVector{X: state[0], Y: state[1], Z: state[2]}
}

func astrodomeStateTangent(state astrodomeRefractionState) AstrodomeECEFVector {
	return AstrodomeECEFVector{X: state[3], Y: state[4], Z: state[5]}
}

func astrodomeNormaliseRefractionState(state astrodomeRefractionState) astrodomeRefractionState {
	tangent := astrodomeStateTangent(state)
	norm := tangent.Norm()
	if norm > 0 && finite(norm) {
		state[3], state[4], state[5] = tangent.X/norm, tangent.Y/norm, tangent.Z/norm
	}
	return state
}

func astrodomeRefractedPoint(state astrodomeRefractionState, pathLengthM float64, observer Location) AstrodomeRefractedRayPoint {
	position := astrodomeStatePosition(state)
	point, _ := astrodomeRayPointFromECEF(position, pathLengthM, mustAstrodomeTimeZone(observer.TimeZone))
	observerECEF, _, _, _ := astrodomeObserverBasis(observer, 0)
	observerUnit := observerECEF.scale(1 / observerECEF.Norm())
	pointUnit := position.scale(1 / position.Norm())
	point.CentralAngleRadians = math.Atan2(observerUnit.cross(pointUnit).Norm(), observerUnit.dot(pointUnit))
	return AstrodomeRefractedRayPoint{
		AstrodomeRayPoint: point, TangentECEF: astrodomeStateTangent(state), OpticalPathM: state[6],
	}
}

func mustAstrodomeTimeZone(name string) *time.Location {
	location, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return location
}

func (vector AstrodomeECEFVector) subtract(other AstrodomeECEFVector) AstrodomeECEFVector {
	return AstrodomeECEFVector{X: vector.X - other.X, Y: vector.Y - other.Y, Z: vector.Z - other.Z}
}

func astrodomeVectorAngle(left, right AstrodomeECEFVector) float64 {
	leftNorm, rightNorm := left.Norm(), right.Norm()
	if leftNorm == 0 || rightNorm == 0 {
		return math.Inf(1)
	}
	left = left.scale(1 / leftNorm)
	right = right.scale(1 / rightNorm)
	return math.Atan2(left.cross(right).Norm(), clampSurfaceValue(left.dot(right), -1, 1))
}
