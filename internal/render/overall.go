package render

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"time"

	"bot_astrosferum/internal/astronomy"
	"bot_astrosferum/internal/forecast"

	xfont "golang.org/x/image/font"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/font"
	"gonum.org/v1/plot/palette"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/text"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
)

const (
	OverallWidth  = 4320
	OverallHeight = 1600
	overallYMax   = 12.4
)

var overallStackColors = map[string]color.NRGBA{
	"suitability":                            {R: 28, G: 132, B: 132, A: 238},
	forecast.OverallPenaltyOpticalTurbulence: {R: 112, G: 78, B: 170, A: 238},
	forecast.OverallPenaltyCloudObstruction:  {R: 85, G: 118, B: 145, A: 238},
	forecast.OverallPenaltySurfaceWind:       {R: 230, G: 149, B: 54, A: 238},
	forecast.OverallPenaltyFog:               {R: 55, G: 180, B: 205, A: 238},
	forecast.OverallPenaltyPrecipitation:     {R: 197, G: 48, B: 62, A: 248},
}

var overallPenaltyOrder = []string{
	forecast.OverallPenaltyOpticalTurbulence,
	forecast.OverallPenaltyCloudObstruction,
	forecast.OverallPenaltySurfaceWind,
	forecast.OverallPenaltyFog,
	forecast.OverallPenaltyPrecipitation,
}

// OverallIndex renders the hourly observing-suitability index together with
// an exact additive allocation of the multiplicative loss. The extra vertical
// room above 10 is reserved for the legend, so it never hides forecast bars.
func OverallIndex(destination string, series forecast.VerticalSeries, frames []forecast.OverallIndexFrame, sky astronomy.Series, options Options) error {
	if len(frames) < 2 {
		return fmt.Errorf("overall index chart requires at least two frames")
	}
	if options.Width == 0 {
		options.Width = OverallWidth
	}
	if options.Height == 0 {
		options.Height = OverallHeight
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return err
	}
	p := plot.New()
	stylePlot(p)
	p.Title.TextStyle.Font.Size = vg.Points(30)
	p.X.Label.TextStyle.Font.Size = vg.Points(20)
	p.Y.Label.TextStyle.Font.Size = vg.Points(23)
	timeZoneLabel := forecast.TimeZoneLabel(series.Location.TimeZone, frames[0].ValidAt)
	p.Title.Text = fmt.Sprintf(localized(options, "(%.2f, %.2f) Общий астрономический индекс (1–10) и состав потерь\n%s · почасовой · пригодность снизу, цветные сегменты показывают вклад ограничений до уровня 10", "(%.2f, %.2f) Overall Astronomy Index (1–10) and loss decomposition\n%s · hourly · suitability is shown from the bottom; colored segments show each constraint's contribution up to 10"), series.Location.Latitude, series.Location.Longitude, timeZoneLabel)
	p.X.Label.Text = fmt.Sprintf(localized(options,
		"Местное время · %s  |  подписи: индекс; сиинг″ / τ₀ мс / T%%; ! = неполные входные данные; P = осадки, наблюдение не рекомендуется  |  %s\n○ = эталонная V-полоса в зените (только Солнце <−18°; PWV/AOD/O₃/Луна/PSF, без засветки)  |  Фон: день · светлые сумерки · астрономические сумерки · ночь",
		"Local time · %s  |  labels: index; seeing″ / τ₀ ms / T%%; ! = incomplete inputs; P = precipitation, observing is not recommended  |  %s\n○ = reference V band at zenith (Sun <−18° only; PWV/AOD/O₃/Moon/PSF, no artificial light)  |  Background: day · bright twilight · astronomical twilight · night"),
		timeZoneLabel, Version)
	p.Y.Label.Text = localized(options, "Пригодность для наблюдений (1–10)", "Observing suitability (1–10)")
	p.X.Min, p.X.Max = -0.6, float64(len(frames))-0.4
	p.Y.Min, p.Y.Max = 0, overallYMax
	times := make([]time.Time, len(frames))
	for index := range frames {
		times[index] = frames[index].ValidAt
	}
	p.X.Tick.Marker = plot.ConstantTicks(timeTicks(times, series.Location.TimeZone))
	p.X.Tick.Label.Rotation = math.Pi / 3
	p.X.Tick.Label.XAlign = draw.XRight
	p.X.Tick.Label.YAlign = draw.YCenter
	p.X.Tick.Label.Font.Size = vg.Points(28)
	p.Y.Tick.Label.Font.Size = vg.Points(24)
	p.Y.Tick.Marker = plot.ConstantTicks([]plot.Tick{{Value: 0, Label: "0"}, {Value: 2, Label: "2"}, {Value: 4, Label: "4"}, {Value: 6, Label: "6"}, {Value: 8, Label: "8"}, {Value: 10, Label: "10"}})
	if err := addSolarBackground(p, frames, sky); err != nil {
		return err
	}
	grid := plotter.NewGrid()
	grid.Vertical.Color = color.NRGBA{R: 225, G: 228, B: 232, A: 255}
	grid.Horizontal.Color = color.NRGBA{R: 225, G: 228, B: 232, A: 255}
	p.Add(grid)
	baseValues, penaltyValues, err := overallStackValues(frames)
	if err != nil {
		return err
	}
	baseBars, err := plotter.NewBarChart(baseValues, vg.Points(42))
	if err != nil {
		return err
	}
	baseBars.Color = overallStackColors["suitability"]
	baseBars.LineStyle.Color = color.NRGBA{R: 32, G: 49, B: 57, A: 255}
	baseBars.LineStyle.Width = vg.Points(0.45)
	p.Add(baseBars)
	p.Legend.Add(localized(options, "Сохранившаяся пригодность", "Retained suitability"), baseBars)
	previous := baseBars
	for _, key := range overallPenaltyOrder {
		bars, createErr := plotter.NewBarChart(penaltyValues[key], vg.Points(42))
		if createErr != nil {
			return createErr
		}
		bars.Color = overallStackColors[key]
		bars.LineStyle.Color = color.NRGBA{R: 32, G: 49, B: 57, A: 255}
		bars.LineStyle.Width = vg.Points(0.35)
		bars.StackOn(previous)
		p.Add(bars)
		p.Legend.Add(overallPenaltyLabel(key, options), bars)
		previous = bars
	}
	referenceMarkers, markerErr := overallReferenceVBandMarkers(frames)
	if markerErr != nil {
		return markerErr
	}
	if referenceMarkers != nil {
		p.Add(referenceMarkers)
		p.Legend.Add(localized(options, "Эталон V, зенит", "Reference V, zenith"), referenceMarkers)
	}
	p.Legend.Top = true
	p.Legend.Left = true
	p.Legend.TextStyle.Font.Size = vg.Points(22)
	p.Legend.Padding = vg.Points(2)
	p.Legend.ThumbnailWidth = vg.Points(20)
	addDayBoundaries(p, times, series.Location.TimeZone, 13)
	top, inside, err := overallIndexLabels(frames, magma(96), options)
	if err != nil {
		return err
	}
	p.Add(top, inside)
	return saveAtomic(p, options, destination)
}

