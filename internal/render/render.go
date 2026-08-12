package render

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"time"

	"bot_astrosferum/internal/forecast"

	xfont "golang.org/x/image/font"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/font"
	"gonum.org/v1/plot/palette"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/text"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
	"gonum.org/v1/plot/vg/vgimg"
)

const (
	DefaultWidth  = 1280
	DefaultHeight = 960
	Version       = "render-v18-celestial-distance"
)

type Options struct {
	Width    int
	Height   int
	Language string
}

func localized(options Options, russian, english string) string {
	if options.Language == "ru" {
		return russian
	}
	return english
}

type Result struct {
	Weather          string `json:"weather,omitempty"`
	CloudObstruction string `json:"cloud_obstruction,omitempty"`
	WindSpeed        string `json:"wind_speed"`
	VectorShear      string `json:"vector_shear"`
	DirectionDelta   string `json:"direction_delta"`
	SeeingIndex      string `json:"seeing_index"`
	OverallIndex     string `json:"overall_index,omitempty"`
	Dataset          string `json:"dataset,omitempty"`
}

type heatSpec struct {
	fileName  string
	title     string
	unit      string
	values    [][]float64
	minimum   float64
	maximum   float64
	decimals  int
	labelSize vg.Length
}

// All writes four independent PNG files using one validated forecast bundle.
func All(outputDirectory string, series forecast.VerticalSeries, options Options) (Result, error) {
	if outputDirectory == "" {
		return Result{}, fmt.Errorf("output directory is required")
	}
	if options.Width == 0 {
		options.Width = DefaultWidth
	}
	if options.Height == 0 {
		options.Height = DefaultHeight
	}
	if options.Width < 640 || options.Height < 480 {
		return Result{}, fmt.Errorf("render size must be at least 640x480")
	}
	diagnostics, err := forecast.ComputeDiagnostics(series)
	if err != nil {
		return Result{}, fmt.Errorf("compute diagnostics: %w", err)
	}
	return AllDiagnostics(outputDirectory, series, diagnostics, options)
}

// AllDiagnostics writes the four upper-air PNG views from one already
// prepared diagnostics bundle. The interactive website serializes this exact
// bundle, so the browser and the static bot charts cannot drift because of a
// second calculation.
func AllDiagnostics(outputDirectory string, series forecast.VerticalSeries, diagnostics forecast.Diagnostics, options Options) (Result, error) {
	if outputDirectory == "" {
		return Result{}, fmt.Errorf("output directory is required")
	}
	if options.Width == 0 {
		options.Width = DefaultWidth
	}
	if options.Height == 0 {
		options.Height = DefaultHeight
	}
	if options.Width < 640 || options.Height < 480 {
		return Result{}, fmt.Errorf("render size must be at least 640x480")
	}
	if len(diagnostics.Times) != len(series.Frames) || len(diagnostics.PressureHPA) < 2 {
		return Result{}, fmt.Errorf("prepared upper-air diagnostics do not match the forecast series")
	}
	if err := os.MkdirAll(outputDirectory, 0o750); err != nil {
		return Result{}, fmt.Errorf("create output directory: %w", err)
	}

	result := Result{
		WindSpeed:      filepath.Join(outputDirectory, "wind-speed.png"),
		VectorShear:    filepath.Join(outputDirectory, "wind-vector-shear.png"),
		DirectionDelta: filepath.Join(outputDirectory, "wind-direction-delta.png"),
		SeeingIndex:    filepath.Join(outputDirectory, "forecast-seeing-index.png"),
	}
	specs := []heatSpec{
		{fileName: result.WindSpeed, title: localized(options, "Скорость ветра по уровням давления", "Wind Speed vs Pressure"), unit: localized(options, "м/с", "m/s"), values: diagnostics.WindSpeedMS, minimum: 0, maximum: 60, decimals: 1},
		{fileName: result.VectorShear, title: localized(options, "Вертикальный векторный сдвиг ветра по уровням давления", "Vertical Vector Wind Shear vs Pressure"), unit: localized(options, "м/с на км", "m/s per km"), values: diagnostics.VectorShearMSPerKM, minimum: 0, maximum: 20, decimals: 1},
		{fileName: result.DirectionDelta, title: localized(options, "Изменение направления ветра по уровням давления", "Wind Direction Delta vs Pressure"), unit: localized(options, "градусы", "degrees"), values: diagnostics.DirectionDelta, minimum: 0, maximum: 120, decimals: 0},
	}
	for _, spec := range specs {
		if err := heatMap(spec, series, diagnostics, options); err != nil {
			return Result{}, err
		}
	}
	if err := seeingBars(result.SeeingIndex, series, diagnostics, options); err != nil {
		return Result{}, err
	}
	return result, nil
}

