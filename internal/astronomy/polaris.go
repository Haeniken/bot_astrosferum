package astronomy

import (
	"fmt"
	"math"
	"time"

	"bot_astrosferum/internal/forecast"

	"github.com/soniakeys/meeus/v3/base"
	"github.com/soniakeys/meeus/v3/coord"
	"github.com/soniakeys/meeus/v3/julian"
	"github.com/soniakeys/meeus/v3/precess"
	"github.com/soniakeys/meeus/v3/sidereal"
	"github.com/soniakeys/unit"
)

const (
	PolarisCatalogVersion = "polaris-simbad-revised-hipparcos-icrs-j2000-v1"
	polarisName           = "Polaris"
	polarisReferenceFrame = "ICRS"
	polarisEpoch          = "J2000.0"

	// SIMBAD revised Hipparcos astrometry for HIP 11767 / alpha UMi.
	polarisCatalogRAHours                    = 2 + 31.0/60 + 49.09456/3600
	polarisCatalogDeclinationDegrees         = 89 + 15.0/60 + 50.7923/3600
	polarisProperMotionRAStarMasPerYear      = 44.48
	polarisProperMotionDeclinationMasPerYear = -11.85
	polarisSynScanRA                         = "02h 31m 49.095s"
	polarisSynScanDeclination                = "+89° 15′ 50.79″"
	masToRadians                             = degree / (3600 * 1000)
)

type PolarisSynScanCoordinates struct {
	RightAscension string `json:"right_ascension"`
	Declination    string `json:"declination"`
	ReferenceFrame string `json:"reference_frame"`
	Epoch          string `json:"epoch"`
}

type PolarisHorizontalPosition struct {
	ValidAt                  time.Time `json:"valid_at"`
	AzimuthDegrees           float64   `json:"azimuth_deg"`
	GeometricAltitudeDegrees float64   `json:"geometric_altitude_deg"`
	ApparentAltitudeDegrees  float64   `json:"apparent_altitude_deg"`
}

type PolarisTrack struct {
	CatalogVersion                    string                      `json:"catalog_version"`
	Name                              string                      `json:"name"`
	CatalogRAHours                    float64                     `json:"catalog_ra_hours"`
	CatalogDeclinationDegrees         float64                     `json:"catalog_declination_deg"`
	ProperMotionRAStarMasPerYear      float64                     `json:"proper_motion_ra_star_mas_per_year"`
	ProperMotionDeclinationMasPerYear float64                     `json:"proper_motion_declination_mas_per_year"`
	SynScan                           PolarisSynScanCoordinates   `json:"synscan"`
	Samples                           []PolarisHorizontalPosition `json:"samples"`
}

func ComputePolarisTrack(location forecast.Location, validTimes []time.Time) (PolarisTrack, error) {
	if err := forecast.ValidateCoordinates(location.Latitude, location.Longitude); err != nil {
		return PolarisTrack{}, err
	}
	if err := validateCelestialTrackTimes(validTimes); err != nil {
		return PolarisTrack{}, err
	}
	track := PolarisTrack{
		CatalogVersion:                    PolarisCatalogVersion,
		Name:                              polarisName,
		CatalogRAHours:                    polarisCatalogRAHours,
		CatalogDeclinationDegrees:         polarisCatalogDeclinationDegrees,
		ProperMotionRAStarMasPerYear:      polarisProperMotionRAStarMasPerYear,
		ProperMotionDeclinationMasPerYear: polarisProperMotionDeclinationMasPerYear,
		SynScan: PolarisSynScanCoordinates{
			RightAscension: polarisSynScanRA,
			Declination:    polarisSynScanDeclination,
			ReferenceFrame: polarisReferenceFrame,
			Epoch:          polarisEpoch,
		},
		Samples: make([]PolarisHorizontalPosition, len(validTimes)),
	}
	for index, validAt := range validTimes {
		position, err := polarisHorizontalPosition(location, validAt)
		if err != nil {
			return PolarisTrack{}, fmt.Errorf("compute Polaris at %s: %w", validAt.Format(time.RFC3339), err)
		}
		track.Samples[index] = position
	}
	if err := ValidatePolarisTrack(track, validTimes); err != nil {
		return PolarisTrack{}, err
	}
	return track, nil
}

