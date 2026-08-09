package forecast

import (
	"fmt"
	"math"
)

const (
	// AstrodomeICONSphereRadiusM is DWD ICON's native reference-sphere
	// radius. Astrodome geometry must not silently reuse the legacy Horizon
	// mean-Earth radius.
	AstrodomeICONSphereRadiusM = 6371229.0
	AstrodomeGeometryVersion   = "astrodome-icon-sphere-straight-ray-v2"
)

// AstrodomeENUVector uses the meteorological local tangent basis: East,
// North, Up. Geographic azimuth is measured clockwise from North.
type AstrodomeENUVector struct {
	East  float64 `json:"east"`
	North float64 `json:"north"`
	Up    float64 `json:"up"`
}

// AstrodomeECEFVector is a Cartesian vector in the ICON reference-sphere
// Earth-centred, Earth-fixed basis.
type AstrodomeECEFVector struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// Norm returns the Euclidean vector magnitude.
func (vector AstrodomeECEFVector) Norm() float64 {
	return math.Sqrt(vector.dot(vector))
}

func (vector AstrodomeECEFVector) dot(other AstrodomeECEFVector) float64 {
	return vector.X*other.X + vector.Y*other.Y + vector.Z*other.Z
}

func (vector AstrodomeECEFVector) cross(other AstrodomeECEFVector) AstrodomeECEFVector {
	return AstrodomeECEFVector{
		X: vector.Y*other.Z - vector.Z*other.Y,
		Y: vector.Z*other.X - vector.X*other.Z,
		Z: vector.X*other.Y - vector.Y*other.X,
	}
}

func (vector AstrodomeECEFVector) scale(factor float64) AstrodomeECEFVector {
	return AstrodomeECEFVector{X: vector.X * factor, Y: vector.Y * factor, Z: vector.Z * factor}
}

func (vector AstrodomeECEFVector) add(other AstrodomeECEFVector) AstrodomeECEFVector {
	return AstrodomeECEFVector{X: vector.X + other.X, Y: vector.Y + other.Y, Z: vector.Z + other.Z}
}

// AstrodomeRay is a straight geometric line of sight in the native ICON
// sphere. Refraction may later provide another versioned geometry mode; it
// must not change the meaning of this compatibility object.
type AstrodomeRay struct {
	GeometryVersion  string              `json:"geometry_version"`
	Observer         Location            `json:"observer"`
	ObserverHeightM  float64             `json:"observer_height_m"`
	ElevationDegrees float64             `json:"elevation_deg"`
	AzimuthDegrees   *float64            `json:"azimuth_deg"`
	DirectionENU     AstrodomeENUVector  `json:"direction_enu"`
	ObserverECEF     AstrodomeECEFVector `json:"observer_ecef"`
	DirectionECEF    AstrodomeECEFVector `json:"direction_ecef"`
}

// AstrodomeRayPoint is the exact Cartesian point reached after moving along a
// ray. Location contains the spherical latitude/longitude of the radial
// projection; HeightM is relative to the ICON reference sphere.
type AstrodomeRayPoint struct {
	PathLengthM         float64             `json:"path_length_m"`
	CentralAngleRadians float64             `json:"central_angle_rad"`
	ECEF                AstrodomeECEFVector `json:"ecef"`
	Location            Location            `json:"location"`
	HeightM             float64             `json:"height_m"`
}

// NewAstrodomeRay constructs the ENU and ECEF form of a geometric line of
// sight. Tilted rays require an azimuth. A zenith ray requires nil azimuth,
// preventing 16 duplicate centre nodes from entering the science contract.
func NewAstrodomeRay(
	observer Location,
	observerHeightM float64,
	elevationDegrees float64,
	azimuthDegrees *float64,
) (AstrodomeRay, error) {
	if err := ValidateCoordinates(observer.Latitude, observer.Longitude); err != nil {
		return AstrodomeRay{}, fmt.Errorf("astrodome observer: %w", err)
	}
	if !finite(observerHeightM) || observerHeightM <= -AstrodomeICONSphereRadiusM {
		return AstrodomeRay{}, fmt.Errorf("astrodome observer height must be finite and above the ICON sphere centre")
	}
	if math.Abs(observer.Latitude) == 90 && elevationDegrees != AstrodomeZenithElevationDegrees {
		return AstrodomeRay{}, fmt.Errorf("astrodome azimuth is degenerate at an exact geographic pole")
	}

	directionENU, normalizedAzimuth, err := astrodomeENUUnitDirection(elevationDegrees, azimuthDegrees)
	if err != nil {
		return AstrodomeRay{}, err
	}
	observer.Longitude = normalizeAstrodomeLongitude(observer.Longitude)
	observerECEF, east, north, up := astrodomeObserverBasis(observer, observerHeightM)
	directionECEF := east.scale(directionENU.East).
		add(north.scale(directionENU.North)).
		add(up.scale(directionENU.Up))

	return AstrodomeRay{
		GeometryVersion:  AstrodomeGeometryVersion,
		Observer:         observer,
		ObserverHeightM:  observerHeightM,
		ElevationDegrees: elevationDegrees,
		AzimuthDegrees:   normalizedAzimuth,
		DirectionENU:     directionENU,
		ObserverECEF:     observerECEF,
		DirectionECEF:    directionECEF,
	}, nil
}

