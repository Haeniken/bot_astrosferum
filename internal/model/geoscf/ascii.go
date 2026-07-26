package geoscf

import (
	"bufio"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var asciiHeaderPattern = regexp.MustCompile(`^([A-Za-z0-9_]+),\s*((?:\[\d+\])+)\s*$`)
var asciiDimensionPattern = regexp.MustCompile(`\[(\d+)\]`)

type rawSlab struct {
	timeCount int
	latCount  int
	lonCount  int
	values    map[string][]float64
}

type rawPointSeries struct {
	timeFirst   int
	timeCount   int
	latCount    int
	lonCount    int
	values      map[string][]float64
	retrievedAt time.Time
}

type indexWindow struct {
	first int
	last  int
}

func (window indexWindow) count() int {
	return window.last - window.first + 1
}

func parseASCII(body []byte, expectedTimes, expectedLatitudes, expectedLongitudes int) (rawSlab, error) {
	slab := rawSlab{
		timeCount: expectedTimes,
		latCount:  expectedLatitudes,
		lonCount:  expectedLongitudes,
		values:    make(map[string][]float64, len(variableNames)),
	}
	expected := make(map[string]struct{}, len(variableNames))
	for _, variable := range variableNames {
		expected[variable] = struct{}{}
	}

	var active string
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	// The requested response is intentionally small, but a full row can still
	// exceed Scanner's conservative default token size on unexpected servers.
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if match := asciiHeaderPattern.FindStringSubmatch(line); match != nil {
			active = ""
			if _, wanted := expected[match[1]]; !wanted {
				continue
			}
			dimensions, err := parseDimensions(match[2])
			if err != nil {
				return rawSlab{}, err
			}
			if len(dimensions) != 3 || dimensions[0] != expectedTimes || dimensions[1] != expectedLatitudes || dimensions[2] != expectedLongitudes {
				return rawSlab{}, fmt.Errorf("%w: %s dimensions %v, expected [%d %d %d]", ErrMalformedData, match[1], dimensions, expectedTimes, expectedLatitudes, expectedLongitudes)
			}
			if _, duplicate := slab.values[match[1]]; duplicate {
				return rawSlab{}, fmt.Errorf("%w: duplicate %s section", ErrMalformedData, match[1])
			}
			slab.values[match[1]] = make([]float64, 0, expectedTimes*expectedLatitudes*expectedLongitudes)
			active = match[1]
			continue
		}
		if active == "" {
			continue
		}
		comma := strings.IndexByte(line, ',')
		if comma < 0 {
			return rawSlab{}, fmt.Errorf("%w: %s data row lacks comma", ErrMalformedData, active)
		}
		for _, rawValue := range strings.Split(line[comma+1:], ",") {
			value, err := strconv.ParseFloat(strings.TrimSpace(rawValue), 64)
			if err != nil {
				return rawSlab{}, fmt.Errorf("%w: %s value %q: %w", ErrMalformedData, active, rawValue, err)
			}
			slab.values[active] = append(slab.values[active], value)
		}
	}
	if err := scanner.Err(); err != nil {
		return rawSlab{}, fmt.Errorf("%w: scan ASCII response: %w", ErrMalformedData, err)
	}
	expectedValues := expectedTimes * expectedLatitudes * expectedLongitudes
	for _, variable := range variableNames {
		values, ok := slab.values[variable]
		if !ok {
			return rawSlab{}, fmt.Errorf("%w: response lacks %s", ErrMalformedData, variable)
		}
		if len(values) != expectedValues {
			return rawSlab{}, fmt.Errorf("%w: %s contains %d values, expected %d", ErrMalformedData, variable, len(values), expectedValues)
		}
	}
	return slab, nil
}

func parseDimensions(value string) ([]int, error) {
	matches := asciiDimensionPattern.FindAllStringSubmatch(value, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("%w: missing array dimensions", ErrMalformedData)
	}
	dimensions := make([]int, 0, len(matches))
	for _, match := range matches {
		dimension, err := strconv.Atoi(match[1])
		if err != nil || dimension <= 0 {
			return nil, fmt.Errorf("%w: invalid array dimension %q", ErrMalformedData, match[1])
		}
		dimensions = append(dimensions, dimension)
	}
	return dimensions, nil
}

