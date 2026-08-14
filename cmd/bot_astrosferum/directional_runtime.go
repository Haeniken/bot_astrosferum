package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"bot_astrosferum/internal/app"
	"bot_astrosferum/internal/app/astrodome"
	"bot_astrosferum/internal/app/bot"
	"bot_astrosferum/internal/app/directional"
	"bot_astrosferum/internal/config"
	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/model"
)

const (
	directionalShutdownTimeout = 20 * time.Second
)

// directionalRuntime is the process-local composition root for all expensive
// direction-dependent work. Horizon and Astrodome register the same remote
// runner in a FIFO with one shared configurable active-job limit.
type directionalRuntime struct {
	coordinator *directional.Coordinator
	concurrency int
	horizon     *bot.HorizonJobs
	listener    net.Listener
	server      *http.Server
	transport   *http.Transport
	serveErrors chan error
	logf        func(string, ...any)
	closeOnce   sync.Once
	closeErr    error
}

func newDirectionalRuntime(ctx context.Context, cfg config.Config, horizonJobs *bot.HorizonJobs, terrain astrodome.TerrainSkylineSource, accountJobs directional.AccountJobBackend, logf func(string, ...any)) (*directionalRuntime, error) {
	credential, err := directional.ReadServiceCredentialFile(cfg.Directional.CredentialFile)
	if err != nil {
		return nil, fmt.Errorf("read directional service credential: %w", err)
	}
	defer clear(credential)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Internal service credentials must never be forwarded through an ambient
	// HTTP_PROXY configured for the downloader's egress traffic.
	transport.Proxy = nil
	remoteRunner, err := directional.NewRemoteRunner(
		cfg.Directional.WorkerURL,
		credential,
		&http.Client{Transport: transport, Timeout: cfg.Directional.InternalRequestTimeout.Duration},
	)
	if err != nil {
		return nil, fmt.Errorf("initialize directional remote runner: %w", err)
	}
	coordinator, err := directional.NewCoordinator(directional.Config{
		QueueCapacity:  cfg.Directional.QueueSize,
		Concurrency:    cfg.Directional.Concurrency,
		CompletedLimit: cfg.Directional.CompletedEntries,
		CompletedTTL:   cfg.Directional.CompletedTTL.Duration,
		ResultRoot:     filepath.Join(cfg.Paths.Data, "cache", "directional"),
		Policies: map[directional.Kind]directional.KindPolicy{
			directional.KindHorizon: {
				Estimate: cfg.Directional.EstimatedHorizon.Duration,
				Timeout:  cfg.HorizonAnalysis.JobTimeout.Duration,
			},
			directional.KindAstrodome: {
				Estimate: cfg.Directional.EstimatedAstrodome.Duration,
				Timeout:  cfg.Astrodome.JobTimeout.Duration,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("initialize directional coordinator: %w", err)
	}
	if horizonJobs != nil {
		if err := horizonJobs.UseDirectionalCoordinatorWithRunner(coordinator, remoteRunner); err != nil {
			return nil, fmt.Errorf("register Horizon directional runner: %w", err)
		}
	}
	if err := coordinator.Register(directional.KindAstrodome, remoteRunner); err != nil {
		return nil, fmt.Errorf("register Astrodome directional runner: %w", err)
	}

	timeZones, err := forecast.NewTimeZoneResolver()
	if err != nil {
		return nil, fmt.Errorf("initialize Astrodome time-zone resolver: %w", err)
	}
	scienceCalibration, err := app.AstrodomeScienceCalibration(cfg.Algorithms)
	if err != nil {
		return nil, fmt.Errorf("initialize Astrodome science calibration: %w", err)
	}
	astrodomeBackend, err := astrodome.NewBackend(astrodome.Config{
		Enabled: cfg.Astrodome.Enabled, DataRoot: cfg.Paths.Data,
		MaxStaleAge: cfg.Providers.ICONEU.MaxStaleAge.Duration,
		AdminIDs:    cfg.Platforms.Telegram.AdminIDs, TimeZones: timeZones,
		Calibration: scienceCalibration, Terrain: terrain,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize Astrodome backend: %w", err)
	}
	handler, err := directional.NewHTTPHandler(directional.HTTPConfig{
		Coordinator: coordinator, AstrodomeBackend: astrodomeBackend, WorkerHealth: remoteRunner,
		AccountJobs: accountJobs, ServiceCredential: credential,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize directional HTTP gateway: %w", err)
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", cfg.Directional.Listen)
	if err != nil {
		return nil, fmt.Errorf("listen for directional gateway requests: %w", err)
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &directionalRuntime{
		coordinator: coordinator,
		concurrency: cfg.Directional.Concurrency,
		horizon:     horizonJobs,
		listener:    listener,
		transport:   transport,
		server: &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      15 * time.Minute,
			IdleTimeout:       2 * time.Minute,
			MaxHeaderBytes:    32 << 10,
		},
		serveErrors: make(chan error, 1),
		logf:        logf,
	}, nil
}

func (runtime *directionalRuntime) Start(ctx context.Context) error {
	if runtime == nil || runtime.coordinator == nil || runtime.server == nil || runtime.listener == nil {
		return errors.New("directional runtime is not initialized")
	}
	if err := runtime.coordinator.Start(ctx); err != nil {
		_ = runtime.listener.Close()
		return fmt.Errorf("start directional coordinator: %w", err)
	}
	if runtime.horizon != nil {
		if err := runtime.horizon.Start(ctx); err != nil {
			_ = runtime.coordinator.Close()
			_ = runtime.listener.Close()
			return fmt.Errorf("start Horizon jobs: %w", err)
		}
	}
	go func() {
		err := runtime.server.Serve(runtime.listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runtime.logf("directional gateway stopped unexpectedly: %v", err)
		}
		runtime.serveErrors <- err
	}()
	runtime.logf("directional gateway started on %s with %d shared workers", runtime.listener.Addr(), runtime.concurrency)
	return nil
}

func (runtime *directionalRuntime) Close() error {
	if runtime == nil {
		return nil
	}
	runtime.closeOnce.Do(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), directionalShutdownTimeout)
		defer cancel()
		if runtime.server != nil {
			if err := runtime.server.Shutdown(shutdownContext); err != nil {
				_ = runtime.server.Close()
				runtime.closeErr = fmt.Errorf("shut down directional gateway: %w", err)
			}
		}
		if runtime.horizon != nil {
			runtime.horizon.Close()
		}
		if runtime.coordinator != nil {
			if err := runtime.coordinator.Close(); err != nil && runtime.closeErr == nil {
				runtime.closeErr = fmt.Errorf("close directional coordinator: %w", err)
			}
		}
		if runtime.transport != nil {
			runtime.transport.CloseIdleConnections()
		}
		if runtime.serveErrors != nil {
			err := <-runtime.serveErrors
			if err != nil && !errors.Is(err, http.ErrServerClosed) && runtime.closeErr == nil {
				runtime.closeErr = fmt.Errorf("serve directional gateway: %w", err)
			}
		}
	})
	return runtime.closeErr
}

func newAstrodomeDiskBudget(cfg config.Config) (*model.DiskBudget, error) {
	stateRoot := filepath.Join(cfg.Paths.Data, "state")
	minimumFree := uint64(cfg.Astrodome.MinFreeSpace)
	if syncMinimum := uint64(cfg.Sync.MinFreeSpace); syncMinimum > minimumFree {
		minimumFree = syncMinimum
	}
	budget, err := model.NewDiskBudget(model.DiskBudgetConfig{
		LockPath:       filepath.Join(stateRoot, "astrodome-disk-budget.lock"),
		ReservationDir: filepath.Join(stateRoot, "astrodome-disk-reservations"),
		FilesystemPath: cfg.Paths.Data,
		PublishedRoots: []string{
			filepath.Join(cfg.Paths.Data, "models", "icon-eu", "runs"),
			filepath.Join(cfg.Paths.Data, "models", "icon-eu", "dome-runs"),
			filepath.Join(cfg.Paths.Data, "models", "icon-global", "runs"),
		},
		StagingRoots: []string{
			filepath.Join(cfg.Paths.Data, "models", "icon-eu", "incoming"),
			filepath.Join(cfg.Paths.Data, "models", "icon-eu", "dome-staging"),
			filepath.Join(cfg.Paths.Data, "models", "icon-global", "incoming"),
		},
		LeasedRoots: []string{
			filepath.Join(cfg.Paths.Data, "models", "icon-eu", "runs"),
			filepath.Join(cfg.Paths.Data, "models", "icon-eu", "dome-runs"),
		},
		CacheRoots: []string{
			filepath.Join(cfg.Paths.Data, "cache", "directional"),
			filepath.Join(cfg.Paths.Data, "cache", "horizon"),
		},
		TemporaryRoots: []string{
			filepath.Join(cfg.Paths.Temp, "astrodome"),
			filepath.Join(cfg.Paths.Temp, "horizon-batch"),
		},
		ProjectCapBytes: uint64(cfg.Astrodome.ProjectDiskCap),
		MinFreeBytes:    minimumFree,
		MinFreeInodes:   cfg.Astrodome.MinFreeInodes,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize Astrodome disk budget: %w", err)
	}
	return budget, nil
}
