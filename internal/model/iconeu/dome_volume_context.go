package iconeu

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"bot_astrosferum/internal/forecast"
)

type domeVolumeTimeBracket struct {
	leftIndex  int
	rightIndex int
	fraction   float64
}

// ResolveAstrodomeScienceNativeContext reconstructs only the raw native
// context needed to choose science branches: HHL/HSURF, MH, and the complete
// P/T thermal profile. It does not return a PBL branch, tropopause, Cn2,
// transmission, seeing, or any score.
func (volume *DomeVolume) ResolveAstrodomeScienceNativeContext(
	ctx context.Context,
	validAt time.Time,
	point forecast.AstrodomeRayPoint,
	stencil forecast.AstrodomeHorizontalStencil,
) (forecast.AstrodomeScienceNativeContext, error) {
	if volume == nil {
		return forecast.AstrodomeScienceNativeContext{}, errors.New("ICON-EU Astrodome volume is required")
	}
	if err := ctx.Err(); err != nil {
		return forecast.AstrodomeScienceNativeContext{}, err
	}
	if err := forecast.ValidateCoordinates(point.Location.Latitude, point.Location.Longitude); err != nil {
		return forecast.AstrodomeScienceNativeContext{}, err
	}
	if !finiteDomeVolume(point.HeightM) {
		return forecast.AstrodomeScienceNativeContext{}, errors.New("ICON-EU Astrodome ray-point height is invalid")
	}
	expected, err := volume.HorizontalStencil(ctx, point.Location)
	if err != nil {
		return forecast.AstrodomeScienceNativeContext{}, err
	}
	if err := sameDomeStencil(expected, stencil); err != nil {
		return forecast.AstrodomeScienceNativeContext{}, err
	}
	bracket, err := domeVolumeBracket(volume.fieldTimes[forecast.AstrodomePrimitivePressure], validAt)
	if err != nil {
		return forecast.AstrodomeScienceNativeContext{}, err
	}
	columns, err := volume.domeStencilColumns(ctx, expected)
	if err != nil {
		return forecast.AstrodomeScienceNativeContext{}, err
	}
	weights := domeStencilWeights(expected)

	halfHeights := make([]float64, domeHalfLevelCount)
	for level := range domeHalfLevelCount {
		values := [4]float64{}
		for columnIndex := range columns {
			values[columnIndex] = columns[columnIndex].HalfLevelGeometry[level].HeightM
		}
		halfHeights[level] = domeWeighted4(values, weights)
		if level > 0 && halfHeights[level] >= halfHeights[level-1] {
			return forecast.AstrodomeScienceNativeContext{}, errors.New("reconstructed ICON-EU HHL geometry is not top-to-surface ordered")
		}
	}

	surfaceHeights := [4]float64{}
	mixedLayerDepths := [4]float64{}
	for columnIndex, column := range columns {
		surfaceHeights[columnIndex] = column.HSURFHeightM
		left := column.Frames[bracket.leftIndex].Surface
		right := column.Frames[bracket.rightIndex].Surface
		if !left.Available.Has(forecast.AstrodomePrimitiveMixedLayerDepth) ||
			!right.Available.Has(forecast.AstrodomePrimitiveMixedLayerDepth) {
			return forecast.AstrodomeScienceNativeContext{}, errors.New("ICON-EU mixed-layer depth is unavailable at a native bracket")
		}
		mixedLayerDepths[columnIndex] = domeLinear(left.MixedLayerDepthM, right.MixedLayerDepthM, bracket.fraction)
	}
	surfaceHeight := domeWeighted4(surfaceHeights, weights)
	mixedLayerDepth := domeWeighted4(mixedLayerDepths, weights)
	if !finiteDomeVolume(surfaceHeight) || !finiteDomeVolume(mixedLayerDepth) || mixedLayerDepth < 0 {
		return forecast.AstrodomeScienceNativeContext{}, errors.New("reconstructed ICON-EU surface context is invalid")
	}

	// HHL and native full levels are top-to-surface. The WMO lapse-rate
	// diagnostic contract is bottom-to-top, so reverse only the final raw
	// thermal sequence; no value is vertically interpolated here.
	thermal := make([]forecast.AstrodomeScienceThermalPrimitive, 0, domeFullLevelCount)
	for modelLevel := domeFullLevelCount; modelLevel >= 1; modelLevel-- {
		index := modelLevel - 1
		pressures := [4]float64{}
		temperatures := [4]float64{}
		for columnIndex, column := range columns {
			left := column.Frames[bracket.leftIndex].FullLevels[index]
			right := column.Frames[bracket.rightIndex].FullLevels[index]
			if !left.Available.Has(forecast.AstrodomePrimitivePressure) ||
				!right.Available.Has(forecast.AstrodomePrimitivePressure) ||
				!left.Available.Has(forecast.AstrodomePrimitiveTemperature) ||
				!right.Available.Has(forecast.AstrodomePrimitiveTemperature) {
				return forecast.AstrodomeScienceNativeContext{}, fmt.Errorf("ICON-EU native P/T level %d is unavailable", modelLevel)
			}
			pressures[columnIndex] = domeLinear(left.PressurePa, right.PressurePa, bracket.fraction)
			temperatures[columnIndex] = domeLinear(left.TemperatureK, right.TemperatureK, bracket.fraction)
		}
		// The primitive contract mixes each native HHL surface horizontally
		// before reconstructing the local terrain-following full level. The
		// WMO planner uses this exact operation order as well; mathematically
		// equivalent corner midpoints are not substituted near a sign boundary.
		height := (halfHeights[index] + halfHeights[index+1]) / 2
		pressure := domeWeighted4(pressures, weights)
		temperature := domeWeighted4(temperatures, weights)
		if !finiteDomeVolume(height) || !finiteDomeVolume(pressure) || pressure <= 0 ||
			!finiteDomeVolume(temperature) || temperature <= 0 {
			return forecast.AstrodomeScienceNativeContext{}, fmt.Errorf("reconstructed ICON-EU thermal level %d is invalid", modelLevel)
		}
		if len(thermal) > 0 && height <= thermal[len(thermal)-1].HeightM {
			return forecast.AstrodomeScienceNativeContext{}, errors.New("reconstructed ICON-EU thermal profile is not bottom-to-top ordered")
		}
		thermal = append(thermal, forecast.AstrodomeScienceThermalPrimitive{
			HeightM: height, PressurePa: pressure, TemperatureK: temperature,
		})
	}
	cellID, err := volume.HorizontalCellID(expected)
	if err != nil {
		return forecast.AstrodomeScienceNativeContext{}, err
	}
	return forecast.AstrodomeScienceNativeContext{
		HorizontalCellID: cellID,
		SurfaceHeightM:   surfaceHeight,
		MixedLayerDepthM: mixedLayerDepth,
		ThermalProfile:   thermal,
	}, nil
}

