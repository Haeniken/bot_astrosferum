package iconeu

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"bot_astrosferum/internal/forecast"
)

func (volume *DomeVolume) AstrodomeSurfaceHeightAt(
	ctx context.Context,
	location forecast.Location,
) (float64, error) {
	if volume == nil {
		return 0, errors.New("ICON-EU Astrodome volume is required")
	}
	stencil, err := volume.HorizontalStencil(ctx, location)
	if err != nil {
		return 0, err
	}
	columns, err := volume.domeStencilColumns(ctx, stencil)
	if err != nil {
		return 0, err
	}
	values := [4]float64{}
	for index := range columns {
		values[index] = columns[index].HSURFHeightM
	}
	height := domeWeighted4(values, domeStencilWeights(stencil))
	if !finiteDomeVolume(height) {
		return 0, errors.New("ICON-EU Astrodome surface height is invalid")
	}
	return height, nil
}

// AstrodomeScienceSiteAt maps native point primitives to the provider-neutral
// operational site contract. Instantaneous quantities are read at the exact
// product hour. Total precipitation is differenced over two adjacent native
// accumulations; a cumulative GRIB value is never treated as an hourly rate.
func (volume *DomeVolume) AstrodomeScienceSiteAt(
	ctx context.Context,
	validAt time.Time,
	location forecast.Location,
) (forecast.AstrodomeScienceSiteInputs, error) {
	if volume == nil {
		return forecast.AstrodomeScienceSiteInputs{}, errors.New("ICON-EU Astrodome volume is required")
	}
	validAt = validAt.UTC()
	current, err := volume.NativeSurfaceAt(ctx, validAt, location)
	if err != nil {
		return forecast.AstrodomeScienceSiteInputs{}, fmt.Errorf("ICON-EU Astrodome current site surface: %w", err)
	}
	previousAt := validAt.Add(-time.Hour)
	previous, err := volume.NativeSurfaceAt(ctx, previousAt, location)
	if err != nil {
		return forecast.AstrodomeScienceSiteInputs{}, fmt.Errorf("ICON-EU Astrodome previous site surface: %w", err)
	}
	requiredWind := forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveEastwardWind10M) |
		forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveNorthwardWind10M) |
		forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveWindGust10M)
	if current.Available&requiredWind != requiredWind {
		return forecast.AstrodomeScienceSiteInputs{}, errors.New("ICON-EU Astrodome 10-m wind/gust is unavailable")
	}
	if !current.Available.Has(forecast.AstrodomePrimitivePrecipitationAccumulation) ||
		(!previousAt.Equal(volume.identity.RunBaseTime) &&
			!previous.Available.Has(forecast.AstrodomePrimitivePrecipitationAccumulation)) {
		return forecast.AstrodomeScienceSiteInputs{}, errors.New("ICON-EU Astrodome adjacent precipitation accumulations are unavailable")
	}
	if !current.PrecipitationIntervalStart.Equal(volume.identity.RunBaseTime) ||
		!previous.PrecipitationIntervalStart.Equal(volume.identity.RunBaseTime) ||
		!current.PrecipitationIntervalEnd.Equal(validAt) ||
		!previous.PrecipitationIntervalEnd.Equal(previousAt) {
		return forecast.AstrodomeScienceSiteInputs{}, errors.New("ICON-EU Astrodome precipitation interval identity is inconsistent")
	}
	currentPackingError, err := volume.astrodomePrecipitationPackingError(ctx, validAt)
	if err != nil {
		return forecast.AstrodomeScienceSiteInputs{}, fmt.Errorf("ICON-EU Astrodome current precipitation packing error: %w", err)
	}
	previousPackingError := 0.0
	if previousAt.Equal(volume.identity.RunBaseTime) {
		// f000 is the exact zero-length origin of the run accumulation, not an
		// observed hourly value. Its mathematical baseline is exactly zero; do
		// not manufacture a missing precipitation field or a packing error.
		previous.PrecipitationAccumulationMM = 0
	} else {
		previousPackingError, err = volume.astrodomePrecipitationPackingError(ctx, previousAt)
		if err != nil {
			return forecast.AstrodomeScienceSiteInputs{}, fmt.Errorf("ICON-EU Astrodome previous precipitation packing error: %w", err)
		}
	}
	precipitation, err := domeHourlyPrecipitationFromPackedAccumulations(
		current.PrecipitationAccumulationMM,
		previous.PrecipitationAccumulationMM,
		currentPackingError,
		previousPackingError,
	)
	if err != nil {
		return forecast.AstrodomeScienceSiteInputs{}, err
	}
	wind := math.Hypot(current.EastwardWind10MMS, current.NorthwardWind10MMS)
	if !finiteDomeVolume(wind) || !finiteDomeVolume(current.WindGust10MMS) || current.WindGust10MMS < 0 {
		return forecast.AstrodomeScienceSiteInputs{}, errors.New("ICON-EU Astrodome site wind is outside its physical domain")
	}
	leadHours := validAt.Sub(volume.identity.RunBaseTime).Hours()
	if !finiteDomeVolume(leadHours) || leadHours < 0 {
		return forecast.AstrodomeScienceSiteInputs{}, errors.New("ICON-EU Astrodome site time precedes its immutable run")
	}
	return forecast.AstrodomeScienceSiteInputs{
		SourceIdentity: volume.identity, ValidAt: validAt,
		WindSpeed10MMS: wind, WindGust10MMS: current.WindGust10MMS,
		FogHeuristic: domeAstrodomeFogHeuristic(current), PrecipitationRateMMPerHour: precipitation,
		PrecipitationIntervalStart: previousAt, PrecipitationIntervalEnd: validAt,
		ForecastLeadHours: leadHours,
	}, nil
}

