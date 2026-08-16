package iconeu

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"bot_astrosferum/internal/forecast"
)

// DomeAstrodomeScienceNativeFrame owns exact native branch inputs for one
// forecast hour. The frame is shared by every node of that hour and discarded
// with the bounded scheduling window; no finished scientific value is cached.
type DomeAstrodomeScienceNativeFrame struct {
	footprint *DomeAstrodomeFootprint
	validAt   time.Time
	bracket   domeVolumeTimeBracket
	cells     sync.Map // map[[4]string]*domePreparedNativeCellEntry
}

type domePreparedNativeCellEntry struct {
	once sync.Once
	cell domePreparedNativeCell
	err  error
}

type domePreparedNativeCell struct {
	cellID           string
	columns          [4]forecast.AstrodomePrimitiveColumn
	surfaceHeights   [4]float64
	mixedLayerDepths [4]float64
	selected         sync.Map // map[domePreparedTropopauseKey]*domePreparedTropopauseEntry
}

type domePreparedTropopauseKey struct {
	kind         forecast.AstrodomeScienceTropopauseBoundaryKind
	lower, upper int
}

type domePreparedTropopauseEntry struct {
	once   sync.Once
	levels [2]domePreparedThermalLevel
	err    error
}

type domePreparedThermalLevel struct {
	upperHalfHeights [4]float64
	lowerHalfHeights [4]float64
	pressures        [4]float64
	temperatures     [4]float64
}

var _ forecast.AstrodomeScienceNativeContextResolver = (*DomeAstrodomeScienceNativeFrame)(nil)
var _ forecast.AstrodomeScienceSelectedNativeContextResolver = (*DomeAstrodomeScienceNativeFrame)(nil)

func (footprint *DomeAstrodomeFootprint) NewAstrodomeScienceNativeFrame(
	validAt time.Time,
) (*DomeAstrodomeScienceNativeFrame, error) {
	if footprint == nil || footprint.volume == nil {
		return nil, errors.New("ICON-EU Astrodome footprint is required")
	}
	bracket, err := domeVolumeBracket(
		footprint.volume.fieldTimes[forecast.AstrodomePrimitivePressure], validAt,
	)
	if err != nil {
		return nil, err
	}
	return &DomeAstrodomeScienceNativeFrame{
		footprint: footprint,
		validAt:   validAt.UTC(),
		bracket:   bracket,
	}, nil
}

func (frame *DomeAstrodomeScienceNativeFrame) ResolveAstrodomeScienceNativeContext(
	ctx context.Context,
	validAt time.Time,
	point forecast.AstrodomeRayPoint,
	stencil forecast.AstrodomeHorizontalStencil,
) (forecast.AstrodomeScienceNativeContext, error) {
	if frame == nil || frame.footprint == nil {
		return forecast.AstrodomeScienceNativeContext{}, errors.New("prepared ICON-EU science frame is required")
	}
	if !validAt.Equal(frame.validAt) {
		return forecast.AstrodomeScienceNativeContext{}, errors.New("prepared ICON-EU science frame cannot serve a different valid time")
	}
	return frame.footprint.ResolveAstrodomeScienceNativeContext(ctx, validAt, point, stencil)
}

