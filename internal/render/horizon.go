package render

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
	"time"

	"bot_astrosferum/internal/astronomy"
	"bot_astrosferum/internal/forecast"
)

const (
	HorizonWidth  = 3200
	HorizonHeight = 1400

	// HorizonRenderVersion is separate from the seven ordinary forecast charts
	// because the Horizon PNG has its own cache and presentation lifecycle.
	HorizonRenderVersion = "horizon-render-v5-hourly-72h-decimal-scores"

	horizonFrameCount   = 73
	horizonLeft         = 155
	horizonRight        = 25
	horizonSolarBandTop = 264
	horizonSolarBandEnd = 309
	horizonMatrixTop    = 314
	horizonRowHeight    = 75
	horizonMatrixEnd    = horizonMatrixTop + forecast.HorizonDirectionCount*horizonRowHeight
)

var (
	horizonBackground    = color.RGBA{R: 10, G: 31, B: 61, A: 255}
	horizonText          = color.RGBA{R: 235, G: 241, B: 248, A: 255}
	horizonMuted         = color.RGBA{R: 151, G: 178, B: 207, A: 255}
	horizonGrid          = color.RGBA{R: 46, G: 83, B: 120, A: 255}
	horizonStrongGrid    = color.RGBA{R: 97, G: 151, B: 202, A: 255}
	horizonMissing       = color.RGBA{R: 55, G: 69, B: 88, A: 255}
	horizonQualityBad    = color.RGBA{R: 111, G: 121, B: 137, A: 255}
	horizonQualityLow    = color.RGBA{R: 242, G: 151, B: 62, A: 255}
	horizonQualityUsable = color.RGBA{R: 69, G: 170, B: 232, A: 255}
	horizonQualityGood   = color.RGBA{R: 77, G: 200, B: 146, A: 255}
)

// HorizonInput contains presentation metadata and the complete immutable
// f000..f072 ICON-EU run. Rendering deliberately has no model or network
// access. There is no generated-at field, so the chart never invents one.
type HorizonInput struct {
	Location forecast.Location
	Provider string
	RunID    string
	Grid     string
	Frames   []forecast.HorizonFrame
}

type validatedHorizonFrame struct {
	ValidAt time.Time
	Results []forecast.HorizonResult
}

type horizonLabels struct {
	title, run, utcRange, localRange, periodNote string
}

