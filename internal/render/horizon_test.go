package render

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
)

func TestHorizonProducesLocalizedReadablePNG(t *testing.T) {
	if horizonSolarBandEnd-horizonSolarBandTop < 40 {
		t.Fatal("Horizon solar band is too narrow to distinguish night and twilight in chat previews")
	}
	for _, language := range []string{"ru", "en"} {
		t.Run(language, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "horizon.png")
			if err := Horizon(context.Background(), path, horizonRenderFixture(), Options{Language: language}); err != nil {
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
			if configuration.Width != HorizonWidth || configuration.Height != HorizonHeight {
				t.Fatalf("horizon PNG is %dx%d, want %dx%d", configuration.Width, configuration.Height, HorizonWidth, HorizonHeight)
			}
			columnWidth := float64(HorizonWidth-horizonLeft-horizonRight) / horizonFrameCount
			if columnWidth < 40 || horizonRowHeight < 70 {
				t.Fatalf("matrix cells are not readable: %.1fx%d px", columnWidth, horizonRowHeight)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() < 80_000 {
				t.Fatalf("horizon PNG is unexpectedly small: %d bytes", info.Size())
			}
		})
	}
}

func TestHorizonRenderIsDeterministicAndLocalized(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first-en.png")
	second := filepath.Join(root, "second-en.png")
	russian := filepath.Join(root, "ru.png")
	input := horizonRenderFixture()
	if err := Horizon(context.Background(), first, input, Options{Language: "en"}); err != nil {
		t.Fatal(err)
	}
	if err := Horizon(context.Background(), second, input, Options{Language: "en"}); err != nil {
		t.Fatal(err)
	}
	if err := Horizon(context.Background(), russian, input, Options{Language: "ru"}); err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	russianBytes, err := os.ReadFile(russian)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("identical Horizon input produced different PNG bytes")
	}
	if bytes.Equal(firstBytes, russianBytes) {
		t.Fatal("Russian and English Horizon renders are unexpectedly identical")
	}
}

func TestHorizonRenderHonorsCancellation(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "canceled.png")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Horizon(ctx, destination, horizonRenderFixture(), Options{Language: "en"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled render error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled render left output: %v", err)
	}
}

func TestValidateHorizonInputCanonicalizesEveryFrame(t *testing.T) {
	input := horizonRenderFixture()
	for frameIndex := range input.Frames {
		results := input.Frames[frameIndex].Results
		for left, right := 0, len(results)-1; left < right; left, right = left+1, right-1 {
			results[left], results[right] = results[right], results[left]
		}
	}
	frames, runTime, err := validateHorizonInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if !runTime.Equal(input.Frames[0].ValidAt.Add(-time.Hour)) {
		t.Fatalf("run time = %s", runTime)
	}
	for frameIndex, frame := range frames {
		for directionIndex, direction := range forecast.HorizonDirections() {
			if frame.Results[directionIndex].Direction != direction {
				t.Fatalf("frame %d direction %d = %q, want %q", frameIndex, directionIndex, frame.Results[directionIndex].Direction, direction)
			}
		}
	}
}