// HorizontalCellID returns the stable native regular-grid cell identity used
// by the ray/path partition and cloud-overlap closure.
func (volume *DomeVolume) HorizontalCellID(stencil forecast.AstrodomeHorizontalStencil) (string, error) {
	if volume == nil {
		return "", errors.New("ICON-EU Astrodome volume is required")
	}
	if err := stencil.Validate(); err != nil {
		return "", err
	}
	latitudes := make([]int, 0, 4)
	longitudes := make([]int, 0, 4)
	for _, support := range stencil.Supports {
		address, err := volume.parseDomeColumnID(support.ColumnID)
		if err != nil {
			return "", err
		}
		if math.Abs(address.location.Latitude-support.Location.Latitude) > 1e-12 ||
			math.Abs(address.location.Longitude-support.Location.Longitude) > 1e-12 {
			return "", errors.New("ICON-EU Astrodome support ID does not match its grid location")
		}
		latitudes = append(latitudes, address.latitudeIndex)
		longitudes = append(longitudes, address.longitudeIndex)
	}
	sort.Ints(latitudes)
	sort.Ints(longitudes)
	if latitudes[0] != latitudes[1] || latitudes[2] != latitudes[3] || latitudes[2] != latitudes[0]+1 ||
		longitudes[0] != longitudes[1] || longitudes[2] != longitudes[3] || longitudes[2] != longitudes[0]+1 {
		return "", errors.New("ICON-EU Astrodome supports do not form one regular grid cell")
	}
	return fmt.Sprintf("cell-lat%04d-lon%04d", latitudes[0], longitudes[0]), nil
}