// Horizon renders the complete 72-hour, eight-direction result as a
// time-by-direction matrix. Scores remain readable in the original PNG while
// solar phase and input quality are encoded separately rather than folded into
// the scientific index. Elapsed terms are disclosed in static text because a
// run-keyed cached image must stay deterministic; live freshness is in its
// message caption.
func Horizon(ctx context.Context, destination string, input HorizonInput, options Options) error {
	if ctx == nil {
		return fmt.Errorf("horizon render context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(destination) == "" {
		return fmt.Errorf("horizon destination is required")
	}
	frames, _, err := validateHorizonInput(input)
	if err != nil {
		return err
	}
	fonts, closeFonts, err := newWeatherFonts()
	if err != nil {
		return fmt.Errorf("load horizon fonts: %w", err)
	}
	defer closeFonts()
	if err := ctx.Err(); err != nil {
		return err
	}

	zone := horizonLocation(input.Location.TimeZone)
	labels := horizonMetadataLabels(input, frames, options)
	canvas := image.NewRGBA(image.Rect(0, 0, HorizonWidth, HorizonHeight))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: horizonBackground}, image.Point{}, draw.Src)

	drawText(canvas, fonts.title, 34, 45, labels.title, horizonText)
	drawText(canvas, fonts.normal, 34, 80, labels.run, horizonMuted)
	drawText(canvas, fonts.normal, 34, 112, labels.utcRange, horizonMuted)
	drawText(canvas, fonts.normal, 34, 144, labels.localRange, horizonMuted)
	drawText(canvas, fonts.normal, 34, 176, labels.periodNote, horizonMuted)

	columnWidth := float64(HorizonWidth-horizonLeft-horizonRight) / float64(len(frames))
	drawHorizonTimeHeader(canvas, fonts, frames, zone, columnWidth, options)
	sky := astronomy.Series{Location: input.Location}
	drawHorizonSolarBand(canvas, sky, frames, columnWidth)
	colors := magma(96)
	for frameIndex, frame := range frames {
		if err := ctx.Err(); err != nil {
			return err
		}
		x0, x1 := horizonColumnBounds(frameIndex, columnWidth)
		for directionIndex, result := range frame.Results {
			if err := ctx.Err(); err != nil {
				return err
			}
			y0 := horizonMatrixTop + directionIndex*horizonRowHeight
			y1 := y0 + horizonRowHeight
			shade := color.Color(horizonMissing)
			if result.Available {
				shade = paletteColor(colors, result.Index, 1, 10)
			}
			draw.Draw(canvas, image.Rect(x0, y0, x1, y1), &image.Uniform{C: shade}, image.Point{}, draw.Src)
			qualityTop := y1 - 7
			draw.Draw(canvas, image.Rect(x0+1, qualityTop, x1-1, y1-1), &image.Uniform{C: horizonConfidenceColor(result)}, image.Point{}, draw.Src)
			textShade := contrastColor(shade)
			if !result.Available {
				textShade = horizonText
			}
			drawCentered(canvas, fonts.large, (x0+x1)/2, y0+43, horizonScoreLabel(result), textShade)
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	drawHorizonMatrixGrid(canvas, frames, zone, columnWidth)
	for index, direction := range forecast.HorizonDirections() {
		baseline := horizonMatrixTop + index*horizonRowHeight + 45
		drawCentered(canvas, fonts.large, horizonLeft/2, baseline, horizonDirectionLabel(direction, options), horizonText)
	}

	drawHorizonLegends(canvas, fonts, options)
	drawHorizonLimiterSummary(canvas, fonts, frames, options)
	drawText(canvas, fonts.small, 34, 1270,
		localized(options,
			"Нижняя полоска показывает качество входных данных с учётом срока прогноза; это не вероятность результата.",
			"The bottom strip shows input-data quality with forecast lead time; it is not an outcome probability."), horizonMuted)
	drawText(canvas, fonts.small, 34, 1300,
		localized(options,
			"Рельеф оценивается по грубой модельной поверхности ICON HHL.",
			"Terrain is estimated from the coarse ICON HHL model surface."), horizonMuted)
	drawText(canvas, fonts.tiny, 34, HorizonHeight-20,
		fmt.Sprintf("%.4f, %.4f · %s · %s", input.Location.Latitude, input.Location.Longitude, forecast.HorizonAlgorithmVersion, HorizonRenderVersion), horizonMuted)

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := savePNGImageAtomic(canvas, destination); err != nil {
		return fmt.Errorf("render horizon chart: %w", err)
	}
	return nil
}

func drawHorizonSolarBand(canvas *image.RGBA, sky astronomy.Series, frames []validatedHorizonFrame, columnWidth float64) {
	first := frames[0].ValidAt
	start := first.Add(-30 * time.Minute)
	end := frames[len(frames)-1].ValidAt.Add(30 * time.Minute)
	for _, interval := range solarPhaseIntervals(sky, start, end) {
		x0 := horizonLeft + int(math.Round((interval.Start.Sub(first).Hours()+0.5)*columnWidth))
		x1 := horizonLeft + int(math.Round((interval.End.Sub(first).Hours()+0.5)*columnWidth))
		draw.Draw(canvas, image.Rect(x0, horizonSolarBandTop, x1, horizonSolarBandEnd), &image.Uniform{C: solarPhaseColor(interval.Phase)}, image.Point{}, draw.Src)
	}
}

func validateHorizonInput(input HorizonInput) ([]validatedHorizonFrame, time.Time, error) {
	if err := forecast.ValidateCoordinates(input.Location.Latitude, input.Location.Longitude); err != nil {
		return nil, time.Time{}, fmt.Errorf("horizon location: %w", err)
	}
	if _, err := time.LoadLocation(strings.TrimSpace(input.Location.TimeZone)); err != nil {
		return nil, time.Time{}, fmt.Errorf("horizon location timezone: %w", err)
	}
	if strings.TrimSpace(input.Provider) == "" || strings.TrimSpace(input.RunID) == "" || strings.TrimSpace(input.Grid) == "" {
		return nil, time.Time{}, fmt.Errorf("horizon provider, run and grid metadata are required")
	}
	runTime, err := time.Parse("2006010215", input.RunID)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("horizon run ID must be YYYYMMDDHH: %w", err)
	}
	if len(input.Frames) != horizonFrameCount {
		return nil, time.Time{}, fmt.Errorf("horizon chart requires exactly %d hourly frames (f000..f072)", horizonFrameCount)
	}
	frames := make([]validatedHorizonFrame, len(input.Frames))
	for frameIndex, frame := range input.Frames {
		expected := runTime.Add(time.Duration(frameIndex) * time.Hour)
		if frame.ValidAt.IsZero() || !frame.ValidAt.Equal(expected) {
			return nil, time.Time{}, fmt.Errorf("horizon frame %d must be run term f%03d at %s", frameIndex, frameIndex, expected.Format(time.RFC3339))
		}
		if len(frame.Results) != forecast.HorizonDirectionCount {
			return nil, time.Time{}, fmt.Errorf("horizon frame %d requires exactly %d directions", frameIndex, forecast.HorizonDirectionCount)
		}
		byDirection := make(map[forecast.HorizonDirection]forecast.HorizonResult, len(frame.Results))
		for _, result := range frame.Results {
			if !result.ValidAt.Equal(frame.ValidAt) {
				return nil, time.Time{}, fmt.Errorf("horizon frame %d contains a result at another valid time", frameIndex)
			}
			if math.Abs(result.GeometricElevationDegrees-forecast.HorizonGeometricElevationDegrees) > 1e-6 {
				return nil, time.Time{}, fmt.Errorf("horizon result %q is not at %.0f degrees", result.Direction, forecast.HorizonGeometricElevationDegrees)
			}
			if result.Available && (math.IsNaN(result.Index) || math.IsInf(result.Index, 0) || result.Index < 1 || result.Index > 10) {
				return nil, time.Time{}, fmt.Errorf("horizon result %q index must be between 1 and 10", result.Direction)
			}
			if math.IsNaN(result.Confidence) || math.IsInf(result.Confidence, 0) || result.Confidence < 0 || result.Confidence > 1 {
				return nil, time.Time{}, fmt.Errorf("horizon result %q confidence must be between 0 and 1", result.Direction)
			}
			if !knownHorizonDataQuality(result.DataQuality) {
				return nil, time.Time{}, fmt.Errorf("horizon result %q has unknown data quality %q", result.Direction, result.DataQuality)
			}
			if !knownHorizonLimitingFactor(result.LimitingFactor) {
				return nil, time.Time{}, fmt.Errorf("horizon result %q has unknown limiting factor %q", result.Direction, result.LimitingFactor)
			}
			if _, duplicate := byDirection[result.Direction]; duplicate {
				return nil, time.Time{}, fmt.Errorf("horizon frame %d repeats direction %q", frameIndex, result.Direction)
			}
			byDirection[result.Direction] = result
		}
		ordered := make([]forecast.HorizonResult, 0, forecast.HorizonDirectionCount)
		for _, direction := range forecast.HorizonDirections() {
			result, found := byDirection[direction]
			if !found {
				return nil, time.Time{}, fmt.Errorf("horizon frame %d is missing direction %q", frameIndex, direction)
			}
			ordered = append(ordered, result)
		}
		frames[frameIndex] = validatedHorizonFrame{ValidAt: frame.ValidAt, Results: ordered}
	}
	return frames, runTime.UTC(), nil
}

