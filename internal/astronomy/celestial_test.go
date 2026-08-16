package astronomy

import (
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
)

func TestGenerateHourlyDistanceScaleTable(t *testing.T) {
	if os.Getenv("ASTRO_GENERATE_CELESTIAL_DISTANCE_SCALES") != "1" {
		t.Skip("manual table generator")
	}
	for _, body := range celestialBodyOrder {
		if selected := os.Getenv("ASTRO_GENERATE_CELESTIAL_DISTANCE_BODY"); selected != "" && string(body) != selected {
			continue
		}
		minimum, maximum := math.Inf(1), math.Inf(-1)
		var minimumAt, maximumAt time.Time
		for at := celestialEphemerisFirstInstant; at.Before(celestialEphemerisLastInstant); at = at.Add(time.Hour) {
			distance, err := celestialReferenceDistanceKM(at, body)
			if err != nil {
				t.Fatal(err)
			}
			if distance < minimum {
				minimum, minimumAt = distance, at
			}
			if distance > maximum {
				maximum, maximumAt = distance, at
			}
		}
		fmt.Printf("%s: {DomainStart: celestialEphemerisFirstInstant, DomainEndExclusive: celestialEphemerisLastInstant, MinimumKM: %.15g, MinimumAt: time.Date(%d, time.%s, %d, %d, 0, 0, 0, time.UTC), MaximumKM: %.15g, MaximumAt: time.Date(%d, time.%s, %d, %d, 0, 0, 0, time.UTC)},\n", body, minimum, minimumAt.Year(), minimumAt.Month(), minimumAt.Day(), minimumAt.Hour(), maximum, maximumAt.Year(), maximumAt.Month(), maximumAt.Day(), maximumAt.Hour())
	}
}

func TestCelestialPositionsAgainstJPLHorizonsObserverTable(t *testing.T) {
	location := forecast.Location{Latitude: 53.65, Longitude: 37.3462, TimeZone: "Europe/Moscow"}
	at := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	// Independent airless topocentric AZ/EL reference from JPL Horizons,
	// observer table quantity 4, DE441, generated for the exact site and
	// instant above. The project implementation is deliberately a lower-
	// accuracy planning ephemeris, so the tolerance reflects the published
	// approximation rather than Horizons' precision.
	reference := map[CelestialBody][2]float64{
		CelestialSun:     {36.000555, -14.715664},
		CelestialMoon:    {42.543319, -7.885703},
		CelestialMercury: {47.658684, -4.287878},
		CelestialVenus:   {351.598585, -39.097970},
		CelestialMars:    {73.425756, 17.355765},
		CelestialJupiter: {43.426197, -7.925954},
		CelestialSaturn:  {158.860528, 37.896240},
		CelestialUranus:  {96.544329, 31.161772},
		CelestialNeptune: {171.910337, 36.418578},
		CelestialPluto:   {225.061324, 1.477104},
	}
	for _, body := range celestialBodyOrder {
		position, err := CelestialPositionAt(location, at, body)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		want := reference[body]
		azimuthError := math.Abs(position.AzimuthDegrees - want[0])
		azimuthError = math.Min(azimuthError, 360-azimuthError)
		altitudeError := math.Abs(position.GeometricAltitudeDegrees - want[1])
		t.Logf("%s az=%.6f (err %.4f), alt=%.6f (err %.4f)", body, position.AzimuthDegrees, azimuthError, position.GeometricAltitudeDegrees, altitudeError)
		if azimuthError > 0.15 || altitudeError > 0.15 {
			t.Errorf("%s differs from JPL Horizons by az %.3f deg, alt %.3f deg", body, azimuthError, altitudeError)
		}
	}
}

