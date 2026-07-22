package iconeu

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type surfaceField struct {
	Directory string
	Code      string
	ShortName string
}

// Keep manifests and the application model stable across ecCodes versions and
// local-definition tables by normalizing known aliases at the boundary.
func canonicalSurfaceShortName(name string) string {
	switch strings.TrimSpace(name) {
	case "max_i10fg":
		return "VMAX_10M"
	case "MH", "mld":
		return "mld"
	default:
		return strings.TrimSpace(name)
	}
}

var surfaceFields = []surfaceField{
	{Directory: "t_2m", Code: "T_2M", ShortName: "2t"},
	{Directory: "td_2m", Code: "TD_2M", ShortName: "2d"},
	{Directory: "relhum_2m", Code: "RELHUM_2M", ShortName: "2r"},
	{Directory: "clct", Code: "CLCT", ShortName: "CLCT"},
	{Directory: "clcl", Code: "CLCL", ShortName: "CLCL"},
	{Directory: "clcm", Code: "CLCM", ShortName: "CLCM"},
	{Directory: "clch", Code: "CLCH", ShortName: "CLCH"},
	{Directory: "tot_prec", Code: "TOT_PREC", ShortName: "tp"},
	{Directory: "u_10m", Code: "U_10M", ShortName: "10u"},
	{Directory: "v_10m", Code: "V_10M", ShortName: "10v"},
	{Directory: "vmax_10m", Code: "VMAX_10M", ShortName: "VMAX_10M"},
	{Directory: "pmsl", Code: "PMSL", ShortName: "prmsl"},
	{Directory: "vis", Code: "VIS", ShortName: "vis"},
	{Directory: "tqv", Code: "TQV", ShortName: "TQV"},
	{Directory: "tqc", Code: "TQC", ShortName: "TQC"},
	{Directory: "tqi", Code: "TQI", ShortName: "TQI"},
	{Directory: "mh", Code: "MH", ShortName: "mld"},
}