func horizonMetadataLabels(input HorizonInput, frames []validatedHorizonFrame, options Options) horizonLabels {
	zone := horizonLocation(input.Location.TimeZone)
	first, last := frames[0].ValidAt, frames[len(frames)-1].ValidAt
	timeZoneLabel := forecast.TimeZoneLabel(input.Location.TimeZone, first)
	return horizonLabels{
		title: localized(options, "Условия у горизонта на высоте 10° · 72 часа", "Horizon conditions at 10° elevation · 72 hours"),
		run: fmt.Sprintf(localized(options, "%s run %s UTC · Сетка: %s", "%s run %s UTC · Grid: %s"),
			strings.TrimSpace(input.Provider), strings.TrimSpace(input.RunID), strings.TrimSpace(input.Grid)),
		utcRange: fmt.Sprintf(localized(options, "UTC: %s — %s · 73 срока f000…f072", "UTC: %s — %s · 73 terms f000…f072"),
			first.UTC().Format("02.01.2006 15:04"), last.UTC().Format("02.01.2006 15:04")),
		localRange: fmt.Sprintf(localized(options, "Местное время: %s — %s · %s", "Local time: %s — %s · %s"),
			first.In(zone).Format("02.01.2006 15:04"), last.In(zone).Format("02.01.2006 15:04"), timeZoneLabel),
		periodNote: localized(options,
			"Первые сроки model run уже могут быть в прошлом; актуальность указана в подписи к изображению.",
			"The earliest model-run terms may have elapsed; live freshness is shown in the image caption."),
	}
}

