package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"bot_astrosferum/internal/model"
	"bot_astrosferum/internal/model/iconeu"
)

type ICONEUScheduler struct {
	Client       *iconeu.Client
	DataRoot     string
	PollInterval time.Duration
	KeepRuns     int
	MaxStaleAge  time.Duration
	DomeEnabled  bool
	DomeBudget   *model.DiskBudget
	Logf         func(string, ...any)
}

func (scheduler ICONEUScheduler) Run(ctx context.Context) {
	if scheduler.PollInterval <= 0 {
		scheduler.PollInterval = 15 * time.Minute
	}
	if scheduler.Logf == nil {
		scheduler.Logf = func(string, ...any) {}
	}
	var domeRequests chan iconeu.LoadedManifest
	var domeWait sync.WaitGroup
	if scheduler.DomeEnabled {
		domeRequests = make(chan iconeu.LoadedManifest, 1)
		domeWait.Add(1)
		go func() {
			defer domeWait.Done()
			scheduler.runDomeWorker(ctx, domeRequests)
		}()
	}
	defer domeWait.Wait()
	scheduler.runOnce(ctx, domeRequests)
	ticker := time.NewTicker(scheduler.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scheduler.runOnce(ctx, domeRequests)
		}
	}
}

func (scheduler ICONEUScheduler) runOnce(ctx context.Context, domeRequests chan iconeu.LoadedManifest) {
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
		needsSurface := !current.HasHourlySurface() || (scheduler.DomeEnabled && !current.HasAstrodomeSurface())
		if needsSurface {
			scheduler.Logf("ICON-EU run %s lacks the required hourly surface contract; augmenting", current.RunID)
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
		enqueueLatestDome(domeRequests, current)
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
	enqueueLatestDome(domeRequests, manifest)
	if err := pruneICONRuns(scheduler.DataRoot, scheduler.KeepRuns, manifest.RunID); err != nil {
		scheduler.Logf("ICON-EU retention failed: %v", err)
	}
}

// enqueueLatestDome never delays the base-run scheduler. One active dome sync
// and at most the newest pending base run are retained; an obsolete pending
// request is replaced before it starts downloading.
func enqueueLatestDome(requests chan iconeu.LoadedManifest, manifest iconeu.LoadedManifest) {
	if requests == nil {
		return
	}
	select {
	case requests <- manifest:
		return
	default:
	}
	select {
	case <-requests:
	default:
	}
	select {
	case requests <- manifest:
	default:
	}
}

func (scheduler ICONEUScheduler) runDomeWorker(ctx context.Context, requests <-chan iconeu.LoadedManifest) {
	leaseManager, err := model.NewRunLeaseManager(filepath.Join(scheduler.DataRoot, "state", "run-leases"))
	if err != nil {
		scheduler.Logf("ICON-EU Astrodome retention lease initialization failed: %v", err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case manifest := <-requests:
			lease, leaseErr := leaseManager.AcquireShared(ctx, "icon-eu", manifest.RunID, model.RunRetentionLeaseDigest)
			if leaseErr != nil {
				if !errors.Is(leaseErr, context.Canceled) {
					scheduler.Logf("ICON-EU Astrodome run %s lease failed: %v", manifest.RunID, leaseErr)
				}
				continue
			}
			scheduler.ensureDome(ctx, manifest)
			if closeErr := lease.Close(); closeErr != nil {
				scheduler.Logf("ICON-EU Astrodome run %s lease release failed: %v", manifest.RunID, closeErr)
			}
		}
	}
}

// ensureDome is deliberately fail-open for the ordinary forecast. A full
// Astrodome input volume is a separately published capability: a transient
// download, disk-budget, or validation failure must not roll back the base
// ICON-EU run used by Telegram/VK.
func (scheduler ICONEUScheduler) ensureDome(ctx context.Context, base iconeu.LoadedManifest) {
	if !scheduler.DomeEnabled {
		return
	}
	if scheduler.DomeBudget == nil {
		scheduler.Logf("ICON-EU Astrodome sync %s skipped: disk budget is not configured", base.RunID)
		return
	}
	if current, err := iconeu.LoadCurrentDomeManifest(scheduler.DataRoot); err == nil &&
		current.RunID == base.RunID && current.InputContractSHA256 == iconeu.DomeInputContractDigest() {
		return
	}
	scheduler.Logf("ICON-EU run %s has no current complete Astrodome volume; augmenting", base.RunID)
	ready, err := scheduler.Client.SyncDome(ctx, scheduler.DataRoot, base, scheduler.DomeBudget)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			scheduler.Logf("ICON-EU Astrodome sync %s failed: %v", base.RunID, err)
		}
		return
	}
	scheduler.Logf("ICON-EU Astrodome volume %s published with profile %s and %d native terms", ready.RunID, ready.GridProfile, len(ready.ModelSteps))
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
	domeRunsDirectory := filepath.Join(dataRoot, "models", "icon-eu", "dome-runs")
	leaseManager, err := model.NewRunLeaseManager(filepath.Join(dataRoot, "state", "run-leases"))
	if err != nil {
		return fmt.Errorf("initialize ICON-EU retention leases: %w", err)
	}
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
		lease, acquired, err := leaseManager.TryExclusive("icon-eu", run.name, model.RunRetentionLeaseDigest)
		if err != nil {
			return fmt.Errorf("lock old ICON-EU run %s for retention: %w", run.name, err)
		}
		if !acquired {
			continue
		}
		removeErr := os.RemoveAll(filepath.Join(domeRunsDirectory, run.name))
		if removeErr == nil {
			removeErr = os.RemoveAll(filepath.Join(runsDirectory, run.name))
		}
		closeErr := lease.Close()
		if removeErr != nil {
			return fmt.Errorf("remove old ICON-EU run %s: %w", run.name, removeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("release old ICON-EU run %s retention lease: %w", run.name, closeErr)
		}
	}
	// Also reclaim a dome directory whose base run disappeared during an older
	// release or interrupted cleanup. The same lease protects an active worker.
	domeEntries, err := os.ReadDir(domeRunsDirectory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read ICON-EU dome runs: %w", err)
	}
	for _, entry := range domeEntries {
		if !entry.IsDir() || retained[entry.Name()] {
			continue
		}
		if _, parseErr := time.Parse("2006010215", entry.Name()); parseErr != nil {
			continue
		}
		lease, acquired, leaseErr := leaseManager.TryExclusive("icon-eu", entry.Name(), model.RunRetentionLeaseDigest)
		if leaseErr != nil {
			return fmt.Errorf("lock orphan ICON-EU dome run %s: %w", entry.Name(), leaseErr)
		}
		if !acquired {
			continue
		}
		removeErr := os.RemoveAll(filepath.Join(domeRunsDirectory, entry.Name()))
		closeErr := lease.Close()
		if removeErr != nil {
			return fmt.Errorf("remove orphan ICON-EU dome run %s: %w", entry.Name(), removeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("release orphan ICON-EU dome run %s lease: %w", entry.Name(), closeErr)
		}
	}
	return nil
}