func overallReferenceVBandMarkers(frames []forecast.OverallIndexFrame) (*plotter.Scatter, error) {
	points := make(plotter.XYs, 0, len(frames))
	for index, frame := range frames {
		if frame.ReferenceVBand == nil || !frame.ReferenceVBand.Result.Available || frame.PrecipitationVeto {
			continue
		}
		value := frame.ReferenceVBand.Result.Index
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 1 || value > 10 {
			return nil, fmt.Errorf("overall frame %d has invalid Reference V-band index %.6g", index, value)
		}
		points = append(points, plotter.XY{X: float64(index), Y: value})
	}
	if len(points) == 0 {
		return nil, nil
	}
	markers, err := plotter.NewScatter(points)
	if err != nil {
		return nil, err
	}
	markers.GlyphStyle = draw.GlyphStyle{
		Color:  color.NRGBA{R: 12, G: 23, B: 31, A: 255},
		Radius: vg.Points(7), Shape: draw.RingGlyph{},
	}
	return markers, nil
}

func overallPenaltyLabel(key string, options Options) string {
	switch key {
	case forecast.OverallPenaltyOpticalTurbulence:
		return localized(options, "Потеря: оптическая турбулентность", "Loss: optical turbulence")
	case forecast.OverallPenaltyCloudObstruction:
		return localized(options, "Потеря: облачная преграда", "Loss: cloud obstruction")
	case forecast.OverallPenaltySurfaceWind:
		return localized(options, "Потеря: приземный ветер", "Loss: surface wind")
	case forecast.OverallPenaltyFog:
		return localized(options, "Потеря: эвристика тумана", "Loss: fog heuristic")
	case forecast.OverallPenaltyPrecipitation:
		return localized(options, "Запрет: осадки", "Veto: precipitation")
	default:
		return key
	}
}