func drawHorizonTimeHeader(canvas *image.RGBA, fonts weatherFonts, frames []validatedHorizonFrame, zone *time.Location, columnWidth float64, options Options) {
	for start := 0; start < len(frames); {
		date := frames[start].ValidAt.In(zone)
		end := start + 1
		for end < len(frames) {
			candidate := frames[end].ValidAt.In(zone)
			if candidate.Year() != date.Year() || candidate.YearDay() != date.YearDay() {
				break
			}
			end++
		}
		x0, _ := horizonColumnBounds(start, columnWidth)
		_, x1 := horizonColumnBounds(end-1, columnWidth)
		drawCentered(canvas, fonts.normalBold, (x0+x1)/2, 218, horizonDayLabel(date, options), horizonText)
		start = end
	}
	for index, frame := range frames {
		if index%3 != 0 && index != len(frames)-1 {
			continue
		}
		x0, x1 := horizonColumnBounds(index, columnWidth)
		drawCentered(canvas, fonts.normalBold, (x0+x1)/2, 254, frame.ValidAt.In(zone).Format("15"), horizonText)
	}
	drawRight(canvas, fonts.small, horizonLeft-14, 254, localized(options, "час", "hour"), horizonMuted)
}

func drawHorizonMatrixGrid(canvas *image.RGBA, frames []validatedHorizonFrame, zone *time.Location, columnWidth float64) {
	for index, frame := range frames {
		x0, _ := horizonColumnBounds(index, columnWidth)
		shade := horizonGrid
		if index == 0 || frame.ValidAt.In(zone).Hour() == 0 {
			shade = horizonStrongGrid
		}
		drawLine(canvas, x0, horizonSolarBandTop, x0, horizonMatrixEnd, shade)
	}
	for index := 0; index <= forecast.HorizonDirectionCount; index++ {
		y := horizonMatrixTop + index*horizonRowHeight
		drawLine(canvas, horizonLeft, y, HorizonWidth-horizonRight, y, horizonGrid)
	}
}

