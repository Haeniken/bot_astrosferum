package iconeu

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AugmentWindThermodynamics atomically upgrades legacy pressure-level bundles
// with geopotential FI and/or temperature. Existing messages are copied
// locally; only missing fields are downloaded.
func (client *Client) AugmentWindThermodynamics(ctx context.Context, dataRoot string, loaded LoadedManifest) (LoadedManifest, error) {
	client.defaults()
	if loaded.HasWindThermodynamics() {
		return loaded, nil
	}
	lockPath := filepath.Join(dataRoot, "state", "icon-eu-sync.lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return LoadedManifest{}, fmt.Errorf("ICON-EU sync lock exists: %s", lockPath)
		}
		return LoadedManifest{}, fmt.Errorf("create ICON-EU wind-height lock: %w", err)
	}
	_, _ = fmt.Fprintf(lock, "run=%s\nkind=wind-height\nstarted=%s\npid=%d\n", loaded.RunID, time.Now().UTC().Format(time.RFC3339), os.Getpid())
	_ = lock.Close()
	defer func() { _ = os.Remove(lockPath) }()

	incoming := filepath.Join(loaded.Directory, fmt.Sprintf(".steps-v4-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(incoming, 0o750); err != nil {
		return LoadedManifest{}, fmt.Errorf("create wind-height incoming directory: %w", err)
	}
	finalName := "steps-v4"
	finalDirectory := filepath.Join(loaded.Directory, finalName)
	oldDirectory := filepath.Dir(loaded.Steps[0].File)
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

	type result struct {
		index int
		step  StepFile
		err   error
	}
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	results := make(chan result, len(loaded.Steps))
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
				step, err := client.upgradeWindProfileStep(workContext, loaded, loaded.Steps[index], incoming)
				results <- result{index: index, step: step, err: err}
				if err != nil {
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := range loaded.Steps {
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

	steps := make([]StepFile, len(loaded.Steps))
	completed := 0
	for item := range results {
		if item.err != nil {
			return LoadedManifest{}, item.err
		}
		steps[item.index] = item.step
		completed++
		client.Progress("ICON-EU %s wind/temperature profile: completed f%03d (%d/%d)", loaded.RunID, item.step.ForecastHour, completed, len(loaded.Steps))
	}
	if completed != len(loaded.Steps) {
		return LoadedManifest{}, fmt.Errorf("ICON-EU wind-height sync produced %d of %d steps", completed, len(loaded.Steps))
	}

	if _, err := os.Stat(finalDirectory); err == nil {
		if oldDirectory == finalName {
			return LoadedManifest{}, fmt.Errorf("current wind-height directory is inconsistent")
		}
		if err := os.RemoveAll(finalDirectory); err != nil {
			return LoadedManifest{}, fmt.Errorf("remove orphan wind-height directory: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return LoadedManifest{}, fmt.Errorf("inspect wind-height directory: %w", err)
	}
	if err := os.Rename(incoming, finalDirectory); err != nil {
		return LoadedManifest{}, fmt.Errorf("publish wind-height directory: %w", err)
	}
	directoryPublished = true

	manifest := loaded.Manifest
	manifest.Variables = []string{"u", "v", "z", "t"}
	for index := range steps {
		steps[index].File = filepath.Join(finalName, filepath.Base(steps[index].File))
	}
	manifest.Steps = steps
	if err := writeManifest(filepath.Join(loaded.Directory, "manifest.json"), manifest); err != nil {
		return LoadedManifest{}, fmt.Errorf("publish wind-height manifest: %w", err)
	}
	manifestPublished = true
	if oldDirectory != "." && oldDirectory != finalName && filepath.Dir(oldDirectory) == "." {
		_ = os.RemoveAll(filepath.Join(loaded.Directory, oldDirectory))
	}
	return LoadedManifest{Manifest: manifest, Directory: loaded.Directory}, nil
}

func (client *Client) upgradeWindProfileStep(ctx context.Context, loaded LoadedManifest, old StepFile, directory string) (StepFile, error) {
	name := fmt.Sprintf("f%03d.grib2", old.ForecastHour)
	temporary := filepath.Join(directory, name+".part")
	destination := filepath.Join(directory, name)
	output, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return StepFile{}, err
	}
	failed := true
	defer func() {
		_ = output.Close()
		if failed {
			_ = os.Remove(temporary)
		}
	}()
	input, err := os.Open(filepath.Join(loaded.Directory, old.File))
	if err != nil {
		return StepFile{}, err
	}
	_, copyError := io.Copy(output, input)
	closeError := input.Close()
	if copyError != nil || closeError != nil {
		return StepFile{}, fmt.Errorf("copy legacy wind bundle f%03d", old.ForecastHour)
	}
	seen := make(map[string]bool, len(loaded.Variables))
	for _, variable := range loaded.Variables {
		seen[variable] = true
	}
	missing := make([]string, 0, 2)
	if !seen["z"] {
		missing = append(missing, "fi")
	}
	if !seen["t"] {
		missing = append(missing, "t")
	}
	for _, level := range DefaultPressureLevelsHPA {
		for _, variable := range missing {
			if err := client.appendField(ctx, output, client.fieldURL(loaded.RunID, old.ForecastHour, level, variable)); err != nil {
				return StepFile{}, fmt.Errorf("download f%03d %dhPa %s: %w", old.ForecastHour, level, strings.ToUpper(variable), err)
			}
		}
	}
	if err := output.Sync(); err != nil {
		return StepFile{}, err
	}
	if err := output.Close(); err != nil {
		return StepFile{}, err
	}
	if err := client.validateBundle(ctx, temporary); err != nil {
		return StepFile{}, err
	}
	digest, size, err := fileDigest(temporary)
	if err != nil {
		return StepFile{}, err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return StepFile{}, err
	}
	failed = false
	return StepFile{
		ForecastHour: old.ForecastHour, ValidAt: old.ValidAt, File: destination,
		Bytes: size, SHA256: digest, Messages: len(DefaultPressureLevelsHPA) * 4,
	}, nil
}

// AugmentWindHeights is kept as a source-compatible alias for operational
// tooling; the current bundle contract also includes temperature.
func (client *Client) AugmentWindHeights(ctx context.Context, dataRoot string, loaded LoadedManifest) (LoadedManifest, error) {
	return client.AugmentWindThermodynamics(ctx, dataRoot, loaded)
}
