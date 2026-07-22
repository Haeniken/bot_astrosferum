package iconeu

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultCloudModelLevels samples the free atmosphere sparsely while retaining
// every full model layer from 58 through 74. The continuous lower-atmosphere
// section is needed for ground-layer turbulence and still keeps the 79-hour
// bundle substantially smaller than a complete 74-level publication.
var DefaultCloudModelLevels = []int{
	25, 30, 35, 40, 45, 48, 50, 52, 54, 56,
	58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74,
}

var DefaultCloudGroundModelLevels = []int{
	58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74,
}

type cloudLevelField struct {
	directory string
	code      string
	shortName string
}

var cloudBaseLevelFields = []cloudLevelField{
	{directory: "clc", code: "CLC", shortName: "ccl"},
	{directory: "p", code: "P", shortName: "pres"},
	{directory: "t", code: "T", shortName: "t"},
	{directory: "qc", code: "QC", shortName: "qc"},
	{directory: "qi", code: "QI", shortName: "qi"},
}

var cloudGroundFullLevelFields = []cloudLevelField{
	{directory: "u", code: "U", shortName: "u"},
	{directory: "v", code: "V", shortName: "v"},
}

// ecCodes maps the DWD QC/QI parameter pair inconsistently across local and
// WMO tables (currently clwmr and QI in 2.45). Normalize at ingestion so the
// manifest and point-cache schema remain stable across tool upgrades.
func canonicalCloudShortName(name string) string {
	switch strings.TrimSpace(name) {
	case "clwmr", "QC", "qc":
		return "qc"
	case "QI", "qi":
		return "qi"
	case "T", "t":
		return "t"
	case "U", "u":
		return "u"
	case "V", "v":
		return "v"
	case "TKE", "tke":
		return "tke"
	default:
		return strings.TrimSpace(name)
	}
}