// SurfaceAt reconstructs instantaneous native surface primitives at a whole
// product hour. Values in the f078..f081/f081..f084 gaps are interpolated
// from the primitive fields themselves. Cumulative precipitation is never
// interpolated and remains unavailable away from an exact native timestamp.
func (volume *DomeVolume) SurfaceAt(
	ctx context.Context,
	validAt time.Time,
	location forecast.Location,
) (forecast.AstrodomeSurfacePrimitives, error) {
	if volume == nil {
		return forecast.AstrodomeSurfacePrimitives{}, errors.New("ICON-EU Astrodome volume is required")
	}
	bracket, err := domeVolumeBracket(volume.fieldTimes[forecast.AstrodomePrimitiveMixedLayerDepth], validAt)
	if err != nil {
		return forecast.AstrodomeSurfacePrimitives{}, err
	}
	stencil, err := volume.HorizontalStencil(ctx, location)
	if err != nil {
		return forecast.AstrodomeSurfacePrimitives{}, err
	}
	columns, err := volume.domeStencilColumns(ctx, stencil)
	if err != nil {
		return forecast.AstrodomeSurfacePrimitives{}, err
	}
	weights := domeStencilWeights(stencil)
	leftInput := [4]forecast.AstrodomeSurfacePrimitives{}
	rightInput := [4]forecast.AstrodomeSurfacePrimitives{}
	for columnIndex, column := range columns {
		leftInput[columnIndex] = column.Frames[bracket.leftIndex].Surface
		rightInput[columnIndex] = column.Frames[bracket.rightIndex].Surface
	}
	left := domeCombineNativeSurface(leftInput, weights)
	if bracket.leftIndex == bracket.rightIndex {
		return left, nil
	}
	right := domeCombineNativeSurface(rightInput, weights)
	return domeInterpolateSurface(left, right, bracket.fraction), nil
}

// NativeSurfaceAt is the stricter cumulative-field API. It accepts only an
// exact native timestamp and therefore preserves the published precipitation
// interval without resampling it.
func (volume *DomeVolume) NativeSurfaceAt(
	ctx context.Context,
	validAt time.Time,
	location forecast.Location,
) (forecast.AstrodomeSurfacePrimitives, error) {
	if volume == nil {
		return forecast.AstrodomeSurfacePrimitives{}, errors.New("ICON-EU Astrodome volume is required")
	}
	times := volume.fieldTimes[forecast.AstrodomePrimitiveMixedLayerDepth]
	index := sort.Search(len(times), func(index int) bool { return !times[index].Before(validAt.UTC()) })
	if index >= len(times) || !times[index].Equal(validAt.UTC()) {
		return forecast.AstrodomeSurfacePrimitives{}, fmt.Errorf("%s is not a native ICON-EU Astrodome surface timestamp", validAt.UTC().Format(time.RFC3339))
	}
	return volume.SurfaceAt(ctx, validAt, location)
}

func (volume *DomeVolume) domeStencilColumns(
	ctx context.Context,
	stencil forecast.AstrodomeHorizontalStencil,
) ([4]forecast.AstrodomePrimitiveColumn, error) {
	columns := [4]forecast.AstrodomePrimitiveColumn{}
	for index, support := range stencil.Supports {
		column, err := volume.domeCachedColumnView(ctx, support.ColumnID)
		if err != nil {
			return columns, err
		}
		if math.Abs(column.Location.Latitude-support.Location.Latitude) > 1e-12 ||
			math.Abs(column.Location.Longitude-support.Location.Longitude) > 1e-12 {
			return columns, fmt.Errorf("ICON-EU Astrodome column %q moved from its stencil", support.ColumnID)
		}
		columns[index] = column
	}
	return columns, nil
}

func sameDomeStencil(expected, supplied forecast.AstrodomeHorizontalStencil) error {
	if err := supplied.Validate(); err != nil {
		return err
	}
	for index := range expected.Supports {
		a, b := expected.Supports[index], supplied.Supports[index]
		if a.ColumnID != b.ColumnID || math.Abs(a.Location.Latitude-b.Location.Latitude) > 1e-12 ||
			math.Abs(a.Location.Longitude-b.Location.Longitude) > 1e-12 || math.Abs(a.Weight-b.Weight) > 1e-12 {
			return errors.New("supplied ICON-EU Astrodome stencil does not match the ray point")
		}
	}
	return nil
}

