package forecast

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

const AstrodomeRefractionGeometryVersion = "astrodome-icon-sphere-refraction-full-ciddor-dopri54-v3"

const (
	AstrodomeRefractionLowerBoundaryVersion = "icon-ps-t2m-qv2m-hydrostatic-2m-v1"
	AstrodomeRefractionApertureHeightAGLM   = 2.0
	AstrodomeDirectionCoordinate            = "apparent_at_aperture"
	AstrodomeDirectionReferenceSurface      = "icon_sphere_hsurf_plus_2m"
	AstrodomeDirectionReferenceWavelengthM  = 500e-9
	AstrodomeICONReferenceGravityMS2        = 9.80665
	// A Dormand--Prince stage may lie beyond HHL1 or below bilinear HSURF while
	// bracketing a top/terrain event. Only P/T/QV refractivity is continued into
	// this numerical guard; no science integral samples below terrain or above
	// the model top.
	AstrodomeRefractionPrimitiveEventGuardM = 2000.0
)

// AstrodomeHydrostaticPressureAtAperture maps native PS at HHL_surface to the
// T2M/QV_2M anchor at HHL_surface+2 m. It is the exact solution of the
// declared constant-(T,q,g) two-metre hydrostatic closure,
// P2=PS exp[-g dz/(Rmix T)], Rmix=(1-q)Rd+qRv. The closure is versioned
// because T2M is not silently relabelled as a surface temperature.
func AstrodomeHydrostaticPressureAtAperture(
	surfacePressurePa,
	temperature2MK,
	specificHumidity2MKgKg,
	dryAirGasConstantJKgK,
	waterVapourGasConstantJKgK float64,
) (float64, error) {
	values := []float64{surfacePressurePa, temperature2MK, specificHumidity2MKgKg,
		dryAirGasConstantJKgK, waterVapourGasConstantJKgK}
	for _, value := range values {
		if !finite(value) {
			return 0, fmt.Errorf("ICON two-metre hydrostatic boundary inputs must be finite")
		}
	}
	if surfacePressurePa <= 0 || temperature2MK <= 0 || specificHumidity2MKgKg < 0 ||
		specificHumidity2MKgKg >= 1 || dryAirGasConstantJKgK <= 0 ||
		waterVapourGasConstantJKgK <= dryAirGasConstantJKgK {
		return 0, fmt.Errorf("ICON two-metre hydrostatic boundary inputs are outside their physical domain")
	}
	mixGasConstant := (1-specificHumidity2MKgKg)*dryAirGasConstantJKgK +
		specificHumidity2MKgKg*waterVapourGasConstantJKgK
	pressure := surfacePressurePa * math.Exp(-AstrodomeICONReferenceGravityMS2*
		AstrodomeRefractionApertureHeightAGLM/(mixGasConstant*temperature2MK))
	if !finite(pressure) || pressure <= 0 || pressure >= surfacePressurePa {
		return 0, fmt.Errorf("ICON two-metre hydrostatic pressure is invalid")
	}
	return pressure, nil
}

type AstrodomeRefractionLowerBoundary struct {
	Version                  string  `json:"version"`
	SurfaceHeightM           float64 `json:"surface_height_m"`
	ApertureHeightM          float64 `json:"aperture_height_m"`
	AperturePressurePa       float64 `json:"aperture_pressure_pa"`
	ApertureTemperatureK     float64 `json:"aperture_temperature_k"`
	ApertureSpecificHumidity float64 `json:"aperture_specific_humidity_kg_kg"`
}

func NewAstrodomeRefractionLowerBoundary(
	surfaceHeightM float64,
	surface AstrodomeSurfacePrimitives,
	dryAirGasConstantJKgK,
	waterVapourGasConstantJKgK float64,
) (AstrodomeRefractionLowerBoundary, error) {
	required := AstrodomePrimitiveFieldSet(AstrodomePrimitiveSurfacePressure) |
		AstrodomePrimitiveFieldSet(AstrodomePrimitiveTemperature2M) |
		AstrodomePrimitiveFieldSet(AstrodomePrimitiveSpecificHumidity2M)
	if surface.Available&required != required {
		return AstrodomeRefractionLowerBoundary{}, fmt.Errorf("PS, T2M, and QV_2M are mandatory for full refraction")
	}
	if !finite(surfaceHeightM) {
		return AstrodomeRefractionLowerBoundary{}, fmt.Errorf("ICON surface height is invalid")
	}
	pressure2M, err := AstrodomeHydrostaticPressureAtAperture(
		surface.SurfacePressurePa, surface.Temperature2MK, surface.SpecificHumidity2MKgKg,
		dryAirGasConstantJKgK, waterVapourGasConstantJKgK,
	)
	if err != nil {
		return AstrodomeRefractionLowerBoundary{}, err
	}
	return AstrodomeRefractionLowerBoundary{
		Version:            AstrodomeRefractionLowerBoundaryVersion,
		SurfaceHeightM:     surfaceHeightM,
		ApertureHeightM:    surfaceHeightM + AstrodomeRefractionApertureHeightAGLM,
		AperturePressurePa: pressure2M, ApertureTemperatureK: surface.Temperature2MK,
		ApertureSpecificHumidity: surface.SpecificHumidity2MKgKg,
	}, nil
}

