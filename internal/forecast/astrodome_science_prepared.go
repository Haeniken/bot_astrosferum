package forecast

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// AstrodomePreparedScienceFrame is an immutable one-valid-time view of all
// native primitives consumed by the science integrand. It performs the
// already-contracted temporal interpolation once per source column. Spatial
// and moving-HHL reconstruction is still evaluated at every quadrature point;
// no seeing, tau0, transmission, water column, or score is cached here.
type AstrodomePreparedScienceFrame struct {
	reconstructor   *AstrodomePrimitiveReconstructor
	validAt         time.Time
	brackets        astrodomeTemporalBracketSet
	surfaceBrackets astrodomeRefractionSurfaceBracketSet

	columns        sync.Map // map[string]*astrodomePreparedScienceColumnEntry
	cells          sync.Map // map[[4]string]*astrodomePreparedScienceCellEntry
	cloudEnvelopes sync.Map // map[astrodomePreparedCloudEnvelopeKey]*astrodomePreparedCloudEnvelopeEntry
}

type astrodomePreparedScienceColumnEntry struct {
	once   sync.Once
	column AstrodomePrimitiveColumn
	err    error
}

type astrodomePreparedScienceCellEntry struct {
	once sync.Once
	cell astrodomePreparedScienceCell
	err  error
}

type astrodomePreparedScienceCell struct {
	columns          [4]AstrodomePrimitiveColumn
	supportLocations [4]Location
}

type astrodomePreparedCloudEnvelopeKey struct {
	supportIDs      [4]string
	verticalSupport astrodomeCloudFractionVerticalSupport
}

type astrodomePreparedCloudEnvelopeEntry struct {
	once     sync.Once
	envelope astrodomeScienceCloudFractionEnvelope
	err      error
}

func NewAstrodomePreparedScienceFrame(
	reconstructor *AstrodomePrimitiveReconstructor,
	validAt time.Time,
) (*AstrodomePreparedScienceFrame, error) {
	if reconstructor == nil || reconstructor.volume == nil {
		return nil, fmt.Errorf("astrodome primitive reconstructor is required")
	}
	if err := validateAstrodomeUTCWholeHour(validAt); err != nil {
		return nil, fmt.Errorf("prepared astrodome science valid time: %w", err)
	}
	if err := reconstructor.ensureIdentity(); err != nil {
		return nil, err
	}
	prepared := &AstrodomePreparedScienceFrame{
		reconstructor: reconstructor,
		validAt:       validAt.UTC(),
	}
	for _, field := range astrodomeRequiredAtmosphericPrimitives() {
		bracket, err := astrodomePrimitiveBracket(reconstructor.fieldTimes[field], prepared.validAt)
		if err != nil {
			return nil, fmt.Errorf("prepare astrodome science field %#x: %w", uint64(field), err)
		}
		prepared.brackets[astrodomeAtmosphericPrimitiveIndex(field)] = bracket
	}
	for _, field := range astrodomeRequiredRefractionSurfacePrimitives() {
		bracket, err := astrodomePrimitiveBracket(reconstructor.fieldTimes[field], prepared.validAt)
		if err != nil {
			return nil, fmt.Errorf("prepare astrodome science surface field %#x: %w", uint64(field), err)
		}
		prepared.surfaceBrackets[astrodomeRefractionSurfacePrimitiveIndex(field)] = bracket
	}
	return prepared, nil
}

