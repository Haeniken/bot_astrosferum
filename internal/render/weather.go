package render

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"time"

	"bot_astrosferum/internal/astronomy"
	"bot_astrosferum/internal/forecast"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const (
	WeatherWidth          = 3200
	WeatherHeight         = 1080
	weatherMainBottom     = 822
	weatherPostCloudShift = 14
	weatherCloudHeaderY   = 195
	weatherLowCloudY      = 214
	weatherMidCloudY      = 234
	weatherHighCloudY     = 254
	weatherTransparencyY  = 276
	weatherLegendShift    = weatherPostCloudShift + 28
)

var (
	weatherBackground           = color.RGBA{R: 12, G: 42, B: 82, A: 255}
	weatherDay                  = color.RGBA{R: 16, G: 68, B: 119, A: 255}
	weatherBrightTwilight       = color.RGBA{R: 13, G: 53, B: 101, A: 255}
	weatherAstronomicalTwilight = color.RGBA{R: 9, G: 37, B: 76, A: 255}
	weatherNight                = color.RGBA{R: 4, G: 22, B: 51, A: 255}
	weatherGrid                 = color.RGBA{R: 38, G: 83, B: 126, A: 255}
	weatherText                 = color.RGBA{R: 232, G: 239, B: 247, A: 255}
	weatherMuted                = color.RGBA{R: 119, G: 153, B: 190, A: 255}
	weatherOrange               = color.RGBA{R: 255, G: 124, B: 45, A: 255}
	weatherCyan                 = color.RGBA{R: 64, G: 192, B: 236, A: 255}
)

type weatherFonts struct {
	tiny, small, normal, normalBold, large, title font.Face
}

func Weather(destination string, surface forecast.SurfaceSeries, sky astronomy.Series, options Options) error {
	if len(surface.Frames) < 2 {
		return fmt.Errorf("weather chart requires at least two surface frames")
	}
	fonts, closeFonts, err := newWeatherFonts()
	if err != nil {
		return err
	}
	defer closeFonts()

	canvas := image.NewRGBA(image.Rect(0, 0, WeatherWidth, WeatherHeight))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: weatherBackground}, image.Point{}, draw.Src)
	const left, right = 250, 20
	columnWidth := float64(WeatherWidth-left-right) / float64(len(surface.Frames))
	zone, err := time.LoadLocation(surface.Location.TimeZone)
	if err != nil {
		zone = time.UTC
	}

	drawWeatherSolarBackground(canvas, sky, surface.Frames, left, columnWidth)
	for index := range surface.Frames {
		x0 := left + int(math.Floor(float64(index)*columnWidth))
		if index > 0 {
			drawLine(canvas, x0, 48, x0, weatherMainBottom, weatherGrid)
		}
	}

	drawWeatherDayHeaders(canvas, fonts, surface.Frames, zone, left, columnWidth, options)
	drawWeatherLabels(canvas, fonts, left, options)
	for index, frame := range surface.Frames {
		center := left + int((float64(index)+0.5)*columnWidth)
		drawCentered(canvas, fonts.normalBold, center, 79, frame.ValidAt.In(zone).Format("15"), weatherText)
		drawWeatherIcon(canvas, center, 126, int(math.Max(12, columnWidth*0.34)), frame, sky.IsDay(frame.ValidAt))
		drawCentered(canvas, fonts.small, center, weatherLowCloudY, fmt.Sprintf("%.0f", frame.LowCloudCoverPercent), cloudCriticalityColor(frame.LowCloudCoverPercent))
		drawCentered(canvas, fonts.small, center, weatherMidCloudY, fmt.Sprintf("%.0f", frame.MidCloudCoverPercent), cloudCriticalityColor(frame.MidCloudCoverPercent))
		drawCentered(canvas, fonts.small, center, weatherHighCloudY, fmt.Sprintf("%.0f", frame.HighCloudCoverPercent), cloudCriticalityColor(frame.HighCloudCoverPercent))
		transparency := "—"
		var transparencyShade color.Color = weatherMuted
		if value, available := frame.TransparencyProxyPercent(); available {
			transparency = fmt.Sprintf("%.0f", value)
			transparencyShade = transparencyColor(value)
		}
		drawCentered(canvas, fonts.small, center, weatherTransparencyY, transparency, transparencyShade)
		precipitation := "·"
		if frame.PrecipitationMM >= 0.05 {
			precipitation = fmt.Sprintf("%.1f", frame.PrecipitationMM)
		}
		drawCentered(canvas, fonts.normal, center, 290+weatherPostCloudShift, precipitation, weatherCyan)
		drawCentered(canvas, fonts.normalBold, center, 332+weatherPostCloudShift, fmt.Sprintf("%+.0f", frame.TemperatureC), weatherOrange)
		pressure := frame.PressureHPA * 0.750061683
		drawCentered(canvas, fonts.normal, center, 374+weatherPostCloudShift, fmt.Sprintf("%.0f", pressure), weatherText)
		drawCentered(canvas, fonts.normal, center, 416+weatherPostCloudShift, fmt.Sprintf("%.0f", frame.WindSpeedMS), weatherText)
		drawCentered(canvas, fonts.normal, center, 458+weatherPostCloudShift, fmt.Sprintf("%.0f", frame.WindGustMS), gustColor(frame.WindGustMS))
		drawWindArrow(canvas, center, 494+weatherPostCloudShift, frame.WindDirectionDegrees, weatherText)
		drawCentered(canvas, fonts.normal, center, 542+weatherPostCloudShift, fmt.Sprintf("%.0f", frame.RelativeHumidityPercent), weatherText)
		drawCentered(canvas, fonts.small, center, 584+weatherPostCloudShift, fmt.Sprintf("%.1f", frame.DewPointSpreadC()), dewColor(frame.DewPointSpreadC()))
	}

	drawAstronomyRows(canvas, fonts, sky, surface.Frames, zone, left, columnWidth, options)
	drawWeatherLegend(canvas, fonts, options)
	timezoneLabel := forecast.TimeZoneLabel(surface.Location.TimeZone, surface.Frames[0].ValidAt)
	footer := fmt.Sprintf(localized(options, "Местное время точки · %s  |  ICON-EU run %s UTC", "Location local time · %s  |  ICON-EU run %s UTC"), timezoneLabel, surface.RunID)
	drawText(canvas, fonts.small, 20, 1058, footer, weatherMuted)
	return saveWeatherAtomic(canvas, destination)
}

