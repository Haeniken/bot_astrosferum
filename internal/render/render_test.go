package render

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bot_astrosferum/internal/astronomy"
	"bot_astrosferum/internal/forecast"
)

func TestWindArrowsAreDrawnForAllEightDirections(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 240, 40))
	shade := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	for index := range 8 {
		centerX := 20 + index*28
		drawWindArrow(canvas, centerX, 20, float64(index*45), shade)
		colored := 0
		for y := 7; y <= 33; y++ {
			for x := centerX - 13; x <= centerX+13; x++ {
				if canvas.RGBAAt(x, y).A != 0 {
					colored++
				}
			}
		}
		if colored < 90 {
			t.Fatalf("direction %d° contains only %d drawn pixels", index*45, colored)
		}
	}
}

func TestLosslessPNGCompressionPreservesPixels(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 640, 320))
	for y := range 320 {
		for x := range 640 {
			shade := uint8((x/16 + y/16) % 4 * 60)
			source.SetRGBA(x, y, color.RGBA{R: shade, G: 255 - shade, B: uint8(y % 32), A: 255})
		}
	}
	destination := filepath.Join(t.TempDir(), "lossless.png")
	if err := savePNGImageAtomic(source, destination); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(file)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds() != source.Bounds() {
		t.Fatalf("decoded bounds = %v, want %v", decoded.Bounds(), source.Bounds())
	}
	for y := range 320 {
		for x := range 640 {
			if decoded.At(x, y) != source.At(x, y) {
				t.Fatalf("pixel (%d,%d) changed", x, y)
			}
		}
	}
	var defaultPNG bytes.Buffer
	if err := png.Encode(&defaultPNG, source); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > int64(defaultPNG.Len()) {
		t.Fatalf("best-compression PNG size = %d, default = %d", info.Size(), defaultPNG.Len())
	}
}

func TestWindArrowsUseStableCompassSectors(t *testing.T) {
	shade := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	draw := func(degrees float64) *image.RGBA {
		canvas := image.NewRGBA(image.Rect(0, 0, 50, 50))
		drawWindArrow(canvas, 25, 25, degrees, shade)
		return canvas
	}
	if !bytes.Equal(draw(0).Pix, draw(20).Pix) {
		t.Fatal("directions in one 45-degree sector must render identically")
	}
	if bytes.Equal(draw(0).Pix, draw(25).Pix) {
		t.Fatal("directions across a sector boundary must differ")
	}
	for _, test := range []struct {
		source, x, y float64
	}{
		{source: 0, x: 25, y: 39},
		{source: 90, x: 11, y: 25},
		{source: 180, x: 25, y: 11},
		{source: 270, x: 39, y: 25},
	} {
		canvas := draw(test.source)
		if canvas.RGBAAt(int(test.x), int(test.y)).A == 0 {
			t.Fatalf("source %.0f° has no arrow tip at %.0f,%.0f", test.source, test.x, test.y)
		}
	}
}

func TestCelestialEventArrowsAreThickAndVisible(t *testing.T) {
	for _, rising := range []bool{true, false} {
		canvas := image.NewRGBA(image.Rect(0, 0, 60, 60))
		drawVerticalEventArrow(canvas, 30, 30, rising, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		colored := 0
		for y := 15; y <= 45; y++ {
			for x := 20; x <= 40; x++ {
				if canvas.RGBAAt(x, y).A != 0 {
					colored++
				}
			}
		}
		if colored < 55 {
			t.Fatalf("rising=%v arrow contains only %d pixels", rising, colored)
		}
	}
}

func TestFogPictogramIsThickAndVisible(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 80, 50))
	shade := color.RGBA{R: 64, G: 192, B: 236, A: 255}
	drawFog(canvas, 15, 12, 13, shade)
	colored := 0
	for y := range 50 {
		for x := range 80 {
			if canvas.RGBAAt(x, y) == shade {
				colored++
			}
		}
	}
	if colored < 120 {
		t.Fatalf("fog pictogram contains only %d colored pixels", colored)
	}
}