func (prepared *AstrodomePreparedScienceFrame) Reconstruct(
	ctx context.Context,
	query AstrodomeReconstructionQuery,
) (AstrodomeReconstructedAtmosphere, error) {
	if prepared == nil || prepared.reconstructor == nil {
		return AstrodomeReconstructedAtmosphere{}, fmt.Errorf("prepared astrodome science frame is required")
	}
	if err := ctx.Err(); err != nil {
		return AstrodomeReconstructedAtmosphere{}, err
	}
	if !query.ValidAt.Equal(prepared.validAt) {
		return AstrodomeReconstructedAtmosphere{}, fmt.Errorf("prepared astrodome science frame cannot serve a different valid time")
	}
	if err := prepared.reconstructor.ensureIdentity(); err != nil {
		return AstrodomeReconstructedAtmosphere{}, err
	}
	if err := ValidateCoordinates(query.Location.Latitude, query.Location.Longitude); err != nil {
		return AstrodomeReconstructedAtmosphere{}, fmt.Errorf("prepared astrodome science location: %w", err)
	}
	if !finite(query.HeightM) || query.HeightM <= -AstrodomeICONSphereRadiusM {
		return AstrodomeReconstructedAtmosphere{}, fmt.Errorf("prepared astrodome science height is invalid")
	}

	stencil, err := prepared.reconstructor.volume.HorizontalStencil(ctx, query.Location)
	if err != nil {
		return AstrodomeReconstructedAtmosphere{}, fmt.Errorf("prepared astrodome science stencil: %w", err)
	}
	if err := stencil.Validate(); err != nil {
		return AstrodomeReconstructedAtmosphere{}, err
	}
	cell, err := prepared.cell(ctx, stencil)
	if err != nil {
		return AstrodomeReconstructedAtmosphere{}, err
	}
	weights := [4]float64{}
	for index, support := range stencil.Supports {
		if !astrodomeSameGridLocation(cell.supportLocations[index], support.Location) {
			return AstrodomeReconstructedAtmosphere{}, fmt.Errorf("prepared astrodome science support %q changed location", support.ColumnID)
		}
		weights[index] = support.Weight
	}
	reconstructed, err := reconstructAstrodomeTerrainFollowing(
		cell.columns, weights, nil, query.HeightM,
		prepared.exactBrackets(), prepared.exactSurfaceBrackets(), false,
	)
	if err != nil {
		return AstrodomeReconstructedAtmosphere{}, err
	}

	available := AstrodomePrimitiveFieldSet(0)
	for _, field := range astrodomeRequiredAtmosphericPrimitives() {
		available |= AstrodomePrimitiveFieldSet(field)
	}
	result := AstrodomeReconstructedAtmosphere{
		SourceIdentity:       prepared.reconstructor.identity,
		ValidAt:              prepared.validAt,
		Location:             query.Location,
		HeightM:              query.HeightM,
		HorizontalStencil:    stencil,
		Available:            available,
		PressurePa:           reconstructed.pressure.value,
		TemperatureK:         reconstructed.temperature.value,
		SpecificHumidityKgKg: reconstructed.specificHumidity.value,
		CloudLiquidKgKg:      reconstructed.cloudLiquid.value,
		CloudIceKgKg:         reconstructed.cloudIce.value,
		CloudFraction:        reconstructed.cloudFraction.value,
		TKEJkg:               reconstructed.tke.value,
		WindECEF:             reconstructed.windECEF,
		cloudFractionSupport: reconstructed.cloudFraction.cloudSupport,
		VerticalDerivatives: AstrodomeReconstructedVerticalDerivatives{
			Available:                available,
			PressurePaPerM:           reconstructed.pressure.derivative,
			TemperatureKPerM:         reconstructed.temperature.derivative,
			SpecificHumidityKgKgPerM: reconstructed.specificHumidity.derivative,
			CloudLiquidKgKgPerM:      reconstructed.cloudLiquid.derivative,
			CloudIceKgKgPerM:         reconstructed.cloudIce.derivative,
			CloudFractionPerM:        reconstructed.cloudFraction.derivative,
			TKEJkgPerM:               reconstructed.tke.derivative,
			WindECEFPerM:             reconstructed.windDerivative,
		},
	}
	if err := validateAstrodomeReconstructedAtmosphere(result, false); err != nil {
		return AstrodomeReconstructedAtmosphere{}, err
	}
	if err := prepared.reconstructor.ensureIdentity(); err != nil {
		return AstrodomeReconstructedAtmosphere{}, err
	}
	return result, nil
}

func (prepared *AstrodomePreparedScienceFrame) exactBrackets() astrodomeTemporalBracketSet {
	result := astrodomeTemporalBracketSet{}
	for index := range result {
		result[index] = astrodomeTemporalBracket{left: prepared.validAt, right: prepared.validAt}
	}
	return result
}

func (prepared *AstrodomePreparedScienceFrame) exactSurfaceBrackets() astrodomeRefractionSurfaceBracketSet {
	result := astrodomeRefractionSurfaceBracketSet{}
	for index := range result {
		result[index] = astrodomeTemporalBracket{left: prepared.validAt, right: prepared.validAt}
	}
	return result
}

