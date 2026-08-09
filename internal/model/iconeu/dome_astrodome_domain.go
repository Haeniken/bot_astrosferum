package iconeu

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"bot_astrosferum/internal/forecast"
)

var _ forecast.AstrodomeRefractionDomainResolver = (*DomeVolume)(nil)

// ResolveAstrodomeRefractionDomain reconstructs the native HHL terrain and
// model-top surfaces with the exact four-column stencil used for P/T/QV. Its
// partition identity changes at every horizontal cell and every branch of the
// bilinearly reconstructed local HHL column, so the ray integrator cannot step
// across a derivative discontinuity unnoticed.
func (volume *DomeVolume) ResolveAstrodomeRefractionDomain(
	ctx context.Context,
	validAt time.Time,
	point forecast.AstrodomeRayPoint,
	stencil forecast.AstrodomeHorizontalStencil,
) (forecast.AstrodomeRefractionDomainPoint, error) {
	if volume == nil {
		return forecast.AstrodomeRefractionDomainPoint{}, errors.New("ICON-EU Astrodome volume is required")
	}
	if err := ctx.Err(); err != nil {
		return forecast.AstrodomeRefractionDomainPoint{}, err
	}
	if !validAt.Equal(validAt.UTC()) || validAt.Minute() != 0 || validAt.Second() != 0 || validAt.Nanosecond() != 0 {
		return forecast.AstrodomeRefractionDomainPoint{}, errors.New("ICON-EU Astrodome refraction time must be a whole UTC hour")
	}
	expected, err := volume.HorizontalStencil(ctx, point.Location)
	if err != nil {
		return forecast.AstrodomeRefractionDomainPoint{}, err
	}
	if err := sameDomeStencil(expected, stencil); err != nil {
		return forecast.AstrodomeRefractionDomainPoint{}, err
	}
	columns, err := volume.domeStencilColumns(ctx, stencil)
	if err != nil {
		return forecast.AstrodomeRefractionDomainPoint{}, err
	}
	weights := domeStencilWeights(stencil)
	localHHL, err := domeInterpolatedHHLColumn(columns, weights)
	if err != nil {
		return forecast.AstrodomeRefractionDomainPoint{}, err
	}
	surfaceValues := [4]float64{}
	for index, column := range columns {
		surfaceValues[index] = column.HSURFHeightM
	}
	cellID, err := volume.HorizontalCellID(stencil)
	if err != nil {
		return forecast.AstrodomeRefractionDomainPoint{}, err
	}
	surfaceHeight := domeWeighted4(surfaceValues, weights)
	modelTopHeight := localHHL[0].HeightM
	if !finiteDomeVolume(surfaceHeight) || !finiteDomeVolume(modelTopHeight) || modelTopHeight <= surfaceHeight {
		return forecast.AstrodomeRefractionDomainPoint{}, errors.New("reconstructed ICON-EU refraction domain is invalid")
	}
	branch, err := domeThermodynamicBranch(&localHHL, surfaceHeight, point.HeightM)
	if err != nil {
		return forecast.AstrodomeRefractionDomainPoint{}, fmt.Errorf("ICON-EU Astrodome local HHL column: %w", err)
	}
	return forecast.AstrodomeRefractionDomainPoint{
		PartitionID:    cellID + "|" + branch,
		SurfaceHeightM: surfaceHeight, ModelTopHeightM: modelTopHeight,
	}, nil
}