// AstrodomeENUUnitDirection returns D4 from the astrodome science contract:
// (E,N,U)=(cos(e)sin(A), cos(e)cos(A), sin(e)). At the zenith azimuth is
// undefined and must be nil.
func AstrodomeENUUnitDirection(elevationDegrees float64, azimuthDegrees *float64) (AstrodomeENUVector, error) {
	direction, _, err := astrodomeENUUnitDirection(elevationDegrees, azimuthDegrees)
	return direction, err
}

func astrodomeENUUnitDirection(
	elevationDegrees float64,
	azimuthDegrees *float64,
) (AstrodomeENUVector, *float64, error) {
	if !finite(elevationDegrees) || elevationDegrees < AstrodomeMinimumElevationDegrees ||
		elevationDegrees > AstrodomeZenithElevationDegrees {
		return AstrodomeENUVector{}, nil, fmt.Errorf("astrodome elevation must be finite and between %.0f and %.0f degrees",
			AstrodomeMinimumElevationDegrees, AstrodomeZenithElevationDegrees)
	}
	if elevationDegrees == AstrodomeZenithElevationDegrees {
		if azimuthDegrees != nil {
			return AstrodomeENUVector{}, nil, fmt.Errorf("astrodome zenith azimuth must be undefined")
		}
		return AstrodomeENUVector{Up: 1}, nil, nil
	}
	if azimuthDegrees == nil || !finite(*azimuthDegrees) {
		return AstrodomeENUVector{}, nil, fmt.Errorf("astrodome tilted ray needs a finite azimuth")
	}
	normalizedAzimuth := normalizeAstrodomeAzimuth(*azimuthDegrees)
	elevation := elevationDegrees * math.Pi / 180
	azimuth := normalizedAzimuth * math.Pi / 180
	cosElevation := math.Cos(elevation)
	sinAzimuth, cosAzimuth := math.Sincos(azimuth)
	// Cardinal bearings are exact input identities, while evaluating cos(pi/2)
	// leaves an artificial ~6e-17 cross-track component. Preserve those four
	// identities exactly so a cardinal ray can be certified as lying on an ECEF
	// coordinate plane rather than manufacturing numerical grid crossings.
	switch normalizedAzimuth {
	case 0:
		sinAzimuth, cosAzimuth = 0, 1
	case 90:
		sinAzimuth, cosAzimuth = 1, 0
	case 180:
		sinAzimuth, cosAzimuth = 0, -1
	case 270:
		sinAzimuth, cosAzimuth = -1, 0
	}
	return AstrodomeENUVector{
		East:  cosElevation * sinAzimuth,
		North: cosElevation * cosAzimuth,
		Up:    math.Sin(elevation),
	}, &normalizedAzimuth, nil
}

// PointAtPathLength returns an exact ECEF point on the straight ray and its
// spherical geographic projection. No flat-Earth destination approximation
// is used.
func (ray AstrodomeRay) PointAtPathLength(pathLengthM float64) (AstrodomeRayPoint, error) {
	if err := ray.validate(); err != nil {
		return AstrodomeRayPoint{}, err
	}
	if !finite(pathLengthM) || pathLengthM < 0 {
		return AstrodomeRayPoint{}, fmt.Errorf("astrodome ray path length must be finite and non-negative")
	}
	return ray.pointAtPathLength(pathLengthM)
}

