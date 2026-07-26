package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"bot_astrosferum/internal/model/iconeu"
)

type ICONEUScheduler struct {
	Client       *iconeu.Client
	DataRoot     string
	PollInterval time.Duration
	KeepRuns     int
	MaxStaleAge  time.Duration
	Logf         func(string, ...any)
}

func (scheduler ICONEUScheduler) Run(ctx context.Context) {
	if scheduler.PollInterval <= 0 {
		scheduler.PollInterval = 15 * time.Minute
	}
	if scheduler.Logf == nil {
		scheduler.Logf = func(string, ...any) {}
	}
	scheduler.runOnce(ctx)
	ticker := time.NewTicker(scheduler.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scheduler.runOnce(ctx)
		}
	}
}

func (scheduler ICONEUScheduler) runOnce(ctx context.Context) {
	if err := cleanupStaleICONArtifacts(scheduler.DataRoot, 24*time.Hour, time.Now()); err != nil {
		scheduler.Logf("ICON-EU temporary-file cleanup failed: %v", err)
	}
	remote, err := scheduler.Client.ProbeLatest(ctx)
	if err != nil {
		scheduler.Logf("ICON-EU probe failed: %v", err)
		return
	}
	current, currentError := iconeu.LoadCurrent(scheduler.DataRoot)
	if currentError == nil && current.RunID == remote.ID {
		if !current.HasWindThermodynamics() {
			scheduler.Logf("ICON-EU run %s lacks geopotential heights or pressure-level temperature; augmenting", current.RunID)
			augmented, err := scheduler.Client.AugmentWindThermodynamics(ctx, scheduler.DataRoot, current)
			if err != nil {
				scheduler.Logf("ICON-EU wind-height sync %s failed: %v", current.RunID, err)
				return
			}
			current = augmented
			scheduler.Logf("ICON-EU run %s wind/temperature profile published", current.RunID)
		}
		if !current.HasHourlySurface() {
			scheduler.Logf("ICON-EU run %s has no hourly surface bundle; augmenting", current.RunID)
			augmented, err := scheduler.Client.AugmentSurface(ctx, scheduler.DataRoot, current)
			if err != nil {
				scheduler.Logf("ICON-EU surface sync %s failed: %v", current.RunID, err)
				return
			}
			current = augmented
			scheduler.Logf("ICON-EU run %s hourly surface published with %d steps", augmented.RunID, len(augmented.SurfaceForecastSteps()))
		}
		if !current.HasHourlyCloud() {
			scheduler.Logf("ICON-EU run %s has no hourly model-layer cloud bundle; augmenting", current.RunID)
			augmented, err := scheduler.Client.AugmentCloud(ctx, scheduler.DataRoot, current)
			if err != nil {
				scheduler.Logf("ICON-EU hourly cloud sync %s failed: %v", current.RunID, err)
				return
			}
			current = augmented
			scheduler.Logf("ICON-EU run %s hourly cloud published with %d steps", current.RunID, len(current.CloudSteps))
		}
		if err := iconeu.PublishCurrent(scheduler.DataRoot, current); err != nil {
			scheduler.Logf("ICON-EU publish %s failed: %v", current.RunID, err)
			return
		}
		age := time.Since(current.BaseTime)
		if scheduler.MaxStaleAge > 0 && age > scheduler.MaxStaleAge {
			scheduler.Logf("ICON-EU current run %s is stale: age=%s", current.RunID, age.Round(time.Minute))
		}
		if err := pruneICONRuns(scheduler.DataRoot, scheduler.KeepRuns, current.RunID); err != nil {
			scheduler.Logf("ICON-EU retention failed: %v", err)
		}
		return
	}
	scheduler.Logf("ICON-EU scheduler discovered run %s", remote.ID)
	manifest, err := scheduler.Client.Sync(ctx, remote, scheduler.DataRoot)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			scheduler.Logf("ICON-EU sync %s failed: %v", remote.ID, err)
		}
		return
	}
	scheduler.Logf("ICON-EU run %s published with %d steps", manifest.RunID, len(manifest.Steps))
	manifest, err = scheduler.Client.AugmentWindThermodynamics(ctx, scheduler.DataRoot, manifest)
	if err != nil {
		scheduler.Logf("ICON-EU wind-height sync %s failed: %v", remote.ID, err)
		return
	}
	manifest, err = scheduler.Client.AugmentSurface(ctx, scheduler.DataRoot, manifest)
	if err != nil {
		scheduler.Logf("ICON-EU surface sync %s failed: %v", remote.ID, err)
		return
	}
	scheduler.Logf("ICON-EU run %s hourly surface published with %d steps", manifest.RunID, len(manifest.SurfaceForecastSteps()))
	manifest, err = scheduler.Client.AugmentCloud(ctx, scheduler.DataRoot, manifest)
	if err != nil {
		scheduler.Logf("ICON-EU hourly cloud sync %s failed: %v", remote.ID, err)
		return
	}
	if err := iconeu.PublishCurrent(scheduler.DataRoot, manifest); err != nil {
		scheduler.Logf("ICON-EU publish %s failed: %v", remote.ID, err)
		return
	}
	scheduler.Logf("ICON-EU run %s hourly cloud published with %d steps", manifest.RunID, len(manifest.CloudSteps))
	if err := pruneICONRuns(scheduler.DataRoot, scheduler.KeepRuns, manifest.RunID); err != nil {
		scheduler.Logf("ICON-EU retention failed: %v", err)
	}
}