func TestValidateHorizonInputKeepsGLO30SkylineInformational(t *testing.T) {
	input := horizonRenderFixture()
	samples := make([]forecast.TerrainSkylineSample, forecast.TerrainSkylineAzimuthCount)
	for index := range samples {
		samples[index] = forecast.TerrainSkylineSample{
			AzimuthDegrees: float64(index), ElevationDegrees: 12, ObstacleSurfaceDistanceM: 1000,
		}
	}
	profile, err := forecast.NewSyntheticTerrainSkyline(
		input.Location.Latitude, input.Location.Longitude, 10, samples,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := forecast.ApplyTerrainSkylineToHorizon(input.Frames, profile); err != nil {
		t.Fatal(err)
	}
	input.TerrainSkyline = profile
	wantIndex := input.Frames[0].Results[0].Index
	if _, _, err := validateHorizonInput(input); err != nil {
		t.Fatalf("informational GLO-30 obstruction was rejected: %v", err)
	}
	result := input.Frames[0].Results[0]
	if !result.Available || result.TerrainBlocked || !result.TerrainSectorHasObstructionAtEvaluationElevation || result.Index != wantIndex {
		t.Fatalf("GLO-30 skyline changed the atmospheric cell: %+v", result)
	}
	input.Frames[0].Results[0].TerrainSectorHasObstructionAtEvaluationElevation = false
	if _, _, err := validateHorizonInput(input); err == nil || !strings.Contains(err.Error(), "inconsistent with its GLO-30 sector") {
		t.Fatalf("inconsistent informational obstruction flag error = %v", err)
	}
}

func TestValidateHorizonInputRequiresF001ThroughF072(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*HorizonInput)
		want   string
	}{
		{
			name: "only one hourly term",
			mutate: func(input *HorizonInput) {
				input.Frames = input.Frames[:1]
			},
			want: "exactly 72 hourly frames",
		},
		{
			name: "hourly gap",
			mutate: func(input *HorizonInput) {
				input.Frames[37].ValidAt = input.Frames[37].ValidAt.Add(time.Hour)
			},
			want: "run term f038",
		},
		{
			name: "not f001",
			mutate: func(input *HorizonInput) {
				for index := range input.Frames {
					input.Frames[index].ValidAt = input.Frames[index].ValidAt.Add(time.Hour)
					for resultIndex := range input.Frames[index].Results {
						input.Frames[index].Results[resultIndex].ValidAt = input.Frames[index].ValidAt
					}
				}
			},
			want: "run term f001",
		},
		{
			name: "duplicate direction",
			mutate: func(input *HorizonInput) {
				input.Frames[7].Results[7].Direction = input.Frames[7].Results[0].Direction
			},
			want: "repeats direction",
		},
		{
			name: "mixed result time",
			mutate: func(input *HorizonInput) {
				input.Frames[7].Results[7].ValidAt = input.Frames[7].Results[7].ValidAt.Add(time.Hour)
			},
			want: "another valid time",
		},
		{
			name: "wrong elevation",
			mutate: func(input *HorizonInput) {
				input.Frames[7].Results[7].GeometricElevationDegrees = 15
			},
			want: "is not at geometric 10 degrees",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := horizonRenderFixture()
			test.mutate(&input)
			_, _, err := validateHorizonInput(input)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestHorizonMetadataIsLocalizedAndUsesActualRanges(t *testing.T) {
	input := horizonRenderFixture()
	frames, _, err := validateHorizonInput(input)
	if err != nil {
		t.Fatal(err)
	}
	russian := horizonMetadataLabels(input, frames, Options{Language: "ru"})
	if !strings.Contains(russian.title, "72 часа") || !strings.Contains(russian.utcRange, "f001…f072") ||
		!strings.Contains(russian.localRange, "MSK (UTC+3)") || !strings.Contains(russian.periodNote, "актуальность указана в подписи") {
		t.Fatalf("unexpected Russian labels: %+v", russian)
	}
	english := horizonMetadataLabels(input, frames, Options{Language: "en"})
	if !strings.Contains(english.title, "72 hours") || !strings.Contains(english.utcRange, "72 terms") ||
		!strings.Contains(english.localRange, "MSK (UTC+3)") || !strings.Contains(english.periodNote, "live freshness") {
		t.Fatalf("unexpected English labels: %+v", english)
	}
	if strings.Contains(strings.ToLower(russian.localRange), "generated") || strings.Contains(strings.ToLower(english.localRange), "generated") {
		t.Fatal("renderer must not invent generated-at metadata")
	}
}

func TestHorizonMissingDataHasExplicitCue(t *testing.T) {
	missing := forecast.HorizonResult{Available: false, DataQualityHeuristic: 0, DataQuality: forecast.HorizonDataUnavailable}
	if got := horizonScoreLabel(missing); got != "—" {
		t.Fatalf("missing score = %q", got)
	}
	if got := horizonDataQualityColor(missing); got != horizonQualityBad {
		t.Fatalf("missing quality color = %#v", got)
	}
	input := horizonRenderFixture()
	input.Frames[31].Results[4] = missing
	input.Frames[31].Results[4].ValidAt = input.Frames[31].ValidAt
	input.Frames[31].Results[4].Direction = forecast.HorizonSouth
	input.Frames[31].Results[4].AzimuthDegrees = 180
	input.Frames[31].Results[4].GeometricElevationDegrees = forecast.HorizonGeometricElevationDegrees
	input.Frames[31].Results[4].LimitingFactor = forecast.HorizonFactorUnavailable
	if _, _, err := validateHorizonInput(input); err != nil {
		t.Fatalf("explicit unavailable cell should remain renderable: %v", err)
	}
}

func TestHorizonScoreLabelAlwaysShowsOneDecimal(t *testing.T) {
	for _, test := range []struct {
		index float64
		want  string
	}{{1, "1.0"}, {1.34, "1.3"}, {9.96, "10.0"}} {
		result := forecast.HorizonResult{Available: true, Index: test.index}
		if got := horizonScoreLabel(result); got != test.want {
			t.Fatalf("score %.2f label = %q, want %q", test.index, got, test.want)
		}
	}
}

func TestHorizonLabelsAndLimiterSummaryAreLocalized(t *testing.T) {
	if got := horizonDirectionLabel(forecast.HorizonNorthEast, Options{Language: "ru"}); got != "СВ" {
		t.Fatalf("Russian direction = %q", got)
	}
	if got := horizonDirectionLabel(forecast.HorizonNorthEast, Options{Language: "en"}); got != "NE" {
		t.Fatalf("English direction = %q", got)
	}
	if got := horizonLimiterLabel(forecast.HorizonFactorCloud, Options{Language: "ru"}); got != "эффективная облачная преграда" {
		t.Fatalf("Russian limiter = %q", got)
	}
	if got := horizonLimiterLabel(forecast.HorizonFactorCloud, Options{Language: "en"}); got != "effective cloud obstruction" {
		t.Fatalf("English limiter = %q", got)
	}
	if got := horizonLimiterLabel(forecast.HorizonFactorSeeing, Options{Language: "ru"}); got != "оптический сиинг" {
		t.Fatalf("Russian seeing limiter = %q", got)
	}
	if got := horizonLimiterLabel(forecast.HorizonFactorSeeing, Options{Language: "en"}); got != "optical seeing" {
		t.Fatalf("English seeing limiter = %q", got)
	}
	if got := horizonLimiterLabel(forecast.HorizonFactorPrecipitation, Options{Language: "ru"}); got != "осадки" {
		t.Fatalf("Russian precipitation limiter = %q", got)
	}
	if got := horizonLimiterLabel(forecast.HorizonFactorPrecipitation, Options{Language: "en"}); got != "precipitation" {
		t.Fatalf("English precipitation limiter = %q", got)
	}
	input := horizonRenderFixture()
	input.Frames[0].Results[0].LimitingFactor = forecast.HorizonFactorPrecipitation
	if _, _, err := validateHorizonInput(input); err != nil {
		t.Fatalf("precipitation-limited Horizon result must remain renderable: %v", err)
	}
	input.Frames[0].Results[0].LimitingFactor = forecast.HorizonFactorCloud
	frames, _, err := validateHorizonInput(input)
	if err != nil {
		t.Fatal(err)
	}
	factor, hours := dominantHorizonLimiter(frames, 0)
	if factor != forecast.HorizonFactorCloud || hours != horizonFrameCount {
		t.Fatalf("dominant limiter = %q for %d hours", factor, hours)
	}
	for frameIndex := range frames {
		frames[frameIndex].Results[0].LimitingFactor = forecast.HorizonFactorPrecipitation
	}
	factor, hours = dominantHorizonLimiter(frames, 0)
	if factor != forecast.HorizonFactorPrecipitation || hours != horizonFrameCount {
		t.Fatalf("precipitation-dominant limiter = %q for %d hours", factor, hours)
	}
}

func horizonRenderFixture() HorizonInput {
	runTime := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	factors := []forecast.HorizonLimitingFactor{
		forecast.HorizonFactorCloud,
		forecast.HorizonFactorSeeing,
		forecast.HorizonFactorCoherence,
		forecast.HorizonFactorFog,
		forecast.HorizonFactorSurfaceWind,
		forecast.HorizonFactorTerrain,
		forecast.HorizonFactorNone,
		forecast.HorizonFactorUnavailable,
	}
	frames := make([]forecast.HorizonFrame, horizonFrameCount)
	for frameIndex := range frames {
		validAt := runTime.Add(time.Duration(frameIndex+1) * time.Hour)
		results := make([]forecast.HorizonResult, 0, forecast.HorizonDirectionCount)
		for directionIndex, direction := range forecast.HorizonDirections() {
			available := directionIndex != forecast.HorizonDirectionCount-1 || frameIndex%9 != 0
			quality := forecast.HorizonDataGood
			dataQualityHeuristic := 0.84 - float64(directionIndex)*0.04
			if !available {
				quality = forecast.HorizonDataUnavailable
				dataQualityHeuristic = 0
			}
			results = append(results, forecast.HorizonResult{
				ValidAt: validAt, Direction: direction, AzimuthDegrees: float64(directionIndex * 45),
				GeometricElevationDegrees: forecast.HorizonGeometricElevationDegrees,
				Index:                     1 + mathMod(float64(frameIndex+directionIndex), 9), Available: available, DataQualityHeuristic: dataQualityHeuristic,
				DataQuality: quality, LimitingFactor: factors[directionIndex],
			})
		}
		frames[frameIndex] = forecast.HorizonFrame{ValidAt: validAt, Results: results}
	}
	return HorizonInput{
		Location: forecast.Location{Latitude: 59.9386, Longitude: 30.3141, TimeZone: "Europe/Moscow"},
		Provider: "ICON-EU", RunID: "2026072212", Grid: "0.0625°", Frames: frames,
	}
}

func mathMod(value, modulus float64) float64 {
	for value >= modulus {
		value -= modulus
	}
	return value
}