func TestObservingDiagnosticsAgainstJPLHorizonsObserverTable(t *testing.T) {
	location := forecast.Location{Latitude: 53.65, Longitude: 37.3462, TimeZone: "Europe/Moscow"}
	at := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	// Independent airless observer quantities 9, 10, 13, 23 and 24 from
	// NASA/JPL Horizons, DE441, for this exact WGS84 site and UTC instant.
	// The local planetary ephemerides and photometric laws are planning
	// approximations, so tolerances represent model agreement, not DE441
	// precision.  Sun and Moon do not publish a project V-magnitude field.
	type reference struct {
		magnitude, illumination, diameter, elongation, phase float64
	}
	references := map[CelestialBody]reference{
		CelestialSun:     {math.NaN(), 100.00000, 1893.044, 0.0000, 0.0000},
		CelestialMoon:    {math.NaN(), 0.66927, 1959.409, 9.3678, 170.6094},
		CelestialMercury: {-0.954, 74.10008, 5.978644, 15.5093, 61.1784},
		CelestialVenus:   {-4.397, 50.44175, 23.43261, 45.8553, 89.4978},
		CelestialMars:    {1.260, 92.96942, 4.802730, 48.9018, 30.7484},
		CelestialJupiter: {-1.784, 99.97252, 31.36412, 9.9520, 1.8941},
		CelestialSaturn:  {0.576, 99.80547, 18.81039, 124.7609, 5.0591},
		CelestialUranus:  {5.745, 99.93719, 3.576803, 74.0050, 2.8708},
		CelestialNeptune: {7.700, 99.98571, 2.342573, 135.2205, 1.3739},
		CelestialPluto:   {14.470, 99.99847, 0.094706, 164.0205, 0.4436},
	}
	for _, body := range celestialBodyOrder {
		position, err := CelestialPositionAt(location, at, body)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		want := references[body]
		t.Logf("%s V=%v illum=%v diameter=%.6f elongation=%v phase=%v", body,
			position.ApparentVMagnitude, position.IlluminatedPercent, position.AngularDiameterArcsec,
			position.SolarElongationDegrees, position.PhaseAngleDegrees)
		if math.Abs(position.AngularDiameterArcsec-want.diameter) > math.Max(0.05, want.diameter*0.005) {
			t.Errorf("%s angular diameter %.6f, Horizons %.6f", body, position.AngularDiameterArcsec, want.diameter)
		}
		if body != CelestialSun {
			t.Logf("%s illumination=%.6f elongation=%.6f phase=%.6f", body,
				*position.IlluminatedPercent, *position.SolarElongationDegrees, *position.PhaseAngleDegrees)
			if position.IlluminatedPercent == nil || math.Abs(*position.IlluminatedPercent-want.illumination) > 0.4 ||
				position.SolarElongationDegrees == nil || math.Abs(*position.SolarElongationDegrees-want.elongation) > 0.2 ||
				position.PhaseAngleDegrees == nil || math.Abs(*position.PhaseAngleDegrees-want.phase) > 0.4 {
				t.Errorf("%s illumination/elongation/phase disagrees with Horizons", body)
			}
		}
		wantMagnitudeStatus := ApparentMagnitudeAvailable
		if body == CelestialMars || body == CelestialPluto {
			wantMagnitudeStatus = ApparentMagnitudePlanningApproximation
		}
		if body.isPlanet() && (position.ApparentVMagnitudeStatus != wantMagnitudeStatus || position.ApparentVMagnitude == nil || math.Abs(*position.ApparentVMagnitude-want.magnitude) > 0.25) {
			t.Errorf("%s V magnitude = %v, Horizons %.3f", body, position.ApparentVMagnitude, want.magnitude)
		}
	}
}

func TestAngularDiameterUsesExactTangentSphereGeometry(t *testing.T) {
	distance := 10.0 * moonEquatorialRadiusKM
	position := CelestialHorizontalPosition{Body: CelestialMoon, ObserverDistanceKM: distance}
	if err := populateCommonObservingDiagnostics(&position, time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC), forecast.Location{}); err != nil {
		t.Fatal(err)
	}
	want := 2 * math.Asin(moonEquatorialRadiusKM/distance) / degree * 3600
	if math.Abs(position.AngularDiameterArcsec-want) > 1e-12 {
		t.Fatalf("angular diameter = %.15g, want %.15g", position.AngularDiameterArcsec, want)
	}
}

func TestApparentMagnitudePublishedDomainIsExplicit(t *testing.T) {
	location := forecast.Location{Latitude: 0, Longitude: 0, TimeZone: "UTC"}
	for _, test := range []struct {
		body CelestialBody
		at   time.Time
	}{
		{CelestialVenus, time.Date(2020, time.June, 4, 0, 0, 0, 0, time.UTC)},
	} {
		position, err := CelestialPositionAt(location, test.at, test.body)
		if err != nil {
			t.Fatalf("%s: %v", test.body, err)
		}
		if position.ApparentVMagnitudeStatus != ApparentMagnitudeOutsidePublishedModelDomain || position.ApparentVMagnitude != nil {
			t.Fatalf("%s magnitude applicability = %q, value %v", test.body, position.ApparentVMagnitudeStatus, position.ApparentVMagnitude)
		}
	}
}