func cleanupStaleICONArtifacts(dataRoot string, maximumAge time.Duration, now time.Time) error {
	providerRoot := filepath.Join(dataRoot, "models", "icon-eu")
	removeMatchingDirectories := func(root string, matches func(string) bool) error {
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() || !matches(entry.Name()) {
				continue
			}
			info, err := entry.Info()
			if err != nil || now.Sub(info.ModTime()) <= maximumAge {
				continue
			}
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if err := removeMatchingDirectories(filepath.Join(providerRoot, "incoming"), func(string) bool { return true }); err != nil {
		return fmt.Errorf("clean provider incoming: %w", err)
	}
	runsRoot := filepath.Join(providerRoot, "runs")
	runs, err := os.ReadDir(runsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read runs for cleanup: %w", err)
	}
	for _, run := range runs {
		if !run.IsDir() {
			continue
		}
		err := removeMatchingDirectories(filepath.Join(runsRoot, run.Name()), func(name string) bool {
			return strings.HasPrefix(name, ".cloud-hourly-") || strings.HasPrefix(name, ".surface-hourly-") || strings.HasPrefix(name, ".steps-v3-") || strings.HasPrefix(name, ".steps-v4-")
		})
		if err != nil {
			return fmt.Errorf("clean run %s temporary data: %w", run.Name(), err)
		}
	}
	return nil
}

func pruneICONRuns(dataRoot string, keep int, currentRun string) error {
	if keep < 1 {
		keep = 1
	}
	runsDirectory := filepath.Join(dataRoot, "models", "icon-eu", "runs")
	entries, err := os.ReadDir(runsDirectory)
	if err != nil {
		return fmt.Errorf("read ICON-EU runs: %w", err)
	}
	type runEntry struct {
		name string
		time time.Time
	}
	var runs []runEntry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		baseTime, err := time.Parse("2006010215", entry.Name())
		if err != nil {
			continue
		}
		runs = append(runs, runEntry{name: entry.Name(), time: baseTime})
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].time.After(runs[j].time) })
	retained := make(map[string]bool, keep+1)
	retained[currentRun] = true
	for _, run := range runs {
		if len(retained) >= keep {
			break
		}
		retained[run.name] = true
	}
	for _, run := range runs {
		if retained[run.name] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(runsDirectory, run.name)); err != nil {
			return fmt.Errorf("remove old ICON-EU run %s: %w", run.name, err)
		}
	}
	return nil
}