func heatMap(spec heatSpec, series forecast.VerticalSeries, diagnostics forecast.Diagnostics, options Options) error {
	p := plot.New()
	stylePlot(p)
	timeZoneLabel := forecast.TimeZoneLabel(series.Location.TimeZone, diagnostics.Times[0])
	p.Title.Text = fmt.Sprintf("(%.2f, %.2f) %s (%s)\n%s", series.Location.Latitude, series.Location.Longitude, spec.title, spec.unit, timeZoneLabel)
	p.X.Label.Text = footer(series, timeZoneLabel, options)
	p.Y.Label.Text = localized(options, "Давление (гПа)", "Pressure (hPa)")
	p.X.Min, p.X.Max = -0.5, float64(len(diagnostics.Times))-0.5
	p.Y.Min, p.Y.Max = -0.5, float64(len(diagnostics.PressureHPA))-0.5
	p.X.Tick.Marker = plot.ConstantTicks(timeTicks(diagnostics.Times, series.Location.TimeZone))
	p.Y.Tick.Marker = plot.ConstantTicks(pressureTicks(diagnostics.PressureHPA))
	p.X.Tick.Label.Rotation = math.Pi / 3
	p.X.Tick.Label.XAlign = draw.XRight
	p.X.Tick.Label.YAlign = draw.YCenter
	p.X.Tick.Label.Font.Size = vg.Points(11)
	p.Y.Tick.Label.Font.Size = vg.Points(14)

	grid := &matrixGrid{values: spec.values}
	colors := magma(96)
	heat := plotter.NewHeatMap(grid, colors)
	heat.Min = spec.minimum
	heat.Max = spec.maximum
	heat.NaN = color.NRGBA{R: 205, G: 209, B: 214, A: 255}
	heat.Underflow = colors.Colors()[0]
	heat.Overflow = colors.Colors()[len(colors.Colors())-1]
	heat.Rasterized = true
	p.Add(heat)
	addDayBoundaries(p, diagnostics.Times, series.Location.TimeZone, len(diagnostics.PressureHPA))

	labels, err := heatLabels(spec, colors)
	if err != nil {
		return fmt.Errorf("create %s labels: %w", spec.title, err)
	}
	p.Add(labels)
	return saveHeatAtomic(p, options, spec.fileName, diagnostics.HeightKM, colors, spec.minimum, spec.maximum, spec.unit)
}

// CloudObstruction renders effective blocked-sky fraction from ICON CLC and
// phase-resolved QC/QI instead of presenting cloud cover as opacity.
func CloudObstruction(destination string, series forecast.CloudSeries, calibration forecast.OverallIndexCalibration, options Options) error {
	if options.Width == 0 {
		options.Width = 3200
	}
	if options.Height == 0 {
		options.Height = 1100
	}
	diagnostics, err := forecast.ComputeCloudDiagnostics(series)
	if err != nil {
		return err
	}
	obstruction, err := forecast.ComputeCloudObstruction(diagnostics, calibration)
	if err != nil {
		return err
	}
	return CloudObstructionDiagnostics(destination, series, diagnostics, obstruction, options)
}