func drawWeatherSolarBackground(canvas *image.RGBA, sky astronomy.Series, frames []forecast.SurfaceFrame, left int, columnWidth float64) {
	first := frames[0].ValidAt
	start := first.Add(-30 * time.Minute)
	end := frames[len(frames)-1].ValidAt.Add(30 * time.Minute)
	for _, interval := range solarPhaseIntervals(sky, start, end) {
		x0 := left + int(math.Round((interval.Start.Sub(first).Hours()+0.5)*columnWidth))
		x1 := left + int(math.Round((interval.End.Sub(first).Hours()+0.5)*columnWidth))
		draw.Draw(canvas, image.Rect(x0, 48, x1, weatherMainBottom), &image.Uniform{C: weatherSolarPhaseColor(interval.Phase)}, image.Point{}, draw.Src)
	}
}

func weatherSolarPhaseColor(phase solarPhase) color.RGBA {
	return [...]color.RGBA{
		weatherNight,
		weatherAstronomicalTwilight,
		weatherBrightTwilight,
		weatherDay,
	}[phase]
}

func drawWeatherDayHeaders(canvas *image.RGBA, fonts weatherFonts, frames []forecast.SurfaceFrame, zone *time.Location, left int, columnWidth float64, options Options) {
	start := 0
	for start < len(frames) {
		day := frames[start].ValidAt.In(zone)
		end := start + 1
		for end < len(frames) {
			candidate := frames[end].ValidAt.In(zone)
			if candidate.YearDay() != day.YearDay() || candidate.Year() != day.Year() {
				break
			}
			end++
		}
		x0 := left + int(float64(start)*columnWidth)
		x1 := left + int(float64(end)*columnWidth)
		label := day.Format("Monday, 2 January")
		if options.Language == "ru" {
			label = fmt.Sprintf("%s, %d %s", russianWeekday(day.Weekday()), day.Day(), russianMonth(day.Month()))
		}
		if end-start < 8 {
			label = day.Format("2 Jan")
			if options.Language == "ru" {
				label = fmt.Sprintf("%d %s", day.Day(), russianMonth(day.Month()))
			}
		}
		drawCentered(canvas, fonts.normalBold, (x0+x1)/2, 31, label, weatherText)
		if end < len(frames) {
			drawLine(canvas, x1, 0, x1, weatherMainBottom, color.RGBA{R: 103, G: 144, B: 185, A: 220})
		}
		start = end
	}
}