func domeVolumeBracket(times []time.Time, validAt time.Time) (domeVolumeTimeBracket, error) {
	validAt = validAt.UTC()
	if validAt.Nanosecond() != 0 || validAt.Second() != 0 || validAt.Minute() != 0 {
		return domeVolumeTimeBracket{}, errors.New("ICON-EU Astrodome valid time must be a whole UTC hour")
	}
	index := sort.Search(len(times), func(index int) bool { return !times[index].Before(validAt) })
	if index < len(times) && times[index].Equal(validAt) {
		return domeVolumeTimeBracket{leftIndex: index, rightIndex: index}, nil
	}
	if index == 0 || index == len(times) {
		return domeVolumeTimeBracket{}, errors.New("ICON-EU Astrodome valid time lies outside native brackets")
	}
	left, right := times[index-1], times[index]
	gap := right.Sub(left)
	if gap <= 0 || gap > forecast.AstrodomeMaximumPrimitiveTemporalGap {
		return domeVolumeTimeBracket{}, errors.New("ICON-EU Astrodome native temporal bracket is unsupported")
	}
	return domeVolumeTimeBracket{
		leftIndex: index - 1, rightIndex: index,
		fraction: float64(validAt.Sub(left)) / float64(gap),
	}, nil
}

func domeStencilWeights(stencil forecast.AstrodomeHorizontalStencil) [4]float64 {
	return [4]float64{
		stencil.Supports[0].Weight,
		stencil.Supports[1].Weight,
		stencil.Supports[2].Weight,
		stencil.Supports[3].Weight,
	}
}

func domeWeighted4(values, weights [4]float64) float64 {
	// Neumaier summation keeps tiny corner weights from being discarded when
	// native values differ substantially (notably pressure and HHL).
	sum, correction := 0.0, 0.0
	for index, value := range values {
		term := value * weights[index]
		next := sum + term
		if math.Abs(sum) >= math.Abs(term) {
			correction += (sum - next) + term
		} else {
			correction += (term - next) + sum
		}
		sum = next
	}
	return sum + correction
}

func domeLinear(left, right, fraction float64) float64 {
	if fraction == 0 {
		return left
	}
	return left*(1-fraction) + right*fraction
}

func domeCombineNativeSurface(
	input [4]forecast.AstrodomeSurfacePrimitives,
	weights [4]float64,
) forecast.AstrodomeSurfacePrimitives {
	result := forecast.AstrodomeSurfacePrimitives{
		PrecipitationIntervalStart: input[0].PrecipitationIntervalStart,
		PrecipitationIntervalEnd:   input[0].PrecipitationIntervalEnd,
	}
	combine := func(field forecast.AstrodomePrimitiveField, get func(forecast.AstrodomeSurfacePrimitives) float64, set func(float64)) {
		values := [4]float64{}
		for index := range input {
			if !input[index].Available.Has(field) {
				return
			}
			values[index] = get(input[index])
		}
		set(domeWeighted4(values, weights))
		result.Available |= forecast.AstrodomePrimitiveFieldSet(field)
	}
	combine(forecast.AstrodomePrimitiveMixedLayerDepth, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.MixedLayerDepthM }, func(v float64) { result.MixedLayerDepthM = v })
	combine(forecast.AstrodomePrimitiveVisibility, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.VisibilityM }, func(v float64) { result.VisibilityM = v })
	combine(forecast.AstrodomePrimitiveRelativeHumidity, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.RelativeHumidityFraction }, func(v float64) { result.RelativeHumidityFraction = v })
	combine(forecast.AstrodomePrimitiveTemperature2M, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.Temperature2MK }, func(v float64) { result.Temperature2MK = v })
	combine(forecast.AstrodomePrimitiveDewPoint2M, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.DewPoint2MK }, func(v float64) { result.DewPoint2MK = v })
	combine(forecast.AstrodomePrimitiveEastwardWind10M, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.EastwardWind10MMS }, func(v float64) { result.EastwardWind10MMS = v })
	combine(forecast.AstrodomePrimitiveNorthwardWind10M, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.NorthwardWind10MMS }, func(v float64) { result.NorthwardWind10MMS = v })
	combine(forecast.AstrodomePrimitiveWindGust10M, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.WindGust10MMS }, func(v float64) { result.WindGust10MMS = v })
	combine(forecast.AstrodomePrimitivePrecipitationAccumulation, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.PrecipitationAccumulationMM }, func(v float64) { result.PrecipitationAccumulationMM = v })
	combine(forecast.AstrodomePrimitiveTotalColumnWaterVapour, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.TotalColumnWaterVapourKgM2 }, func(v float64) { result.TotalColumnWaterVapourKgM2 = v })
	combine(forecast.AstrodomePrimitiveTotalColumnCloudLiquid, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.TotalColumnCloudLiquidKgM2 }, func(v float64) { result.TotalColumnCloudLiquidKgM2 = v })
	combine(forecast.AstrodomePrimitiveTotalColumnCloudIce, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.TotalColumnCloudIceKgM2 }, func(v float64) { result.TotalColumnCloudIceKgM2 = v })
	combine(forecast.AstrodomePrimitiveSurfacePressure, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.SurfacePressurePa }, func(v float64) { result.SurfacePressurePa = v })
	combine(forecast.AstrodomePrimitiveSpecificHumidity2M, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.SpecificHumidity2MKgKg }, func(v float64) { result.SpecificHumidity2MKgKg = v })
	return result
}