// AugmentCloud atomically publishes hourly model-layer cover/condensate and
// pressure plus one time-invariant HHL geometry bundle.
func (client *Client) AugmentCloud(ctx context.Context, dataRoot string, loaded LoadedManifest) (LoadedManifest, error) {
	client.defaults()
	if loaded.HasHourlyCloud() {
		return loaded, nil
	}
	lockPath := filepath.Join(dataRoot, "state", "icon-eu-sync.lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return LoadedManifest{}, fmt.Errorf("ICON-EU sync lock exists: %s", lockPath)
		}
		return LoadedManifest{}, fmt.Errorf("create ICON-EU cloud lock: %w", err)
	}
	_, _ = fmt.Fprintf(lock, "run=%s\nkind=cloud\nstarted=%s\npid=%d\n", loaded.RunID, time.Now().UTC().Format(time.RFC3339), os.Getpid())
	_ = lock.Close()
	defer func() { _ = os.Remove(lockPath) }()

	incoming := filepath.Join(loaded.Directory, fmt.Sprintf(".cloud-hourly-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(incoming, 0o750); err != nil {
		return LoadedManifest{}, fmt.Errorf("create cloud incoming directory: %w", err)
	}
	finalName := "cloud-hourly-v4"
	finalDirectory := filepath.Join(loaded.Directory, finalName)
	oldDirectory := ""
	if len(loaded.CloudSteps) > 0 {
		oldDirectory = filepath.Dir(loaded.CloudSteps[0].File)
	}
	manifestPublished := false
	directoryPublished := false
	defer func() {
		if !manifestPublished {
			_ = os.RemoveAll(incoming)
			if directoryPublished {
				_ = os.RemoveAll(finalDirectory)
			}
		}
	}()

	geometry, err := client.downloadCloudGeometry(ctx, loaded.RunID, incoming)
	if err != nil {
		return LoadedManifest{}, err
	}

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
				bundle, err := client.downloadCloudStep(workContext, loaded.RunID, index, incoming)
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
		client.Progress("ICON-EU %s hourly cloud: completed f%03d (%d/%d)", loaded.RunID, item.index, completed, HourlySurfaceStepCount)
	}
	if completed != HourlySurfaceStepCount {
		return LoadedManifest{}, fmt.Errorf("ICON-EU hourly cloud sync produced %d of %d steps", completed, HourlySurfaceStepCount)
	}

	if _, err := os.Stat(finalDirectory); err == nil {
		if err := os.RemoveAll(finalDirectory); err != nil {
			return LoadedManifest{}, fmt.Errorf("remove orphan cloud directory: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return LoadedManifest{}, fmt.Errorf("inspect cloud directory: %w", err)
	}
	if err := os.Rename(incoming, finalDirectory); err != nil {
		return LoadedManifest{}, fmt.Errorf("publish hourly cloud directory: %w", err)
	}
	directoryPublished = true

	manifest := loaded.Manifest
	manifest.CloudVariables = []string{"ccl", "pres", "qc", "qi", "t", "u", "v", "tke", "HHL"}
	manifest.CloudModelLevels = append([]int(nil), DefaultCloudModelLevels...)
	publishedAt := time.Now().UTC()
	manifest.CloudPublishedAt = &publishedAt
	manifest.CloudGeometry = &BundleFile{
		File: filepath.Join(finalName, filepath.Base(geometry.File)), Bytes: geometry.Bytes,
		SHA256: geometry.SHA256, Messages: geometry.Messages,
	}
	manifest.CloudSteps = make([]SurfaceStepFile, HourlySurfaceStepCount)
	for index, bundle := range bundles {
		manifest.CloudSteps[index] = SurfaceStepFile{
			ForecastHour: index, ValidAt: manifest.BaseTime.Add(time.Duration(index) * time.Hour),
			File: filepath.Join(finalName, filepath.Base(bundle.File)), Bytes: bundle.Bytes,
			SHA256: bundle.SHA256, Messages: bundle.Messages,
		}
	}
	if err := writeManifest(filepath.Join(loaded.Directory, "manifest.json"), manifest); err != nil {
		return LoadedManifest{}, fmt.Errorf("publish hourly cloud manifest: %w", err)
	}
	manifestPublished = true
	if oldDirectory != "" && oldDirectory != "." && oldDirectory != finalName && filepath.Dir(oldDirectory) == "." {
		_ = os.RemoveAll(filepath.Join(loaded.Directory, oldDirectory))
	}
	return LoadedManifest{Manifest: manifest, Directory: loaded.Directory}, nil
}

func (client *Client) downloadCloudGeometry(ctx context.Context, runID, directory string) (BundleFile, error) {
	levels := cloudGeometryLevels()
	return client.downloadCloudBundle(ctx, filepath.Join(directory, "geometry.grib2"), len(levels), func(file *os.File) error {
		for _, level := range levels {
			if err := client.appendField(ctx, file, client.hhlFieldURL(runID, level)); err != nil {
				return fmt.Errorf("download HHL level %d: %w", level, err)
			}
		}
		return nil
	}, client.validateCloudGeometry)
}

func (client *Client) downloadCloudStep(ctx context.Context, runID string, forecastHour int, directory string) (BundleFile, error) {
	name := fmt.Sprintf("f%03d.grib2", forecastHour)
	return client.downloadCloudBundle(ctx, filepath.Join(directory, name), cloudStepMessageCount(), func(file *os.File) error {
		for _, level := range DefaultCloudModelLevels {
			for _, field := range cloudBaseLevelFields {
				if err := client.appendField(ctx, file, client.modelLevelFieldURL(runID, forecastHour, level, field.directory, field.code)); err != nil {
					return fmt.Errorf("download cloud f%03d level %d %s: %w", forecastHour, level, field.code, err)
				}
			}
		}
		for _, level := range DefaultCloudGroundModelLevels {
			for _, field := range cloudGroundFullLevelFields {
				if err := client.appendField(ctx, file, client.modelLevelFieldURL(runID, forecastHour, level, field.directory, field.code)); err != nil {
					return fmt.Errorf("download cloud f%03d ground level %d %s: %w", forecastHour, level, field.code, err)
				}
			}
		}
		for _, level := range cloudTKEHalfLevels() {
			if err := client.appendField(ctx, file, client.modelLevelFieldURL(runID, forecastHour, level, "tke", "TKE")); err != nil {
				return fmt.Errorf("download cloud f%03d half-level %d TKE: %w", forecastHour, level, err)
			}
		}
		return nil
	}, client.validateCloudStep)
}

func (client *Client) downloadCloudBundle(ctx context.Context, destination string, messages int, appendMessages func(*os.File) error, validate func(context.Context, string) error) (BundleFile, error) {
	temporary := destination + ".part"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return BundleFile{}, err
	}
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(temporary)
		}
	}()
	if err := appendMessages(file); err != nil {
		return BundleFile{}, err
	}
	if err := file.Sync(); err != nil {
		return BundleFile{}, err
	}
	if err := file.Close(); err != nil {
		return BundleFile{}, err
	}
	if err := validate(ctx, temporary); err != nil {
		return BundleFile{}, err
	}
	digest, size, err := fileDigest(temporary)
	if err != nil {
		return BundleFile{}, err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return BundleFile{}, err
	}
	failed = false
	return BundleFile{File: destination, Bytes: size, SHA256: digest, Messages: messages}, nil
}

func (client *Client) validateCloudGeometry(ctx context.Context, path string) error {
	levels := cloudGeometryLevels()
	if err := client.validateCloudMetadata(ctx, path, len(levels), map[string]bool{"HHL": true}, "generalVertical", levels); err != nil {
		return err
	}
	return nil
}