func drawWeatherLabels(canvas *image.RGBA, fonts weatherFonts, left int, options Options) {
	labels := []struct {
		text string
		y    int
	}{
		{localized(options, "Местное время", "Local time"), 79}, {localized(options, "Условия", "Conditions"), 126},
		{localized(options, "Осадки, мм/ч", "Precip., mm/h"), 290 + weatherPostCloudShift}, {localized(options, "Температура, °C", "Temperature, °C"), 332 + weatherPostCloudShift}, {localized(options, "Давление, мм рт. ст.", "Pressure, mmHg"), 374 + weatherPostCloudShift},
		{localized(options, "Ветер, м/с", "Wind, m/s"), 416 + weatherPostCloudShift}, {localized(options, "Порывы, м/с", "Gusts, m/s"), 458 + weatherPostCloudShift}, {localized(options, "Направление", "Direction"), 500 + weatherPostCloudShift},
		{localized(options, "Влажность, %", "Humidity, %"), 542 + weatherPostCloudShift}, {"T−Td, °C", 584 + weatherPostCloudShift}, {localized(options, "Солнце", "Sun"), 626 + weatherPostCloudShift},
		{localized(options, "Луна", "Moon"), 668 + weatherPostCloudShift}, {localized(options, "Фаза Луны", "Moon phase"), 710 + weatherPostCloudShift}, {localized(options, "Юпитер", "Jupiter"), 752 + weatherPostCloudShift}, {localized(options, "Сатурн", "Saturn"), 794 + weatherPostCloudShift},
	}
	for _, label := range labels {
		drawRight(canvas, fonts.normal, left-18, label.y, label.text, weatherMuted)
	}
	drawRight(canvas, fonts.small, left-18, weatherCloudHeaderY, localized(options, "Облака по ярусам, %", "Cloud layers, %"), weatherMuted)
	drawRight(canvas, fonts.small, left-18, weatherLowCloudY, localized(options, "Нижний", "Low"), weatherMuted)
	drawRight(canvas, fonts.small, left-18, weatherMidCloudY, localized(options, "Средний", "Middle"), weatherMuted)
	drawRight(canvas, fonts.small, left-18, weatherHighCloudY, localized(options, "Верхний", "High"), weatherMuted)
	drawRight(canvas, fonts.small, left-18, weatherTransparencyY, localized(options, "Прозрачность %", "Transparency %"), weatherMuted)
	for _, y := range []int{184, 282, 310 + weatherPostCloudShift, 352 + weatherPostCloudShift, 394 + weatherPostCloudShift, 436 + weatherPostCloudShift, 478 + weatherPostCloudShift, 520 + weatherPostCloudShift, 562 + weatherPostCloudShift, 604 + weatherPostCloudShift, 646 + weatherPostCloudShift, 686 + weatherPostCloudShift, 724 + weatherPostCloudShift, 766 + weatherPostCloudShift, 808 + weatherPostCloudShift} {
		drawLine(canvas, 0, y, WeatherWidth, y, weatherGrid)
	}
}

func drawAstronomyRows(canvas *image.RGBA, fonts weatherFonts, sky astronomy.Series, frames []forecast.SurfaceFrame, zone *time.Location, left int, columnWidth float64, options Options) {
	for _, day := range sky.Days {
		first, last := -1, -1
		date := day.Date.In(zone)
		for index, frame := range frames {
			local := frame.ValidAt.In(zone)
			if local.Year() == date.Year() && local.YearDay() == date.YearDay() {
				if first < 0 {
					first = index
				}
				last = index
			}
		}
		// A run can begin near the end of a local day. A one- or two-column
		// astronomy label is unreadable and spills into the row headings; the
		// following complete days still carry every event to the minute.
		if first < 0 || last-first+1 < 8 {
			continue
		}
		center := left + int((float64(first+last+1)/2)*columnWidth)
		drawAstronomyTimelineEvent(canvas, fonts, frames, left, columnWidth, 626+weatherPostCloudShift, day.Sunrise, true, false, day.MoonCycle)
		drawAstronomyTimelineEvent(canvas, fonts, frames, left, columnWidth, 626+weatherPostCloudShift, day.Sunset, false, false, day.MoonCycle)
		phaseName := astronomy.PhaseNameRU(day.MoonPhase)
		phaseText := fmt.Sprintf("%s · %.0f%% · %.1f д", phaseName, day.MoonIlluminationPercent, day.MoonAgeDays)
		if options.Language != "ru" {
			phaseText = fmt.Sprintf("%s · %.0f%% · %.1f d", englishMoonPhase(day.MoonPhase), day.MoonIlluminationPercent, day.MoonAgeDays)
		}
		if day.MoonAlwaysUp {
			drawCentered(canvas, fonts.normal, center, 668+weatherPostCloudShift, localized(options, "над горизонтом весь день", "above horizon all day"), weatherText)
		} else if day.MoonAlwaysDown {
			drawCentered(canvas, fonts.normal, center, 668+weatherPostCloudShift, localized(options, "ниже горизонта весь день", "below horizon all day"), weatherText)
		} else {
			drawAstronomyTimelineEvent(canvas, fonts, frames, left, columnWidth, 668+weatherPostCloudShift, day.Moonrise, true, true, day.MoonCycle)
			drawAstronomyTimelineEvent(canvas, fonts, frames, left, columnWidth, 668+weatherPostCloudShift, day.Moonset, false, true, day.MoonCycle)
		}
		phaseCenter := center + 10
		phaseWidth := font.MeasureString(fonts.small, phaseText).Round()
		drawMoonPhase(canvas, phaseCenter-phaseWidth/2-20, 704+weatherPostCloudShift, 11, day.MoonCycle)
		drawCentered(canvas, fonts.small, phaseCenter, 710+weatherPostCloudShift, phaseText, weatherText)
		drawPlanetTimelineEvent(canvas, fonts, frames, left, columnWidth, 752+weatherPostCloudShift, day.JupiterRise, true, false)
		drawPlanetTimelineEvent(canvas, fonts, frames, left, columnWidth, 752+weatherPostCloudShift, day.JupiterSet, false, false)
		drawPlanetTimelineEvent(canvas, fonts, frames, left, columnWidth, 794+weatherPostCloudShift, day.SaturnRise, true, true)
		drawPlanetTimelineEvent(canvas, fonts, frames, left, columnWidth, 794+weatherPostCloudShift, day.SaturnSet, false, true)
	}
}