func (frame *DomeAstrodomeScienceNativeFrame) ResolveAstrodomeScienceSelectedNativeContext(
	ctx context.Context,
	validAt time.Time,
	point forecast.AstrodomeRayPoint,
	stencil forecast.AstrodomeHorizontalStencil,
	selection forecast.AstrodomeScienceTropopauseSelection,
) (forecast.AstrodomeScienceNativeContext, float64, string, error) {
	empty := forecast.AstrodomeScienceNativeContext{}
	if frame == nil || frame.footprint == nil || frame.footprint.volume == nil {
		return empty, 0, "", errors.New("prepared ICON-EU science frame is required")
	}
	if err := ctx.Err(); err != nil {
		return empty, 0, "", err
	}
	if !validAt.Equal(frame.validAt) {
		return empty, 0, "", errors.New("prepared ICON-EU science frame cannot serve a different valid time")
	}
	if err := forecast.ValidateCoordinates(point.Location.Latitude, point.Location.Longitude); err != nil {
		return empty, 0, "", err
	}
	if !finiteDomeVolume(point.HeightM) {
		return empty, 0, "", errors.New("ICON-EU Astrodome ray-point height is invalid")
	}
	expected, err := frame.footprint.HorizontalStencil(ctx, point.Location)
	if err != nil {
		return empty, 0, "", err
	}
	if err := sameDomeStencil(expected, stencil); err != nil {
		return empty, 0, "", err
	}
	cell, err := frame.cell(ctx, expected)
	if err != nil {
		return empty, 0, "", err
	}
	weights := domeStencilWeights(expected)
	surfaceHeight := domeWeighted4(cell.surfaceHeights, weights)
	mixedLayerDepth := domeWeighted4(cell.mixedLayerDepths, weights)
	if !finiteDomeVolume(surfaceHeight) || !finiteDomeVolume(mixedLayerDepth) || mixedLayerDepth < 0 {
		return empty, 0, "", errors.New("reconstructed ICON-EU surface context is invalid")
	}
	native := forecast.AstrodomeScienceNativeContext{
		HorizontalCellID: cell.cellID,
		SurfaceHeightM:   surfaceHeight,
		MixedLayerDepthM: mixedLayerDepth,
	}
	if selection.BoundaryKind == forecast.AstrodomeScienceTropopauseBoundaryNone {
		height, method, selectionErr := forecast.AstrodomeScienceTropopauseFromSelection(nil, selection)
		return native, height, method, selectionErr
	}
	prepared, err := cell.tropopause(frame.bracket, selection)
	if err != nil {
		return empty, 0, "", err
	}
	levels := [2]forecast.AstrodomeScienceThermalPrimitive{}
	for offset, thermalIndex := range []int{selection.LowerLevelIndex, selection.UpperLevelIndex} {
		if offset == 1 && thermalIndex == selection.LowerLevelIndex {
			continue
		}
		level := prepared.levels[offset]
		height := (domeWeighted4(level.upperHalfHeights, weights) +
			domeWeighted4(level.lowerHalfHeights, weights)) / 2
		pressure := domeWeighted4(level.pressures, weights)
		temperature := domeWeighted4(level.temperatures, weights)
		levels[offset] = forecast.AstrodomeScienceThermalPrimitive{
			HeightM: height, PressurePa: pressure, TemperatureK: temperature,
		}
	}
	height, method, err := forecast.AstrodomeScienceTropopauseFromSelectedLevels(
		levels[0], levels[1], selection,
	)
	if err != nil {
		return empty, 0, "", err
	}
	return native, height, method, nil
}

func (frame *DomeAstrodomeScienceNativeFrame) cell(
	ctx context.Context,
	stencil forecast.AstrodomeHorizontalStencil,
) (*domePreparedNativeCell, error) {
	key := [4]string{}
	for index, support := range stencil.Supports {
		key[index] = support.ColumnID
	}
	loaded, ok := frame.cells.Load(key)
	if !ok {
		candidate := &domePreparedNativeCellEntry{}
		loaded, _ = frame.cells.LoadOrStore(key, candidate)
	}
	entry := loaded.(*domePreparedNativeCellEntry)
	entry.once.Do(func() {
		entry.cell.columns, entry.err = frame.footprint.volume.domeStencilColumns(ctx, stencil)
		if entry.err != nil {
			return
		}
		entry.cell.cellID, entry.err = frame.footprint.volume.HorizontalCellID(stencil)
		if entry.err != nil {
			return
		}
		for corner, column := range entry.cell.columns {
			entry.cell.surfaceHeights[corner] = column.HSURFHeightM
			if frame.bracket.leftIndex >= len(column.Frames) || frame.bracket.rightIndex >= len(column.Frames) {
				entry.err = fmt.Errorf("ICON-EU native frame is unavailable for column %q", column.ColumnID)
				return
			}
			left := column.Frames[frame.bracket.leftIndex].Surface
			right := column.Frames[frame.bracket.rightIndex].Surface
			if !left.Available.Has(forecast.AstrodomePrimitiveMixedLayerDepth) ||
				!right.Available.Has(forecast.AstrodomePrimitiveMixedLayerDepth) {
				entry.err = errors.New("ICON-EU mixed-layer depth is unavailable at a native bracket")
				return
			}
			entry.cell.mixedLayerDepths[corner] = domeLinear(
				left.MixedLayerDepthM, right.MixedLayerDepthM, frame.bracket.fraction,
			)
		}
	})
	if entry.err != nil {
		return nil, entry.err
	}
	return &entry.cell, nil
}

