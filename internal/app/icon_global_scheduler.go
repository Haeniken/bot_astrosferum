package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"bot_astrosferum/internal/model/iconglobal"
)

type ICONGlobalScheduler struct {
	Client       *iconglobal.Client
	DataRoot     string
	PollInterval time.Duration
	KeepRuns     int
	MaxStaleAge  time.Duration
	Logf         func(string, ...any)
}

func (scheduler ICONGlobalScheduler) Run(ctx context.Context) {
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

func (scheduler ICONGlobalScheduler) runOnce(ctx context.Context) {
	if err := iconglobal.EnsureGrid(ctx, scheduler.DataRoot, scheduler.Logf); err != nil {
		scheduler.Logf("ICON Global static grid unavailable: %v", err)
		return
	}
	remote, err := scheduler.Client.ProbeLatest(ctx)
	if err != nil {
		scheduler.Logf("ICON Global probe failed: %v", err)
		return
	}
	current, currentError := iconglobal.LoadCurrent(scheduler.DataRoot)
	if currentError == nil && current.RunID == remote.ID {
		if !current.HasHourlyCloud() {
			scheduler.Logf("ICON Global run %s has no hourly model-layer cloud bundle; augmenting", current.RunID)
			augmented, augmentError := scheduler.Client.AugmentCloud(ctx, scheduler.DataRoot, current)
			if augmentError != nil {
				scheduler.Logf("ICON Global hourly cloud sync %s failed: %v", current.RunID, augmentError)
				return
			}
			current = augmented
			scheduler.Logf("ICON Global run %s hourly cloud published with %d steps", current.RunID, len(current.CloudSteps))
		}
		if scheduler.MaxStaleAge > 0 && time.Since(current.BaseTime) > scheduler.MaxStaleAge {
			scheduler.Logf("ICON Global current run %s is stale", current.RunID)
		}
		pruneICONGlobalRuns(scheduler.DataRoot, scheduler.KeepRuns, current.RunID)
		return
	}
	scheduler.Logf("ICON Global scheduler discovered run %s", remote.ID)
	manifest, err := scheduler.Client.Sync(ctx, remote, scheduler.DataRoot)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			scheduler.Logf("ICON Global sync %s failed: %v", remote.ID, err)
		}
		return
	}
	scheduler.Logf("ICON Global run %s published with %d pressure and %d surface steps", manifest.RunID, len(manifest.PressureSteps), len(manifest.SurfaceSteps))
	manifest, err = scheduler.Client.AugmentCloud(ctx, scheduler.DataRoot, manifest)
	if err != nil {
		scheduler.Logf("ICON Global hourly cloud sync %s failed: %v", remote.ID, err)
		return
	}
	if err := iconglobal.PublishCurrent(scheduler.DataRoot, manifest); err != nil {
		scheduler.Logf("ICON Global publish %s failed: %v", remote.ID, err)
		return
	}
	scheduler.Logf("ICON Global run %s hourly cloud published with %d steps", manifest.RunID, len(manifest.CloudSteps))
	pruneICONGlobalRuns(scheduler.DataRoot, scheduler.KeepRuns, manifest.RunID)
}

func pruneICONGlobalRuns(dataRoot string, keep int, currentRun string) {
	keep = max(1, keep)
	root := filepath.Join(dataRoot, "models", "icon-global", "runs")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var runs []string
	for _, entry := range entries {
		if entry.IsDir() {
			runs = append(runs, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(runs)))
	retained := 0
	for _, run := range runs {
		if run == currentRun || retained < keep {
			retained++
			continue
		}
		_ = os.RemoveAll(filepath.Join(root, run))
	}
}