func (volume *DomeVolume) astrodomePrecipitationPackingError(ctx context.Context, validAt time.Time) (float64, error) {
	lead := validAt.UTC().Sub(volume.identity.RunBaseTime).Hours()
	forecastHour := int(math.Round(lead))
	if !finiteDomeVolume(lead) || forecastHour < 0 || math.Abs(lead-float64(forecastHour)) > 1e-12 {
		return 0, errors.New("precipitation packing metadata needs an exact non-negative forecast hour")
	}

	volume.mu.Lock()
	if packingError, ok := volume.precipitationPackingErrors[forecastHour]; ok {
		volume.mu.Unlock()
		return packingError, nil
	}
	volume.mu.Unlock()

	source, ok := volume.surfaceFiles[forecastHour]
	if !ok {
		return 0, fmt.Errorf("surface f%03d is absent", forecastHour)
	}
	path, err := volume.resolveDomeVolumeSource(source)
	if err != nil {
		return 0, err
	}
	output, err := volume.runner.CombinedOutput(
		ctx,
		"grib_get",
		"-w", "shortName=tp",
		"-F", "%.17g",
		"-p", "packingError",
		path,
	)
	if err != nil {
		return 0, fmt.Errorf("read f%03d TOT_PREC packingError: %w", forecastHour, err)
	}
	fields := strings.Fields(string(output))
	if len(fields) != 1 {
		return 0, fmt.Errorf("f%03d TOT_PREC has %d packingError values, expected one", forecastHour, len(fields))
	}
	packingError, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || !finiteDomeVolume(packingError) || packingError < 0 {
		return 0, fmt.Errorf("f%03d TOT_PREC packingError is invalid", forecastHour)
	}
	// Expand by one representable float so parsing/rounding cannot make the
	// ecCodes error enclosure narrower than the source GRIB metadata.
	packingError = math.Nextafter(packingError, math.Inf(1))

	volume.mu.Lock()
	if volume.precipitationPackingErrors == nil {
		volume.precipitationPackingErrors = make(map[int]float64)
	}
	if cached, ok := volume.precipitationPackingErrors[forecastHour]; ok {
		packingError = cached
	} else {
		volume.precipitationPackingErrors[forecastHour] = packingError
	}
	volume.mu.Unlock()
	return packingError, nil
}

func domeHourlyPrecipitationFromPackedAccumulations(
	currentMM float64,
	previousMM float64,
	currentPackingErrorMM float64,
	previousPackingErrorMM float64,
) (float64, error) {
	values := []float64{currentMM, previousMM, currentPackingErrorMM, previousPackingErrorMM}
	for _, value := range values {
		if !finiteDomeVolume(value) || value < 0 {
			return 0, errors.New("ICON-EU Astrodome precipitation accumulation or packing error is invalid")
		}
	}
	delta := currentMM - previousMM
	ambiguity := currentPackingErrorMM + previousPackingErrorMM
	if delta < -ambiguity {
		return 0, errors.New("ICON-EU Astrodome cumulative precipitation decreased beyond its GRIB packing-error enclosure")
	}
	// The true de-accumulated value is indistinguishable from zero throughout
	// this interval. Suppress both negative and positive packing artefacts, as
	// recommended for start-of-run accumulations, before the rain veto sees it.
	if delta <= ambiguity {
		return 0, nil
	}
	return delta, nil
}

func domeAstrodomeFogHeuristic(surface forecast.AstrodomeSurfacePrimitives) forecast.AstrodomeScienceFogHeuristic {
	required := forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveVisibility) |
		forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveRelativeHumidity) |
		forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveTemperature2M) |
		forecast.AstrodomePrimitiveFieldSet(forecast.AstrodomePrimitiveDewPoint2M)
	if surface.Available&required != required || !finiteDomeVolume(surface.VisibilityM) ||
		!finiteDomeVolume(surface.RelativeHumidityFraction) || !finiteDomeVolume(surface.Temperature2MK) ||
		!finiteDomeVolume(surface.DewPoint2MK) {
		return forecast.AstrodomeScienceFogUnavailable
	}
	dewPointDepression := surface.Temperature2MK - surface.DewPoint2MK
	if surface.VisibilityM < 1000 && surface.RelativeHumidityFraction >= 0.95 && dewPointDepression <= 1.5 {
		return forecast.AstrodomeScienceFogHigh
	}
	if surface.VisibilityM < 5000 && surface.RelativeHumidityFraction >= 0.90 && dewPointDepression <= 2.5 {
		return forecast.AstrodomeScienceFogPossible
	}
	return forecast.AstrodomeScienceFogNone
}
