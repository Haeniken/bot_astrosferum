package forecast

import (
	"math"
	"testing"
)

func TestAstrodomeENUUnitDirectionUsesNorthClockwiseAzimuth(t *testing.T) {
	t.Parallel()

	elevation := 30.0
	cosElevation := math.Cos(elevation * math.Pi / 180)
	sinElevation := math.Sin(elevation * math.Pi / 180)
	tests := []struct {
		name      string
		azimuth   float64
		wantEast  float64
		wantNorth float64
	}{
		{name: "north", azimuth: 0, wantNorth: cosElevation},
		{name: "east", azimuth: 90, wantEast: cosElevation},
		{name: "south", azimuth: 180, wantNorth: -cosElevation},
		{name: "west", azimuth: 270, wantEast: -cosElevation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			direction, err := AstrodomeENUUnitDirection(elevation, &test.azimuth)
			if err != nil {
				t.Fatalf("AstrodomeENUUnitDirection: %v", err)
			}
			assertAstrodomeClose(t, "east", direction.East, test.wantEast, 1e-15)
			assertAstrodomeClose(t, "north", direction.North, test.wantNorth, 1e-15)
			assertAstrodomeClose(t, "up", direction.Up, sinElevation, 1e-15)
			assertAstrodomeClose(t, "norm", math.Sqrt(direction.East*direction.East+direction.North*direction.North+direction.Up*direction.Up), 1, 1e-15)
			if (test.azimuth == 0 || test.azimuth == 180) && direction.East != 0 {
				t.Fatalf("cardinal east component = %.18g, want exact zero", direction.East)
			}
			if (test.azimuth == 90 || test.azimuth == 270) && direction.North != 0 {
				t.Fatalf("cardinal north component = %.18g, want exact zero", direction.North)
			}
		})
	}
}

func TestAstrodomeRayRejectsUnsupportedAngularGeometry(t *testing.T) {
	t.Parallel()

	observer := Location{Latitude: 55.75, Longitude: 37.62, TimeZone: "Europe/Moscow"}
	azimuth := 0.0
	if _, err := NewAstrodomeRay(observer, 150, 9.999, &azimuth); err == nil {
		t.Fatal("elevation below the 10-degree product boundary was accepted")
	}
	if _, err := NewAstrodomeRay(observer, 150, 20, nil); err == nil {
		t.Fatal("tilted ray without azimuth was accepted")
	}
	if _, err := NewAstrodomeRay(observer, 150, 90, &azimuth); err == nil {
		t.Fatal("zenith with arbitrary azimuth was accepted")
	}
	if _, err := NewAstrodomeRay(Location{Latitude: 90, Longitude: 0}, 0, 20, &azimuth); err == nil {
		t.Fatal("tilted ray at exact geographic pole was accepted")
	}
	if _, err := NewAstrodomeRay(Location{Latitude: 90, Longitude: 0}, 0, 90, nil); err != nil {
		t.Fatalf("azimuth-independent zenith at pole was rejected: %v", err)
	}
}

