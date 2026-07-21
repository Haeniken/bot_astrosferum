package forecast

import (
	"fmt"
	"math"
	"sort"
	"time"
)

type CloudLevel struct {
	ModelLevel      int     `json:"model_level"`
	PressureHPA     float64 `json:"pressure_hpa"`
	HeightM         float64 `json:"height_m"`
	LayerThicknessM float64 `json:"layer_thickness_m"`
	TemperatureK    float64 `json:"temperature_k"`
	UMS             float64 `json:"u_ms"`
	VMS             float64 `json:"v_ms"`
	TKEJkg          float64 `json:"tke_j_kg"`
	CoverPercent    float64 `json:"cover_percent"`
	// CloudLiquidKgKg and CloudIceKgKg are ICON grid-box mean mass
	// mixing ratios (QC and QI). They are not volumetric density.
	CloudLiquidKgKg float64 `json:"cloud_liquid_kg_kg"`
	CloudIceKgKg    float64 `json:"cloud_ice_kg_kg"`
}

type CloudFrame struct {
	ValidAt time.Time    `json:"valid_at"`
	Levels  []CloudLevel `json:"levels"`
}

type CloudSeries struct {
	Location          Location     `json:"location"`
	Provider          string       `json:"provider"`
	Product           string       `json:"product"`
	RunID             string       `json:"run_id"`
	BaseTime          time.Time    `json:"base_time"`
	GeneratedAt       time.Time    `json:"generated_at"`
	SurfaceElevationM float64      `json:"surface_elevation_m"`
	Frames            []CloudFrame `json:"frames"`
}

type CloudDiagnostics struct {
	Times             []time.Time
	PressureHPA       []float64
	HeightKM          []float64
	SurfaceElevationM float64
	CoverPercent      [][]float64
	// AirMassKgM2 is the dry-air column mass of each sampled native ICON
	// model layer. It is calculated independently for every forecast hour
	// from P/(Rd*T) times the two enclosing HHL half-levels' separation.
	AirMassKgM2 [][]float64
	// CondensateMGKG is (QC+QI) expressed in mg/kg for display. This is
	// numerically equal to g/t and preserves the model's vertical profile.
	CondensateMGKG [][]float64
	LiquidMGKG     [][]float64
	IceMGKG        [][]float64
}

// ComputeCloudObstruction combines model-layer cloud fraction with QC/QI.
// Native HHL layer thickness and P/(Rd*T) convert grid-box mean mixing ratio
// to water path. Sparse upper-atmosphere sampling therefore never assigns the
// mass of skipped model layers to a retained level. The result is the blocked
// fraction of the grid cell rather than cloud cover:
// C*(1-exp(-tau/C)). Because diagnostic CLC includes sub-grid variability that
// public grid-scale QC/QI may not retain, a height-dependent fraction of CLC
// is a conservative uncertainty guard. The configured value applies to low
// cloud; it fades to 55% of that value in the middle tier and 18% in the high
// tier. Thus widespread thin cirrus remains distinguishable from an opaque
// low deck without becoming falsely invisible.
func ComputeCloudObstruction(diagnostics CloudDiagnostics, calibration OverallIndexCalibration) ([][]float64, error) {
	if err := calibration.Validate(); err != nil {
		return nil, err
	}
	rows := len(diagnostics.PressureHPA)
	if rows < 1 || len(diagnostics.HeightKM) != rows || len(diagnostics.LiquidMGKG) != rows || len(diagnostics.IceMGKG) != rows ||
		len(diagnostics.CoverPercent) != rows || len(diagnostics.AirMassKgM2) != rows {
		return nil, fmt.Errorf("cloud diagnostics are incomplete")
	}
	columns := len(diagnostics.Times)
	result := make([][]float64, rows)
	for row := range rows {
		if len(diagnostics.LiquidMGKG[row]) != columns || len(diagnostics.IceMGKG[row]) != columns ||
			len(diagnostics.CoverPercent[row]) != columns || len(diagnostics.AirMassKgM2[row]) != columns {
			return nil, fmt.Errorf("cloud diagnostics row %d is incomplete", row)
		}
		result[row] = make([]float64, columns)
		for column := range columns {
			airMassKgM2 := diagnostics.AirMassKgM2[row][column]
			if !finiteCloud(airMassKgM2) || airMassKgM2 <= 0 {
				return nil, fmt.Errorf("invalid native-layer air mass at row %d column %d", row, column)
			}
			cover := clampSurfaceValue(diagnostics.CoverPercent[row][column]/100, 0, 1)
			if cover < 1e-9 {
				result[row][column] = 0
				continue
			}
			liquidPath := diagnostics.LiquidMGKG[row][column] * 1e-6 * airMassKgM2
			icePath := diagnostics.IceMGKG[row][column] * 1e-6 * airMassKgM2
			liquidTau := phaseOpticalDepth(liquidPath, 2.0, 1000, calibration.CloudLiquidRadiusMicrometers)
			iceTau := phaseOpticalDepth(icePath, 2.1, 916.7, calibration.CloudIceRadiusMicrometers)
			blocked := cover * (1 - math.Exp(-(liquidTau+iceTau)/cover))
			heightAGLM := diagnostics.HeightKM[row]*1000 - diagnostics.SurfaceElevationM
			guard := unresolvedLayerCloudObstruction(cover, heightAGLM, calibration.UnresolvedCloudObstruction)
			blocked = math.Max(blocked, guard)
			result[row][column] = 100 * clampSurfaceValue(blocked, 0, 1)
		}
	}
	return result, nil
}