func ValidatePolarisTrack(track PolarisTrack, validTimes []time.Time) error {
	if err := validateCelestialTrackTimes(validTimes); err != nil {
		return err
	}
	if track.CatalogVersion != PolarisCatalogVersion || track.Name != polarisName ||
		track.CatalogRAHours != polarisCatalogRAHours ||
		track.CatalogDeclinationDegrees != polarisCatalogDeclinationDegrees ||
		track.ProperMotionRAStarMasPerYear != polarisProperMotionRAStarMasPerYear ||
		track.ProperMotionDeclinationMasPerYear != polarisProperMotionDeclinationMasPerYear ||
		track.SynScan.RightAscension != polarisSynScanRA ||
		track.SynScan.Declination != polarisSynScanDeclination ||
		track.SynScan.ReferenceFrame != polarisReferenceFrame || track.SynScan.Epoch != polarisEpoch ||
		len(track.Samples) != len(validTimes) {
		return fmt.Errorf("polaris catalog contract is invalid")
	}
	for index, sample := range track.Samples {
		if !sample.ValidAt.Equal(validTimes[index]) || !finite(sample.AzimuthDegrees) || sample.AzimuthDegrees < 0 || sample.AzimuthDegrees >= 360 ||
			!finite(sample.GeometricAltitudeDegrees) || sample.GeometricAltitudeDegrees < -90 || sample.GeometricAltitudeDegrees > 90 ||
			!finite(sample.ApparentAltitudeDegrees) || sample.ApparentAltitudeDegrees < -90 || sample.ApparentAltitudeDegrees > 90 {
			return fmt.Errorf("polaris sample %d is invalid", index)
		}
	}
	return nil
}

func polarisHorizontalPosition(location forecast.Location, at time.Time) (PolarisHorizontalPosition, error) {
	if err := validateCelestialEphemerisInstant(at); err != nil {
		return PolarisHorizontalPosition{}, err
	}
	ra, dec := polarisMeanEquatorialOfDate(at)
	hourAngle := sidereal.Mean(julian.TimeToJD(at)).Rad() + location.Longitude*degree - ra
	geometric := altitude(hourAngle, location.Latitude*degree, dec)
	east := -math.Cos(dec) * math.Sin(hourAngle)
	north := math.Sin(dec)*math.Cos(location.Latitude*degree) -
		math.Cos(dec)*math.Cos(hourAngle)*math.Sin(location.Latitude*degree)
	return PolarisHorizontalPosition{
		ValidAt:                  at.UTC(),
		AzimuthDegrees:           normalizeRadians(math.Atan2(east, north)) / degree,
		GeometricAltitudeDegrees: geometric / degree,
		ApparentAltitudeDegrees:  planningApparentAltitude(geometric) / degree,
	}, nil
}

func polarisMeanEquatorialOfDate(at time.Time) (float64, float64) {
	ra0 := polarisCatalogRAHours * 15 * degree
	dec0 := polarisCatalogDeclinationDegrees * degree
	sinRA, cosRA := math.Sincos(ra0)
	sinDec, cosDec := math.Sincos(dec0)
	u0 := vector3{x: cosDec * cosRA, y: cosDec * sinRA, z: sinDec}
	eRA := vector3{x: -sinRA, y: cosRA}
	eDec := vector3{x: -cosRA * sinDec, y: -sinRA * sinDec, z: cosDec}
	properMotion := vector3{
		x: polarisProperMotionRAStarMasPerYear*masToRadians*eRA.x + polarisProperMotionDeclinationMasPerYear*masToRadians*eDec.x,
		y: polarisProperMotionRAStarMasPerYear*masToRadians*eRA.y + polarisProperMotionDeclinationMasPerYear*masToRadians*eDec.y,
		z: polarisProperMotionRAStarMasPerYear*masToRadians*eRA.z + polarisProperMotionDeclinationMasPerYear*masToRadians*eDec.z,
	}
	years := base.JDEToJulianYear(timeToEphemerisDay(at)) - 2000
	u := vector3{x: u0.x + years*properMotion.x, y: u0.y + years*properMotion.y, z: u0.z + years*properMotion.z}
	norm := vectorNorm(u)
	meanJ2000 := coord.Equatorial{
		RA:  unit.RAFromRad(math.Atan2(u.y, u.x)),
		Dec: unit.Angle(math.Asin(u.z / norm)),
	}
	var meanOfDate coord.Equatorial
	precess.NewPrecessor(2000, 2000+years).Precess(&meanJ2000, &meanOfDate)
	return meanOfDate.RA.Rad(), meanOfDate.Dec.Rad()
}