func TestWeatherIconIncludesFogHeuristicPictogram(t *testing.T) {
	tests := []struct {
		name  string
		frame forecast.SurfaceFrame
		shade color.RGBA
	}{
		{
			name:  "possible",
			frame: forecast.SurfaceFrame{VisibilityKM: 3, RelativeHumidityPercent: 92, TemperatureC: 8, DewPointC: 6, FogHeuristicAvailable: true},
			shade: weatherCyan,
		},
		{
			name:  "high",
			frame: forecast.SurfaceFrame{VisibilityKM: 0.5, RelativeHumidityPercent: 98, TemperatureC: 8, DewPointC: 7.5, FogHeuristicAvailable: true},
			shade: weatherOrange,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			canvas := image.NewRGBA(image.Rect(0, 0, 80, 80))
			drawWeatherIcon(canvas, 40, 30, 13, test.frame, false)
			colored := 0
			for y := 48; y < 75; y++ {
				for x := 20; x < 60; x++ {
					if canvas.RGBAAt(x, y) == test.shade {
						colored++
					}
				}
			}
			if colored < 100 {
				t.Fatalf("weather fog marker contains only %d colored pixels", colored)
			}
		})
	}
}

func TestSeeingLeadTimeQualityHeuristicUsesTextInsideBars(t *testing.T) {
	diagnostics, err := forecast.ComputeDiagnostics(forecast.SyntheticVerticalFixture())
	if err != nil {
		t.Fatal(err)
	}
	labels, err := seeingLeadTimeQualityHeuristicLabels(diagnostics, magma(96))
	if err != nil {
		t.Fatal(err)
	}
	if len(labels.Labels) != len(diagnostics.SeeingIndex) {
		t.Fatalf("lead-time quality label count = %d, want %d", len(labels.Labels), len(diagnostics.SeeingIndex))
	}
	if labels.Labels[0] != "96%" || labels.Labels[len(labels.Labels)-1] != "67%" {
		t.Fatalf("unexpected lead-time quality labels: first=%q last=%q", labels.Labels[0], labels.Labels[len(labels.Labels)-1])
	}
	for _, point := range labels.XYs {
		if point.Y <= 0 {
			t.Fatalf("lead-time quality label is outside its seeing bar: %+v", point)
		}
	}
}

