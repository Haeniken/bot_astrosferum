package bot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"bot_astrosferum/internal/astronomy"
	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/render"
)

const renderCacheVersion = "shared-render-v21-interactive-axes"

func forecastRenderCacheKey(vertical forecast.VerticalSeries, surface forecast.SurfaceSeries, cloud forecast.CloudSeries, composition forecast.AtmosphericCompositionSeries, sky astronomy.Series, options render.Options, calibration forecast.OverallIndexCalibration) string {
	firstSurface, lastSurface := time.Time{}, time.Time{}
	if len(surface.Frames) > 0 {
		firstSurface, lastSurface = surface.Frames[0].ValidAt, surface.Frames[len(surface.Frames)-1].ValidAt
	}
	firstCloud, lastCloud := time.Time{}, time.Time{}
	if len(cloud.Frames) > 0 {
		firstCloud, lastCloud = cloud.Frames[0].ValidAt, cloud.Frames[len(cloud.Frames)-1].ValidAt
	}
	identity := strings.Join([]string{
		renderCacheVersion,
		"vertical=" + vertical.Provider + "/" + vertical.Product + "/" + vertical.RunID,
		"surface=" + surface.Provider + "/" + surface.Product + "/" + surface.RunID,
		"cloud=" + cloud.Provider + "/" + cloud.Product + "/" + cloud.RunID,
		"composition=" + composition.Provider + "/" + composition.Product + "/" + composition.RunID + "/" + composition.BaseTime.UTC().Format(time.RFC3339) + "/" + composition.Grid + "/" + compositionFrameCacheIdentity(composition.Frames),
		"reference_v=" + forecast.ReferenceVBandContractVersion + "/" + forecast.ReferenceVBandAtmosphereVersion + "/" + forecast.ReferenceVBandBenchmarkVersion + "/" + forecast.ReferenceVBandPassbandID,
		"grid=" + vertical.Grid,
		fmt.Sprintf("location=%.6f,%.6f,%s", vertical.Location.Latitude, vertical.Location.Longitude, vertical.Location.TimeZone),
		"surface_period=" + firstSurface.UTC().Format(time.RFC3339) + "/" + lastSurface.UTC().Format(time.RFC3339),
		"cloud_period=" + firstCloud.UTC().Format(time.RFC3339) + "/" + lastCloud.UTC().Format(time.RFC3339),
		"algorithm=" + vertical.AlgorithmVersion,
		"overall=" + forecast.OverallIndexAlgorithmVersion,
		"cloud_obstruction=" + forecast.CloudObstructionAlgorithmVersion,
		fmt.Sprintf("size=%dx%d", options.Width, options.Height),
		"language=" + options.Language,
		"render=" + render.Version,
		fmt.Sprintf("sky_days=%d", len(sky.Days)),
		fmt.Sprintf("calibration=%#v", calibration),
	}, "|")
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:])
}

func compositionFrameCacheIdentity(frames []forecast.AtmosphericCompositionFrame) string {
	var identity strings.Builder
	for _, frame := range frames {
		_, _ = fmt.Fprintf(&identity, "%s/%016x/%016x/%s/%s/%s/%s|",
			frame.ValidAt.UTC().Format(time.RFC3339Nano),
			math.Float64bits(frame.AerosolOpticalDepth550),
			math.Float64bits(frame.TotalColumnOzoneDU),
			frame.Provider,
			frame.RunID,
			frame.BaseTime.UTC().Format(time.RFC3339Nano),
			frame.Grid+"/"+frame.AerosolSpectralAssumption,
		)
	}
	digest := sha256.Sum256([]byte(identity.String()))
	return hex.EncodeToString(digest[:])
}

func cachedRenderResult(directory string) render.Result {
	return render.Result{
		Weather:          filepath.Join(directory, "weather-hourly.png"),
		CloudObstruction: filepath.Join(directory, "cloud-obstruction-height-hourly.png"),
		WindSpeed:        filepath.Join(directory, "wind-speed.png"),
		VectorShear:      filepath.Join(directory, "wind-vector-shear.png"),
		DirectionDelta:   filepath.Join(directory, "wind-direction-delta.png"),
		SeeingIndex:      filepath.Join(directory, "forecast-seeing-index.png"),
		OverallIndex:     filepath.Join(directory, "overall-astronomy-index-hourly.png"),
		Dataset:          filepath.Join(directory, "forecast.json"),
	}
}

func loadRenderCache(root, key string) (render.Result, bool) {
	directory := filepath.Join(root, key)
	result := cachedRenderResult(directory)
	for _, path := range renderResultPaths(result) {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return render.Result{}, false
		}
	}
	now := time.Now()
	_ = os.Chtimes(directory, now, now)
	return result, true
}

func makeRenderStaging(root string) (string, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", err
	}
	return os.MkdirTemp(root, ".incoming-")
}

func publishRenderCache(root, key, staging string) (render.Result, error) {
	final := filepath.Join(root, key)
	if result, ok := loadRenderCache(root, key); ok {
		_ = os.RemoveAll(staging)
		return result, nil
	}
	if err := os.Rename(staging, final); err != nil {
		if result, ok := loadRenderCache(root, key); ok {
			_ = os.RemoveAll(staging)
			return result, nil
		}
		return render.Result{}, err
	}
	go pruneRenderCache(root, 48*time.Hour, 256)
	return cachedRenderResult(final), nil
}

func renderResultPaths(result render.Result) []string {
	return []string{result.Weather, result.OverallIndex, result.CloudObstruction, result.WindSpeed, result.VectorShear, result.DirectionDelta, result.SeeingIndex, result.Dataset}
}

func pruneRenderCache(root string, maximumAge time.Duration, maximumEntries int) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type candidate struct {
		path     string
		modified time.Time
	}
	var candidates []candidate
	now := time.Now()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".incoming-") {
			if now.Sub(info.ModTime()) > time.Hour {
				_ = os.RemoveAll(filepath.Join(root, entry.Name()))
			}
			continue
		}
		if len(entry.Name()) != 64 {
			continue
		}
		item := candidate{path: filepath.Join(root, entry.Name()), modified: info.ModTime()}
		if now.Sub(item.modified) > maximumAge {
			_ = os.RemoveAll(item.path)
			continue
		}
		candidates = append(candidates, item)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].modified.After(candidates[j].modified) })
	if maximumEntries < 0 {
		maximumEntries = 0
	}
	if maximumEntries < len(candidates) {
		for _, item := range candidates[maximumEntries:] {
			_ = os.RemoveAll(item.path)
		}
	}
}