func TestApparentMagnitudePublishedBoundariesAreStrict(t *testing.T) {
	geometry := planetGeometry{
		targetHeliocentric: vector3{x: 1},
		earthToTarget:      vector3{x: -1},
		distanceAU:         1,
	}
	for _, test := range []struct {
		body  CelestialBody
		phase float64
	}{
		{CelestialVenus, 0},
		{CelestialJupiter, 130},
		{CelestialSaturn, 6.5},
	} {
		_, status, err := planetApparentVMagnitude(test.body, time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC), geometry, test.phase)
		if err != nil {
			t.Fatalf("%s boundary: %v", test.body, err)
		}
		if status != ApparentMagnitudeOutsidePublishedModelDomain {
			t.Fatalf("%s boundary status = %q", test.body, status)
		}
	}
}

func TestApparentMagnitudeStatusIsBodySpecific(t *testing.T) {
	location := forecast.Location{Latitude: 53.65, Longitude: 37.3462, TimeZone: "Europe/Moscow"}
	at := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		body   CelestialBody
		status ApparentMagnitudeStatus
		value  *float64
	}{
		{CelestialMars, ApparentMagnitudeAvailable, pointer(1.0)},
		{CelestialMercury, ApparentMagnitudePlanningApproximation, pointer(1.0)},
		{CelestialUranus, ApparentMagnitudeOutsidePublishedModelDomain, nil},
	} {
		position, err := CelestialPositionAt(location, at, test.body)
		if err != nil {
			t.Fatal(err)
		}
		position.ApparentVMagnitudeStatus = test.status
		position.ApparentVMagnitude = test.value
		if err := validateCelestialPosition(position); err == nil {
			t.Fatalf("%s accepted incompatible magnitude status %q", test.body, test.status)
		}
	}
}

func pointer(value float64) *float64 {
	return &value
}

func TestPolarisCatalogSynScanAndHourlyTrack(t *testing.T) {
	location := forecast.Location{Latitude: 53.65, Longitude: 37.3462, TimeZone: "Europe/Moscow"}
	times := []time.Time{
		time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.August, 12, 1, 0, 0, 0, time.UTC),
	}
	track, err := ComputePolarisTrack(location, times)
	if err != nil {
		t.Fatal(err)
	}
	if track.SynScan.RightAscension != "02h 31m 49.095s" || track.SynScan.Declination != "+89° 15′ 50.79″" ||
		track.SynScan.ReferenceFrame != "ICRS" || track.SynScan.Epoch != "J2000.0" {
		t.Fatalf("SynScan contract = %+v", track.SynScan)
	}
	if len(track.Samples) != len(times) || math.Abs(track.Samples[0].GeometricAltitudeDegrees-location.Latitude) > 1.5 {
		t.Fatalf("Polaris track = %+v", track.Samples)
	}
	if err := ValidatePolarisTrack(track, times); err != nil {
		t.Fatal(err)
	}
	track.Samples[0].AzimuthDegrees = math.NaN()
	if err := ValidatePolarisTrack(track, times); err == nil {
		t.Fatal("non-finite Polaris sample was accepted")
	}
}

func TestCelestialDistanceDiagnostics(t *testing.T) {
	location := forecast.Location{Latitude: 53.65, Longitude: 37.3462, TimeZone: "Europe/Moscow"}
	at := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	for _, body := range celestialBodyOrder {
		position, err := CelestialPositionAt(location, at, body)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		scale, err := celestialDistanceScale(body)
		if err != nil {
			t.Fatalf("%s scale: %v", body, err)
		}
		ringOpening := math.NaN()
		if position.SaturnRingOpeningDegrees != nil {
			ringOpening = *position.SaturnRingOpeningDegrees
		}
		t.Logf("%s d=%.4f km rate=%.8f km/s close=%.6f scale=[%.4f @ %s, %.4f @ %s] B=%.6f",
			body, position.ReferenceDistanceKM, position.RadialVelocityKMS, position.DistanceClosenessPercent,
			scale.MinimumKM, scale.MinimumAt.Format(time.RFC3339), scale.MaximumKM, scale.MaximumAt.Format(time.RFC3339), ringOpening)
	}
}