// overallStackValues converts the unitless Shapley allocation into chart
// points. The displayed index is 1+9Q and each loss segment is 9*phi_i, so
// every complete column closes exactly at 10 (up to floating-point error).
func overallStackValues(frames []forecast.OverallIndexFrame) (plotter.Values, map[string]plotter.Values, error) {
	base := make(plotter.Values, len(frames))
	penalties := make(map[string]plotter.Values, len(overallPenaltyOrder))
	for _, key := range overallPenaltyOrder {
		penalties[key] = make(plotter.Values, len(frames))
	}
	for index, frame := range frames {
		if math.IsNaN(frame.Index) || math.IsInf(frame.Index, 0) || frame.Index < 1-1e-9 || frame.Index > 10+1e-9 {
			return nil, nil, fmt.Errorf("overall frame %d has invalid index %.6g", index, frame.Index)
		}
		base[index] = frame.Index
		seen := make(map[string]struct{}, len(frame.PenaltyContributions))
		loss := 0.0
		for _, contribution := range frame.PenaltyContributions {
			values, known := penalties[contribution.Key]
			if !known {
				return nil, nil, fmt.Errorf("overall frame %d has unknown penalty %q", index, contribution.Key)
			}
			if _, duplicate := seen[contribution.Key]; duplicate {
				return nil, nil, fmt.Errorf("overall frame %d repeats penalty %q", index, contribution.Key)
			}
			seen[contribution.Key] = struct{}{}
			if math.IsNaN(contribution.LossFraction) || math.IsInf(contribution.LossFraction, 0) || contribution.LossFraction < 0 {
				return nil, nil, fmt.Errorf("overall frame %d has invalid %q loss %.6g", index, contribution.Key, contribution.LossFraction)
			}
			value, err := forecast.OverallPenaltyPoints(contribution.LossFraction)
			if err != nil {
				return nil, nil, fmt.Errorf("overall frame %d has invalid %q loss %.6g: %w", index, contribution.Key, contribution.LossFraction, err)
			}
			values[index] = value
			loss += contribution.LossFraction
		}
		if len(frame.PenaltyContributions) == 0 {
			// Compatibility for callers that render historical frames without
			// decomposition. The empty space above the index remains visible.
			continue
		}
		if math.Abs((frame.Index+9*loss)-10) > 1e-7 {
			return nil, nil, fmt.Errorf("overall frame %d penalty stack closes at %.9f instead of 10", index, frame.Index+9*loss)
		}
	}
	return base, penalties, nil
}

type solarPhase int

type solarPhaseInterval struct {
	Start time.Time
	End   time.Time
	Phase solarPhase
}

const (
	solarNight solarPhase = iota
	solarAstronomicalTwilight
	solarBrightTwilight
	solarDay
)

func phaseForSunAltitude(altitude float64) solarPhase {
	switch {
	case altitude >= 0:
		return solarDay
	case altitude >= -12:
		return solarBrightTwilight
	case altitude >= -18:
		return solarAstronomicalTwilight
	default:
		return solarNight
	}
}

func solarPhaseColor(phase solarPhase) color.Color {
	return [...]color.NRGBA{
		{R: 143, G: 163, B: 196, A: 255}, // astronomical night
		{R: 178, G: 193, B: 217, A: 255}, // astronomical twilight
		{R: 224, G: 219, B: 219, A: 255}, // bright twilight: civil + nautical ranges
		{R: 255, G: 242, B: 187, A: 255}, // day
	}[phase]
}

// solarPhaseIntervals resolves visual solar bands independently of the model
// hour. A short scan brackets each 0/-12/-18 degree crossing within five
// minutes; the final raster then rounds only to its own
// pixel grid. This matches the useful resolution of a 72-hour chart without
// claiming second-level precision. Polar day/night needs no special case.
func solarPhaseIntervals(sky astronomy.Series, start, end time.Time) []solarPhaseInterval {
	if start.IsZero() || !end.After(start) {
		return nil
	}
	const scanStep = 5 * time.Minute
	currentStart := start
	previousTime := start
	previousPhase := phaseForSunAltitude(sky.SunAltitudeDegrees(start))
	intervals := make([]solarPhaseInterval, 0, 16)
	for previousTime.Before(end) {
		nextTime := previousTime.Add(scanStep)
		if nextTime.After(end) {
			nextTime = end
		}
		nextPhase := phaseForSunAltitude(sky.SunAltitudeDegrees(nextTime))
		if nextPhase != previousPhase {
			boundary := previousTime.Add(nextTime.Sub(previousTime) / 2)
			intervals = append(intervals, solarPhaseInterval{Start: currentStart, End: boundary, Phase: previousPhase})
			currentStart = boundary
			previousPhase = nextPhase
		}
		previousTime = nextTime
	}
	intervals = append(intervals, solarPhaseInterval{Start: currentStart, End: end, Phase: previousPhase})
	return intervals
}