// pointAtPathLength is the validated-ray fast path for the later adaptive
// quadrature loop. It intentionally remains package-private: public callers
// cannot bypass the immutable geometry checks.
func (ray AstrodomeRay) pointAtPathLength(pathLengthM float64) (AstrodomeRayPoint, error) {
	pointECEF := ray.ObserverECEF.add(ray.DirectionECEF.scale(pathLengthM))
	pointRadius := pointECEF.Norm()
	if !finite(pointRadius) || pointRadius <= 0 {
		return AstrodomeRayPoint{}, fmt.Errorf("astrodome ray point has invalid radius")
	}
	observerUnit := ray.ObserverECEF.scale(1 / ray.ObserverECEF.Norm())
	pointUnit := pointECEF.scale(1 / pointRadius)
	centralAngle := math.Atan2(observerUnit.cross(pointUnit).Norm(), observerUnit.dot(pointUnit))
	location := Location{
		Latitude:  math.Atan2(pointECEF.Z, math.Hypot(pointECEF.X, pointECEF.Y)) * 180 / math.Pi,
		Longitude: normalizeAstrodomeLongitude(math.Atan2(pointECEF.Y, pointECEF.X) * 180 / math.Pi),
		TimeZone:  ray.Observer.TimeZone,
	}
	return AstrodomeRayPoint{
		PathLengthM:         pathLengthM,
		CentralAngleRadians: centralAngle,
		ECEF:                pointECEF,
		Location:            location,
		HeightM:             pointRadius - AstrodomeICONSphereRadiusM,
	}, nil
}

// IntersectAltitude finds the unique forward intersection with the concentric
// ICON sphere R_ICON+targetHeightM. The algebraically equivalent c/root form
// avoids loss of precision from subtracting two approximately Earth-radius
// terms in -b+sqrt(b^2-c).
func (ray AstrodomeRay) IntersectAltitude(targetHeightM float64) (AstrodomeRayPoint, error) {
	if err := ray.validate(); err != nil {
		return AstrodomeRayPoint{}, err
	}
	if !finite(targetHeightM) || targetHeightM <= ray.ObserverHeightM {
		return AstrodomeRayPoint{}, fmt.Errorf("astrodome target altitude must be finite and above the observer")
	}
	targetRadius := AstrodomeICONSphereRadiusM + targetHeightM
	if targetRadius <= 0 {
		return AstrodomeRayPoint{}, fmt.Errorf("astrodome target altitude is below the ICON sphere centre")
	}
	observerRadius := ray.ObserverECEF.Norm()
	b := ray.ObserverECEF.dot(ray.DirectionECEF)
	c := (observerRadius - targetRadius) * (observerRadius + targetRadius)
	discriminant := b*b - c
	if !finite(discriminant) || discriminant <= 0 {
		return AstrodomeRayPoint{}, fmt.Errorf("astrodome ray does not intersect the requested altitude")
	}
	squareRoot := math.Sqrt(discriminant)
	denominator := b + squareRoot
	if !finite(denominator) || denominator <= 0 {
		return AstrodomeRayPoint{}, fmt.Errorf("astrodome ray intersection is not forward of the observer")
	}
	pathLengthM := -c / denominator
	if !finite(pathLengthM) || pathLengthM <= 0 {
		return AstrodomeRayPoint{}, fmt.Errorf("astrodome ray intersection is not forward of the observer")
	}
	return ray.pointAtPathLength(pathLengthM)
}

// AstrodomeSphereDestination returns the exact great-circle destination on
// the ICON sphere for a central angle. It is useful for addressing horizontal
// support columns independently of a ray height.
func AstrodomeSphereDestination(origin Location, azimuthDegrees, centralAngleRadians float64) (Location, error) {
	if err := ValidateCoordinates(origin.Latitude, origin.Longitude); err != nil {
		return Location{}, fmt.Errorf("astrodome origin: %w", err)
	}
	if math.Abs(origin.Latitude) == 90 {
		return Location{}, fmt.Errorf("astrodome azimuth is degenerate at an exact geographic pole")
	}
	if !finite(azimuthDegrees) || !finite(centralAngleRadians) || centralAngleRadians < 0 || centralAngleRadians > math.Pi {
		return Location{}, fmt.Errorf("astrodome destination needs a finite azimuth and a central angle between 0 and pi")
	}
	unitOrigin, east, north, _ := astrodomeObserverBasis(origin, 0)
	unitOrigin = unitOrigin.scale(1 / AstrodomeICONSphereRadiusM)
	azimuth := normalizeAstrodomeAzimuth(azimuthDegrees) * math.Pi / 180
	tangent := east.scale(math.Sin(azimuth)).add(north.scale(math.Cos(azimuth)))
	destinationECEF := unitOrigin.scale(math.Cos(centralAngleRadians)).
		add(tangent.scale(math.Sin(centralAngleRadians)))
	return Location{
		Latitude:  math.Atan2(destinationECEF.Z, math.Hypot(destinationECEF.X, destinationECEF.Y)) * 180 / math.Pi,
		Longitude: normalizeAstrodomeLongitude(math.Atan2(destinationECEF.Y, destinationECEF.X) * 180 / math.Pi),
		TimeZone:  origin.TimeZone,
	}, nil
}

