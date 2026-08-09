package forecast

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// astrodomePreparedRefractiveFrame is an immutable, one-valid-time view of
// the six native primitives used by Ciddor refraction. It caches only temporal
// interpolation of the raw model variables P/T/QV and PS/T2M/QV_2M. Spatial
// and moving-HHL interpolation, analytic gradients, Ciddor refractivity, and
// every later science quantity are still recomputed at every solver point.
type astrodomePreparedRefractiveFrame struct {
	reconstructor   *AstrodomePrimitiveReconstructor
	validAt         time.Time
	brackets        astrodomeTemporalBracketSet
	surfaceBrackets astrodomeRefractionSurfaceBracketSet

	columns sync.Map // map[string]*astrodomePreparedRefractiveColumnEntry
	cells   sync.Map // map[[4]string]*astrodomePreparedRefractiveCellEntry
}

type astrodomePreparedRefractiveColumnEntry struct {
	once   sync.Once
	column AstrodomePrimitiveColumn
	err    error
}

type astrodomePreparedRefractiveCellEntry struct {
	once sync.Once
	cell astrodomePreparedRefractiveCell
	err  error
}

type astrodomePreparedRefractiveCell struct {
	columns              [4]AstrodomePrimitiveColumn
	supportLocations     [4]Location
	lowerPartitionID     string
	upperPartitionID     string
	fullLevelPartitionID [astrodomeMaximumTerrainFollowingHalfLevels - 1]string
}

type astrodomePreparedRefractivePoint struct {
	state     AstrodomeReconstructedAtmosphere
	gradients AstrodomeReconstructedSpatialGradients
	domain    AstrodomeRefractionDomainPoint
}

func newAstrodomePreparedRefractiveFrame(
	reconstructor *AstrodomePrimitiveReconstructor,
	validAt time.Time,
) (*astrodomePreparedRefractiveFrame, error) {
	if reconstructor == nil || reconstructor.volume == nil {
		return nil, fmt.Errorf("astrodome primitive reconstructor is required")
	}
	if err := validateAstrodomeUTCWholeHour(validAt); err != nil {
		return nil, fmt.Errorf("astrodome refraction valid time: %w", err)
	}
	if err := reconstructor.ensureIdentity(); err != nil {
		return nil, err
	}
	prepared := &astrodomePreparedRefractiveFrame{
		reconstructor: reconstructor,
		validAt:       validAt.UTC(),
	}
	for _, field := range [...]AstrodomePrimitiveField{
		AstrodomePrimitivePressure,
		AstrodomePrimitiveTemperature,
		AstrodomePrimitiveSpecificHumidity,
	} {
		bracket, err := astrodomePrimitiveBracket(reconstructor.fieldTimes[field], prepared.validAt)
		if err != nil {
			return nil, fmt.Errorf("astrodome refractive primitive field %#x: %w", uint64(field), err)
		}
		prepared.brackets[astrodomeAtmosphericPrimitiveIndex(field)] = bracket
	}
	for _, field := range astrodomeRequiredRefractionSurfacePrimitives() {
		bracket, err := astrodomePrimitiveBracket(reconstructor.fieldTimes[field], prepared.validAt)
		if err != nil {
			return nil, fmt.Errorf("astrodome refractive surface field %#x: %w", uint64(field), err)
		}
		prepared.surfaceBrackets[astrodomeRefractionSurfacePrimitiveIndex(field)] = bracket
	}
	return prepared, nil
}

