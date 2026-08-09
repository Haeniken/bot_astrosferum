package forecast

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// astrodomeScienceCloudFractionEnvelope is a conservative bound on the
// reconstructed native CLC primitive for one exact vertical-support branch
// in one horizontal cell. The support identity is part of the science
// partition signature, so a missing full-level breakpoint fails closed.
type astrodomeScienceCloudFractionEnvelope struct {
	upperBound      float64
	supportIDs      [4]string
	verticalSupport astrodomeCloudFractionVerticalSupport
}

// certifiedCloudFractionUpperEnvelope bounds every CLC value produced while
// one terrain-following vertical support branch remains active. It uses only
// raw native CLC values from the four surrounding columns, both endpoints of
// the native time bracket, and the active full-level anchor(s). No cloud
// transmission, cloud score, or other derived output is interpolated.
//
// Temporal, bilinear-horizontal, and vertical reconstruction are convex.
// Their composition may be nonlinear in curved-ray path length, but it cannot
// exceed the largest raw value in this support set. The tiny outward allowance
// covers the stencil's admitted binary64 sum error and arithmetic in the three
// interpolation stages; CLC is finally bounded by its physical [0,1] domain.
func (reconstructor *AstrodomePrimitiveReconstructor) certifiedCloudFractionUpperEnvelope(
	ctx context.Context,
	validAt time.Time,
	stencil AstrodomeHorizontalStencil,
	verticalSupport astrodomeCloudFractionVerticalSupport,
) (astrodomeScienceCloudFractionEnvelope, error) {
	result := astrodomeScienceCloudFractionEnvelope{verticalSupport: verticalSupport}
	if reconstructor == nil || reconstructor.volume == nil {
		return result, fmt.Errorf("astrodome primitive reconstructor is required")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := reconstructor.ensureIdentity(); err != nil {
		return result, err
	}
	if err := stencil.Validate(); err != nil {
		return result, fmt.Errorf("cloud-fraction envelope stencil: %w", err)
	}
	if err := validateAstrodomeUTCWholeHour(validAt); err != nil {
		return result, fmt.Errorf("cloud-fraction envelope valid time: %w", err)
	}
	validAt = validAt.UTC()
	bracket, err := astrodomePrimitiveBracket(
		reconstructor.fieldTimes[AstrodomePrimitiveCloudFraction], validAt,
	)
	if err != nil {
		return result, fmt.Errorf("cloud-fraction envelope temporal bracket: %w", err)
	}

	weightsSum := 0.0
	maximumRaw := math.Inf(-1)
	for index, support := range stencil.Supports {
		column, columnErr := reconstructor.column(ctx, support.ColumnID)
		if columnErr != nil {
			return result, columnErr
		}
		if !astrodomeSameGridLocation(column.Location, support.Location) {
			return result, fmt.Errorf("cloud-fraction envelope column %q location changed", support.ColumnID)
		}
		if err := validateAstrodomeCloudVerticalSupport(verticalSupport, len(column.HalfLevelGeometry)-1); err != nil {
			return result, err
		}
		result.supportIDs[index] = support.ColumnID
		weightsSum += support.Weight
		left, right, frameErr := astrodomePrimitiveFrames(column.Frames, bracket)
		if frameErr != nil {
			return result, frameErr
		}
		for _, levelIndex := range astrodomeCloudVerticalSupportLevels(verticalSupport) {
			leftValue, valueErr := astrodomeFullLevelValue(
				left.FullLevels[levelIndex], AstrodomePrimitiveCloudFraction,
			)
			if valueErr != nil {
				return result, valueErr
			}
			maximumRaw = math.Max(maximumRaw, leftValue)
			if !bracket.right.Equal(bracket.left) {
				rightValue, rightErr := astrodomeFullLevelValue(
					right.FullLevels[levelIndex], AstrodomePrimitiveCloudFraction,
				)
				if rightErr != nil {
					return result, rightErr
				}
				maximumRaw = math.Max(maximumRaw, rightValue)
			}
		}
	}
	sort.Strings(result.supportIDs[:])
	if !finite(maximumRaw) || maximumRaw < 0 || maximumRaw > 1 {
		return result, fmt.Errorf("cloud-fraction envelope has no valid native support")
	}
	// At most three convex interpolation stages are composed. 256u exceeds
	// the operation count of the compensated four-column sum plus temporal and
	// vertical mixing. max(0,sum(w)-1) covers the stencil contract explicitly.
	if maximumRaw > 0 {
		arithmeticExcess := math.Max(0, weightsSum-1) + 256*astrodomeScienceFloatUnitRoundoff
		result.upperBound = maximumRaw * (1 + arithmeticExcess)
		result.upperBound = math.Nextafter(result.upperBound, math.Inf(1))
		result.upperBound = clampSurfaceValue(result.upperBound, 0, 1)
	}
	if err := reconstructor.ensureIdentity(); err != nil {
		return astrodomeScienceCloudFractionEnvelope{}, err
	}
	return result, nil
}

func validateAstrodomeCloudVerticalSupport(
	support astrodomeCloudFractionVerticalSupport,
	levelCount int,
) error {
	if levelCount < 2 || support.lowerLevelIndex < 0 || support.lowerLevelIndex >= levelCount ||
		support.upperLevelIndex < 0 || support.upperLevelIndex >= levelCount {
		return fmt.Errorf("cloud-fraction vertical support is outside native full levels")
	}
	switch support.mode {
	case astrodomeCloudFractionSupportPair:
		if support.lowerLevelIndex != support.upperLevelIndex+1 {
			return fmt.Errorf("cloud-fraction vertical pair is not adjacent and bottom-to-top ordered")
		}
	case astrodomeCloudFractionSupportConstantBottom:
		if support.lowerLevelIndex != levelCount-1 || support.upperLevelIndex != levelCount-1 {
			return fmt.Errorf("cloud-fraction bottom extension is not bound to the bottom full level")
		}
	case astrodomeCloudFractionSupportConstantTop:
		if support.lowerLevelIndex != 0 || support.upperLevelIndex != 0 {
			return fmt.Errorf("cloud-fraction top extension is not bound to the top full level")
		}
	default:
		return fmt.Errorf("cloud-fraction vertical support mode is invalid")
	}
	return nil
}

func astrodomeCloudVerticalSupportLevels(support astrodomeCloudFractionVerticalSupport) []int {
	if support.lowerLevelIndex == support.upperLevelIndex {
		return []int{support.lowerLevelIndex}
	}
	return []int{support.lowerLevelIndex, support.upperLevelIndex}
}