func drawWeatherLegend(canvas *image.RGBA, fonts weatherFonts, options Options) {
	drawText(canvas, fonts.normalBold, 22, 832+weatherPostCloudShift, localized(options, "Легенда", "Legend"), weatherText)
	solarPhases := []struct {
		phase  solarPhase
		ru, en string
	}{
		{solarDay, "день ≥0°", "day ≥0°"},
		{solarBrightTwilight, "светлые сумерки 0…−12°", "bright twilight 0…−12°"},
		{solarAstronomicalTwilight, "астрономические сумерки −12…−18°", "astronomical twilight −12…−18°"},
		{solarNight, "ночь <−18°", "night <−18°"},
	}
	for index, item := range solarPhases {
		x := 240 + index*590
		draw.Draw(canvas, image.Rect(x, 838, x+28, 858), &image.Uniform{C: weatherSolarPhaseColor(item.phase)}, image.Point{}, draw.Src)
		drawText(canvas, fonts.small, x+38, 856, localized(options, item.ru, item.en), weatherText)
	}
	items := []struct {
		x, y                 int
		cloud, precipitation float64
		temperature          float64
		day                  bool
		label                string
	}{
		{140, 854 + weatherLegendShift, 5, 0, 8, true, localized(options, "ясно, день", "clear, day")},
		{760, 854 + weatherLegendShift, 5, 0, 8, false, localized(options, "ясно, ночь", "clear, night")},
		{1380, 854 + weatherLegendShift, 45, 0, 8, true, localized(options, "переменная облачность, день", "partly cloudy, day")},
		{2180, 854 + weatherLegendShift, 45, 0, 8, false, localized(options, "переменная облачность, ночь", "partly cloudy, night")},
		{140, 889 + weatherLegendShift, 95, 0, 8, false, localized(options, "пасмурно", "overcast")},
		{760, 889 + weatherLegendShift, 90, 1, 8, false, localized(options, "дождь", "rain")},
		{1380, 889 + weatherLegendShift, 90, 1, 0, false, localized(options, "снег", "snow")},
	}
	for _, item := range items {
		frame := forecast.SurfaceFrame{CloudCoverPercent: item.cloud, PrecipitationMM: item.precipitation, TemperatureC: item.temperature, DewPointC: item.temperature - 6}
		drawWeatherIcon(canvas, item.x, item.y, 13, frame, item.day)
		drawText(canvas, fonts.small, item.x+24, item.y+6, item.label, weatherText)
	}
	drawDrop(canvas, 2050, 884+weatherLegendShift, 8, weatherCyan)
	drawText(canvas, fonts.small, 2072, 895+weatherLegendShift, localized(options, "возможна роса: T−Td ≤3°C", "possible dew: T−Td ≤3°C"), weatherText)
	drawDrop(canvas, 2650, 884+weatherLegendShift, 8, weatherOrange)
	drawText(canvas, fonts.small, 2672, 895+weatherLegendShift, localized(options, "высокий риск росы: ≤1°C", "high dew risk: ≤1°C"), weatherText)
	drawFog(canvas, 140, 914+weatherLegendShift, 18, weatherCyan)
	drawText(canvas, fonts.small, 190, 930+weatherLegendShift, localized(options, "возможен туман: ICON VIS <5 км + насыщение", "possible fog: ICON VIS <5 km + saturation"), weatherText)
	drawFog(canvas, 1200, 914+weatherLegendShift, 18, weatherOrange)
	drawText(canvas, fonts.small, 1250, 930+weatherLegendShift, localized(options, "высокий риск тумана: ICON VIS <1 км + насыщение", "high fog risk: ICON VIS <1 km + saturation"), weatherText)
	drawText(canvas, fonts.normal, 22, 958+weatherLegendShift, localized(options, "Покрытие облаков: <10% — белый, 10–49% — синий, ≥50% — оранжевый; верхние облака тоже критичны для длинных выдержек и фотометрии.", "Cloud cover: <10% white, 10–49% blue, ≥50% orange; high clouds also matter for long exposures and photometry."), weatherMuted)
	drawText(canvas, fonts.normal, 22, 986+weatherLegendShift, localized(options, "Прозрачность %: облака + VIS + PWV; сравнительная оценка, не измерение экстинкции/AOD.", "Transparency %: clouds + VIS + PWV; comparative proxy, not measured extinction/AOD."), weatherMuted)
}