// CloudObstructionDiagnostics renders the already computed physical
// obstruction matrix used by the interactive dataset.
func CloudObstructionDiagnostics(destination string, series forecast.CloudSeries, diagnostics forecast.CloudDiagnostics, obstruction [][]float64, options Options) error {
	if options.Width == 0 {
		options.Width = 3200
	}
	if options.Height == 0 {
		options.Height = 1100
	}
	if len(diagnostics.Times) != len(series.Frames) || len(obstruction) != len(diagnostics.PressureHPA) {
		return fmt.Errorf("prepared cloud diagnostics do not match the forecast series")
	}
	p := plot.New()
	stylePlot(p)
	p.Title.TextStyle.Font.Size = vg.Points(19)
	p.X.Label.TextStyle.Font.Size = vg.Points(12)
	p.Y.Label.TextStyle.Font.Size = vg.Points(14)
	zone := forecast.TimeZoneLabel(series.Location.TimeZone, diagnostics.Times[0])
	p.Title.Text = fmt.Sprintf(localized(options, "(%.2f, %.2f) Эффективная облачная преграда ICON: CLC + QC/QI (%%)\n%s", "(%.2f, %.2f) ICON Effective Cloud Obstruction: CLC + QC/QI (%%)\n%s"), series.Location.Latitude, series.Location.Longitude, zone)
	p.X.Label.Text = fmt.Sprintf(localized(options, "Почасовое местное время · %s  |  видимая оптическая толщина QC/QI + высотно-зависимая защита от неопределённости CLC (нижние > средние > верхние)  |  run %s UTC  |  %s", "Hourly local time · %s  |  QC/QI visible optical depth + height-aware CLC uncertainty guard (low > middle > high)  |  run %s UTC  |  %s"), zone, series.RunID, Version)
	p.Y.Label.Text = localized(options, "Давление (гПа)", "Pressure (hPa)")
	p.X.Min, p.X.Max = -0.5, float64(len(diagnostics.Times))-0.5
	p.Y.Min, p.Y.Max = -0.5, float64(len(diagnostics.PressureHPA))-0.5
	p.X.Tick.Marker = plot.ConstantTicks(timeTicks(diagnostics.Times, series.Location.TimeZone))
	p.Y.Tick.Marker = plot.ConstantTicks(pressureTicks(diagnostics.PressureHPA))
	p.X.Tick.Label.Rotation = math.Pi / 3
	p.X.Tick.Label.XAlign = draw.XRight
	p.X.Tick.Label.YAlign = draw.YCenter
	p.X.Tick.Label.Font.Size = vg.Points(16)
	p.Y.Tick.Label.Font.Size = vg.Points(16)
	colors := magma(96)
	heat := plotter.NewHeatMap(&matrixGrid{values: obstruction}, colors)
	heat.Min, heat.Max, heat.Rasterized = 0, 100, true
	heat.NaN = color.NRGBA{R: 205, G: 209, B: 214, A: 255}
	p.Add(heat)
	addDayBoundaries(p, diagnostics.Times, series.Location.TimeZone, len(diagnostics.HeightKM))
	labels, err := heatLabels(heatSpec{values: obstruction, minimum: 0, maximum: 100, decimals: 0, labelSize: vg.Points(13.5)}, colors)
	if err != nil {
		return err
	}
	p.Add(labels)
	return saveHeatAtomic(p, options, destination, diagnostics.HeightKM, colors, 0, 100, "%")
}

