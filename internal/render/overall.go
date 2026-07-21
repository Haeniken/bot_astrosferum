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

// OverallIndex renders the hourly observing-suitability index. The main label
// is the 1..10 result; the compact label inside each bar exposes the two model
// inputs so a low value is not presented as a black box.
func OverallIndex(destination string, series forecast.VerticalSeries, frames []forecast.OverallIndexFrame, sky astronomy.Series, options Options) error {
	if len(frames) < 2 {
		return fmt.Errorf("overall index chart requires at least two frames")
	}
	if options.Width == 0 {
		options.Width = 3200
	}
	if options.Height == 0 {
		options.Height = 960
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return err
	}
	p := plot.New()
	stylePlot(p)
	timeZoneLabel := forecast.TimeZoneLabel(series.Location.TimeZone, frames[0].ValidAt)
	p.Title.Text = fmt.Sprintf(localized(options, "(%.2f, %.2f) Общий индекс пригодности для астрономии (1–10)\n%s · почасовой · ICON TKE до динамической MH (500–2000 м над землёй) + HMNSP99 выше · сиинг и τ₀ на 500 нм · эффективная облачная преграда + туман", "(%.2f, %.2f) Overall Astronomy Index (1–10)\n%s · hourly · ICON TKE to dynamic MH (500–2000 m AGL) + HMNSP99 aloft · seeing and tau0 at 500 nm · effective cloud obstruction + fog"), series.Location.Latitude, series.Location.Longitude, timeZoneLabel)
	p.X.Label.Text = fmt.Sprintf(localized(options, "Местное время · %s  |  подписи: сиинг″ / τ₀ мс / T%%; T = эффективное пропускание облаков; f/F = возможный/сильный туман; MH = почасовая глубина перемешанного слоя ICON  |  %s", "Local time · %s  |  stacked labels: seeing″ / τ₀ ms / T%%; T = effective cloud transmission; f/F = possible/high fog; MH = hourly ICON mixed-layer depth  |  %s"), timeZoneLabel, Version)
	p.Y.Label.Text = localized(options, "Пригодность для наблюдений (1–10)", "Observing suitability (1–10)")
	p.X.Min, p.X.Max = -0.6, float64(len(frames))-0.4
	p.Y.Min, p.Y.Max = 0, 10.8
	times := make([]time.Time, len(frames))
	for index := range frames {
		times[index] = frames[index].ValidAt
	}
	p.X.Tick.Marker = plot.ConstantTicks(timeTicks(times, series.Location.TimeZone))
	p.X.Tick.Label.Rotation = math.Pi / 3
	p.X.Tick.Label.XAlign = draw.XRight
	p.X.Tick.Label.YAlign = draw.YCenter
	p.X.Tick.Label.Font.Size = vg.Points(8)
	p.Y.Tick.Marker = plot.ConstantTicks([]plot.Tick{{Value: 0, Label: "0"}, {Value: 2, Label: "2"}, {Value: 4, Label: "4"}, {Value: 6, Label: "6"}, {Value: 8, Label: "8"}, {Value: 10, Label: "10"}})
	if err := addSolarBackground(p, frames, sky); err != nil {
		return err
	}
	grid := plotter.NewGrid()
	grid.Vertical.Color = color.NRGBA{R: 225, G: 228, B: 232, A: 255}
	grid.Horizontal.Color = color.NRGBA{R: 225, G: 228, B: 232, A: 255}
	p.Add(grid)
	colors := magma(96)
	for index, frame := range frames {
		bar, err := plotter.NewBarChart(plotter.Values{frame.Index}, vg.Points(22))
		if err != nil {
			return err
		}
		bar.XMin = float64(index)
		bar.Color = paletteColor(colors, frame.Index, 1, 10)
		bar.LineStyle.Color = color.NRGBA{R: 70, G: 60, B: 80, A: 255}
		bar.LineStyle.Width = vg.Points(0.3)
		p.Add(bar)
	}
	addDayBoundaries(p, times, series.Location.TimeZone, 11)
	top, inside, err := overallIndexLabels(frames, colors, options)
	if err != nil {
		return err
	}
	p.Add(top, inside)
	return saveAtomic(p, options, destination)
}