func englishMoonPhase(value string) string {
	return map[string]string{
		"new": "new moon", "waxing_crescent": "waxing crescent", "first_quarter": "first quarter",
		"waxing_gibbous": "waxing gibbous", "full": "full moon", "waning_gibbous": "waning gibbous",
		"last_quarter": "last quarter", "waning_crescent": "waning crescent",
	}[value]
}

func drawWeatherIcon(canvas *image.RGBA, centerX, centerY, radius int, frame forecast.SurfaceFrame, daylight bool) {
	cloud := frame.CloudCoverPercent
	if cloud < 75 {
		if daylight {
			drawSun(canvas, centerX-radius/2, centerY-radius/3, radius*2/3)
		} else {
			drawCrescent(canvas, centerX-radius/2, centerY-radius/3, radius*2/3)
		}
	}
	if cloud >= 15 {
		drawCloud(canvas, centerX+radius/4, centerY+radius/4, radius, cloud)
	}
	if frame.PrecipitationMM >= 0.05 {
		if frame.TemperatureC <= 1 {
			drawSnow(canvas, centerX, centerY+radius+5, radius/2)
		} else {
			drawDrop(canvas, centerX-radius/3, centerY+radius, radius/3, weatherCyan)
			drawDrop(canvas, centerX+radius/3, centerY+radius+3, radius/3, weatherCyan)
		}
	}
	spread := frame.DewPointSpreadC()
	if spread <= 3 {
		dew := weatherCyan
		if spread <= 1 {
			dew = weatherOrange
		}
		drawDrop(canvas, centerX+radius, centerY-radius, maxInt(3, radius/3), dew)
	}
	if risk := frame.FogRisk(); risk > 0 {
		shade := weatherCyan
		if risk == 2 {
			shade = weatherOrange
		}
		drawFog(canvas, centerX-radius, centerY+radius+6, radius, shade)
	}
}

func drawFog(canvas *image.RGBA, x, y, radius int, shade color.RGBA) {
	width := maxInt(8, radius*2)
	for offset := range 3 {
		yLine := y + offset*5
		shift := 0
		if offset == 1 {
			shift = width / 4
		}
		for stroke := -1; stroke <= 2; stroke++ {
			drawLine(canvas, x+shift-1, yLine+stroke, x+width-shift+1, yLine+stroke, weatherBackground)
		}
		drawLine(canvas, x+shift, yLine, x+width-shift, yLine, shade)
		drawLine(canvas, x+shift, yLine+1, x+width-shift, yLine+1, shade)
	}
}

func drawSun(canvas *image.RGBA, x, y, radius int) {
	yellow := color.RGBA{R: 255, G: 196, B: 62, A: 255}
	fillCircle(canvas, x, y, radius/2, yellow)
	for index := range 8 {
		angle := float64(index) * math.Pi / 4
		drawLine(canvas, x+int(math.Cos(angle)*float64(radius*2/3)), y+int(math.Sin(angle)*float64(radius*2/3)),
			x+int(math.Cos(angle)*float64(radius)), y+int(math.Sin(angle)*float64(radius)), yellow)
	}
}

func drawCrescent(canvas *image.RGBA, x, y, radius int) {
	moon := color.RGBA{R: 211, G: 224, B: 239, A: 255}
	fillCircle(canvas, x, y, radius, moon)
	fillCircle(canvas, x+radius/2, y-radius/5, radius, weatherNight)
}