func seeingBars(fileName string, series forecast.VerticalSeries, diagnostics forecast.Diagnostics, options Options) error {
	p := plot.New()
	stylePlot(p)
	timeZoneLabel := forecast.TimeZoneLabel(series.Location.TimeZone, diagnostics.Times[0])
	p.Title.Text = fmt.Sprintf(localized(options, "(%.2f, %.2f) Прогнозный индекс сиинга по ветру (прототип 1–10)\n%s · Условная уверенность зависит только от дальности срока и не входит в общий индекс пригодности", "(%.2f, %.2f) Forecast Wind Seeing Index (prototype 1–10)\n%s · Confidence is conditional on lead time only; it is not part of Overall Astronomy Index"), series.Location.Latitude, series.Location.Longitude, timeZoneLabel)
	p.X.Label.Text = footer(series, timeZoneLabel, options)
	p.Y.Label.Text = localized(options, "Прогнозный индекс (1–10)", "Forecast index (1–10)")
	p.X.Min, p.X.Max = -0.6, float64(len(diagnostics.Times))-0.4
	p.Y.Min, p.Y.Max = 0, 10.6
	p.X.Tick.Marker = plot.ConstantTicks(timeTicks(diagnostics.Times, series.Location.TimeZone))
	p.X.Tick.Label.Rotation = math.Pi / 3
	p.X.Tick.Label.XAlign = draw.XRight
	p.X.Tick.Label.YAlign = draw.YCenter
	p.X.Tick.Label.Font.Size = vg.Points(11)
	p.Y.Tick.Label.Font.Size = vg.Points(14)
	p.Y.Tick.Marker = plot.ConstantTicks([]plot.Tick{
		{Value: 0, Label: "0"}, {Value: 2, Label: "2"}, {Value: 4, Label: "4"},
		{Value: 6, Label: "6"}, {Value: 8, Label: "8"}, {Value: 10, Label: "10"},
	})
	grid := plotter.NewGrid()
	grid.Vertical.Color = color.NRGBA{R: 225, G: 228, B: 232, A: 255}
	grid.Horizontal.Color = color.NRGBA{R: 225, G: 228, B: 232, A: 255}
	p.Add(grid)
	barPalette := magma(96)
	for index, value := range diagnostics.SeeingIndex {
		if math.IsNaN(value) {
			continue
		}
		bar, err := plotter.NewBarChart(plotter.Values{value}, vg.Points(24))
		if err != nil {
			return fmt.Errorf("create seeing bar: %w", err)
		}
		bar.XMin = float64(index)
		bar.Color = paletteColor(barPalette, value, 1, 10)
		bar.LineStyle.Color = color.NRGBA{R: 70, G: 60, B: 80, A: 255}
		bar.LineStyle.Width = vg.Points(0.35)
		p.Add(bar)

	}
	addDayBoundaries(p, diagnostics.Times, series.Location.TimeZone, 11)
	labels, err := seeingLabels(diagnostics)
	if err != nil {
		return fmt.Errorf("create seeing labels: %w", err)
	}
	p.Add(labels)
	confidence, err := seeingConfidenceLabels(diagnostics, barPalette)
	if err != nil {
		return fmt.Errorf("create seeing confidence labels: %w", err)
	}
	p.Add(confidence)
	return saveAtomic(p, options, fileName)
}

func stylePlot(p *plot.Plot) {
	p.BackgroundColor = color.White
	p.Title.TextStyle.Font.Size = vg.Points(16)
	p.Title.Padding = vg.Points(8)
	p.X.Label.TextStyle.Font.Size = vg.Points(8)
	p.X.Label.Padding = vg.Points(20)
	p.Y.Label.TextStyle.Font.Size = vg.Points(11)
	p.Legend.TextStyle.Font.Size = vg.Points(9)
	axisColor := color.NRGBA{R: 55, G: 58, B: 64, A: 255}
	p.X.Color = axisColor
	p.Y.Color = axisColor
	p.X.Tick.Label.Color = axisColor
	p.Y.Tick.Label.Color = axisColor
}

func heatLabels(spec heatSpec, colors palette.Palette) (*plotter.Labels, error) {
	points := make(plotter.XYs, 0, len(spec.values)*len(spec.values[0]))
	strings := make([]string, 0, cap(points))
	styles := make([]text.Style, 0, cap(points))
	for row, values := range spec.values {
		for column, value := range values {
			label := "×"
			var labelColor color.Color = color.NRGBA{R: 90, G: 94, B: 100, A: 255}
			if !math.IsNaN(value) {
				label = fmt.Sprintf("%.*f", spec.decimals, value)
				labelColor = contrastColor(paletteColor(colors, value, spec.minimum, spec.maximum))
			}
			points = append(points, plotter.XY{X: float64(column), Y: float64(row)})
			strings = append(strings, label)
			size := spec.labelSize
			if size == 0 {
				size = vg.Points(8)
			}
			labelFont := font.From(plot.DefaultFont, size)
			labelFont.Weight = xfont.WeightSemiBold
			styles = append(styles, text.Style{
				Color:   labelColor,
				Font:    labelFont,
				XAlign:  draw.XCenter,
				YAlign:  draw.YCenter,
				Handler: plot.DefaultTextHandler,
			})
		}
	}
	labels, err := plotter.NewLabels(plotter.XYLabels{XYs: points, Labels: strings})
	if err != nil {
		return nil, err
	}
	labels.TextStyle = styles
	return labels, nil
}