func TestAstrodomeRayECEFAndSphereIntersectionInvariants(t *testing.T) {
	t.Parallel()

	observer := Location{Latitude: 59.9386, Longitude: 30.3141, TimeZone: "Europe/Moscow"}
	observerHeightM := 18.25
	elevationDegrees := 10.0
	azimuthDegrees := 67.5
	ray, err := NewAstrodomeRay(observer, observerHeightM, elevationDegrees, &azimuthDegrees)
	if err != nil {
		t.Fatalf("NewAstrodomeRay: %v", err)
	}
	assertAstrodomeClose(t, "observer radius", ray.ObserverECEF.Norm(), AstrodomeICONSphereRadiusM+observerHeightM, 1e-8)
	assertAstrodomeClose(t, "ECEF direction norm", ray.DirectionECEF.Norm(), 1, 1e-15)
	assertAstrodomeClose(t, "ENU direction norm", math.Sqrt(
		ray.DirectionENU.East*ray.DirectionENU.East+
			ray.DirectionENU.North*ray.DirectionENU.North+
			ray.DirectionENU.Up*ray.DirectionENU.Up,
	), 1, 1e-15)

	targetHeightM := 22500.0
	point, err := ray.IntersectAltitude(targetHeightM)
	if err != nil {
		t.Fatalf("IntersectAltitude: %v", err)
	}
	assertAstrodomeClose(t, "target shell radius", point.ECEF.Norm(), AstrodomeICONSphereRadiusM+targetHeightM, 1e-7)
	assertAstrodomeClose(t, "reported target height", point.HeightM, targetHeightM, 1e-7)

	elevation := elevationDegrees * math.Pi / 180
	observerRadius := AstrodomeICONSphereRadiusM + observerHeightM
	targetRadius := AstrodomeICONSphereRadiusM + targetHeightM
	wantPath := -observerRadius*math.Sin(elevation) + math.Sqrt(
		targetRadius*targetRadius-observerRadius*observerRadius*math.Cos(elevation)*math.Cos(elevation),
	)
	wantAngle := math.Atan2(wantPath*math.Cos(elevation), observerRadius+wantPath*math.Sin(elevation))
	assertAstrodomeClose(t, "intersection path", point.PathLengthM, wantPath, 1e-7)
	assertAstrodomeClose(t, "central angle", point.CentralAngleRadians, wantAngle, 1e-14)

	destination, err := AstrodomeSphereDestination(observer, azimuthDegrees, point.CentralAngleRadians)
	if err != nil {
		t.Fatalf("AstrodomeSphereDestination: %v", err)
	}
	assertAstrodomeClose(t, "destination latitude", point.Location.Latitude, destination.Latitude, 1e-12)
	assertLongitudeClose(t, point.Location.Longitude, destination.Longitude, 1e-12)
	if point.Location.TimeZone != observer.TimeZone {
		t.Fatalf("destination timezone = %q, want %q", point.Location.TimeZone, observer.TimeZone)
	}
}

func TestAstrodomeZenithIsOneAzimuthIndependentRadialRay(t *testing.T) {
	t.Parallel()

	observer := Location{Latitude: 55.7522, Longitude: 37.6156, TimeZone: "Europe/Moscow"}
	observerHeightM := 155.0
	ray, err := NewAstrodomeRay(observer, observerHeightM, 90, nil)
	if err != nil {
		t.Fatalf("NewAstrodomeRay: %v", err)
	}
	if ray.AzimuthDegrees != nil {
		t.Fatalf("zenith azimuth = %v, want nil", *ray.AzimuthDegrees)
	}
	if ray.DirectionENU != (AstrodomeENUVector{Up: 1}) {
		t.Fatalf("zenith ENU direction = %+v", ray.DirectionENU)
	}
	targetHeightM := 22500.0
	point, err := ray.IntersectAltitude(targetHeightM)
	if err != nil {
		t.Fatalf("IntersectAltitude: %v", err)
	}
	assertAstrodomeClose(t, "zenith path", point.PathLengthM, targetHeightM-observerHeightM, 1e-8)
	assertAstrodomeClose(t, "zenith central angle", point.CentralAngleRadians, 0, 1e-16)
	assertAstrodomeClose(t, "zenith latitude", point.Location.Latitude, observer.Latitude, 1e-12)
	assertLongitudeClose(t, point.Location.Longitude, observer.Longitude, 1e-12)
}

func TestAstrodomeCardinalDestinations(t *testing.T) {
	t.Parallel()

	origin := Location{Latitude: 0, Longitude: 0, TimeZone: "UTC"}
	north, err := AstrodomeSphereDestination(origin, 0, 0.1)
	if err != nil {
		t.Fatal(err)
	}
	east, err := AstrodomeSphereDestination(origin, 90, 0.1)
	if err != nil {
		t.Fatal(err)
	}
	assertAstrodomeClose(t, "north latitude", north.Latitude, 0.1*180/math.Pi, 1e-13)
	assertLongitudeClose(t, north.Longitude, 0, 1e-13)
	assertAstrodomeClose(t, "east latitude", east.Latitude, 0, 1e-13)
	assertLongitudeClose(t, east.Longitude, 0.1*180/math.Pi, 1e-13)
}

func assertAstrodomeClose(t *testing.T, name string, got, want, tolerance float64) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Fatalf("%s = %.17g, want %.17g (tolerance %.3g)", name, got, want, tolerance)
	}
}

func assertLongitudeClose(t *testing.T, got, want, tolerance float64) {
	t.Helper()
	delta := math.Abs(normalizeAstrodomeLongitude(got - want))
	if delta > tolerance {
		t.Fatalf("longitude = %.17g, want %.17g (wrapped delta %.3g)", got, want, delta)
	}
}