// domeInterpolatedHHLColumn reconstructs the terrain-following native HHL
// surfaces at one horizontal point. It is deliberately geometry-only: model
// primitives on corresponding native levels are horizontally combined onto
// this same local column before vertical reconstruction by the provider-neutral
// forecast kernel.
func domeInterpolatedHHLColumn(
	columns [4]forecast.AstrodomePrimitiveColumn,
	weights [4]float64,
) ([domeHalfLevelCount]forecast.AstrodomeHalfLevelGeometry, error) {
	var geometry [domeHalfLevelCount]forecast.AstrodomeHalfLevelGeometry
	for _, column := range columns {
		if len(column.HalfLevelGeometry) != domeHalfLevelCount {
			return geometry, fmt.Errorf("ICON-EU Astrodome column %q has %d HHL levels, want %d",
				column.ColumnID, len(column.HalfLevelGeometry), domeHalfLevelCount)
		}
	}
	for levelIndex := range geometry {
		values := [4]float64{}
		for columnIndex, column := range columns {
			level := column.HalfLevelGeometry[levelIndex]
			if level.ModelHalfLevel != levelIndex+1 || !finiteDomeVolume(level.HeightM) {
				return geometry, fmt.Errorf("ICON-EU Astrodome column %q has invalid HHL level %d",
					column.ColumnID, levelIndex+1)
			}
			values[columnIndex] = level.HeightM
		}
		height := domeWeighted4(values, weights)
		if !finiteDomeVolume(height) {
			return geometry, fmt.Errorf("bilinearly reconstructed ICON-EU HHL level %d is invalid", levelIndex+1)
		}
		geometry[levelIndex] = forecast.AstrodomeHalfLevelGeometry{
			ModelHalfLevel: levelIndex + 1,
			HeightM:        height,
		}
		if levelIndex > 0 && height >= geometry[levelIndex-1].HeightM {
			return geometry, errors.New("bilinearly reconstructed ICON-EU HHL geometry is not top-to-surface ordered")
		}
	}
	return geometry, nil
}

func domeInterpolatedHHLHeight(
	columns [4]forecast.AstrodomePrimitiveColumn,
	weights [4]float64,
	levelIndex int,
) (float64, error) {
	if levelIndex < 0 || levelIndex >= domeHalfLevelCount {
		return 0, fmt.Errorf("ICON-EU HHL index %d is outside the native geometry", levelIndex)
	}
	values := [4]float64{}
	for columnIndex, column := range columns {
		if len(column.HalfLevelGeometry) != domeHalfLevelCount {
			return 0, fmt.Errorf("ICON-EU Astrodome column %q has %d HHL levels, want %d",
				column.ColumnID, len(column.HalfLevelGeometry), domeHalfLevelCount)
		}
		level := column.HalfLevelGeometry[levelIndex]
		if level.ModelHalfLevel != levelIndex+1 || !finiteDomeVolume(level.HeightM) {
			return 0, fmt.Errorf("ICON-EU Astrodome column %q has invalid HHL level %d", column.ColumnID, levelIndex+1)
		}
		values[columnIndex] = level.HeightM
	}
	height := domeWeighted4(values, weights)
	if !finiteDomeVolume(height) {
		return 0, fmt.Errorf("bilinearly reconstructed ICON-EU HHL level %d is invalid", levelIndex+1)
	}
	return height, nil
}

func domeThermodynamicBranch(
	geometry *[domeHalfLevelCount]forecast.AstrodomeHalfLevelGeometry,
	surfaceHeightM float64,
	heightM float64,
) (string, error) {
	if geometry == nil || !finiteDomeVolume(surfaceHeightM) || !finiteDomeVolume(heightM) {
		return "", errors.New("invalid HHL thermodynamic branch input")
	}
	topHHL := geometry[0].HeightM
	lowerGuardHeight := surfaceHeightM - forecast.AstrodomeRefractionPrimitiveEventGuardM
	if heightM < lowerGuardHeight || heightM > topHHL+forecast.AstrodomeRefractionPrimitiveEventGuardM {
		return "", fmt.Errorf("height %.3f m lies outside the refraction domain/guard", heightM)
	}
	topFull := (geometry[0].HeightM + geometry[1].HeightM) / 2
	bottomFull := (geometry[domeHalfLevelCount-2].HeightM + geometry[domeHalfLevelCount-1].HeightM) / 2
	if heightM < bottomFull {
		return "lower-boundary", nil
	}
	if heightM > topFull {
		return "upper-hydrostatic", nil
	}
	fullCount := domeHalfLevelCount - 1
	firstAtOrBelow := sort.Search(fullCount, func(index int) bool {
		return (geometry[index].HeightM+geometry[index+1].HeightM)/2 <= heightM
	})
	if firstAtOrBelow == 0 {
		return "full-000-001", nil
	}
	if firstAtOrBelow >= fullCount {
		return "", errors.New("failed to locate native full-level branch")
	}
	lower, upper := firstAtOrBelow, firstAtOrBelow-1
	return fmt.Sprintf("full-%03d-%03d", lower, upper), nil
}