func seeingLabels(diagnostics forecast.Diagnostics) (*plotter.Labels, error) {
	points := make(plotter.XYs, 0, len(diagnostics.SeeingIndex))
	strings := make([]string, 0, len(diagnostics.SeeingIndex))
	for index, value := range diagnostics.SeeingIndex {
		if math.IsNaN(value) {
			continue
		}
		points = append(points, plotter.XY{X: float64(index), Y: value + 0.18})
		strings = append(strings, fmt.Sprintf("%.1f", value))
	}
	labels, err := plotter.NewLabels(plotter.XYLabels{XYs: points, Labels: strings})
	if err != nil {
		return nil, err
	}
	for index := range labels.TextStyle {
		labels.TextStyle[index].Font.Size = vg.Points(10)
		labels.TextStyle[index].Font.Weight = xfont.WeightSemiBold
		labels.TextStyle[index].XAlign = draw.XCenter
		labels.TextStyle[index].YAlign = draw.YBottom
		labels.TextStyle[index].Color = color.NRGBA{R: 42, G: 44, B: 50, A: 255}
	}
	return labels, nil
}

func seeingConfidenceLabels(diagnostics forecast.Diagnostics, colors palette.Palette) (*plotter.Labels, error) {
	points := make(plotter.XYs, 0, len(diagnostics.SeeingIndex))
	strings := make([]string, 0, len(diagnostics.SeeingIndex))
	styles := make([]text.Style, 0, len(diagnostics.SeeingIndex))
	for index, value := range diagnostics.SeeingIndex {
		if math.IsNaN(value) {
			continue
		}
		points = append(points, plotter.XY{X: float64(index), Y: 0.32})
		strings = append(strings, fmt.Sprintf("%.0f%%", diagnostics.Confidence[index]*100))
		labelFont := font.From(plot.DefaultFont, vg.Points(8))
		labelFont.Weight = xfont.WeightSemiBold
		styles = append(styles, text.Style{
			Color:   contrastColor(paletteColor(colors, value, 1, 10)),
			Font:    labelFont,
			XAlign:  draw.XCenter,
			YAlign:  draw.YCenter,
			Handler: plot.DefaultTextHandler,
		})
	}
	labels, err := plotter.NewLabels(plotter.XYLabels{XYs: points, Labels: strings})
	if err != nil {
		return nil, err
	}
	labels.TextStyle = styles
	return labels, nil
}

func addDayBoundaries(p *plot.Plot, times []time.Time, timeZone string, height int) {
	location, err := time.LoadLocation(timeZone)
	if err != nil {
		location = time.UTC
	}
	for index := 1; index < len(times); index++ {
		previous := times[index-1].In(location)
		current := times[index].In(location)
		if previous.Day() == current.Day() {
			continue
		}
		line, err := plotter.NewLine(plotter.XYs{{X: float64(index) - 0.5, Y: -0.7}, {X: float64(index) - 0.5, Y: float64(height) - 0.3}})
		if err != nil {
			continue
		}
		line.Color = color.NRGBA{R: 245, G: 245, B: 245, A: 210}
		line.Width = vg.Points(1.4)
		p.Add(line)
	}
}

func timeTicks(times []time.Time, timeZone string) []plot.Tick {
	location, err := time.LoadLocation(timeZone)
	if err != nil {
		location = time.UTC
	}
	ticks := make([]plot.Tick, len(times))
	for index, timestamp := range times {
		local := timestamp.In(location)
		label := local.Format("02 15:04")
		if index > 0 && local.Day() == times[index-1].In(location).Day() {
			label = local.Format("15:04")
		}
		ticks[index] = plot.Tick{Value: float64(index), Label: label}
	}
	return ticks
}

