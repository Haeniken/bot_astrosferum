package iconglobal

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ICON Global has 120 full levels versus ICON-EU's 74. These levels match
// the physical HHL heights used by the EU subset (the index offset is +46).
var globalCloudModelLevels = []int{
	71, 76, 81, 86, 91, 94, 96, 98, 100, 102,
	104, 105, 106, 107, 108, 109, 110, 111, 112, 113, 114, 115, 116, 117, 118, 119, 120,
}

var globalCloudGroundModelLevels = []int{
	104, 105, 106, 107, 108, 109, 110, 111, 112, 113, 114, 115, 116, 117, 118, 119, 120,
}

const globalSurfaceHalfLevel = 121

type cloudField struct {
	directory string
	code      string
	shortName string
}

var globalCloudBaseFields = []cloudField{
	{directory: "clc", code: "CLC", shortName: "ccl"},
	{directory: "p", code: "P", shortName: "pres"},
	{directory: "t", code: "T", shortName: "t"},
	{directory: "qc", code: "QC", shortName: "qc"},
	{directory: "qi", code: "QI", shortName: "qi"},
}

var globalCloudGroundFields = []cloudField{
	{directory: "u", code: "U", shortName: "u"},
	{directory: "v", code: "V", shortName: "v"},
}

// AugmentCloud atomically adds the same height-resolved cloud and PBL data
// contract used by ICON-EU to an already published ICON Global run.
func (client *Client) AugmentCloud(ctx context.Context, dataRoot string, loaded LoadedManifest) (LoadedManifest, error) {
	client.defaults()
	if loaded.HasHourlyCloud() {
		return loaded, nil
	}
	lockPath := filepath.Join(dataRoot, "state", "icon-global-sync.lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return LoadedManifest{}, fmt.Errorf("ICON Global sync lock exists: %s", lockPath)
		}
		return LoadedManifest{}, fmt.Errorf("create ICON Global cloud lock: %w", err)
	}
	_, _ = fmt.Fprintf(lock, "run=%s\nkind=cloud\nstarted=%s\npid=%d\n", loaded.RunID, time.Now().UTC().Format(time.RFC3339), os.Getpid())
	_ = lock.Close()
	defer func() { _ = os.Remove(lockPath) }()

	incoming := filepath.Join(loaded.Directory, fmt.Sprintf(".cloud-hourly-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(incoming, 0o750); err != nil {
		return LoadedManifest{}, fmt.Errorf("create ICON Global cloud incoming directory: %w", err)
	}
	finalName := "cloud-hourly-v1"
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

	geometry, err := client.downloadGlobalCloudGeometry(ctx, loaded.RunID, incoming)
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
	results := make(chan result, 79)
	var group sync.WaitGroup
	for range max(1, client.Workers) {
		group.Add(1)
		go func() {
			defer group.Done()
			for hour := range jobs {
				bundle, downloadError := client.downloadGlobalCloudStep(workContext, loaded.RunID, hour, incoming)
				results <- result{index: hour, bundle: bundle, err: downloadError}
				if downloadError != nil {
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for hour := range 79 {
			select {
			case jobs <- hour:
			case <-workContext.Done():
				return
			}
		}
	}()
	go func() { group.Wait(); close(results) }()

	bundles := make([]BundleFile, 79)
	completed := 0
	for item := range results {
		if item.err != nil {
			return LoadedManifest{}, item.err
		}
		bundles[item.index] = item.bundle
		completed++
		client.Progress("ICON Global %s hourly cloud: completed f%03d (%d/%d)", loaded.RunID, item.index, completed, 79)
	}
	if completed != 79 {
		return LoadedManifest{}, fmt.Errorf("ICON Global hourly cloud sync produced %d of 79 steps", completed)
	}
	if _, err := os.Stat(finalDirectory); err == nil {
		if err := os.RemoveAll(finalDirectory); err != nil {
			return LoadedManifest{}, fmt.Errorf("remove orphan ICON Global cloud directory: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return LoadedManifest{}, fmt.Errorf("inspect ICON Global cloud directory: %w", err)
	}
	if err := os.Rename(incoming, finalDirectory); err != nil {
		return LoadedManifest{}, fmt.Errorf("publish ICON Global cloud directory: %w", err)
	}
	directoryPublished = true

	manifest := loaded.Manifest
	manifest.Product = "native pressure/single-level/model-level"
	manifest.CloudVariables = []string{"ccl", "pres", "qc", "qi", "t", "u", "v", "tke", "HHL"}
	manifest.CloudModelLevels = append([]int(nil), globalCloudModelLevels...)
	publishedAt := time.Now().UTC()
	manifest.CloudPublishedAt = &publishedAt
	manifest.CloudGeometry = &BundleFile{
		File: filepath.Join(finalName, filepath.Base(geometry.File)), Bytes: geometry.Bytes,
		SHA256: geometry.SHA256, Messages: geometry.Messages,
	}
	manifest.CloudSteps = make([]StepFile, 79)
	for hour, bundle := range bundles {
		manifest.CloudSteps[hour] = StepFile{
			ForecastHour: hour, ValidAt: manifest.BaseTime.Add(time.Duration(hour) * time.Hour),
			File: filepath.Join(finalName, filepath.Base(bundle.File)), Bytes: bundle.Bytes,
			SHA256: bundle.SHA256, Messages: bundle.Messages,
		}
	}
	if err := writeManifest(filepath.Join(loaded.Directory, "manifest.json"), manifest); err != nil {
		return LoadedManifest{}, fmt.Errorf("publish ICON Global cloud manifest: %w", err)
	}
	manifestPublished = true
	if oldDirectory != "" && oldDirectory != "." && oldDirectory != finalName && filepath.Dir(oldDirectory) == "." {
		_ = os.RemoveAll(filepath.Join(loaded.Directory, oldDirectory))
	}
	return LoadedManifest{Manifest: manifest, Directory: loaded.Directory}, nil
}

func (client *Client) downloadGlobalCloudGeometry(ctx context.Context, runID, directory string) (BundleFile, error) {
	levels := globalCloudGeometryLevels()
	return client.downloadGlobalCloudBundle(ctx, filepath.Join(directory, "geometry.grib2"), len(levels), func(file *os.File) error {
		for _, level := range levels {
			if err := client.appendField(ctx, file, client.globalHHLFieldURL(runID, level)); err != nil {
				return fmt.Errorf("download ICON Global HHL level %d: %w", level, err)
			}
		}
		return nil
	}, func(path string) error {
		return validateGlobalCloudMetadata(ctx, path, len(levels), map[string]bool{"HHL": true}, "generalVertical", levels)
	})
}

func (client *Client) downloadGlobalCloudStep(ctx context.Context, runID string, hour int, directory string) (BundleFile, error) {
	return client.downloadGlobalCloudBundle(ctx, filepath.Join(directory, fmt.Sprintf("f%03d.grib2", hour)), globalCloudStepMessageCount(hour), func(file *os.File) error {
		for _, level := range globalCloudModelLevels {
			for _, field := range globalCloudBaseFields {
				if err := client.appendField(ctx, file, client.globalModelLevelFieldURL(runID, hour, level, field)); err != nil {
					return fmt.Errorf("download ICON Global cloud f%03d level %d %s: %w", hour, level, field.code, err)
				}
			}
		}
		for _, level := range globalCloudGroundModelLevels {
			for _, field := range globalCloudGroundFields {
				if err := client.appendField(ctx, file, client.globalModelLevelFieldURL(runID, hour, level, field)); err != nil {
					return fmt.Errorf("download ICON Global cloud f%03d level %d %s: %w", hour, level, field.code, err)
				}
			}
		}
		if globalTKEAvailable(hour) {
			for _, level := range globalCloudTKEHalfLevels() {
				field := cloudField{directory: "tke", code: "TKE", shortName: "tke"}
				if err := client.appendField(ctx, file, client.globalModelLevelFieldURL(runID, hour, level, field)); err != nil {
					return fmt.Errorf("download ICON Global cloud f%03d half-level %d TKE: %w", hour, level, err)
				}
			}
		}
		return nil
	}, func(path string) error { return validateGlobalCloudStep(ctx, path) })
}

func (client *Client) downloadGlobalCloudBundle(ctx context.Context, destination string, messages int, appendMessages func(*os.File) error, validate func(string) error) (BundleFile, error) {
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
	if err := validate(temporary); err != nil {
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

func validateGlobalCloudStep(ctx context.Context, path string) error {
	hour, err := cloudForecastHourFromPath(path)
	if err != nil {
		return err
	}
	if err := validateMessageCount(ctx, path, globalCloudStepMessageCount(hour)); err != nil {
		return err
	}
	metadata, err := exec.CommandContext(ctx, "grib_get", "-p", "shortName,typeOfLevel,level", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("grib_get %s failed: %s", filepath.Base(path), strings.TrimSpace(string(metadata)))
	}
	expected := make(map[string]bool, globalCloudStepMessageCount(hour))
	for _, level := range globalCloudModelLevels {
		for _, field := range globalCloudBaseFields {
			expected[fmt.Sprintf("%s:generalVerticalLayer:%d", field.shortName, level)] = false
		}
	}
	for _, level := range globalCloudGroundModelLevels {
		for _, field := range globalCloudGroundFields {
			expected[fmt.Sprintf("%s:generalVerticalLayer:%d", field.shortName, level)] = false
		}
	}
	if globalTKEAvailable(hour) {
		for _, level := range globalCloudTKEHalfLevels() {
			expected[fmt.Sprintf("tke:generalVertical:%d", level)] = false
		}
	}
	return consumeGlobalCloudMetadata(path, metadata, expected)
}

func validateGlobalCloudMetadata(ctx context.Context, path string, count int, allowed map[string]bool, levelType string, levels []int) error {
	if err := validateMessageCount(ctx, path, count); err != nil {
		return err
	}
	metadata, err := exec.CommandContext(ctx, "grib_get", "-p", "shortName,typeOfLevel,level", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("grib_get %s failed: %s", filepath.Base(path), strings.TrimSpace(string(metadata)))
	}
	expected := make(map[string]bool, count)
	for _, level := range levels {
		for name := range allowed {
			expected[fmt.Sprintf("%s:%s:%d", name, levelType, level)] = false
		}
	}
	return consumeGlobalCloudMetadata(path, metadata, expected)
}

func consumeGlobalCloudMetadata(path string, metadata []byte, expected map[string]bool) error {
	scanner := bufio.NewScanner(strings.NewReader(string(metadata)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			return fmt.Errorf("unexpected cloud metadata in %s: %q", filepath.Base(path), scanner.Text())
		}
		key := fmt.Sprintf("%s:%s:%s", canonicalGlobalCloudShortName(fields[0]), fields[1], fields[2])
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

func canonicalGlobalCloudShortName(name string) string {
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

func globalCloudGeometryLevels() []int {
	seen := make(map[int]bool, len(globalCloudModelLevels)*2)
	for _, level := range globalCloudModelLevels {
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

func globalCloudTKEHalfLevels() []int {
	seen := make(map[int]bool, len(globalCloudGroundModelLevels)+1)
	for _, level := range globalCloudGroundModelLevels {
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

func globalCloudStepMessageCount(hour int) int {
	count := len(globalCloudModelLevels)*len(globalCloudBaseFields) +
		len(globalCloudGroundModelLevels)*len(globalCloudGroundFields)
	if globalTKEAvailable(hour) {
		count += len(globalCloudTKEHalfLevels())
	}
	return count
}

func globalTKEAvailable(hour int) bool { return hour >= 0 && hour <= 48 }

func cloudForecastHourFromPath(path string) (int, error) {
	base := filepath.Base(path)
	if len(base) < 4 || base[0] != 'f' {
		return 0, fmt.Errorf("cannot derive cloud forecast hour from %s", base)
	}
	hour, err := strconv.Atoi(base[1:4])
	if err != nil {
		return 0, fmt.Errorf("cannot derive cloud forecast hour from %s", base)
	}
	return hour, nil
}

func (client *Client) globalModelLevelFieldURL(runID string, hour, level int, field cloudField) string {
	cycle := runID[len(runID)-2:]
	name := fmt.Sprintf("icon_global_icosahedral_model-level_%s_%03d_%d_%s.grib2.bz2", runID, hour, level, field.code)
	return fmt.Sprintf("%s/%s/%s/%s", strings.TrimRight(client.BaseURL, "/"), cycle, field.directory, name)
}

func (client *Client) globalHHLFieldURL(runID string, level int) string {
	cycle := runID[len(runID)-2:]
	name := fmt.Sprintf("icon_global_icosahedral_time-invariant_%s_%d_HHL.grib2.bz2", runID, level)
	return fmt.Sprintf("%s/%s/hhl/%s", strings.TrimRight(client.BaseURL, "/"), cycle, name)
}