// AugmentSurface atomically adds independently validated surface bundles to a
// published wind-profile run. Readers either see the previous complete surface
// section or all hourly steps for forecast hours 0 through 78.
func (client *Client) AugmentSurface(ctx context.Context, dataRoot string, loaded LoadedManifest) (LoadedManifest, error) {
	client.defaults()
	if loaded.HasHourlySurface() {
		return loaded, nil
	}
	stateDirectory := filepath.Join(dataRoot, "state")
	if err := os.MkdirAll(stateDirectory, 0o750); err != nil {
		return LoadedManifest{}, fmt.Errorf("create ICON-EU state directory: %w", err)
	}
	lockPath := filepath.Join(stateDirectory, "icon-eu-sync.lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return LoadedManifest{}, fmt.Errorf("ICON-EU sync lock exists: %s", lockPath)
		}
		return LoadedManifest{}, fmt.Errorf("create ICON-EU surface lock: %w", err)
	}
	_, _ = fmt.Fprintf(lock, "run=%s\nkind=surface\nstarted=%s\npid=%d\n", loaded.RunID, time.Now().UTC().Format(time.RFC3339), os.Getpid())
	_ = lock.Close()
	defer func() { _ = os.Remove(lockPath) }()

	incoming := filepath.Join(loaded.Directory, fmt.Sprintf(".surface-hourly-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(incoming, 0o750); err != nil {
		return LoadedManifest{}, fmt.Errorf("create surface incoming directory: %w", err)
	}
	finalSurfaceName := fmt.Sprintf("surface-hourly-v%d", SurfaceBundleSchemaVersion)
	finalSurface := filepath.Join(loaded.Directory, finalSurfaceName)
	oldSurfaceDirectory := ""
	if len(loaded.SurfaceSteps) > 0 {
		oldSurfaceDirectory = filepath.Dir(loaded.SurfaceSteps[0].File)
	}
	manifestPublished := false
	directoryPublished := false
	defer func() {
		if !manifestPublished {
			_ = os.RemoveAll(incoming)
			if directoryPublished {
				_ = os.RemoveAll(finalSurface)
			}
		}
	}()

	type result struct {
		index  int
		bundle BundleFile
		err    error
	}
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	results := make(chan result, HourlySurfaceStepCount)
	workers := client.Workers
	if workers < 1 {
		workers = 1
	}
	var waitGroup sync.WaitGroup
	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for index := range jobs {
				bundle, err := client.downloadSurfaceStep(workContext, loaded.RunID, index, incoming)
				results <- result{index: index, bundle: bundle, err: err}
				if err != nil {
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := range HourlySurfaceStepCount {
			select {
			case jobs <- index:
			case <-workContext.Done():
				return
			}
		}
	}()
	go func() {
		waitGroup.Wait()
		close(results)
	}()

	bundles := make([]BundleFile, HourlySurfaceStepCount)
	completed := 0
	for item := range results {
		if item.err != nil {
			return LoadedManifest{}, item.err
		}
		bundles[item.index] = item.bundle
		completed++
		client.Progress("ICON-EU %s hourly surface: completed f%03d (%d/%d)", loaded.RunID, item.index, completed, HourlySurfaceStepCount)
	}
	if completed != HourlySurfaceStepCount {
		return LoadedManifest{}, fmt.Errorf("ICON-EU hourly surface sync produced %d of %d steps", completed, HourlySurfaceStepCount)
	}

	if _, err := os.Stat(finalSurface); err == nil {
		if err := os.RemoveAll(finalSurface); err != nil {
			return LoadedManifest{}, fmt.Errorf("remove orphan hourly surface directory: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return LoadedManifest{}, fmt.Errorf("inspect surface directory: %w", err)
	}
	if err := os.Rename(incoming, finalSurface); err != nil {
		return LoadedManifest{}, fmt.Errorf("publish ICON-EU hourly surface directory: %w", err)
	}
	directoryPublished = true

	manifest := loaded.Manifest
	for index := range manifest.Steps {
		manifest.Steps[index].Surface = nil
	}
	manifest.SurfaceSteps = make([]SurfaceStepFile, HourlySurfaceStepCount)
	for index := range bundles {
		bundle := bundles[index]
		manifest.SurfaceSteps[index] = SurfaceStepFile{
			ForecastHour: index, ValidAt: manifest.BaseTime.Add(time.Duration(index) * time.Hour),
			File: filepath.Join(finalSurfaceName, filepath.Base(bundle.File)), Bytes: bundle.Bytes,
			SHA256: bundle.SHA256, Messages: bundle.Messages,
		}
	}
	manifest.SurfaceVariables = make([]string, len(surfaceFields))
	for index, field := range surfaceFields {
		manifest.SurfaceVariables[index] = field.ShortName
	}
	publishedAt := time.Now().UTC()
	manifest.SurfacePublishedAt = &publishedAt
	if err := writeManifest(filepath.Join(loaded.Directory, "manifest.json"), manifest); err != nil {
		return LoadedManifest{}, fmt.Errorf("publish hourly surface manifest: %w", err)
	}
	manifestPublished = true
	// Old directories are reclaimed only after the manifest atomically points
	// every reader at the new versioned field set.
	if oldSurfaceDirectory != "" && oldSurfaceDirectory != "." && oldSurfaceDirectory != finalSurfaceName && filepath.Dir(oldSurfaceDirectory) == "." {
		_ = os.RemoveAll(filepath.Join(loaded.Directory, oldSurfaceDirectory))
	}
	_ = os.RemoveAll(filepath.Join(loaded.Directory, "surface"))
	return LoadedManifest{Manifest: manifest, Directory: loaded.Directory}, nil
}

func hasAllSurfaceVariables(variables []string) bool {
	seen := make(map[string]bool, len(variables))
	for _, variable := range variables {
		seen[variable] = true
	}
	for _, field := range surfaceFields {
		if !seen[field.ShortName] {
			return false
		}
	}
	return true
}

func (client *Client) downloadSurfaceStep(ctx context.Context, runID string, forecastHour int, directory string) (BundleFile, error) {
	if len(surfaceFields) != SurfaceBundleSchemaVersion {
		return BundleFile{}, fmt.Errorf("surface field count %d does not match bundle schema v%d", len(surfaceFields), SurfaceBundleSchemaVersion)
	}
	name := fmt.Sprintf("f%03d.grib2", forecastHour)
	destination := filepath.Join(directory, name)
	file, err := os.OpenFile(destination+".part", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return BundleFile{}, err
	}
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(destination + ".part")
		}
	}()
	for _, field := range surfaceFields {
		if err := client.appendField(ctx, file, client.surfaceFieldURL(runID, forecastHour, field)); err != nil {
			return BundleFile{}, fmt.Errorf("download surface f%03d %s: %w", forecastHour, field.ShortName, err)
		}
	}
	if err := file.Sync(); err != nil {
		return BundleFile{}, err
	}
	if err := file.Close(); err != nil {
		return BundleFile{}, err
	}
	if err := client.validateSurfaceBundle(ctx, destination+".part"); err != nil {
		return BundleFile{}, err
	}
	digest, size, err := fileDigest(destination + ".part")
	if err != nil {
		return BundleFile{}, err
	}
	if err := os.Rename(destination+".part", destination); err != nil {
		return BundleFile{}, err
	}
	failed = false
	return BundleFile{File: destination, Bytes: size, SHA256: digest, Messages: SurfaceBundleSchemaVersion}, nil
}

func (client *Client) validateSurfaceBundle(ctx context.Context, path string) error {
	countOutput, err := client.Runner.CombinedOutput(ctx, "grib_count", path)
	if err != nil {
		return fmt.Errorf("grib_count %s failed", filepath.Base(path))
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(countOutput)))
	if err != nil || count != SurfaceBundleSchemaVersion {
		return fmt.Errorf("%s contains %d surface messages, expected %d", filepath.Base(path), count, SurfaceBundleSchemaVersion)
	}
	metadata, err := client.Runner.CombinedOutput(ctx, "grib_get", "-p", "shortName", path)
	if err != nil {
		return fmt.Errorf("grib_get %s failed", filepath.Base(path))
	}
	seen := make(map[string]bool, len(surfaceFields))
	scanner := bufio.NewScanner(strings.NewReader(string(metadata)))
	for scanner.Scan() {
		seen[canonicalSurfaceShortName(scanner.Text())] = true
	}
	for _, field := range surfaceFields {
		if !seen[field.ShortName] {
			return fmt.Errorf("%s is missing surface field %s", filepath.Base(path), field.ShortName)
		}
	}
	mixedLayerUnits, err := client.Runner.CombinedOutput(ctx, "grib_get", "-w", "shortName=mld", "-p", "units", path)
	if err != nil || strings.TrimSpace(string(mixedLayerUnits)) != "m" {
		return fmt.Errorf("%s mixed-layer depth must use metres", filepath.Base(path))
	}
	return nil
}

func (client *Client) surfaceFieldURL(runID string, forecastHour int, field surfaceField) string {
	cycle := runID[len(runID)-2:]
	name := fmt.Sprintf("icon-eu_europe_regular-lat-lon_single-level_%s_%03d_%s.grib2.bz2", runID, forecastHour, field.Code)
	return fmt.Sprintf("%s/%s/%s/%s", strings.TrimRight(client.BaseURL, "/"), cycle, field.Directory, name)
}
