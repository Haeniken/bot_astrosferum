package geoscf

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"bot_astrosferum/internal/forecast"
)

var (
	stringAttribute   = regexp.MustCompile(`(?m)\bString\s+([A-Za-z0-9_]+)\s+"([^"]*)"\s*;`)
	numericAttribute  = regexp.MustCompile(`(?m)\bFloat(?:32|64)\s+([A-Za-z0-9_]+)\s+([-+0-9.eE]+)\s*;`)
	publicationRegexp = regexp.MustCompile(`(?i)(?:Mon|Tue|Wed|Thu|Fri|Sat|Sun)\s+[A-Z][a-z]{2}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}\s+(?:UTC|GMT|EDT|EST)\s+\d{4}`)
)

type datasetMetadata struct {
	runID           string
	runTime         time.Time
	firstValid      time.Time
	lastValid       time.Time
	publicationTime time.Time
	timeStep        time.Duration
	timeCount       int
	latMin          float64
	latMax          float64
	latResolution   float64
	latCount        int
	lonMin          float64
	lonMax          float64
	lonResolution   float64
	lonCount        int
}

type dasSection struct {
	strings map[string]string
	numbers map[string]float64
}

func parseDAS(body []byte) (datasetMetadata, error) {
	timeSection, err := parseDASSection(body, "time")
	if err != nil {
		return datasetMetadata{}, err
	}
	latSection, err := parseDASSection(body, "lat")
	if err != nil {
		return datasetMetadata{}, err
	}
	lonSection, err := parseDASSection(body, "lon")
	if err != nil {
		return datasetMetadata{}, err
	}
	firstValid, err := parseGrADSDate(firstNonEmpty(timeSection.strings["minimum"], timeSection.strings["grads_min"]))
	if err != nil {
		return datasetMetadata{}, fmt.Errorf("%w: time minimum: %w", ErrMalformedData, err)
	}
	lastValid, err := parseGrADSDate(timeSection.strings["maximum"])
	if err != nil {
		return datasetMetadata{}, fmt.Errorf("%w: time maximum: %w", ErrMalformedData, err)
	}
	timeStep, err := parseGrADSDuration(timeSection.strings["grads_step"])
	if err != nil {
		return datasetMetadata{}, fmt.Errorf("%w: time step: %w", ErrMalformedData, err)
	}
	timeCount, err := positiveIntegerAttribute(timeSection, "grads_size")
	if err != nil {
		return datasetMetadata{}, fmt.Errorf("%w: time size: %w", ErrMalformedData, err)
	}
	latCount, err := positiveIntegerAttribute(latSection, "grads_size")
	if err != nil {
		return datasetMetadata{}, fmt.Errorf("%w: latitude size: %w", ErrMalformedData, err)
	}
	lonCount, err := positiveIntegerAttribute(lonSection, "grads_size")
	if err != nil {
		return datasetMetadata{}, fmt.Errorf("%w: longitude size: %w", ErrMalformedData, err)
	}
	metadata := datasetMetadata{
		firstValid:    firstValid,
		lastValid:     lastValid,
		timeStep:      timeStep,
		timeCount:     timeCount,
		latMin:        latSection.numbers["minimum"],
		latMax:        latSection.numbers["maximum"],
		latResolution: latSection.numbers["resolution"],
		latCount:      latCount,
		lonMin:        lonSection.numbers["minimum"],
		lonMax:        lonSection.numbers["maximum"],
		lonResolution: lonSection.numbers["resolution"],
		lonCount:      lonCount,
	}
	if global, globalErr := parseDASSection(body, "NC_GLOBAL"); globalErr == nil {
		metadata.publicationTime = parsePublicationTime(global.strings["history"])
	}
	if err := validateMetadata(metadata); err != nil {
		return datasetMetadata{}, err
	}
	metadata.runTime = firstValid.Add(-timeStep / 2)
	metadata.runID = "geos-cf-" + metadata.runTime.Format("20060102T1504Z")
	return metadata, nil
}

func parseDASSection(body []byte, name string) (dasSection, error) {
	pattern := regexp.MustCompile(`(?ms)^\s*` + regexp.QuoteMeta(name) + `\s*\{(.*?)^\s*\}`)
	match := pattern.FindSubmatch(body)
	if match == nil {
		return dasSection{}, fmt.Errorf("%w: DAS lacks %s section", ErrMalformedData, name)
	}
	section := dasSection{strings: make(map[string]string), numbers: make(map[string]float64)}
	for _, attribute := range stringAttribute.FindAllStringSubmatch(string(match[1]), -1) {
		section.strings[attribute[1]] = attribute[2]
	}
	for _, attribute := range numericAttribute.FindAllStringSubmatch(string(match[1]), -1) {
		value, err := strconv.ParseFloat(attribute[2], 64)
		if err != nil {
			return dasSection{}, fmt.Errorf("%w: invalid %s.%s value %q", ErrMalformedData, name, attribute[1], attribute[2])
		}
		section.numbers[attribute[1]] = value
	}
	return section, nil
}