func TestPlanetRangesAndRangeRatesAgainstJPLHorizonsQuantity20(t *testing.T) {
	location := forecast.Location{Latitude: 0, Longitude: 0, TimeZone: "UTC"}
	at := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	// Independent geocentric JPL Horizons observer quantity 20, DE441,
	// generated 2026-08-12 with RANGE_UNITS=KM. The implementation uses the
	// approximate JPL Earth-Moon-barycentre elements, so these tolerances are
	// planning residual limits rather than DE441 precision claims.
	references := map[CelestialBody][2]float64{
		CelestialMars:    {2.9171700573e8, -7.8926089},
		CelestialJupiter: {9.4032741807e8, -4.3546376},
		CelestialSaturn:  {1.3217383736e9, -23.6226736},
		CelestialUranus:  {2.9478436075e9, -28.3174188},
		CelestialNeptune: {4.3609701393e9, -20.3997154},
		CelestialPluto:   {5.1761036676e9, 9.2254154},
	}
	for body, reference := range references {
		position, err := CelestialPositionAt(location, at, body)
		if err != nil {
			t.Fatal(err)
		}
		distanceResidual := math.Abs(position.ReferenceDistanceKM - reference[0])
		rateResidual := math.Abs(position.RadialVelocityKMS - reference[1])
		if distanceResidual > 2_000_000 || rateResidual > 0.08 {
			t.Errorf("%s Horizons residual: distance %.0f km, range rate %.6f km/s", body, distanceResidual, rateResidual)
		}
	}
}

func TestSaturnRingOpeningAgainstIAUPoleAndHorizonsGeometry(t *testing.T) {
	at := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	opening, err := saturnRingOpeningDegrees(at)
	if err != nil {
		t.Fatal(err)
	}
	// Independent construction from Horizons astrometric RA/Dec quantity 1
	// and pole RA/Dec quantity 32 at the same instant gives -8.98046°.
	if math.Abs(opening-(-8.9804625)) > 0.08 {
		t.Fatalf("Saturn ring opening = %.6f°, want -8.98046° ±0.08°", opening)
	}
	if opening >= 0 {
		t.Fatalf("Saturn ring opening sign = %.6f°, want the IAU-south side in 2026", opening)
	}
}

func TestDistanceClosenessScaleEndpointsAndOutOfRange(t *testing.T) {
	for _, body := range celestialBodyOrder {
		scale, err := celestialDistanceScale(body)
		if err != nil {
			t.Fatal(err)
		}
		near, err := distanceClosenessPercent(scale.MinimumKM, scale)
		if err != nil || math.Abs(near-100) > 1e-12 {
			t.Fatalf("%s minimum closeness = %v, %v", body, near, err)
		}
		far, err := distanceClosenessPercent(scale.MaximumKM, scale)
		if err != nil || math.Abs(far) > 1e-12 {
			t.Fatalf("%s maximum closeness = %v, %v", body, far, err)
		}
		mid, err := distanceClosenessPercent((scale.MinimumKM+scale.MaximumKM)/2, scale)
		if err != nil || math.Abs(mid-50) > 1e-12 {
			t.Fatalf("%s midpoint closeness = %v, %v", body, mid, err)
		}
		if _, err := distanceClosenessPercent(math.Nextafter(scale.MinimumKM, math.Inf(-1)), scale); err == nil {
			t.Fatalf("%s accepted a distance below the exhaustive scale", body)
		}
		if _, err := distanceClosenessPercent(math.Nextafter(scale.MaximumKM, math.Inf(1)), scale); err == nil {
			t.Fatalf("%s accepted a distance above the exhaustive scale", body)
		}
	}
}