func (series CloudSeries) Window(now time.Time, hours int) CloudSeries {
	if len(series.Frames) == 0 || hours < 1 {
		return series
	}
	threshold := now.UTC().Truncate(time.Hour)
	start := 0
	for start < len(series.Frames) && series.Frames[start].ValidAt.Before(threshold) {
		start++
	}
	if start >= len(series.Frames) {
		start = len(series.Frames) - 1
	}
	limit := series.Frames[start].ValidAt.Add(time.Duration(hours) * time.Hour)
	end := start
	for end < len(series.Frames) && !series.Frames[end].ValidAt.After(limit) {
		end++
	}
	series.Frames = series.Frames[start:end]
	return series
}

func ComputeCloudDiagnostics(series CloudSeries) (CloudDiagnostics, error) {
	if len(series.Frames) < 2 {
		return CloudDiagnostics{}, fmt.Errorf("cloud series needs at least two frames")
	}
	levels := len(series.Frames[0].Levels)
	if levels < 2 {
		return CloudDiagnostics{}, fmt.Errorf("cloud series needs at least two levels")
	}
	diagnostics := CloudDiagnostics{
		Times: make([]time.Time, len(series.Frames)), PressureHPA: make([]float64, levels),
		HeightKM: make([]float64, levels), SurfaceElevationM: series.SurfaceElevationM,
		CoverPercent: make([][]float64, levels),
		AirMassKgM2:  make([][]float64, levels), CondensateMGKG: make([][]float64, levels),
		LiquidMGKG: make([][]float64, levels), IceMGKG: make([][]float64, levels),
	}
	for level := range levels {
		diagnostics.CoverPercent[level] = make([]float64, len(series.Frames))
		diagnostics.AirMassKgM2[level] = make([]float64, len(series.Frames))
		diagnostics.CondensateMGKG[level] = make([]float64, len(series.Frames))
		diagnostics.LiquidMGKG[level] = make([]float64, len(series.Frames))
		diagnostics.IceMGKG[level] = make([]float64, len(series.Frames))
	}
	for column, frame := range series.Frames {
		if len(frame.Levels) != levels || frame.ValidAt.IsZero() {
			return CloudDiagnostics{}, fmt.Errorf("inconsistent cloud frame %d", column)
		}
		sorted := append([]CloudLevel(nil), frame.Levels...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].HeightM < sorted[j].HeightM })
		diagnostics.Times[column] = frame.ValidAt
		for row, level := range sorted {
			if !finiteCloud(level.PressureHPA) || level.PressureHPA <= 0 || !finiteCloud(level.HeightM) ||
				!finiteCloud(level.LayerThicknessM) || level.LayerThicknessM <= 0 ||
				!finiteCloud(level.TemperatureK) || level.TemperatureK <= 0 || !finiteCloud(level.CoverPercent) ||
				!finiteCloud(level.CloudLiquidKgKg) || !finiteCloud(level.CloudIceKgKg) {
				return CloudDiagnostics{}, fmt.Errorf("invalid cloud value in frame %d", column)
			}
			airMassKgM2 := level.PressureHPA * 100 / (287.05 * level.TemperatureK) * level.LayerThicknessM
			if !finiteCloud(airMassKgM2) || airMassKgM2 <= 0 {
				return CloudDiagnostics{}, fmt.Errorf("invalid native-layer air mass in frame %d", column)
			}
			diagnostics.PressureHPA[row] += level.PressureHPA / float64(len(series.Frames))
			diagnostics.HeightKM[row] += level.HeightM / 1000 / float64(len(series.Frames))
			diagnostics.CoverPercent[row][column] = math.Max(0, math.Min(100, level.CoverPercent))
			diagnostics.AirMassKgM2[row][column] = airMassKgM2
			diagnostics.CondensateMGKG[row][column] = math.Max(0, level.CloudLiquidKgKg+level.CloudIceKgKg) * 1e6
			diagnostics.LiquidMGKG[row][column] = math.Max(0, level.CloudLiquidKgKg) * 1e6
			diagnostics.IceMGKG[row][column] = math.Max(0, level.CloudIceKgKg) * 1e6
		}
	}
	return diagnostics, nil
}