func (prepared *AstrodomePreparedScienceFrame) cell(
	ctx context.Context,
	stencil AstrodomeHorizontalStencil,
) (*astrodomePreparedScienceCell, error) {
	key := astrodomePreparedSupportKey(stencil)
	loaded, ok := prepared.cells.Load(key)
	if !ok {
		candidate := &astrodomePreparedScienceCellEntry{}
		loaded, _ = prepared.cells.LoadOrStore(key, candidate)
	}
	entry := loaded.(*astrodomePreparedScienceCellEntry)
	entry.once.Do(func() {
		for index, support := range stencil.Supports {
			entry.cell.columns[index], entry.err = prepared.column(ctx, support.ColumnID)
			if entry.err != nil {
				return
			}
			if !astrodomeSameGridLocation(entry.cell.columns[index].Location, support.Location) {
				entry.err = fmt.Errorf("prepared astrodome science column %q changed location", support.ColumnID)
				return
			}
			entry.cell.supportLocations[index] = support.Location
		}
	})
	if entry.err != nil {
		return nil, entry.err
	}
	return &entry.cell, nil
}

func (prepared *AstrodomePreparedScienceFrame) column(
	ctx context.Context,
	columnID string,
) (AstrodomePrimitiveColumn, error) {
	loaded, ok := prepared.columns.Load(columnID)
	if !ok {
		candidate := &astrodomePreparedScienceColumnEntry{}
		loaded, _ = prepared.columns.LoadOrStore(columnID, candidate)
	}
	entry := loaded.(*astrodomePreparedScienceColumnEntry)
	entry.once.Do(func() {
		entry.column, entry.err = prepared.prepareColumn(ctx, columnID)
	})
	return entry.column, entry.err
}

func (prepared *AstrodomePreparedScienceFrame) prepareColumn(
	ctx context.Context,
	columnID string,
) (AstrodomePrimitiveColumn, error) {
	source, err := prepared.reconstructor.column(ctx, columnID)
	if err != nil {
		return AstrodomePrimitiveColumn{}, err
	}
	if len(source.HalfLevelGeometry) < 3 || len(source.Frames) == 0 {
		return AstrodomePrimitiveColumn{}, fmt.Errorf("prepared astrodome science column %q has incomplete geometry or frames", columnID)
	}
	fullLevelCount := len(source.HalfLevelGeometry) - 1
	halfLevelCount := len(source.HalfLevelGeometry)
	frame := AstrodomePrimitiveColumnFrame{
		ValidAt:    prepared.validAt,
		FullLevels: make([]AstrodomeFullLevelPrimitives, fullLevelCount),
		HalfLevels: make([]AstrodomeHalfLevelPrimitives, halfLevelCount),
	}
	for _, field := range astrodomeRequiredAtmosphericPrimitives() {
		bracket := prepared.brackets[astrodomeAtmosphericPrimitiveIndex(field)]
		left, right, frameErr := astrodomePrimitiveFrames(source.Frames, bracket)
		if frameErr != nil {
			return AstrodomePrimitiveColumn{}, fmt.Errorf("prepare column %q field %#x: %w", columnID, uint64(field), frameErr)
		}
		if astrodomeFullLevelField(field) {
			if len(left.FullLevels) != fullLevelCount || len(right.FullLevels) != fullLevelCount {
				return AstrodomePrimitiveColumn{}, fmt.Errorf("prepare column %q field %#x has inconsistent full levels", columnID, uint64(field))
			}
			for level := range fullLevelCount {
				leftValue, valueErr := astrodomeFullLevelValue(left.FullLevels[level], field)
				if valueErr != nil {
					return AstrodomePrimitiveColumn{}, valueErr
				}
				rightValue := leftValue
				if !bracket.right.Equal(bracket.left) {
					rightValue, valueErr = astrodomeFullLevelValue(right.FullLevels[level], field)
					if valueErr != nil {
						return AstrodomePrimitiveColumn{}, valueErr
					}
				}
				preparedLevel := &frame.FullLevels[level]
				preparedLevel.ModelLevel = left.FullLevels[level].ModelLevel
				preparedLevel.Available |= AstrodomePrimitiveFieldSet(field)
				setAstrodomePreparedFullLevel(preparedLevel, field,
					astrodomeTemporalPrimitive(leftValue, rightValue, bracket.fraction))
			}
			continue
		}
		if len(left.HalfLevels) != halfLevelCount || len(right.HalfLevels) != halfLevelCount {
			return AstrodomePrimitiveColumn{}, fmt.Errorf("prepare column %q field %#x has inconsistent half levels", columnID, uint64(field))
		}
		for level := range halfLevelCount {
			leftValue, valueErr := astrodomeHalfLevelValue(left.HalfLevels[level], field)
			if valueErr != nil {
				return AstrodomePrimitiveColumn{}, valueErr
			}
			rightValue := leftValue
			if !bracket.right.Equal(bracket.left) {
				rightValue, valueErr = astrodomeHalfLevelValue(right.HalfLevels[level], field)
				if valueErr != nil {
					return AstrodomePrimitiveColumn{}, valueErr
				}
			}
			preparedLevel := &frame.HalfLevels[level]
			preparedLevel.ModelHalfLevel = left.HalfLevels[level].ModelHalfLevel
			preparedLevel.Available |= AstrodomePrimitiveFieldSet(field)
			setAstrodomePreparedHalfLevel(preparedLevel, field,
				astrodomeTemporalPrimitive(leftValue, rightValue, bracket.fraction))
		}
	}
	for _, field := range astrodomeRequiredRefractionSurfacePrimitives() {
		bracket := prepared.surfaceBrackets[astrodomeRefractionSurfacePrimitiveIndex(field)]
		value, valueErr := astrodomeSurfacePrimitiveAt(source.Frames, field, bracket)
		if valueErr != nil {
			return AstrodomePrimitiveColumn{}, valueErr
		}
		frame.Surface.Available |= AstrodomePrimitiveFieldSet(field)
		setAstrodomePreparedSurface(&frame.Surface, field, value)
	}
	return AstrodomePrimitiveColumn{
		ColumnID:          source.ColumnID,
		Location:          source.Location,
		HSURFHeightM:      source.HSURFHeightM,
		HalfLevelGeometry: source.HalfLevelGeometry,
		Frames:            []AstrodomePrimitiveColumnFrame{frame},
	}, nil
}