// AstrodomeRefractionDomainPoint supplies only interpolation-domain geometry.
// PartitionID must change at every horizontal-cell or native-HHL branch where
// a primitive derivative is discontinuous. Refractive index itself must remain
// C0 across an internal partition; a physical discontinuity requires a separate
// signed event and must not be hidden behind PartitionID. Surface and model-top
// heights are reconstructed native HHL surfaces in the same ICON-sphere datum
// as ECEF.
type AstrodomeRefractionDomainPoint struct {
	PartitionID     string  `json:"partition_id"`
	SurfaceHeightM  float64 `json:"surface_height_m"`
	ModelTopHeightM float64 `json:"model_top_height_m"`
}

type AstrodomeRefractionDomainResolver interface {
	ResolveAstrodomeRefractionDomain(
		ctx context.Context,
		validAt time.Time,
		point AstrodomeRayPoint,
		stencil AstrodomeHorizontalStencil,
	) (AstrodomeRefractionDomainPoint, error)
}

// AstrodomeRefractionFieldSample is one continuous branch of n(r). Signed
// distances are positive above their respective native HHL surfaces.
type AstrodomeRefractionFieldSample struct {
	RefractivityVersion     string              `json:"refractivity_version"`
	RefractiveIndex         float64             `json:"refractive_index"`
	GradientECEF            AstrodomeECEFVector `json:"gradient_ecef"`
	PartitionID             string              `json:"partition_id"`
	SignedSurfaceDistanceM  float64             `json:"signed_surface_distance_m"`
	SignedModelTopDistanceM float64             `json:"signed_model_top_distance_m"`
}

// AstrodomeRefractionField is deliberately small and provider-neutral. A
// production implementation is immutable for one run/valid time and must be
// evaluable in the narrow event guard used to locate terrain/top roots.
type AstrodomeRefractionField interface {
	EvaluateAstrodomeRefraction(
		ctx context.Context,
		positionECEF AstrodomeECEFVector,
	) (AstrodomeRefractionFieldSample, error)
}

// AstrodomeRefractivityCalibration declares the optical wavelength and fixed
// CO2 assumption used by Ciddor. The value and provenance are part of the
// science identity; neither is silently inferred from wall-clock time.
type AstrodomeRefractivityCalibration struct {
	Version             string  `json:"version"`
	WavelengthM         float64 `json:"wavelength_m"`
	CarbonDioxidePPM    float64 `json:"carbon_dioxide_ppm"`
	CarbonDioxideSource string  `json:"carbon_dioxide_source"`
}

func DefaultAstrodomeRefractivityCalibration() AstrodomeRefractivityCalibration {
	return AstrodomeRefractivityCalibration{
		Version: AstrodomeCiddorVersion, WavelengthM: AstrodomeDirectionReferenceWavelengthM,
		CarbonDioxidePPM:    425,
		CarbonDioxideSource: "fixed project assumption; replace only through a versioned calibration",
	}
}

func (calibration AstrodomeRefractivityCalibration) Validate() error {
	if calibration.Version != AstrodomeCiddorVersion {
		return fmt.Errorf("unsupported astrodome refractivity calibration %q", calibration.Version)
	}
	if strings.TrimSpace(calibration.CarbonDioxideSource) == "" {
		return fmt.Errorf("astrodome refractivity CO2 source is required")
	}
	_, err := AstrodomeCiddorPhaseRefractivity(
		101325, 288.15, 0, calibration.WavelengthM, calibration.CarbonDioxidePPM,
	)
	return err
}

type AstrodomeReconstructedRefractivityField struct {
	prepared    *astrodomePreparedRefractiveFrame
	validAt     time.Time
	calibration AstrodomeRefractivityCalibration
}