func TestPlanningPlanetPositionsAcrossSeasonsAgainstJPLHorizons(t *testing.T) {
	location := forecast.Location{Latitude: 53.65, Longitude: 37.3462, TimeZone: "Europe/Moscow"}
	// Independent JPL Horizons observer tables, API v1.2, generated
	// 2026-08-12. Parameters: CENTER=coord@399,
	// SITE_COORD=37.3462,53.65,0 km, TIME_TYPE=UT, QUANTITIES=4,
	// REF_SYSTEM=ICRF, ANG_FORMAT=DEG, APPARENT=AIRLESS,
	// EXTRA_PREC=YES, ELEV_CUT=-90. Earth orientation source was
	// eop.260811.p261107 and the centre ephemeris was DE441. The raw table
	// covered 2026-01-15 through 2027-01-15 in three-calendar-month steps.
	references := map[CelestialBody][][2]float64{
		CelestialMars:    {{55.591513148, -48.429978551}, {61.487074787, -18.537620375}, {69.168062978, 12.099392703}, {93.031038982, 26.122794966}, {163.204607239, 46.519633964}},
		CelestialJupiter: {{240.564603660, 46.171611829}, {316.715108308, -3.152004395}, {23.734499973, -13.468311914}, {86.127015321, 15.613624632}, {185.536774628, 50.253416254}},
		CelestialSaturn:  {{327.102770301, -35.066358369}, {58.245467357, -20.110801161}, {127.807665023, 28.253529878}, {237.017060836, 23.741206680}, {316.844497148, -26.729515718}},
		CelestialUranus:  {{286.743470463, 11.877276133}, {3.198697732, -16.434063400}, {75.582858351, 15.395714095}, {175.799228010, 57.316518720}, {283.740898116, 15.337094284}},
		CelestialNeptune: {{325.524146803, -32.645702197}, {62.906445498, -18.674468740}, {139.440886536, 29.734412829}, {243.150529167, 18.015433696}, {323.355760207, -31.084084389}},
		CelestialPluto:   {{42.134323299, -53.970809350}, {120.477344859, -6.307604243}, {200.689982357, 10.741652063}, {274.645915024, -33.208189307}, {39.211798946, -54.844061940}},
	}
	times := []time.Time{
		time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.April, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.July, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.October, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2027, time.January, 15, 0, 0, 0, 0, time.UTC),
	}
	maximumAzimuthError := 0.0
	maximumAltitudeError := 0.0
	for body, bodyReferences := range references {
		for index, at := range times {
			position, err := CelestialPositionAt(location, at, body)
			if err != nil {
				t.Fatalf("%s at %s: %v", body, at.Format(time.DateOnly), err)
			}
			want := bodyReferences[index]
			azimuthError := math.Abs(position.AzimuthDegrees - want[0])
			azimuthError = math.Min(azimuthError, 360-azimuthError)
			altitudeError := math.Abs(position.GeometricAltitudeDegrees - want[1])
			maximumAzimuthError = math.Max(maximumAzimuthError, azimuthError)
			maximumAltitudeError = math.Max(maximumAltitudeError, altitudeError)
			if azimuthError > 0.15 || altitudeError > 0.15 {
				t.Errorf("%s at %s differs from JPL Horizons by az %.3f deg, alt %.3f deg", body, at.Format(time.DateOnly), azimuthError, altitudeError)
			}
		}
	}
	t.Logf("maximum seasonal JPL Horizons residual: az %.4f deg, alt %.4f deg", maximumAzimuthError, maximumAltitudeError)
}

func TestComputeCelestialTracksPreservesCanonicalOrderAndTimes(t *testing.T) {
	location := forecast.Location{Latitude: 59.9386, Longitude: 30.3141, TimeZone: "Europe/Moscow"}
	start := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	times := make([]time.Time, 72)
	for index := range times {
		times[index] = start.Add(time.Duration(index) * time.Hour)
	}
	tracks, err := ComputeCelestialTracks(location, times)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != len(celestialBodyOrder) {
		t.Fatalf("track count = %d, want %d", len(tracks), len(celestialBodyOrder))
	}
	for bodyIndex, track := range tracks {
		if track.Body != celestialBodyOrder[bodyIndex] || len(track.Samples) != len(times) {
			t.Fatalf("track %d identity/cardinality mismatch", bodyIndex)
		}
		for timeIndex, sample := range track.Samples {
			if sample.Body != track.Body || !sample.ValidAt.Equal(times[timeIndex]) {
				t.Fatalf("track %s sample %d identity/time mismatch", track.Body, timeIndex)
			}
		}
	}
}

func TestPlanningRefractionUsesNegativeAltitudeDownToOneDegree(t *testing.T) {
	minusOne := -degree
	got := planningApparentAltitude(minusOne) / degree
	if got < -0.37 || got > -0.34 {
		t.Fatalf("apparent altitude at -1 degree = %.6f, want about -0.35", got)
	}
	belowBoundary := -1.001 * degree
	if got := planningApparentAltitude(belowBoundary); got != belowBoundary {
		t.Fatalf("refraction below -1 degree changed %.9f to %.9f", belowBoundary, got)
	}
}