func (prepared *AstrodomePreparedScienceFrame) certifiedCloudFractionUpperEnvelope(
	ctx context.Context,
	stencil AstrodomeHorizontalStencil,
	support astrodomeCloudFractionVerticalSupport,
) (astrodomeScienceCloudFractionEnvelope, error) {
	key := astrodomePreparedCloudEnvelopeKey{
		supportIDs:      astrodomePreparedSupportKey(stencil),
		verticalSupport: support,
	}
	loaded, ok := prepared.cloudEnvelopes.Load(key)
	if !ok {
		candidate := &astrodomePreparedCloudEnvelopeEntry{}
		loaded, _ = prepared.cloudEnvelopes.LoadOrStore(key, candidate)
	}
	entry := loaded.(*astrodomePreparedCloudEnvelopeEntry)
	entry.once.Do(func() {
		entry.envelope, entry.err = prepared.reconstructor.certifiedCloudFractionUpperEnvelope(
			ctx, prepared.validAt, stencil, support,
		)
	})
	return entry.envelope, entry.err
}

func astrodomePreparedSupportKey(stencil AstrodomeHorizontalStencil) [4]string {
	key := [4]string{}
	for index, support := range stencil.Supports {
		key[index] = support.ColumnID
	}
	return key
}

func setAstrodomePreparedFullLevel(level *AstrodomeFullLevelPrimitives, field AstrodomePrimitiveField, value float64) {
	switch field {
	case AstrodomePrimitivePressure:
		level.PressurePa = value
	case AstrodomePrimitiveTemperature:
		level.TemperatureK = value
	case AstrodomePrimitiveSpecificHumidity:
		level.SpecificHumidityKgKg = value
	case AstrodomePrimitiveCloudLiquid:
		level.CloudLiquidKgKg = value
	case AstrodomePrimitiveCloudIce:
		level.CloudIceKgKg = value
	case AstrodomePrimitiveCloudFraction:
		level.CloudFraction = value
	case AstrodomePrimitiveEastwardWind:
		level.EastwardWindMS = value
	case AstrodomePrimitiveNorthwardWind:
		level.NorthwardWindMS = value
	default:
		panic("unsupported prepared full-level field")
	}
}

func setAstrodomePreparedHalfLevel(level *AstrodomeHalfLevelPrimitives, field AstrodomePrimitiveField, value float64) {
	switch field {
	case AstrodomePrimitiveVerticalWind:
		level.VerticalWindMS = value
	case AstrodomePrimitiveTKE:
		level.TKEJkg = value
	default:
		panic("unsupported prepared half-level field")
	}
}

func setAstrodomePreparedSurface(surface *AstrodomeSurfacePrimitives, field AstrodomePrimitiveField, value float64) {
	switch field {
	case AstrodomePrimitiveSurfacePressure:
		surface.SurfacePressurePa = value
	case AstrodomePrimitiveTemperature2M:
		surface.Temperature2MK = value
	case AstrodomePrimitiveSpecificHumidity2M:
		surface.SpecificHumidity2MKgKg = value
	default:
		panic("unsupported prepared surface field")
	}
}