func validateMetadata(metadata datasetMetadata) error {
	if metadata.timeStep <= 0 || metadata.timeCount < 2 {
		return fmt.Errorf("%w: invalid temporal grid", ErrMalformedData)
	}
	expectedLast := metadata.firstValid.Add(time.Duration(metadata.timeCount-1) * metadata.timeStep)
	if delta := expectedLast.Sub(metadata.lastValid); delta < -time.Second || delta > time.Second {
		return fmt.Errorf("%w: inconsistent time window: computed %s, declared %s", ErrMalformedData, expectedLast.Format(time.RFC3339), metadata.lastValid.Format(time.RFC3339))
	}
	if metadata.latCount < 2 || metadata.lonCount < 2 || metadata.latResolution <= 0 || metadata.lonResolution <= 0 {
		return fmt.Errorf("%w: invalid spatial grid", ErrMalformedData)
	}
	expectedLatMax := metadata.latMin + float64(metadata.latCount-1)*metadata.latResolution
	expectedLonMax := metadata.lonMin + float64(metadata.lonCount-1)*metadata.lonResolution
	if math.Abs(expectedLatMax-metadata.latMax) > 1e-6 || math.Abs(expectedLonMax-metadata.lonMax) > 1e-6 {
		return fmt.Errorf("%w: inconsistent spatial grid", ErrMalformedData)
	}
	if math.Abs(metadata.latMin+90) > 1e-6 || math.Abs(metadata.latMax-90) > 1e-6 {
		return fmt.Errorf("%w: latitude grid does not cover the globe", ErrMalformedData)
	}
	if math.Abs((metadata.lonMax+metadata.lonResolution)-metadata.lonMin-360) > 1e-6 {
		return fmt.Errorf("%w: longitude grid is not periodic over 360 degrees", ErrMalformedData)
	}
	return nil
}

func positiveIntegerAttribute(section dasSection, name string) (int, error) {
	value, ok := section.strings[name]
	if !ok {
		return 0, fmt.Errorf("missing %s", name)
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("invalid %s %q", name, value)
	}
	return parsed, nil
}

func parseGrADSDate(value string) (time.Time, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return time.Time{}, fmt.Errorf("missing date")
	}
	parsed, err := time.Parse("15:04z02Jan2006", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %q: %w", value, err)
	}
	return parsed.UTC(), nil
}

func parseGrADSDuration(value string) (time.Duration, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if strings.HasSuffix(value, "mn") {
		minutes, err := strconv.Atoi(strings.TrimSuffix(value, "mn"))
		if err == nil && minutes > 0 {
			return time.Duration(minutes) * time.Minute, nil
		}
	}
	if strings.HasSuffix(value, "hr") {
		hours, err := strconv.Atoi(strings.TrimSuffix(value, "hr"))
		if err == nil && hours > 0 {
			return time.Duration(hours) * time.Hour, nil
		}
	}
	return 0, fmt.Errorf("unsupported duration %q", value)
}

func parsePublicationTime(history string) time.Time {
	value := publicationRegexp.FindString(history)
	if value == "" {
		return time.Time{}
	}
	parts := strings.Fields(value)
	if len(parts) != 6 {
		return time.Time{}
	}
	zone := parts[4]
	offset := 0
	switch zone {
	case "EDT":
		offset = -4 * 60 * 60
	case "EST":
		offset = -5 * 60 * 60
	case "UTC", "GMT":
	default:
		return time.Time{}
	}
	location := time.FixedZone(zone, offset)
	parsed, err := time.ParseInLocation("Mon Jan 2 15:04:05 MST 2006", value, location)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type gridCell struct {
	lat0      int
	lat1      int
	lon0      int
	lon1      int
	latWeight float64
	lonWeight float64
}

func locateGridCell(metadata datasetMetadata, location forecast.Location) gridCell {
	latitudePosition := (location.Latitude - metadata.latMin) / metadata.latResolution
	lat0 := int(math.Floor(latitudePosition))
	lat0 = max(0, min(lat0, metadata.latCount-1))
	lat1 := min(lat0+1, metadata.latCount-1)
	latWeight := latitudePosition - float64(lat0)
	if lat0 == lat1 {
		latWeight = 0
	}

	longitude := location.Longitude
	if longitude == 180 {
		longitude = -180
	}
	longitudePosition := (longitude - metadata.lonMin) / metadata.lonResolution
	lon0 := int(math.Floor(longitudePosition))
	lon0 = ((lon0 % metadata.lonCount) + metadata.lonCount) % metadata.lonCount
	lon1 := (lon0 + 1) % metadata.lonCount
	lonWeight := longitudePosition - math.Floor(longitudePosition)
	return gridCell{lat0: lat0, lat1: lat1, lon0: lon0, lon1: lon1, latWeight: clampUnit(latWeight), lonWeight: clampUnit(lonWeight)}
}

func (cell gridCell) latCount() int {
	if cell.lat0 == cell.lat1 {
		return 1
	}
	return 2
}

func (cell gridCell) support(metadata datasetMetadata) []GridPoint {
	latitudes := []int{cell.lat0}
	if cell.lat1 != cell.lat0 {
		latitudes = append(latitudes, cell.lat1)
	}
	longitudes := []int{cell.lon0}
	if cell.lon1 != cell.lon0 {
		longitudes = append(longitudes, cell.lon1)
	}
	points := make([]GridPoint, 0, len(latitudes)*len(longitudes))
	for _, latitude := range latitudes {
		for _, longitude := range longitudes {
			points = append(points, GridPoint{
				Latitude:  metadata.latMin + float64(latitude)*metadata.latResolution,
				Longitude: metadata.lonMin + float64(longitude)*metadata.lonResolution,
			})
		}
	}
	return points
}

func clampUnit(value float64) float64 {
	return math.Max(0, math.Min(1, value))
}