func pressureTicks(pressures []float64) []plot.Tick {
	ticks := make([]plot.Tick, len(pressures))
	for index, pressure := range pressures {
		ticks[index] = plot.Tick{Value: float64(index), Label: fmt.Sprintf("%.0f", pressure)}
	}
	return ticks
}

func footer(series forecast.VerticalSeries, timeZoneLabel string, options Options) string {
	return fmt.Sprintf(localized(options, "Местное время · %s  |  %s / %s  |  run %s UTC  |  сетка %s  |  %s / %s", "Local time · %s  |  %s / %s  |  run %s UTC  |  grid %s  |  %s / %s"),
		timeZoneLabel, series.Provider, series.Product, series.RunID, series.Grid, series.AlgorithmVersion, Version)
}

func saveAtomic(p *plot.Plot, options Options, destination string) error {
	width := vg.Length(float64(options.Width)/96) * vg.Inch
	height := vg.Length(float64(options.Height)/96) * vg.Inch
	imageCanvas := vgimg.NewWith(vgimg.UseWH(width, height), vgimg.UseDPI(96), vgimg.UseBackgroundColor(color.White))
	p.Draw(draw.New(imageCanvas))
	if err := savePNGImageAtomic(imageCanvas.Image(), destination); err != nil {
		return fmt.Errorf("render %s: %w", filepath.Base(destination), err)
	}
	return nil
}

func saveHeatAtomic(p *plot.Plot, options Options, destination string, heights []float64, colors palette.Palette, minimum, maximum float64, unit string) error {
	width := vg.Length(float64(options.Width)/96) * vg.Inch
	height := vg.Length(float64(options.Height)/96) * vg.Inch
	imageCanvas := vgimg.NewWith(vgimg.UseWH(width, height), vgimg.UseDPI(96), vgimg.UseBackgroundColor(color.White))
	full := draw.New(imageCanvas)
	plotCanvas := draw.Crop(full, 0, -vg.Points(92), vg.Points(54), 0)
	p.Draw(plotCanvas)
	data := p.DataCanvas(plotCanvas)
	axisX := data.Max.X + vg.Points(7)
	axisStyle := draw.LineStyle{Color: color.NRGBA{R: 55, G: 58, B: 64, A: 255}, Width: vg.Points(0.7)}
	full.StrokeLine2(axisStyle, axisX, data.Min.Y, axisX, data.Max.Y)
	supplementalSize := vg.Points(14)
	if options.Width >= 3000 {
		supplementalSize = vg.Points(16)
	}
	labelStyle := text.Style{Font: font.From(plot.DefaultFont, supplementalSize), Color: color.NRGBA{R: 55, G: 58, B: 64, A: 255}, XAlign: draw.XLeft, YAlign: draw.YCenter, Handler: plot.DefaultTextHandler}
	for index, heightKM := range heights {
		y := data.Y(p.Y.Norm(float64(index)))
		full.StrokeLine2(axisStyle, axisX, y, axisX+vg.Points(3), y)
		full.FillText(labelStyle, vg.Point{X: axisX + vg.Points(5), Y: y}, fmt.Sprintf("%.1f", heightKM))
	}
	headingStyle := labelStyle
	headingStyle.Font.Size = supplementalSize + vg.Points(1)
	headingStyle.Font.Weight = xfont.WeightSemiBold
	full.FillText(headingStyle, vg.Point{X: axisX, Y: data.Max.Y + vg.Points(12)}, localized(options, "Высота (км)", "Height (km)"))
	barLeft, barRight := full.Min.X+vg.Points(130), full.Max.X-vg.Points(130)
	barBottom, barTop := full.Min.Y+vg.Points(22), full.Min.Y+vg.Points(34)
	items := colors.Colors()
	for index, item := range items {
		x0 := barLeft + (barRight-barLeft)*vg.Length(float64(index)/float64(len(items)))
		x1 := barLeft + (barRight-barLeft)*vg.Length(float64(index+1)/float64(len(items)))
		full.FillPolygon(item, []vg.Point{{X: x0, Y: barBottom}, {X: x1, Y: barBottom}, {X: x1, Y: barTop}, {X: x0, Y: barTop}})
	}
	legendStyle := labelStyle
	legendStyle.YAlign = draw.YBottom
	full.FillText(legendStyle, vg.Point{X: barLeft, Y: barTop + vg.Points(3)}, fmt.Sprintf(localized(options, "%.0f %s · тёмный = мало", "%.0f %s · dark = low"), minimum, unit))
	legendStyle.XAlign = draw.XCenter
	full.FillText(legendStyle, vg.Point{X: (barLeft + barRight) / 2, Y: barTop + vg.Points(3)}, fmt.Sprintf("%.0f %s", (minimum+maximum)/2, unit))
	legendStyle.XAlign = draw.XRight
	full.FillText(legendStyle, vg.Point{X: barRight, Y: barTop + vg.Points(3)}, fmt.Sprintf(localized(options, "%.0f %s · светлый = много", "%.0f %s · light = high"), maximum, unit))
	if err := savePNGImageAtomic(imageCanvas.Image(), destination); err != nil {
		return fmt.Errorf("render %s: %w", filepath.Base(destination), err)
	}
	return nil
}