func (ray AstrodomeRay) validate() error {
	if ray.GeometryVersion != AstrodomeGeometryVersion {
		return fmt.Errorf("unsupported astrodome geometry version %q", ray.GeometryVersion)
	}
	if err := ValidateCoordinates(ray.Observer.Latitude, ray.Observer.Longitude); err != nil {
		return fmt.Errorf("astrodome observer: %w", err)
	}
	if !finite(ray.ObserverHeightM) || ray.ObserverHeightM <= -AstrodomeICONSphereRadiusM {
		return fmt.Errorf("astrodome observer height is invalid")
	}
	if math.Abs(ray.Observer.Latitude) == 90 && ray.ElevationDegrees != AstrodomeZenithElevationDegrees {
		return fmt.Errorf("astrodome azimuth is degenerate at an exact geographic pole")
	}
	wantENU, _, err := astrodomeENUUnitDirection(ray.ElevationDegrees, ray.AzimuthDegrees)
	if err != nil {
		return err
	}
	if math.Abs(wantENU.East-ray.DirectionENU.East) > 1e-12 ||
		math.Abs(wantENU.North-ray.DirectionENU.North) > 1e-12 ||
		math.Abs(wantENU.Up-ray.DirectionENU.Up) > 1e-12 {
		return fmt.Errorf("astrodome ray ENU direction is inconsistent with its angles")
	}
	wantObserverECEF, east, north, up := astrodomeObserverBasis(ray.Observer, ray.ObserverHeightM)
	wantDirectionECEF := east.scale(wantENU.East).add(north.scale(wantENU.North)).add(up.scale(wantENU.Up))
	if astrodomeVectorDistance(ray.ObserverECEF, wantObserverECEF) > 1e-6 ||
		astrodomeVectorDistance(ray.DirectionECEF, wantDirectionECEF) > 1e-12 ||
		math.Abs(ray.DirectionECEF.Norm()-1) > 1e-12 {
		return fmt.Errorf("astrodome ray ECEF geometry is inconsistent")
	}
	return nil
}

func astrodomeObserverBasis(
	observer Location,
	observerHeightM float64,
) (position, east, north, up AstrodomeECEFVector) {
	latitude := observer.Latitude * math.Pi / 180
	longitude := observer.Longitude * math.Pi / 180
	sinLatitude, cosLatitude := math.Sincos(latitude)
	sinLongitude, cosLongitude := math.Sincos(longitude)
	up = AstrodomeECEFVector{X: cosLatitude * cosLongitude, Y: cosLatitude * sinLongitude, Z: sinLatitude}
	east = AstrodomeECEFVector{X: -sinLongitude, Y: cosLongitude}
	north = AstrodomeECEFVector{X: -sinLatitude * cosLongitude, Y: -sinLatitude * sinLongitude, Z: cosLatitude}
	position = up.scale(AstrodomeICONSphereRadiusM + observerHeightM)
	return position, east, north, up
}

func astrodomeVectorDistance(a, b AstrodomeECEFVector) float64 {
	return AstrodomeECEFVector{X: a.X - b.X, Y: a.Y - b.Y, Z: a.Z - b.Z}.Norm()
}

func normalizeAstrodomeAzimuth(value float64) float64 {
	normalized := math.Mod(value, 360)
	if normalized < 0 {
		normalized += 360
	}
	if normalized == 0 {
		return 0
	}
	return normalized
}

func normalizeAstrodomeLongitude(value float64) float64 {
	normalized := math.Mod(value+180, 360)
	if normalized < 0 {
		normalized += 360
	}
	return normalized - 180
}