func (client *Client) validateCloudStep(ctx context.Context, path string) error {
	countOutput, err := client.Runner.CombinedOutput(ctx, "grib_count", path)
	if err != nil {
		return fmt.Errorf("grib_count %s failed", filepath.Base(path))
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(countOutput)))
	if err != nil || count != cloudStepMessageCount() {
		return fmt.Errorf("%s contains %d cloud messages, expected %d", filepath.Base(path), count, cloudStepMessageCount())
	}
	metadata, err := client.Runner.CombinedOutput(ctx, "grib_get", "-p", "shortName,typeOfLevel,level", path)
	if err != nil {
		return fmt.Errorf("grib_get %s failed", filepath.Base(path))
	}
	expected := make(map[string]bool, cloudStepMessageCount())
	for _, level := range DefaultCloudModelLevels {
		for _, field := range cloudBaseLevelFields {
			expected[fmt.Sprintf("%s:generalVerticalLayer:%d", field.shortName, level)] = false
		}
	}
	for _, level := range DefaultCloudGroundModelLevels {
		for _, field := range cloudGroundFullLevelFields {
			expected[fmt.Sprintf("%s:generalVerticalLayer:%d", field.shortName, level)] = false
		}
	}
	for _, level := range cloudTKEHalfLevels() {
		expected[fmt.Sprintf("tke:generalVertical:%d", level)] = false
	}
	scanner := bufio.NewScanner(strings.NewReader(string(metadata)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			return fmt.Errorf("unexpected cloud metadata in %s: %q", filepath.Base(path), scanner.Text())
		}
		key := fmt.Sprintf("%s:%s:%s", canonicalCloudShortName(fields[0]), fields[1], fields[2])
		seen, ok := expected[key]
		if !ok || seen {
			return fmt.Errorf("unexpected or duplicate cloud metadata in %s: %q", filepath.Base(path), scanner.Text())
		}
		expected[key] = true
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	for key, seen := range expected {
		if !seen {
			return fmt.Errorf("%s is missing cloud field %s", filepath.Base(path), key)
		}
	}
	return nil
}

func (client *Client) validateCloudMetadata(ctx context.Context, path string, expectedCount int, allowed map[string]bool, levelType string, expectedLevels []int) error {
	countOutput, err := client.Runner.CombinedOutput(ctx, "grib_count", path)
	if err != nil {
		return fmt.Errorf("grib_count %s failed", filepath.Base(path))
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(countOutput)))
	if err != nil || count != expectedCount {
		return fmt.Errorf("%s contains %d cloud messages, expected %d", filepath.Base(path), count, expectedCount)
	}
	metadata, err := client.Runner.CombinedOutput(ctx, "grib_get", "-p", "shortName,typeOfLevel,level", path)
	if err != nil {
		return fmt.Errorf("grib_get %s failed", filepath.Base(path))
	}
	seen := make(map[string]bool, expectedCount)
	scanner := bufio.NewScanner(strings.NewReader(string(metadata)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			return fmt.Errorf("unexpected cloud metadata in %s: %q", filepath.Base(path), scanner.Text())
		}
		name := canonicalCloudShortName(fields[0])
		if !allowed[name] || fields[1] != levelType {
			return fmt.Errorf("unexpected cloud metadata in %s: %q", filepath.Base(path), scanner.Text())
		}
		seen[name+":"+fields[2]] = true
	}
	for _, level := range expectedLevels {
		for variable := range allowed {
			if !seen[fmt.Sprintf("%s:%d", variable, level)] {
				return fmt.Errorf("%s is missing %s at model level %d", filepath.Base(path), variable, level)
			}
		}
	}
	return nil
}

func cloudGeometryLevels() []int {
	seen := make(map[int]bool, len(DefaultCloudModelLevels)*2)
	for _, level := range DefaultCloudModelLevels {
		seen[level] = true
		seen[level+1] = true
	}
	levels := make([]int, 0, len(seen))
	for level := range seen {
		levels = append(levels, level)
	}
	sort.Ints(levels)
	return levels
}

func cloudTKEHalfLevels() []int {
	seen := make(map[int]bool, len(DefaultCloudGroundModelLevels)+1)
	for _, level := range DefaultCloudGroundModelLevels {
		seen[level] = true
		seen[level+1] = true
	}
	levels := make([]int, 0, len(seen))
	for level := range seen {
		levels = append(levels, level)
	}
	sort.Ints(levels)
	return levels
}

func cloudStepMessageCount() int {
	return len(DefaultCloudModelLevels)*len(cloudBaseLevelFields) +
		len(DefaultCloudGroundModelLevels)*len(cloudGroundFullLevelFields) +
		len(cloudTKEHalfLevels())
}

func isCloudGroundModelLevel(level int) bool {
	return level >= DefaultCloudGroundModelLevels[0] && level <= DefaultCloudGroundModelLevels[len(DefaultCloudGroundModelLevels)-1]
}

func (client *Client) modelLevelFieldURL(runID string, forecastHour, level int, directory, code string) string {
	cycle := runID[len(runID)-2:]
	name := fmt.Sprintf("icon-eu_europe_regular-lat-lon_model-level_%s_%03d_%d_%s.grib2.bz2", runID, forecastHour, level, code)
	return fmt.Sprintf("%s/%s/%s/%s", strings.TrimRight(client.BaseURL, "/"), cycle, directory, name)
}

func (client *Client) hhlFieldURL(runID string, level int) string {
	cycle := runID[len(runID)-2:]
	name := fmt.Sprintf("icon-eu_europe_regular-lat-lon_time-invariant_%s_%d_HHL.grib2.bz2", runID, level)
	return fmt.Sprintf("%s/%s/hhl/%s", strings.TrimRight(client.BaseURL, "/"), cycle, name)
}