func mergeSlabs(parts []rawSlab, window indexWindow, grid gridCell) (rawPointSeries, error) {
	if len(parts) != 1 && len(parts) != 2 {
		return rawPointSeries{}, fmt.Errorf("%w: expected one or two longitude slabs, got %d", ErrMalformedData, len(parts))
	}
	result := rawPointSeries{
		timeFirst: window.first,
		timeCount: window.count(),
		latCount:  grid.latCount(),
		lonCount:  2,
		values:    make(map[string][]float64, len(variableNames)),
	}
	if grid.lon0 == grid.lon1 {
		result.lonCount = 1
	}
	if len(parts) == 1 {
		result.lonCount = parts[0].lonCount
		for _, variable := range variableNames {
			result.values[variable] = append([]float64(nil), parts[0].values[variable]...)
		}
		return result, nil
	}
	for _, part := range parts {
		if part.timeCount != result.timeCount || part.latCount != result.latCount || part.lonCount != 1 {
			return rawPointSeries{}, fmt.Errorf("%w: incompatible dateline slabs", ErrMalformedData)
		}
	}
	for _, variable := range variableNames {
		merged := make([]float64, 0, result.timeCount*result.latCount*2)
		for timeIndex := 0; timeIndex < result.timeCount; timeIndex++ {
			for latIndex := 0; latIndex < result.latCount; latIndex++ {
				offset := timeIndex*result.latCount + latIndex
				merged = append(merged, parts[0].values[variable][offset], parts[1].values[variable][offset])
			}
		}
		result.values[variable] = merged
	}
	return result, nil
}

func interpolateFrame(raw rawPointSeries, metadata datasetMetadata, grid gridCell, validTime time.Time) (Frame, error) {
	position := float64(validTime.Sub(metadata.firstValid)) / float64(metadata.timeStep)
	lowerGlobal := int(math.Floor(position + 1e-9))
	upperGlobal := int(math.Ceil(position - 1e-9))
	lower := lowerGlobal - raw.timeFirst
	upper := upperGlobal - raw.timeFirst
	if lower < 0 || upper >= raw.timeCount || lower > upper {
		return Frame{}, fmt.Errorf("%w: time index %d:%d is not in fetched slab", ErrOutsideCoverage, lowerGlobal, upperGlobal)
	}
	timeWeight := clampUnit(position - float64(lowerGlobal))
	values := make(map[string]float64, len(variableNames))
	for _, variable := range variableNames {
		lowerValue, err := spatialValue(raw, variable, lower, grid)
		if err != nil {
			return Frame{}, err
		}
		upperValue := lowerValue
		if upper != lower {
			upperValue, err = spatialValue(raw, variable, upper, grid)
			if err != nil {
				return Frame{}, err
			}
		}
		value := lowerValue + timeWeight*(upperValue-lowerValue)
		if !validSourceValue(variable, value) {
			return Frame{}, fmt.Errorf("%w: invalid interpolated %s value %g", ErrMissingData, variable, value)
		}
		values[variable] = value
	}
	components := AOD550Components{
		BlackCarbon:                values["aod550_bc"],
		Dust:                       values["aod550_dust"],
		OrganicCarbon:              values["aod550_oc"],
		PolarStratosphericCloud:    values["aod550_psc"],
		StratosphericLiquidAerosol: values["aod550_sla"],
		SulfateNitrateAmmonium:     values["aod550_sna"],
		SeaSalt:                    values["aod550_ss"],
	}
	return Frame{
		ValidTimeUTC:       validTime.UTC(),
		AOD550:             components.Total(),
		AOD550Components:   components,
		TotalColumnOzoneDU: values["totcol_o3"],
	}, nil
}

func spatialValue(raw rawPointSeries, variable string, timeIndex int, grid gridCell) (float64, error) {
	values, ok := raw.values[variable]
	if !ok {
		return 0, fmt.Errorf("%w: no %s values", ErrMalformedData, variable)
	}
	at := func(latitudeIndex, longitudeIndex int) (float64, error) {
		offset := (timeIndex*raw.latCount+latitudeIndex)*raw.lonCount + longitudeIndex
		if offset < 0 || offset >= len(values) {
			return 0, fmt.Errorf("%w: %s offset %d is outside %d values", ErrMalformedData, variable, offset, len(values))
		}
		value := values[offset]
		if !validSourceValue(variable, value) {
			return 0, fmt.Errorf("%w: invalid %s source value %g", ErrMissingData, variable, value)
		}
		return value, nil
	}
	v00, err := at(0, 0)
	if err != nil {
		return 0, err
	}
	latWeight := grid.latWeight
	lonWeight := grid.lonWeight
	v10 := v00
	if raw.latCount > 1 {
		v10, err = at(1, 0)
		if err != nil {
			return 0, err
		}
	} else {
		latWeight = 0
	}
	var v01, v11 float64
	if raw.lonCount > 1 {
		v01, err = at(0, 1)
		if err != nil {
			return 0, err
		}
		if raw.latCount > 1 {
			v11, err = at(1, 1)
			if err != nil {
				return 0, err
			}
		} else {
			v11 = v01
		}
	} else {
		lonWeight = 0
		v01, v11 = v00, v10
	}
	low := v00 + lonWeight*(v01-v00)
	high := v10 + lonWeight*(v11-v10)
	return low + latWeight*(high-low), nil
}

func validSourceValue(variable string, value float64) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) >= 1e14 {
		return false
	}
	if variable == "totcol_o3" {
		return value > 0
	}
	return value >= 0
}