func TestOverallLabelsExposeHybridInputsAndFogSeverity(t *testing.T) {
	frames := []forecast.OverallIndexFrame{
		{
			Index: 6.4, SeeingArcsec: 2.21, CoherenceTimeMS: 3.14,
			PhysicalSeeing: true, PhysicalCoherence: true, GroundLayerPhysics: true,
			CloudTransmissionPercent: 55.2, FogHeuristic: 1,
		},
		{
			Index: 3.2, SeeingArcsec: 1.04, CoherenceTimeMS: 2.01,
			PhysicalSeeing: true, PhysicalCoherence: true,
			CloudTransmissionPercent: 19.7, FogHeuristic: 2,
		},
	}
	top, inside, err := overallIndexLabels(frames, magma(96), Options{Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := inside.Labels[0], "2.2″\nτ3.1\nT55% f"; got != want {
		t.Fatalf("hybrid label = %q, want %q", got, want)
	}
	if got, want := inside.Labels[1], "1.0″\nτ2.0\nT20% F"; got != want {
		t.Fatalf("strict hybrid label = %q, want %q", got, want)
	}
	if inside.TextStyle[0].Font.Size < 16 {
		t.Fatalf("overall input label is too small: %v", inside.TextStyle[0].Font.Size)
	}
	if top.TextStyle[0].Font.Size < 17 {
		t.Fatalf("overall index label is too small: %v", top.TextStyle[0].Font.Size)
	}
}

func TestOverallPenaltyStackClosesAtTenAndExposesPrecipitationVeto(t *testing.T) {
	frames, err := forecast.ComputeHourlyOverallIndex(
		forecast.SyntheticVerticalFixture(),
		forecast.SyntheticSurfaceFixture(),
		forecast.SyntheticCloudFixture(),
		forecast.DefaultOverallIndexCalibration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	frames[0].Index = 1
	frames[0].PrecipitationVeto = true
	frames[0].PenaltyContributions = []forecast.OverallPenaltyContribution{
		{Key: forecast.OverallPenaltyOpticalTurbulence, LossFraction: 0},
		{Key: forecast.OverallPenaltyCloudObstruction, LossFraction: 0},
		{Key: forecast.OverallPenaltySurfaceWind, LossFraction: 0},
		{Key: forecast.OverallPenaltyFog, LossFraction: 0},
		{Key: forecast.OverallPenaltyPrecipitation, LossFraction: 1},
	}
	base, penalties, err := overallStackValues(frames)
	if err != nil {
		t.Fatal(err)
	}
	if base[0] != 1 || penalties[forecast.OverallPenaltyPrecipitation][0] != 9 {
		t.Fatalf("precipitation veto stack = base %.3f, precipitation %.3f", base[0], penalties[forecast.OverallPenaltyPrecipitation][0])
	}
	top, _, err := overallIndexLabels(frames[:1], magma(96), Options{Language: "ru"})
	if err != nil {
		t.Fatal(err)
	}
	if top.Labels[0] != "1.0P" {
		t.Fatalf("precipitation veto marker = %q, want stable ASCII marker 1.0P", top.Labels[0])
	}
	for index := range frames {
		total := base[index]
		for _, key := range overallPenaltyOrder {
			total += penalties[key][index]
		}
		if math.Abs(total-10) > 1e-7 {
			t.Fatalf("stack %d closes at %.12f", index, total)
		}
	}
}

func TestOverallPenaltyStackRejectsAnyNegativeLoss(t *testing.T) {
	frames := []forecast.OverallIndexFrame{{
		ValidAt: time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC),
		Index:   10,
		PenaltyContributions: []forecast.OverallPenaltyContribution{{
			Key: forecast.OverallPenaltyCloudObstruction, LossFraction: -1e-13,
		}},
	}}
	if _, _, err := overallStackValues(frames); err == nil {
		t.Fatal("negative Overall loss was accepted by the static renderer")
	}
}

func TestAllProducesFour1280x960PNGs(t *testing.T) {
	output := t.TempDir()
	result, err := All(output, forecast.SyntheticVerticalFixture(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{result.WindSpeed, result.VectorShear, result.DirectionDelta, result.SeeingIndex}
	seen := make(map[string]bool)
	for _, path := range paths {
		if seen[path] {
			t.Fatalf("duplicate output path %q", path)
		}
		seen[path] = true
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		configuration, err := png.DecodeConfig(file)
		closeErr := file.Close()
		if err != nil {
			t.Fatalf("decode %s: %v", filepath.Base(path), err)
		}
		if closeErr != nil {
			t.Fatalf("close %s: %v", filepath.Base(path), closeErr)
		}
		if configuration.Width != DefaultWidth || configuration.Height != DefaultHeight {
			t.Fatalf("%s is %dx%d, want %dx%d", filepath.Base(path), configuration.Width, configuration.Height, DefaultWidth, DefaultHeight)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() < 20_000 {
			t.Fatalf("%s is unexpectedly small: %d bytes", filepath.Base(path), info.Size())
		}
	}
}

func TestWeatherProducesHourlyLandscapePNG(t *testing.T) {
	surface := forecast.SyntheticSurfaceFixture()
	sky, err := astronomy.Compute(surface.Location, surface.Frames[0].ValidAt, surface.Frames[len(surface.Frames)-1].ValidAt)
	if err != nil {
		t.Fatal(err)
	}
	validTimes := make([]time.Time, len(surface.Frames))
	for index := range surface.Frames {
		validTimes[index] = surface.Frames[index].ValidAt
	}
	celestialTracks, err := astronomy.ComputeCelestialTracks(surface.Location, validTimes)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "weather-hourly.png")
	if err := Weather(path, surface, sky, celestialTracks, Options{Language: "ru"}); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := png.DecodeConfig(file)
	closeErr := file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if configuration.Width != WeatherWidth || configuration.Height != WeatherHeight {
		t.Fatalf("weather PNG is %dx%d", configuration.Width, configuration.Height)
	}
}

func TestAllRejectsUndersizedCanvas(t *testing.T) {
	_, err := All(t.TempDir(), forecast.SyntheticVerticalFixture(), Options{Width: 320, Height: 240})
	if err == nil {
		t.Fatal("expected size validation error")
	}
}

func TestAstronomyEventUsesHourlyTimelinePosition(t *testing.T) {
	base := time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC)
	frames := make([]forecast.SurfaceFrame, 24)
	for index := range frames {
		frames[index].ValidAt = base.Add(time.Duration(index) * time.Hour)
	}
	x, visible := astronomyTimelineX(frames, 250, 40, base.Add(4*time.Hour+15*time.Minute))
	if !visible || x != 440 {
		t.Fatalf("event position = %d, visible=%v; want 440, true", x, visible)
	}
	if _, visible := astronomyTimelineX(frames, 250, 40, base.Add(-time.Hour)); visible {
		t.Fatal("event outside the rendered window was treated as visible")
	}
}

func TestSolarPhaseBoundaries(t *testing.T) {
	tests := []struct {
		altitude float64
		want     solarPhase
	}{{5, solarDay}, {0, solarDay}, {-0.1, solarBrightTwilight}, {-6, solarBrightTwilight}, {-6.1, solarBrightTwilight}, {-12, solarBrightTwilight}, {-12.1, solarAstronomicalTwilight}, {-18, solarAstronomicalTwilight}, {-18.1, solarNight}}
	for _, test := range tests {
		if got := phaseForSunAltitude(test.altitude); got != test.want {
			t.Errorf("phaseForSunAltitude(%v) = %v, want %v", test.altitude, got, test.want)
		}
	}
}

func TestSolarPhaseIntervalsResolveCrossingsBelowHourlyResolution(t *testing.T) {
	sky := astronomy.Series{Location: forecast.Location{Latitude: 0, Longitude: 0, TimeZone: "UTC"}}
	start := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	intervals := solarPhaseIntervals(sky, start, end)
	if len(intervals) < 7 {
		t.Fatalf("solar intervals = %d, want at least seven across an equatorial equinox day", len(intervals))
	}
	if !intervals[0].Start.Equal(start) || !intervals[len(intervals)-1].End.Equal(end) {
		t.Fatalf("solar intervals do not cover the complete render period: %#v", intervals)
	}
	thresholds := []float64{0, -12, -18}
	for index := 1; index < len(intervals); index++ {
		if !intervals[index-1].End.Equal(intervals[index].Start) {
			t.Fatalf("solar intervals %d and %d are not contiguous", index-1, index)
		}
		if intervals[index-1].Phase == intervals[index].Phase {
			t.Fatalf("solar intervals %d and %d repeat phase %v", index-1, index, intervals[index].Phase)
		}
		altitude := sky.SunAltitudeDegrees(intervals[index].Start)
		nearest := math.Inf(1)
		for _, threshold := range thresholds {
			nearest = math.Min(nearest, math.Abs(altitude-threshold))
		}
		if nearest > 0.75 {
			t.Fatalf("solar transition %d altitude = %.5f°, not near a displayed boundary", index, altitude)
		}
	}
}

func TestWeatherSolarPhaseShadesGetDarkerTowardNight(t *testing.T) {
	brightness := func(value color.RGBA) int { return int(value.R) + int(value.G) + int(value.B) }
	phases := []solarPhase{solarDay, solarBrightTwilight, solarAstronomicalTwilight, solarNight}
	for index := 1; index < len(phases); index++ {
		previous := weatherSolarPhaseColor(phases[index-1])
		current := weatherSolarPhaseColor(phases[index])
		if brightness(current) >= brightness(previous) {
			t.Fatalf("phase %v shade %#v is not darker than phase %v shade %#v", phases[index], current, phases[index-1], previous)
		}
	}
}