func savePNGImageAtomic(source image.Image, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("create chart directory: %w", err)
	}
	temporary := destination + ".part.png"
	defer func() { _ = os.Remove(temporary) }()
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(file, source); err != nil {
		_ = file.Close()
		return fmt.Errorf("encode lossless PNG: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return fmt.Errorf("publish PNG: %w", err)
	}
	return nil
}

type matrixGrid struct {
	values [][]float64
}

func (grid *matrixGrid) Dims() (columns, rows int) {
	return len(grid.values[0]), len(grid.values)
}

func (grid *matrixGrid) Z(column, row int) float64 { return grid.values[row][column] }
func (grid *matrixGrid) X(column int) float64      { return float64(column) }
func (grid *matrixGrid) Y(row int) float64         { return float64(row) }

type colorList []color.Color

func (colors colorList) Colors() []color.Color { return colors }

func magma(count int) palette.Palette {
	anchors := []color.NRGBA{
		{R: 0, G: 0, B: 4, A: 255},
		{R: 48, G: 18, B: 92, A: 255},
		{R: 112, G: 31, B: 128, A: 255},
		{R: 181, G: 54, B: 122, A: 255},
		{R: 236, G: 100, B: 96, A: 255},
		{R: 253, G: 175, B: 112, A: 255},
		{R: 252, G: 253, B: 191, A: 255},
	}
	result := make(colorList, count)
	for index := range result {
		position := float64(index) / float64(count-1) * float64(len(anchors)-1)
		left := int(math.Floor(position))
		right := int(math.Ceil(position))
		fraction := position - float64(left)
		result[index] = interpolate(anchors[left], anchors[right], fraction)
	}
	return result
}

func interpolate(a, b color.NRGBA, amount float64) color.NRGBA {
	mix := func(left, right uint8) uint8 {
		return uint8(math.Round(float64(left) + (float64(right)-float64(left))*amount))
	}
	return color.NRGBA{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), A: 255}
}

func paletteColor(colors palette.Palette, value, minimum, maximum float64) color.Color {
	items := colors.Colors()
	position := (value - minimum) / (maximum - minimum)
	position = math.Max(0, math.Min(1, position))
	return items[int(math.Round(position*float64(len(items)-1)))]
}

func contrastColor(background color.Color) color.Color {
	r, g, b, _ := background.RGBA()
	luminance := 0.2126*float64(r>>8) + 0.7152*float64(g>>8) + 0.0722*float64(b>>8)
	if luminance < 145 {
		return color.White
	}
	return color.NRGBA{R: 25, G: 26, B: 30, A: 255}
}