// unresolvedLayerCloudObstruction is not a second optical-depth model. It is
// a bounded uncertainty guard for diagnostic CLC that is not represented by
// public grid-scale QC/QI. Low and middle cloud can hide unresolved opaque
// sub-grid cloud, whereas high cloud is commonly optically thin; the physical
// QC/QI optical depth still overrides this guard whenever it is stronger.
func unresolvedLayerCloudObstruction(cover, heightAGLM, lowCloudObstruction float64) float64 {
	scale := 1.0
	switch {
	case heightAGLM >= 7000:
		scale = 0.18
	case heightAGLM >= 2000:
		scale = 0.55
	}
	return clampSurfaceValue(cover, 0, 1) * clampSurfaceValue(lowCloudObstruction, 0, 1) * scale
}

// unresolvedColumnCloudObstruction combines the already available ICON low,
// middle, and high diagnostic covers with random overlap. The cap by
// total/tier cover prevents the uncertainty guard itself from blocking more
// sky than ICON diagnoses as cloudy. Physical TQC/TQI optical depth is handled
// separately and may produce a stronger obstruction.
func unresolvedColumnCloudObstruction(frame SurfaceFrame, lowCloudObstruction float64) float64 {
	lowCover := clampSurfaceValue(frame.LowCloudCoverPercent/100, 0, 1)
	middleCover := clampSurfaceValue(frame.MidCloudCoverPercent/100, 0, 1)
	highCover := clampSurfaceValue(frame.HighCloudCoverPercent/100, 0, 1)
	if lowCover+middleCover+highCover < 1e-9 {
		return clampSurfaceValue(frame.CloudCoverPercent/100, 0, 1) * lowCloudObstruction
	}
	low := unresolvedLayerCloudObstruction(lowCover, 0, lowCloudObstruction)
	middle := unresolvedLayerCloudObstruction(middleCover, 3000, lowCloudObstruction)
	high := unresolvedLayerCloudObstruction(highCover, 8000, lowCloudObstruction)
	combined := 1 - (1-low)*(1-middle)*(1-high)
	coverCap := math.Max(clampSurfaceValue(frame.CloudCoverPercent/100, 0, 1), math.Max(lowCover, math.Max(middleCover, highCover)))
	return math.Min(coverCap, combined)
}

func finiteCloud(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