func (cell *domePreparedNativeCell) tropopause(
	bracket domeVolumeTimeBracket,
	selection forecast.AstrodomeScienceTropopauseSelection,
) (*domePreparedTropopauseEntry, error) {
	key := domePreparedTropopauseKey{
		kind: selection.BoundaryKind, lower: selection.LowerLevelIndex, upper: selection.UpperLevelIndex,
	}
	loaded, ok := cell.selected.Load(key)
	if !ok {
		candidate := &domePreparedTropopauseEntry{}
		loaded, _ = cell.selected.LoadOrStore(key, candidate)
	}
	entry := loaded.(*domePreparedTropopauseEntry)
	entry.once.Do(func() {
		indices := [2]int{selection.LowerLevelIndex, selection.UpperLevelIndex}
		if selection.BoundaryKind == forecast.AstrodomeScienceTropopauseBoundaryWMOLevel {
			indices[1] = indices[0]
		}
		for offset, thermalIndex := range indices {
			if thermalIndex < 0 || thermalIndex >= domeFullLevelCount {
				entry.err = errors.New("selected ICON-EU tropopause level is out of range")
				return
			}
			modelIndex := domeFullLevelCount - 1 - thermalIndex
			for corner, column := range cell.columns {
				if modelIndex+1 >= len(column.HalfLevelGeometry) ||
					bracket.leftIndex >= len(column.Frames) || bracket.rightIndex >= len(column.Frames) ||
					modelIndex >= len(column.Frames[bracket.leftIndex].FullLevels) ||
					modelIndex >= len(column.Frames[bracket.rightIndex].FullLevels) {
					entry.err = errors.New("selected ICON-EU tropopause support is unavailable")
					return
				}
				left := column.Frames[bracket.leftIndex].FullLevels[modelIndex]
				right := column.Frames[bracket.rightIndex].FullLevels[modelIndex]
				if !left.Available.Has(forecast.AstrodomePrimitivePressure) ||
					!right.Available.Has(forecast.AstrodomePrimitivePressure) ||
					!left.Available.Has(forecast.AstrodomePrimitiveTemperature) ||
					!right.Available.Has(forecast.AstrodomePrimitiveTemperature) {
					entry.err = errors.New("selected ICON-EU tropopause P/T support is unavailable")
					return
				}
				level := &entry.levels[offset]
				level.upperHalfHeights[corner] = column.HalfLevelGeometry[modelIndex].HeightM
				level.lowerHalfHeights[corner] = column.HalfLevelGeometry[modelIndex+1].HeightM
				level.pressures[corner] = domeLinear(left.PressurePa, right.PressurePa, bracket.fraction)
				level.temperatures[corner] = domeLinear(left.TemperatureK, right.TemperatureK, bracket.fraction)
				if !finiteDomeVolume(level.upperHalfHeights[corner]) ||
					!finiteDomeVolume(level.lowerHalfHeights[corner]) ||
					level.upperHalfHeights[corner] <= level.lowerHalfHeights[corner] ||
					!finiteDomeVolume(level.pressures[corner]) || level.pressures[corner] <= 0 ||
					!finiteDomeVolume(level.temperatures[corner]) ||
					level.temperatures[corner] < 150 || level.temperatures[corner] > 350 {
					entry.err = errors.New("selected ICON-EU tropopause support is invalid")
					return
				}
			}
		}
	})
	if entry.err != nil {
		return nil, entry.err
	}
	return entry, nil
}
