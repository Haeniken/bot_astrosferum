package iconeu

import (
	"fmt"
	"math"
	"strings"
	"time"

	"bot_astrosferum/internal/forecast"
)

func verticalFromBatch(values map[batchValueKey]float64, step StepFile, sourceName string) (forecast.VerticalFrame, error) {
	vectors := make(map[float64]verticalVector, len(DefaultPressureLevelsHPA))
	for key, value := range values {
		name := strings.TrimSpace(key.name)
		if name != "u" && name != "v" && name != "z" && name != "t" {
			return forecast.VerticalFrame{}, fmt.Errorf("%s has unexpected pressure field %q", sourceName, name)
		}
		if key.level <= 2000 || key.level > 110000 {
			return forecast.VerticalFrame{}, fmt.Errorf("%s has invalid pressure level %.10g Pa", sourceName, key.level)
		}
		pressureHPA := key.level / 100
		item := vectors[pressureHPA]
		switch name {
		case "u":
			item.u, item.hasU = value, true
		case "v":
			item.v, item.hasV = value, true
		case "z":
			item.height, item.hasZ = value/9.80665, true
		case "t":
			item.temperature, item.hasT = value, true
		}
		vectors[pressureHPA] = item
	}
	return verticalFrameFromVectors(vectors, step, sourceName)
}

func surfaceFromBatch(values map[batchValueKey]float64, validAt time.Time, sourceName string) (forecast.SurfaceFrame, error) {
	extracted, err := surfaceExtractedFromBatch(values, validAt, sourceName)
	if err != nil {
		return forecast.SurfaceFrame{}, err
	}
	return extracted.Frame, nil
}

func surfaceExtractedFromBatch(values map[batchValueKey]float64, validAt time.Time, sourceName string) (ExtractedSurface, error) {
	normalized := make(map[string]float64, len(surfaceFields))
	expected := make(map[string]struct{}, len(surfaceFields))
	for _, field := range surfaceFields {
		expected[field.ShortName] = struct{}{}
	}
	for key, value := range values {
		name := canonicalSurfaceShortName(key.name)
		if _, ok := expected[name]; !ok {
			return ExtractedSurface{}, fmt.Errorf("%s has unexpected surface field %q", sourceName, key.name)
		}
		if _, duplicate := normalized[name]; duplicate {
			return ExtractedSurface{}, fmt.Errorf("%s repeats surface field %q", sourceName, name)
		}
		normalized[name] = value
	}
	extracted, err := surfaceFromValues(normalized, validAt, sourceName)
	if err != nil {
		return ExtractedSurface{}, err
	}
	return extracted, nil
}

func cloudHeightsFromBatch(values map[batchValueKey]float64, sourceName string) (map[int]float64, error) {
	heights := make(map[int]float64, len(values))
	for key, value := range values {
		if strings.TrimSpace(key.name) != "HHL" || math.Trunc(key.level) != key.level {
			return nil, fmt.Errorf("%s has unexpected geometry field %q level %.10g", sourceName, key.name, key.level)
		}
		level := int(key.level)
		if _, duplicate := heights[level]; duplicate {
			return nil, fmt.Errorf("%s repeats HHL level %d", sourceName, level)
		}
		heights[level] = value
	}
	return heights, nil
}

func cloudFromBatch(values map[batchValueKey]float64, heights map[int]float64, validAt time.Time, sourceName string) (forecast.CloudFrame, error) {
	normalized := make(map[cloudValueKey]float64, len(values))
	for key, value := range values {
		if math.Trunc(key.level) != key.level {
			return forecast.CloudFrame{}, fmt.Errorf("%s has non-integer model level %.10g", sourceName, key.level)
		}
		name := canonicalCloudShortName(key.name)
		switch name {
		case "ccl", "pres", "t", "qc", "qi", "u", "v", "tke":
		default:
			return forecast.CloudFrame{}, fmt.Errorf("%s has unexpected cloud field %q", sourceName, key.name)
		}
		normalized[cloudValueKey{name: name, level: int(key.level)}] = value
	}
	return cloudFrameFromValues(normalized, validAt, DefaultCloudModelLevels, DefaultCloudGroundModelLevels, heights, sourceName)
}
