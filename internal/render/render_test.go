package render

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
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

func TestSeeingConfidenceUsesTextInsideBars(t *testing.T) {
	diagnostics, err := forecast.ComputeDiagnostics(forecast.SyntheticVerticalFixture())
	if err != nil {
		t.Fatal(err)
	}
	labels, err := seeingConfidenceLabels(diagnostics, magma(96))
	if err != nil {
		t.Fatal(err)
	}
	if len(labels.Labels) != len(diagnostics.SeeingIndex) {
		t.Fatalf("confidence label count = %d, want %d", len(labels.Labels), len(diagnostics.SeeingIndex))
	}
	if labels.Labels[0] != "96%" || labels.Labels[len(labels.Labels)-1] != "67%" {
		t.Fatalf("unexpected confidence labels: first=%q last=%q", labels.Labels[0], labels.Labels[len(labels.Labels)-1])
	}
	for _, point := range labels.XYs {
		if point.Y <= 0 {
			t.Fatalf("confidence label is outside its seeing bar: %+v", point)
		}
	}
}

func TestOverallLabelsExposeHybridInputsAndFogSeverity(t *testing.T) {
	frames := []forecast.OverallIndexFrame{
		{
			Index: 6.4, SeeingArcsec: 2.21, CoherenceTimeMS: 3.14,
			PhysicalSeeing: true, PhysicalCoherence: true, GroundLayerPhysics: true,
			CloudTransmissionPercent: 55.2, FogRisk: 1,
		},
		{
			Index: 3.2, SeeingArcsec: 1.04, CoherenceTimeMS: 2.01,
			PhysicalSeeing: true, PhysicalCoherence: true,
			CloudTransmissionPercent: 19.7, FogRisk: 2,
		},
	}
	_, inside, err := overallIndexLabels(frames, magma(96), Options{Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := inside.Labels[0], "2.2″\nτ3.1\nT55% f"; got != want {
		t.Fatalf("hybrid label = %q, want %q", got, want)
	}
	if got, want := inside.Labels[1], "1.0″\nτ2.0\nT20% F"; got != want {
		t.Fatalf("strict hybrid label = %q, want %q", got, want)
	}
	if inside.TextStyle[0].Font.Size < 8 {
		t.Fatalf("overall input label is too small: %v", inside.TextStyle[0].Font.Size)
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
		file.Close()
		if err != nil {
			t.Fatalf("decode %s: %v", filepath.Base(path), err)
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
	path := filepath.Join(t.TempDir(), "weather-hourly.png")
	if err := Weather(path, surface, sky, Options{Language: "ru"}); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := png.DecodeConfig(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
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
	}{{5, solarDay}, {0, solarDay}, {-0.1, solarCivilTwilight}, {-6, solarCivilTwilight}, {-6.1, solarNauticalTwilight}, {-12, solarNauticalTwilight}, {-12.1, solarAstronomicalTwilight}, {-18, solarAstronomicalTwilight}, {-18.1, solarNight}}
	for _, test := range tests {
		if got := phaseForSunAltitude(test.altitude); got != test.want {
			t.Errorf("phaseForSunAltitude(%v) = %v, want %v", test.altitude, got, test.want)
		}
	}
}