func drawHorizonLegends(canvas *image.RGBA, fonts weatherFonts, options Options) {
	colors := magma(96)
	drawText(canvas, fonts.normalBold, 155, 936, localized(options, "Индекс условий: 1 — неблагоприятно, 10 — благоприятно", "Conditions index: 1 unfavorable, 10 favorable"), horizonText)
	for value := 1; value <= 10; value++ {
		x0 := 155 + (value-1)*62
		x1 := x0 + 56
		shade := paletteColor(colors, float64(value), 1, 10)
		draw.Draw(canvas, image.Rect(x0, 950, x1, 986), &image.Uniform{C: shade}, image.Point{}, draw.Src)
		drawCentered(canvas, fonts.normalBold, (x0+x1)/2, 976, fmt.Sprintf("%d", value), contrastColor(shade))
	}
	draw.Draw(canvas, image.Rect(775, 950, 831, 986), &image.Uniform{C: horizonMissing}, image.Point{}, draw.Src)
	drawCentered(canvas, fonts.normalBold, 803, 976, "—", horizonText)

	drawText(canvas, fonts.normalBold, 900, 936, localized(options, "Качество данных", "Data quality"), horizonText)
	quality := []struct {
		shade  color.RGBA
		ru, en string
	}{
		{horizonQualityBad, "нет", "missing"},
		{horizonQualityLow, "огранич.", "limited"},
		{horizonQualityUsable, "пригодные", "usable"},
		{horizonQualityGood, "хорошие*", "good*"},
	}
	for index, item := range quality {
		x := 900 + index*245
		draw.Draw(canvas, image.Rect(x, 951, x+34, 984), &image.Uniform{C: item.shade}, image.Point{}, draw.Src)
		drawText(canvas, fonts.small, x+44, 977, localized(options, item.ru, item.en), horizonText)
	}

	drawText(canvas, fonts.normalBold, 1900, 936, localized(options, "Солнце", "Sun"), horizonText)
	phases := []struct {
		phase  solarPhase
		ru, en string
	}{
		{solarNight, "ночь <−18°", "night <−18°"},
		{solarAstronomicalTwilight, "астр. −12…−18°", "astro −12…−18°"},
		{solarBrightTwilight, "светлые 0…−12°", "bright 0…−12°"},
		{solarDay, "день ≥0°", "day ≥0°"},
	}
	for index, item := range phases {
		x := 1900 + index*250
		draw.Draw(canvas, image.Rect(x, 951, x+34, 984), &image.Uniform{C: solarPhaseColor(item.phase)}, image.Point{}, draw.Src)
		drawText(canvas, fonts.small, x+44, 977, localized(options, item.ru, item.en), horizonText)
	}
	drawText(canvas, fonts.small, 155, 1018, localized(options, "Цветная полоса над матрицей показывает день, сумерки и ночь для каждого срока.", "The colored strip above the matrix shows day, twilight, and night for every term."), horizonMuted)
}

func drawHorizonLimiterSummary(canvas *image.RGBA, fonts weatherFonts, frames []validatedHorizonFrame, options Options) {
	drawText(canvas, fonts.normalBold, 155, 1072,
		localized(options, "Наиболее частый главный ограничитель по каждому направлению", "Most frequent primary limiter by direction"), horizonText)
	directions := forecast.HorizonDirections()
	for index, direction := range directions {
		row, column := index/4, index%4
		x := 155 + column*755
		y := 1114 + row*48
		factor, hours := dominantHorizonLimiter(frames, index)
		value := fmt.Sprintf(localized(options, "%s: %s · %d сроков", "%s: %s · %d terms"),
			horizonDirectionLabel(direction, options), horizonLimiterLabel(factor, options), hours)
		drawText(canvas, fonts.normal, x, y, value, horizonText)
	}
}

func dominantHorizonLimiter(frames []validatedHorizonFrame, directionIndex int) (forecast.HorizonLimitingFactor, int) {
	counts := make(map[forecast.HorizonLimitingFactor]int)
	for _, frame := range frames {
		result := frame.Results[directionIndex]
		factor := result.LimitingFactor
		if !result.Available {
			factor = forecast.HorizonFactorUnavailable
		}
		counts[factor]++
	}
	order := []forecast.HorizonLimitingFactor{
		forecast.HorizonFactorCloud, forecast.HorizonFactorSeeing, forecast.HorizonFactorCoherence,
		forecast.HorizonFactorFog, forecast.HorizonFactorSurfaceWind, forecast.HorizonFactorTerrain,
		forecast.HorizonFactorNone, forecast.HorizonFactorUnavailable,
	}
	best, bestCount := forecast.HorizonFactorUnavailable, -1
	for _, factor := range order {
		if counts[factor] > bestCount {
			best, bestCount = factor, counts[factor]
		}
	}
	return best, bestCount
}

func horizonColumnBounds(index int, columnWidth float64) (int, int) {
	x0 := horizonLeft + int(math.Floor(float64(index)*columnWidth))
	x1 := horizonLeft + int(math.Floor(float64(index+1)*columnWidth))
	return x0, x1
}