func drawCloud(canvas *image.RGBA, x, y, radius int, cover float64) {
	shade := color.RGBA{R: 161, G: 190, B: 216, A: 255}
	if cover >= 80 {
		shade = color.RGBA{R: 112, G: 144, B: 174, A: 255}
	}
	fillCircle(canvas, x-radius/2, y, radius/2, shade)
	fillCircle(canvas, x, y-radius/3, radius*2/3, shade)
	fillCircle(canvas, x+radius/2, y, radius/2, shade)
	draw.Draw(canvas, image.Rect(x-radius, y, x+radius, y+radius/2+1), &image.Uniform{C: shade}, image.Point{}, draw.Src)
}

func drawDrop(canvas *image.RGBA, x, y, radius int, shade color.RGBA) {
	for dy := -radius; dy <= radius; dy++ {
		width := radius - int(math.Abs(float64(dy)))/2
		if dy < 0 {
			width = maxInt(0, radius+dy)
		}
		for dx := -width; dx <= width; dx++ {
			setPixel(canvas, x+dx, y+dy, shade)
		}
	}
}

func drawSnow(canvas *image.RGBA, x, y, radius int) {
	for index := range 3 {
		angle := float64(index) * math.Pi / 3
		dx, dy := int(math.Cos(angle)*float64(radius)), int(math.Sin(angle)*float64(radius))
		drawLine(canvas, x-dx, y-dy, x+dx, y+dy, weatherCyan)
	}
}

func drawMoonPhase(canvas *image.RGBA, centerX, centerY, radius int, cycle float64) {
	dark := color.RGBA{R: 45, G: 69, B: 99, A: 255}
	lit := color.RGBA{R: 211, G: 224, B: 239, A: 255}
	angle := 2 * math.Pi * cycle
	sx, sz := math.Sin(angle), -math.Cos(angle)
	for y := -radius; y <= radius; y++ {
		for x := -radius; x <= radius; x++ {
			nx, ny := float64(x)/float64(radius), float64(y)/float64(radius)
			r2 := nx*nx + ny*ny
			if r2 > 1 {
				continue
			}
			shade := dark
			nz := math.Sqrt(1 - r2)
			if nx*sx+nz*sz > 0 {
				shade = lit
			}
			setPixel(canvas, centerX+x, centerY+y, shade)
		}
	}
}

func fillCircle(canvas *image.RGBA, centerX, centerY, radius int, shade color.RGBA) {
	for y := -radius; y <= radius; y++ {
		width := int(math.Sqrt(float64(radius*radius - y*y)))
		for x := -width; x <= width; x++ {
			setPixel(canvas, centerX+x, centerY+y, shade)
		}
	}
}

func drawLine(canvas *image.RGBA, x0, y0, x1, y1 int, shade color.RGBA) {
	dx, dy := absInt(x1-x0), -absInt(y1-y0)
	sx, sy := -1, -1
	if x0 < x1 {
		sx = 1
	}
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	for {
		setPixel(canvas, x0, y0, shade)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func setPixel(canvas *image.RGBA, x, y int, shade color.RGBA) {
	if image.Pt(x, y).In(canvas.Bounds()) {
		canvas.SetRGBA(x, y, shade)
	}
}

func drawText(canvas *image.RGBA, face font.Face, x, baseline int, value string, shade color.Color) {
	drawer := font.Drawer{Dst: canvas, Src: image.NewUniform(shade), Face: face, Dot: fixedPoint(x, baseline)}
	drawer.DrawString(value)
}

func drawCentered(canvas *image.RGBA, face font.Face, center, baseline int, value string, shade color.Color) {
	width := font.MeasureString(face, value).Round()
	drawText(canvas, face, center-width/2, baseline, value, shade)
}

func drawRight(canvas *image.RGBA, face font.Face, right, baseline int, value string, shade color.Color) {
	width := font.MeasureString(face, value).Round()
	drawText(canvas, face, right-width, baseline, value, shade)
}

func fixedPoint(x, y int) fixed.Point26_6 {
	return fixed.P(x, y)
}

func newWeatherFonts() (weatherFonts, func(), error) {
	regular, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return weatherFonts{}, func() {}, fmt.Errorf("parse regular weather font: %w", err)
	}
	bold, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return weatherFonts{}, func() {}, fmt.Errorf("parse bold weather font: %w", err)
	}
	makeFace := func(source *opentype.Font, size float64) (font.Face, error) {
		return opentype.NewFace(source, &opentype.FaceOptions{Size: size, DPI: 96, Hinting: font.HintingFull})
	}
	tiny, err := makeFace(regular, 10.5)
	if err != nil {
		return weatherFonts{}, func() {}, err
	}
	small, err := makeFace(regular, 14)
	if err != nil {
		return weatherFonts{}, func() {}, err
	}
	normal, err := makeFace(regular, 17)
	if err != nil {
		return weatherFonts{}, func() {}, err
	}
	normalBold, err := makeFace(bold, 17)
	if err != nil {
		return weatherFonts{}, func() {}, err
	}
	large, err := makeFace(bold, 20)
	if err != nil {
		return weatherFonts{}, func() {}, err
	}
	title, err := makeFace(bold, 24)
	if err != nil {
		return weatherFonts{}, func() {}, err
	}
	faces := weatherFonts{tiny: tiny, small: small, normal: normal, normalBold: normalBold, large: large, title: title}
	closeFaces := func() {
		for _, face := range []font.Face{tiny, small, normal, normalBold, large, title} {
			if closer, ok := face.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
		}
	}
	return faces, closeFaces, nil
}