func (prepared *astrodomePreparedRefractiveFrame) reconstruct(
	ctx context.Context,
	query AstrodomeReconstructionQuery,
) (astrodomePreparedRefractivePoint, error) {
	result := astrodomePreparedRefractivePoint{}
	if prepared == nil || prepared.reconstructor == nil {
		return result, fmt.Errorf("prepared astrodome refractive frame is required")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !query.ValidAt.Equal(prepared.validAt) {
		return result, fmt.Errorf("prepared astrodome refractive frame cannot serve a different valid time")
	}
	if err := prepared.reconstructor.ensureIdentity(); err != nil {
		return result, err
	}
	if err := ValidateCoordinates(query.Location.Latitude, query.Location.Longitude); err != nil {
		return result, fmt.Errorf("astrodome refractive location: %w", err)
	}
	if !finite(query.HeightM) || query.HeightM <= -AstrodomeICONSphereRadiusM {
		return result, fmt.Errorf("astrodome refractive height is invalid")
	}

	stencil, err := prepared.reconstructor.volume.HorizontalStencil(ctx, query.Location)
	if err != nil {
		return result, fmt.Errorf("astrodome refractive horizontal stencil: %w", err)
	}
	if err := stencil.Validate(); err != nil {
		return result, err
	}
	bilinear, err := astrodomeResolveBilinearGeometry(query.Location, stencil)
	if err != nil {
		return result, err
	}
	cell, err := prepared.cell(ctx, stencil)
	if err != nil {
		return result, err
	}
	weights := [4]float64{}
	for index, support := range stencil.Supports {
		if !astrodomeSameGridLocation(cell.supportLocations[index], support.Location) {
			return result, fmt.Errorf("astrodome refractive support %q changed location", support.ColumnID)
		}
		weights[index] = support.Weight
	}

	geometry := astrodomeTerrainFollowingGeometry{}
	if err := astrodomeBuildTerrainFollowingGeometry(cell.columns, weights, &bilinear, &geometry); err != nil {
		return result, err
	}
	reconstructed, err := reconstructAstrodomeTerrainFollowingWithGeometry(
		cell.columns, weights, &bilinear, &geometry, query.HeightM,
		prepared.exactBrackets(), prepared.exactSurfaceBrackets(), true,
	)
	if err != nil {
		return result, err
	}
	partitionID, err := cell.partitionID(&geometry, query.HeightM)
	if err != nil {
		return result, err
	}

	available := AstrodomePrimitiveFieldSet(AstrodomePrimitivePressure) |
		AstrodomePrimitiveFieldSet(AstrodomePrimitiveTemperature) |
		AstrodomePrimitiveFieldSet(AstrodomePrimitiveSpecificHumidity)
	result.state = AstrodomeReconstructedAtmosphere{
		SourceIdentity:       prepared.reconstructor.identity,
		ValidAt:              prepared.validAt,
		Location:             query.Location,
		HeightM:              query.HeightM,
		HorizontalStencil:    stencil,
		Available:            available,
		PressurePa:           reconstructed.pressure.value,
		TemperatureK:         reconstructed.temperature.value,
		SpecificHumidityKgKg: reconstructed.specificHumidity.value,
		VerticalDerivatives: AstrodomeReconstructedVerticalDerivatives{
			Available:                available,
			PressurePaPerM:           reconstructed.pressure.derivative,
			TemperatureKPerM:         reconstructed.temperature.derivative,
			SpecificHumidityKgKgPerM: reconstructed.specificHumidity.derivative,
		},
	}
	result.gradients = AstrodomeReconstructedSpatialGradients{
		Available:            available,
		PressurePaPerM:       astrodomeTerrainFollowingECEFGradient(result.state, reconstructed.pressure),
		TemperatureKPerM:     astrodomeTerrainFollowingECEFGradient(result.state, reconstructed.temperature),
		SpecificHumidityPerM: astrodomeTerrainFollowingECEFGradient(result.state, reconstructed.specificHumidity),
	}
	result.domain = AstrodomeRefractionDomainPoint{
		PartitionID:     partitionID,
		SurfaceHeightM:  geometry.surface.value,
		ModelTopHeightM: geometry.halfLevels[0].value,
	}
	if strings.TrimSpace(result.domain.PartitionID) == "" || !finite(result.domain.SurfaceHeightM) ||
		!finite(result.domain.ModelTopHeightM) || result.domain.ModelTopHeightM <= result.domain.SurfaceHeightM {
		return astrodomePreparedRefractivePoint{}, fmt.Errorf("prepared astrodome refraction domain point is invalid")
	}
	return result, nil
}

// exactBrackets address the single prepared frame stored in every synthetic
// raw-primitive column. Keeping the existing moving-HHL reconstruction code
// preserves its arithmetic order and derivative implementation.
func (prepared *astrodomePreparedRefractiveFrame) exactBrackets() astrodomeTemporalBracketSet {
	brackets := astrodomeTemporalBracketSet{}
	for _, field := range [...]AstrodomePrimitiveField{
		AstrodomePrimitivePressure,
		AstrodomePrimitiveTemperature,
		AstrodomePrimitiveSpecificHumidity,
	} {
		brackets[astrodomeAtmosphericPrimitiveIndex(field)] = astrodomeTemporalBracket{
			left: prepared.validAt, right: prepared.validAt,
		}
	}
	return brackets
}

func (prepared *astrodomePreparedRefractiveFrame) exactSurfaceBrackets() astrodomeRefractionSurfaceBracketSet {
	brackets := astrodomeRefractionSurfaceBracketSet{}
	for index := range brackets {
		brackets[index] = astrodomeTemporalBracket{left: prepared.validAt, right: prepared.validAt}
	}
	return brackets
}

func (prepared *astrodomePreparedRefractiveFrame) cell(
	ctx context.Context,
	stencil AstrodomeHorizontalStencil,
) (*astrodomePreparedRefractiveCell, error) {
	key := [4]string{}
	for index, support := range stencil.Supports {
		key[index] = support.ColumnID
	}
	loaded, ok := prepared.cells.Load(key)
	if !ok {
		candidate := &astrodomePreparedRefractiveCellEntry{}
		loaded, _ = prepared.cells.LoadOrStore(key, candidate)
	}
	entry := loaded.(*astrodomePreparedRefractiveCellEntry)
	entry.once.Do(func() {
		entry.cell, entry.err = prepared.prepareCell(ctx, stencil)
	})
	if entry.err != nil {
		return nil, entry.err
	}
	return &entry.cell, nil
}

func (prepared *astrodomePreparedRefractiveFrame) prepareCell(
	ctx context.Context,
	stencil AstrodomeHorizontalStencil,
) (astrodomePreparedRefractiveCell, error) {
	cell := astrodomePreparedRefractiveCell{}
	for index, support := range stencil.Supports {
		column, err := prepared.column(ctx, support.ColumnID)
		if err != nil {
			return cell, err
		}
		if !astrodomeSameGridLocation(column.Location, support.Location) {
			return cell, fmt.Errorf("astrodome refractive column %q changed location", support.ColumnID)
		}
		cell.columns[index] = column
		cell.supportLocations[index] = support.Location
	}
	prefix := astrodomeRefractionCellPartitionPrefix(stencil)
	cell.lowerPartitionID = prefix + "|lower-boundary"
	cell.upperPartitionID = prefix + "|upper-hydrostatic"
	for lower := 1; lower < len(cell.fullLevelPartitionID); lower++ {
		cell.fullLevelPartitionID[lower] = fmt.Sprintf("%s|full-%03d-%03d", prefix, lower, lower-1)
	}
	return cell, nil
}

func (prepared *astrodomePreparedRefractiveFrame) column(
	ctx context.Context,
	columnID string,
) (AstrodomePrimitiveColumn, error) {
	loaded, ok := prepared.columns.Load(columnID)
	if !ok {
		candidate := &astrodomePreparedRefractiveColumnEntry{}
		loaded, _ = prepared.columns.LoadOrStore(columnID, candidate)
	}
	entry := loaded.(*astrodomePreparedRefractiveColumnEntry)
	entry.once.Do(func() {
		entry.column, entry.err = prepared.prepareColumn(ctx, columnID)
	})
	if entry.err != nil {
		return AstrodomePrimitiveColumn{}, entry.err
	}
	return entry.column, nil
}

func (prepared *astrodomePreparedRefractiveFrame) prepareColumn(
	ctx context.Context,
	columnID string,
) (AstrodomePrimitiveColumn, error) {
	source, err := prepared.reconstructor.column(ctx, columnID)
	if err != nil {
		return AstrodomePrimitiveColumn{}, err
	}
	if err := prepared.reconstructor.ensureIdentity(); err != nil {
		return AstrodomePrimitiveColumn{}, err
	}
	if len(source.HalfLevelGeometry) < 3 {
		return AstrodomePrimitiveColumn{}, fmt.Errorf("astrodome refractive column %q has incomplete HHL geometry", columnID)
	}
	fullLevelCount := len(source.HalfLevelGeometry) - 1
	frame := AstrodomePrimitiveColumnFrame{
		ValidAt:    prepared.validAt,
		FullLevels: make([]AstrodomeFullLevelPrimitives, fullLevelCount),
	}
	for _, field := range [...]AstrodomePrimitiveField{
		AstrodomePrimitivePressure,
		AstrodomePrimitiveTemperature,
		AstrodomePrimitiveSpecificHumidity,
	} {
		bracket := prepared.brackets[astrodomeAtmosphericPrimitiveIndex(field)]
		left, right, frameErr := astrodomePrimitiveFrames(source.Frames, bracket)
		if frameErr != nil {
			return AstrodomePrimitiveColumn{}, fmt.Errorf("prepare column %q field %#x: %w", columnID, uint64(field), frameErr)
		}
		if len(left.FullLevels) != fullLevelCount || len(right.FullLevels) != fullLevelCount {
			return AstrodomePrimitiveColumn{}, fmt.Errorf("prepare column %q field %#x has inconsistent full levels", columnID, uint64(field))
		}
		for level := range fullLevelCount {
			leftValue, valueErr := astrodomeFullLevelValue(left.FullLevels[level], field)
			if valueErr != nil {
				return AstrodomePrimitiveColumn{}, fmt.Errorf("prepare column %q field %#x level %d: %w", columnID, uint64(field), level, valueErr)
			}
			rightValue := leftValue
			if !bracket.right.Equal(bracket.left) {
				rightValue, valueErr = astrodomeFullLevelValue(right.FullLevels[level], field)
				if valueErr != nil {
					return AstrodomePrimitiveColumn{}, fmt.Errorf("prepare column %q field %#x level %d: %w", columnID, uint64(field), level, valueErr)
				}
			}
			value := astrodomeTemporalPrimitive(leftValue, rightValue, bracket.fraction)
			preparedLevel := &frame.FullLevels[level]
			preparedLevel.ModelLevel = left.FullLevels[level].ModelLevel
			preparedLevel.Available |= AstrodomePrimitiveFieldSet(field)
			switch field {
			case AstrodomePrimitivePressure:
				preparedLevel.PressurePa = value
			case AstrodomePrimitiveTemperature:
				preparedLevel.TemperatureK = value
			case AstrodomePrimitiveSpecificHumidity:
				preparedLevel.SpecificHumidityKgKg = value
			}
		}
	}
	for _, field := range astrodomeRequiredRefractionSurfacePrimitives() {
		bracket := prepared.surfaceBrackets[astrodomeRefractionSurfacePrimitiveIndex(field)]
		value, valueErr := astrodomeSurfacePrimitiveAt(source.Frames, field, bracket)
		if valueErr != nil {
			return AstrodomePrimitiveColumn{}, fmt.Errorf("prepare column %q surface field %#x: %w", columnID, uint64(field), valueErr)
		}
		frame.Surface.Available |= AstrodomePrimitiveFieldSet(field)
		switch field {
		case AstrodomePrimitiveSurfacePressure:
			frame.Surface.SurfacePressurePa = value
		case AstrodomePrimitiveTemperature2M:
			frame.Surface.Temperature2MK = value
		case AstrodomePrimitiveSpecificHumidity2M:
			frame.Surface.SpecificHumidity2MKgKg = value
		}
	}
	return AstrodomePrimitiveColumn{
		ColumnID:          columnID,
		Location:          source.Location,
		HSURFHeightM:      source.HSURFHeightM,
		HalfLevelGeometry: source.HalfLevelGeometry,
		Frames:            []AstrodomePrimitiveColumnFrame{frame},
	}, nil
}

func (cell *astrodomePreparedRefractiveCell) partitionID(
	geometry *astrodomeTerrainFollowingGeometry,
	heightM float64,
) (string, error) {
	if cell == nil || geometry == nil || geometry.fullLevelCount < 2 ||
		!finite(heightM) || !finite(geometry.surface.value) {
		return "", fmt.Errorf("invalid prepared astrodome refraction partition input")
	}
	if heightM < geometry.surface.value-AstrodomeRefractionPrimitiveEventGuardM ||
		heightM > geometry.halfLevels[0].value+AstrodomeRefractionPrimitiveEventGuardM {
		return "", fmt.Errorf("height %.3f m lies outside the prepared refraction domain/guard", heightM)
	}
	bottomFull := geometry.fullLevels[geometry.fullLevelCount-1].value
	if heightM < bottomFull {
		return cell.lowerPartitionID, nil
	}
	if heightM > geometry.fullLevels[0].value {
		return cell.upperPartitionID, nil
	}
	lower, _, err := astrodomeTerrainFollowingBracket(geometry.fullLevelSlice(), heightM)
	if err != nil {
		return "", err
	}
	if lower < 1 || lower >= len(cell.fullLevelPartitionID) || cell.fullLevelPartitionID[lower] == "" {
		return "", fmt.Errorf("prepared astrodome refraction vertical partition is invalid")
	}
	return cell.fullLevelPartitionID[lower], nil
}

func astrodomeRefractionCellPartitionPrefix(stencil AstrodomeHorizontalStencil) string {
	var builder strings.Builder
	builder.WriteString("supports")
	for _, support := range stencil.Supports {
		_, _ = fmt.Fprintf(&builder, "|%d:%s", len(support.ColumnID), support.ColumnID)
	}
	return builder.String()
}