func domeInterpolateSurface(
	left forecast.AstrodomeSurfacePrimitives,
	right forecast.AstrodomeSurfacePrimitives,
	fraction float64,
) forecast.AstrodomeSurfacePrimitives {
	result := forecast.AstrodomeSurfacePrimitives{}
	combine := func(field forecast.AstrodomePrimitiveField, get func(forecast.AstrodomeSurfacePrimitives) float64, set func(float64)) {
		if !left.Available.Has(field) || !right.Available.Has(field) {
			return
		}
		set(domeLinear(get(left), get(right), fraction))
		result.Available |= forecast.AstrodomePrimitiveFieldSet(field)
	}
	combine(forecast.AstrodomePrimitiveMixedLayerDepth, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.MixedLayerDepthM }, func(v float64) { result.MixedLayerDepthM = v })
	combine(forecast.AstrodomePrimitiveVisibility, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.VisibilityM }, func(v float64) { result.VisibilityM = v })
	combine(forecast.AstrodomePrimitiveRelativeHumidity, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.RelativeHumidityFraction }, func(v float64) { result.RelativeHumidityFraction = v })
	combine(forecast.AstrodomePrimitiveTemperature2M, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.Temperature2MK }, func(v float64) { result.Temperature2MK = v })
	combine(forecast.AstrodomePrimitiveDewPoint2M, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.DewPoint2MK }, func(v float64) { result.DewPoint2MK = v })
	combine(forecast.AstrodomePrimitiveEastwardWind10M, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.EastwardWind10MMS }, func(v float64) { result.EastwardWind10MMS = v })
	combine(forecast.AstrodomePrimitiveNorthwardWind10M, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.NorthwardWind10MMS }, func(v float64) { result.NorthwardWind10MMS = v })
	combine(forecast.AstrodomePrimitiveWindGust10M, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.WindGust10MMS }, func(v float64) { result.WindGust10MMS = v })
	combine(forecast.AstrodomePrimitiveTotalColumnWaterVapour, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.TotalColumnWaterVapourKgM2 }, func(v float64) { result.TotalColumnWaterVapourKgM2 = v })
	combine(forecast.AstrodomePrimitiveTotalColumnCloudLiquid, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.TotalColumnCloudLiquidKgM2 }, func(v float64) { result.TotalColumnCloudLiquidKgM2 = v })
	combine(forecast.AstrodomePrimitiveTotalColumnCloudIce, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.TotalColumnCloudIceKgM2 }, func(v float64) { result.TotalColumnCloudIceKgM2 = v })
	combine(forecast.AstrodomePrimitiveSurfacePressure, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.SurfacePressurePa }, func(v float64) { result.SurfacePressurePa = v })
	combine(forecast.AstrodomePrimitiveSpecificHumidity2M, func(v forecast.AstrodomeSurfacePrimitives) float64 { return v.SpecificHumidity2MKgKg }, func(v float64) { result.SpecificHumidity2MKgKg = v })
	return result
}