func horizonScoreLabel(result forecast.HorizonResult) string {
	if !result.Available {
		return "—"
	}
	value := math.Max(1, math.Min(10, result.Index))
	return fmt.Sprintf("%.1f", value)
}

func horizonConfidenceColor(result forecast.HorizonResult) color.RGBA {
	if !result.Available || result.DataQuality == forecast.HorizonDataUnavailable {
		return horizonQualityBad
	}
	switch result.DataQuality {
	case forecast.HorizonDataLimited:
		return horizonQualityLow
	case forecast.HorizonDataUsable:
		return horizonQualityUsable
	case forecast.HorizonDataGoodCoarse:
		return horizonQualityGood
	default:
		return horizonQualityBad
	}
}

func knownHorizonDataQuality(quality forecast.HorizonDataQuality) bool {
	switch quality {
	case forecast.HorizonDataUnavailable, forecast.HorizonDataLimited, forecast.HorizonDataUsable, forecast.HorizonDataGoodCoarse:
		return true
	default:
		return false
	}
}

func knownHorizonLimitingFactor(factor forecast.HorizonLimitingFactor) bool {
	switch factor {
	case forecast.HorizonFactorNone, forecast.HorizonFactorUnavailable, forecast.HorizonFactorTerrain,
		forecast.HorizonFactorCloud, forecast.HorizonFactorSeeing, forecast.HorizonFactorCoherence,
		forecast.HorizonFactorFog, forecast.HorizonFactorSurfaceWind:
		return true
	default:
		return false
	}
}

func horizonDayLabel(value time.Time, options Options) string {
	weekdayRU := [...]string{"Вс", "Пн", "Вт", "Ср", "Чт", "Пт", "Сб"}
	if options.Language == "ru" {
		return fmt.Sprintf("%s, %s", weekdayRU[value.Weekday()], value.Format("02.01"))
	}
	return value.Format("Mon, 02 Jan")
}

func horizonDirectionLabel(direction forecast.HorizonDirection, options Options) string {
	if options.Language != "ru" {
		return string(direction)
	}
	return map[forecast.HorizonDirection]string{
		forecast.HorizonNorth: "С", forecast.HorizonNorthEast: "СВ",
		forecast.HorizonEast: "В", forecast.HorizonSouthEast: "ЮВ",
		forecast.HorizonSouth: "Ю", forecast.HorizonSouthWest: "ЮЗ",
		forecast.HorizonWest: "З", forecast.HorizonNorthWest: "СЗ",
	}[direction]
}

func horizonLimiterLabel(factor forecast.HorizonLimitingFactor, options Options) string {
	labelsRU := map[forecast.HorizonLimitingFactor]string{
		forecast.HorizonFactorNone: "нет", forecast.HorizonFactorUnavailable: "нет данных",
		forecast.HorizonFactorTerrain: "рельеф (грубо)", forecast.HorizonFactorCloud: "эффективная облачная преграда",
		forecast.HorizonFactorSeeing: "оптический сиинг", forecast.HorizonFactorCoherence: "время когерентности τ₀",
		forecast.HorizonFactorFog: "туман", forecast.HorizonFactorSurfaceWind: "приземный ветер",
	}
	labelsEN := map[forecast.HorizonLimitingFactor]string{
		forecast.HorizonFactorNone: "none", forecast.HorizonFactorUnavailable: "unavailable data",
		forecast.HorizonFactorTerrain: "coarse terrain", forecast.HorizonFactorCloud: "effective cloud obstruction",
		forecast.HorizonFactorSeeing: "optical seeing", forecast.HorizonFactorCoherence: "coherence time τ₀",
		forecast.HorizonFactorFog: "fog", forecast.HorizonFactorSurfaceWind: "surface wind",
	}
	label := labelsEN[factor]
	if options.Language == "ru" {
		label = labelsRU[factor]
	}
	if label == "" {
		return localized(options, "неизвестно", "unknown")
	}
	return label
}

func horizonLocation(name string) *time.Location {
	location, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return location
}