func TestCelestialPlanningEphemerisRejectsDatesOutsideCommonDomain(t *testing.T) {
	location := forecast.Location{Latitude: 53.65, Longitude: 37.3462, TimeZone: "Europe/Moscow"}
	for _, year := range []int{1884, 2050, 2051} {
		for _, body := range celestialBodyOrder {
			if _, err := CelestialPositionAt(location, time.Date(year, time.July, 1, 0, 0, 0, 0, time.UTC), body); err == nil {
				t.Errorf("%s accepted unsupported year %d", body, year)
			}
		}
	}
}

func TestCelestialPlanningEphemerisKeepsShortElementsAtLastSupportedUTCMinute(t *testing.T) {
	location := forecast.Location{Latitude: 53.65, Longitude: 37.3462, TimeZone: "UTC"}
	at := time.Date(2049, time.December, 31, 23, 59, 0, 0, time.UTC)
	position, err := CelestialPositionAt(location, at, CelestialJupiter)
	if err != nil {
		t.Fatal(err)
	}
	if !finite(position.AzimuthDegrees) || !finite(position.GeometricAltitudeDegrees) {
		t.Fatalf("invalid final-domain position: %+v", position)
	}
}

func TestCelestialTracksRejectOrderAndTimeMismatch(t *testing.T) {
	location := forecast.Location{Latitude: 53.65, Longitude: 37.3462, TimeZone: "Europe/Moscow"}
	times := []time.Time{
		time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.August, 12, 1, 0, 0, 0, time.UTC),
	}
	tracks, err := ComputeCelestialTracks(location, times)
	if err != nil {
		t.Fatal(err)
	}
	tracks[0], tracks[1] = tracks[1], tracks[0]
	if err := ValidateCelestialTracks(tracks, times); err == nil {
		t.Fatal("reordered celestial tracks were accepted")
	}
	tracks, err = ComputeCelestialTracks(location, times)
	if err != nil {
		t.Fatal(err)
	}
	tracks[4].Samples[1].ValidAt = tracks[4].Samples[1].ValidAt.Add(time.Hour)
	if err := ValidateCelestialTracks(tracks, times); err == nil {
		t.Fatal("time-shifted celestial sample was accepted")
	}
}

func TestCelestialCulminationsUseIANALocalMidnightsAcrossDST(t *testing.T) {
	location := forecast.Location{Latitude: 52.52, Longitude: 13.405, TimeZone: "Europe/Berlin"}
	times := make([]time.Time, 72)
	start := time.Date(2026, time.October, 24, 0, 0, 0, 0, time.UTC)
	for index := range times {
		times[index] = start.Add(time.Duration(index) * time.Hour)
	}
	tracks, err := ComputeCelestialTracks(location, times)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCelestialTracksForLocation(tracks, times, location); err != nil {
		t.Fatal(err)
	}
	foundTwentyFiveHourDay := false
	for _, culmination := range tracks[0].Culminations {
		if culmination.DayEndExclusive.Sub(culmination.DayStart) == 25*time.Hour {
			foundTwentyFiveHourDay = true
		}
	}
	if !foundTwentyFiveHourDay {
		t.Fatal("DST transition did not produce a 25-hour local civil day")
	}
	shortLocation := forecast.Location{Latitude: 53.65, Longitude: 37.3462, TimeZone: "Europe/Moscow"}
	shortTimes := []time.Time{
		time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC),
		time.Date(2026, time.August, 12, 13, 0, 0, 0, time.UTC),
	}
	shiftedTracks, err := ComputeCelestialTracks(shortLocation, shortTimes)
	if err != nil {
		t.Fatal(err)
	}
	shiftedTracks[0].Culminations[0].DayStart = shiftedTracks[0].Culminations[0].DayStart.Add(12 * time.Hour)
	shiftedTracks[0].Culminations[0].DayEndExclusive = shiftedTracks[0].Culminations[0].DayEndExclusive.Add(12 * time.Hour)
	shiftedTracks[0].Culminations[0].ValidAt = shiftedTracks[0].Culminations[0].ValidAt.Add(12 * time.Hour)
	if err := ValidateCelestialTracks(shiftedTracks, shortTimes); err != nil {
		t.Fatalf("basic structural validator unexpectedly rejected shifted daily boundaries: %v", err)
	}
	if err := ValidateCelestialTracksForLocation(shiftedTracks, shortTimes, shortLocation); err == nil {
		t.Fatal("non-midnight culmination boundaries were accepted")
	}
}