func saveWeatherAtomic(canvas image.Image, destination string) error {
	if err := savePNGImageAtomic(canvas, destination); err != nil {
		return fmt.Errorf("encode weather chart: %w", err)
	}
	return nil
}

func drawAstronomyTimelineEvent(canvas *image.RGBA, fonts weatherFonts, frames []forecast.SurfaceFrame, left int, columnWidth float64, baseline int, event time.Time, rising, moon bool, cycle float64) {
	x, visible := astronomyTimelineX(frames, left, columnWidth, event)
	if !visible {
		return
	}
	textValue := event.Round(time.Minute).Format("15:04")
	textWidth := font.MeasureString(fonts.normal, textValue).Round()
	drawCelestialEventIcon(canvas, x, baseline-7, rising, moon, cycle)
	if rising {
		drawText(canvas, fonts.normal, x+28, baseline, textValue, weatherText)
	} else {
		drawText(canvas, fonts.normal, x-28-textWidth, baseline, textValue, weatherText)
	}
}

func drawPlanetTimelineEvent(canvas *image.RGBA, fonts weatherFonts, frames []forecast.SurfaceFrame, left int, columnWidth float64, baseline int, event time.Time, rising, saturn bool) {
	x, visible := astronomyTimelineX(frames, left, columnWidth, event)
	if !visible {
		return
	}
	value := event.Round(time.Minute).Format("15:04")
	width := font.MeasureString(fonts.normal, value).Round()
	drawPlanetEventIcon(canvas, x, baseline-7, rising, saturn)
	if rising {
		drawText(canvas, fonts.normal, x+28, baseline, value, weatherText)
	} else {
		drawText(canvas, fonts.normal, x-28-width, baseline, value, weatherText)
	}
}

func drawPlanetEventIcon(canvas *image.RGBA, centerX, centerY int, rising, saturn bool) {
	shade := color.RGBA{R: 225, G: 191, B: 139, A: 255}
	if saturn {
		shade = color.RGBA{R: 229, G: 203, B: 139, A: 255}
		drawLine(canvas, centerX-12, centerY+4, centerX+8, centerY-4, shade)
		drawLine(canvas, centerX-11, centerY+7, centerX+9, centerY-1, shade)
	}
	fillCircle(canvas, centerX-2, centerY, 8, shade)
	if !saturn {
		drawLine(canvas, centerX-9, centerY-2, centerX+5, centerY-2, color.RGBA{R: 154, G: 101, B: 71, A: 255})
		drawLine(canvas, centerX-9, centerY+3, centerX+5, centerY+3, color.RGBA{R: 154, G: 101, B: 71, A: 255})
	}
	drawVerticalEventArrow(canvas, centerX+14, centerY, rising, shade)
}

func astronomyTimelineX(frames []forecast.SurfaceFrame, left int, columnWidth float64, event time.Time) (int, bool) {
	if event.IsZero() || len(frames) == 0 {
		return 0, false
	}
	hours := event.Sub(frames[0].ValidAt).Hours()
	if hours < -0.5 || hours > float64(len(frames))-0.5 {
		return 0, false
	}
	return left + int(math.Round((hours+0.5)*columnWidth)), true
}

func drawCelestialEventIcon(canvas *image.RGBA, centerX, centerY int, rising, moon bool, cycle float64) {
	if moon {
		drawMoonPhase(canvas, centerX-3, centerY, 9, cycle)
	} else {
		drawSun(canvas, centerX-3, centerY, 12)
	}
	shade := weatherText
	if !moon {
		shade = color.RGBA{R: 255, G: 196, B: 62, A: 255}
	}
	drawVerticalEventArrow(canvas, centerX+14, centerY, rising, shade)
}