type solarPhase int

const (
	solarNight solarPhase = iota
	solarAstronomicalTwilight
	solarNauticalTwilight
	solarCivilTwilight
	solarDay
)

func phaseForSunAltitude(altitude float64) solarPhase {
	switch {
	case altitude >= 0:
		return solarDay
	case altitude >= -6:
		return solarCivilTwilight
	case altitude >= -12:
		return solarNauticalTwilight
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
		{R: 207, G: 218, B: 235, A: 255}, // nautical twilight
		{R: 240, G: 220, B: 202, A: 255}, // civil twilight
		{R: 255, G: 242, B: 187, A: 255}, // day
	}[phase]
}

func addSolarBackground(p *plot.Plot, frames []forecast.OverallIndexFrame, sky astronomy.Series) error {
	for index, frame := range frames {
		phase := phaseForSunAltitude(sky.SunAltitudeDegrees(frame.ValidAt))
		polygon, err := plotter.NewPolygon(plotter.XYs{
			{X: float64(index) - 0.5, Y: 0},
			{X: float64(index) + 0.5, Y: 0},
			{X: float64(index) + 0.5, Y: 10.8},
			{X: float64(index) - 0.5, Y: 10.8},
		})
		if err != nil {
			return fmt.Errorf("create solar background: %w", err)
		}
		polygon.Color = solarPhaseColor(phase)
		polygon.LineStyle.Width = 0
		p.Add(polygon)
	}
	return nil
}

func overallIndexLabels(frames []forecast.OverallIndexFrame, colors palette.Palette, options Options) (*plotter.Labels, *plotter.Labels, error) {
	topPoints := make(plotter.XYs, len(frames))
	topText := make([]string, len(frames))
	insidePoints := make(plotter.XYs, len(frames))
	insideText := make([]string, len(frames))
	insideStyles := make([]text.Style, len(frames))
	for index, frame := range frames {
		topPoints[index] = plotter.XY{X: float64(index), Y: frame.Index + 0.12}
		topText[index] = fmt.Sprintf("%.1f", frame.Index)
		insidePoints[index] = plotter.XY{X: float64(index), Y: 0.72}
		seeing := localized(options, "ветер", "wind")
		if frame.PhysicalSeeing {
			seeing = fmt.Sprintf("%.1f″", frame.SeeingArcsec)
		}
		coherence := "τ–"
		if frame.PhysicalCoherence {
			coherence = fmt.Sprintf("τ%.1f", frame.CoherenceTimeMS)
		}
		fog := ""
		switch frame.FogRisk {
		case 1:
			fog = " f"
		case 2:
			fog = " F"
		}
		insideText[index] = fmt.Sprintf("%s\n%s\nT%d%%%s", seeing, coherence, int(math.Round(frame.CloudTransmissionPercent)), fog)
		labelFont := font.From(plot.DefaultFont, vg.Points(8.5))
		labelFont.Weight = xfont.WeightSemiBold
		insideStyles[index] = text.Style{Color: contrastColor(paletteColor(colors, frame.Index, 1, 10)), Font: labelFont, XAlign: draw.XCenter, YAlign: draw.YCenter, Handler: plot.DefaultTextHandler}
	}
	top, err := plotter.NewLabels(plotter.XYLabels{XYs: topPoints, Labels: topText})
	if err != nil {
		return nil, nil, err
	}
	for index := range top.TextStyle {
		top.TextStyle[index].Font.Size = vg.Points(10)
		top.TextStyle[index].Font.Weight = xfont.WeightSemiBold
		top.TextStyle[index].XAlign = draw.XCenter
		top.TextStyle[index].YAlign = draw.YBottom
	}
	inside, err := plotter.NewLabels(plotter.XYLabels{XYs: insidePoints, Labels: insideText})
	if err != nil {
		return nil, nil, err
	}
	inside.TextStyle = insideStyles
	return top, inside, nil
}