func addSolarBackground(p *plot.Plot, frames []forecast.OverallIndexFrame, sky astronomy.Series) error {
	first := frames[0].ValidAt
	start := first.Add(-30 * time.Minute)
	end := frames[len(frames)-1].ValidAt.Add(30 * time.Minute)
	for _, interval := range solarPhaseIntervals(sky, start, end) {
		x0 := interval.Start.Sub(first).Hours()
		x1 := interval.End.Sub(first).Hours()
		polygon, err := plotter.NewPolygon(plotter.XYs{
			{X: x0, Y: 0},
			{X: x1, Y: 0},
			{X: x1, Y: overallYMax},
			{X: x0, Y: overallYMax},
		})
		if err != nil {
			return fmt.Errorf("create solar background: %w", err)
		}
		polygon.Color = solarPhaseColor(interval.Phase)
		polygon.Width = 0
		p.Add(polygon)
	}
	return nil
}

func overallIndexLabels(frames []forecast.OverallIndexFrame, _ palette.Palette, options Options) (*plotter.Labels, *plotter.Labels, error) {
	topPoints := make(plotter.XYs, len(frames))
	topText := make([]string, len(frames))
	insidePoints := make(plotter.XYs, len(frames))
	insideText := make([]string, len(frames))
	insideStyles := make([]text.Style, len(frames))
	for index, frame := range frames {
		labelY := frame.Index - 0.22
		if labelY < 0.35 {
			labelY = 0.35
		}
		topPoints[index] = plotter.XY{X: float64(index), Y: labelY}
		status := ""
		if frame.DataCompleteness == forecast.OverallDataPartial {
			status = "!"
		}
		if frame.PrecipitationVeto {
			// Keep the critical marker in the guaranteed Latin glyph set. Emoji
			// fallback varies between production containers and previously made
			// operational warnings disappear from otherwise valid PNGs.
			status = "P"
		}
		topText[index] = fmt.Sprintf("%.1f%s", frame.Index, status)
		insidePoints[index] = plotter.XY{X: float64(index), Y: 0.52}
		seeing := localized(options, "ветер", "wind")
		if frame.PhysicalSeeing {
			seeing = fmt.Sprintf("%.1f″", frame.SeeingArcsec)
		}
		coherence := "τ–"
		if frame.PhysicalCoherence {
			coherence = fmt.Sprintf("τ%.1f", frame.CoherenceTimeMS)
		}
		fog := ""
		switch frame.FogHeuristic {
		case 1:
			fog = " f"
		case 2:
			fog = " F"
		}
		insideText[index] = fmt.Sprintf("%s\n%s\nT%d%%%s", seeing, coherence, int(math.Round(frame.CloudTransmissionPercent)), fog)
		if frame.PrecipitationVeto || frame.Index < 2.5 {
			insideText[index] = ""
		}
		labelFont := font.From(plot.DefaultFont, vg.Points(21))
		labelFont.Weight = xfont.WeightSemiBold
		insideStyles[index] = text.Style{Color: color.White, Font: labelFont, XAlign: draw.XCenter, YAlign: draw.YCenter, Handler: plot.DefaultTextHandler}
	}
	top, err := plotter.NewLabels(plotter.XYLabels{XYs: topPoints, Labels: topText})
	if err != nil {
		return nil, nil, err
	}
	for index := range top.TextStyle {
		top.TextStyle[index].Color = color.White
		top.TextStyle[index].Font.Size = vg.Points(23)
		top.TextStyle[index].Font.Weight = xfont.WeightSemiBold
		top.TextStyle[index].XAlign = draw.XCenter
		top.TextStyle[index].YAlign = draw.YTop
	}
	inside, err := plotter.NewLabels(plotter.XYLabels{XYs: insidePoints, Labels: insideText})
	if err != nil {
		return nil, nil, err
	}
	inside.TextStyle = insideStyles
	return top, inside, nil
}