func drawVerticalEventArrow(canvas *image.RGBA, centerX, centerY int, rising bool, shade color.RGBA) {
	tipY, tailY := centerY-11, centerY+11
	headBaseY := tipY + 7
	if !rising {
		tipY, tailY = centerY+11, centerY-11
		headBaseY = tipY - 7
	}
	for offset := -1; offset <= 1; offset++ {
		drawLine(canvas, centerX+offset, tailY, centerX+offset, tipY, shade)
		drawLine(canvas, centerX+offset, tipY, centerX-6+offset, headBaseY, shade)
		drawLine(canvas, centerX+offset, tipY, centerX+6+offset, headBaseY, shade)
	}
}

func drawWindArrow(canvas *image.RGBA, centerX, centerY int, sourceDegrees float64, shade color.RGBA) {
	// ICON uses the meteorological direction the wind comes from. Draw the
	// opposite, downwind vector. Eight fixed sectors remain regular and legible
	// after Telegram scales the 3200 px chart down for previews.
	if math.IsNaN(sourceDegrees) || math.IsInf(sourceDegrees, 0) {
		return
	}
	downwindDegrees := math.Mod(sourceDegrees+180+22.5, 360)
	sector := math.Floor(downwindDegrees / 45)
	bearing := sector * 45 * math.Pi / 180
	dx, dy := math.Sin(bearing), -math.Cos(bearing)
	perpendicularX, perpendicularY := -dy, dx
	tipX := centerX + int(math.Round(dx*14))
	tipY := centerY + int(math.Round(dy*14))
	tailX := centerX - int(math.Round(dx*13))
	tailY := centerY - int(math.Round(dy*13))
	headBaseX := float64(tipX) - dx*9
	headBaseY := float64(tipY) - dy*9
	shaftEndX := int(math.Round(headBaseX + dx*2))
	shaftEndY := int(math.Round(headBaseY + dy*2))
	drawThickLine(canvas, tailX, tailY, shaftEndX, shaftEndY, 2, shade)
	fillTriangle(canvas,
		image.Pt(tipX, tipY),
		image.Pt(int(math.Round(headBaseX+perpendicularX*7)), int(math.Round(headBaseY+perpendicularY*7))),
		image.Pt(int(math.Round(headBaseX-perpendicularX*7)), int(math.Round(headBaseY-perpendicularY*7))),
		shade,
	)
}

func drawThickLine(canvas *image.RGBA, x0, y0, x1, y1, radius int, shade color.RGBA) {
	dx, dy := absInt(x1-x0), -absInt(y1-y0)
	sx, sy := -1, -1
	if x0 < x1 {
		sx = 1
	}
	if y0 < y1 {
		sy = 1
	}
	err := dx + dy
	for {
		fillCircle(canvas, x0, y0, radius, shade)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func fillTriangle(canvas *image.RGBA, a, b, c image.Point, shade color.RGBA) {
	minimumX := min(a.X, min(b.X, c.X))
	maximumX := max(a.X, max(b.X, c.X))
	minimumY := min(a.Y, min(b.Y, c.Y))
	maximumY := max(a.Y, max(b.Y, c.Y))
	edge := func(start, end image.Point, x, y int) int {
		return (x-start.X)*(end.Y-start.Y) - (y-start.Y)*(end.X-start.X)
	}
	for y := minimumY; y <= maximumY; y++ {
		for x := minimumX; x <= maximumX; x++ {
			first := edge(a, b, x, y)
			second := edge(b, c, x, y)
			third := edge(c, a, x, y)
			if (first >= 0 && second >= 0 && third >= 0) || (first <= 0 && second <= 0 && third <= 0) {
				setPixel(canvas, x, y, shade)
			}
		}
	}
}

func russianWeekday(value time.Weekday) string {
	return []string{"Воскресенье", "Понедельник", "Вторник", "Среда", "Четверг", "Пятница", "Суббота"}[value]
}

func russianMonth(value time.Month) string {
	return []string{"", "января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"}[value]
}

func gustColor(speed float64) color.Color {
	if speed >= 12 {
		return color.RGBA{R: 245, G: 77, B: 77, A: 255}
	}
	return weatherText
}

func dewColor(spread float64) color.Color {
	if spread <= 1 {
		return weatherOrange
	}
	if spread <= 3 {
		return weatherCyan
	}
	return weatherText
}

func cloudCriticalityColor(cover float64) color.Color {
	if cover >= 50 {
		return weatherOrange
	}
	if cover >= 10 {
		return weatherCyan
	}
	return weatherText
}

func transparencyColor(value float64) color.Color {
	if value < 40 {
		return weatherOrange
	}
	if value < 70 {
		return weatherCyan
	}
	return weatherText
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