func NewAstrodomeReconstructedRefractivityField(
	reconstructor *AstrodomePrimitiveReconstructor,
	validAt time.Time,
	calibration AstrodomeRefractivityCalibration,
) (*AstrodomeReconstructedRefractivityField, error) {
	if reconstructor == nil {
		return nil, fmt.Errorf("astrodome primitive reconstructor is required")
	}
	if err := validateAstrodomeUTCWholeHour(validAt); err != nil {
		return nil, fmt.Errorf("astrodome refraction valid time: %w", err)
	}
	if err := calibration.Validate(); err != nil {
		return nil, err
	}
	if err := reconstructor.ensureIdentity(); err != nil {
		return nil, err
	}
	prepared, err := newAstrodomePreparedRefractiveFrame(reconstructor, validAt)
	if err != nil {
		return nil, err
	}
	return &AstrodomeReconstructedRefractivityField{
		prepared: prepared, validAt: validAt.UTC(), calibration: calibration,
	}, nil
}

func (field *AstrodomeReconstructedRefractivityField) EvaluateAstrodomeRefraction(
	ctx context.Context,
	positionECEF AstrodomeECEFVector,
) (AstrodomeRefractionFieldSample, error) {
	if field == nil || field.prepared == nil {
		return AstrodomeRefractionFieldSample{}, fmt.Errorf("astrodome reconstructed refractivity field is incomplete")
	}
	point, err := astrodomeRayPointFromECEF(positionECEF, 0, field.validAt.Location())
	if err != nil {
		return AstrodomeRefractionFieldSample{}, err
	}
	prepared, err := field.prepared.reconstruct(ctx, AstrodomeReconstructionQuery{
		ValidAt: field.validAt, Location: point.Location, HeightM: point.HeightM,
	})
	if err != nil {
		return AstrodomeRefractionFieldSample{}, err
	}
	state, gradients, domain := prepared.state, prepared.gradients, prepared.domain
	if strings.TrimSpace(domain.PartitionID) == "" || !finite(domain.SurfaceHeightM) ||
		!finite(domain.ModelTopHeightM) || domain.ModelTopHeightM <= domain.SurfaceHeightM {
		return AstrodomeRefractionFieldSample{}, fmt.Errorf("astrodome refraction domain point is invalid")
	}
	refractivity, err := AstrodomeCiddorPhaseRefractivity(
		state.PressurePa, state.TemperatureK, state.SpecificHumidityKgKg,
		field.calibration.WavelengthM, field.calibration.CarbonDioxidePPM,
	)
	if err != nil {
		return AstrodomeRefractionFieldSample{}, err
	}
	gradient := gradients.PressurePaPerM.scale(refractivity.DerivativePressurePa).
		add(gradients.TemperatureKPerM.scale(refractivity.DerivativeTemperatureK)).
		add(gradients.SpecificHumidityPerM.scale(refractivity.DerivativeSpecificHumidityKgKg))
	return AstrodomeRefractionFieldSample{
		RefractivityVersion: refractivity.Version,
		RefractiveIndex:     refractivity.RefractiveIndex, GradientECEF: gradient,
		PartitionID:             domain.PartitionID,
		SignedSurfaceDistanceM:  point.HeightM - domain.SurfaceHeightM,
		SignedModelTopDistanceM: point.HeightM - domain.ModelTopHeightM,
	}, nil
}

func astrodomeRayPointFromECEF(
	position AstrodomeECEFVector,
	pathLengthM float64,
	timeZone *time.Location,
) (AstrodomeRayPoint, error) {
	radius := position.Norm()
	if !finite(radius) || radius <= 0 || !finite(pathLengthM) || pathLengthM < 0 {
		return AstrodomeRayPoint{}, fmt.Errorf("astrodome refracted ray point is invalid")
	}
	zoneName := "UTC"
	if timeZone != nil {
		zoneName = timeZone.String()
	}
	return AstrodomeRayPoint{
		PathLengthM: pathLengthM,
		ECEF:        position,
		Location: Location{
			Latitude:  math.Atan2(position.Z, math.Hypot(position.X, position.Y)) * 180 / math.Pi,
			Longitude: normalizeAstrodomeLongitude(math.Atan2(position.Y, position.X) * 180 / math.Pi),
			TimeZone:  zoneName,
		},
		HeightM: radius - AstrodomeICONSphereRadiusM,
	}, nil
}
