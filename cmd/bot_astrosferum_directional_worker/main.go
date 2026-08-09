package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"bot_astrosferum/internal/app"
	"bot_astrosferum/internal/app/astrodome"
	"bot_astrosferum/internal/app/bot"
	"bot_astrosferum/internal/app/directional"
	"bot_astrosferum/internal/config"
	"bot_astrosferum/internal/model/iconeu"
	"bot_astrosferum/internal/render"
)

const (
	workerShutdownTimeout = 90 * time.Second
	healthcheckTimeout    = 3 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if ctx == nil {
		return errors.New("directional worker context is required")
	}
	flags := flag.NewFlagSet("bot_astrosferum_directional_worker", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "/app/config/config.yaml", "configuration file")
	healthcheckURL := flags.String("healthcheck", "", "probe the local worker health URL and exit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("directional worker accepts flags only")
	}
	if *healthcheckURL != "" {
		return runHealthcheck(ctx, *healthcheckURL)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	credential, err := directional.ReadServiceCredentialFile(cfg.Directional.CredentialFile)
	if err != nil {
		return fmt.Errorf("read directional worker credential: %w", err)
	}
	defer clear(credential)
	logf := func(format string, values ...any) {
		_, _ = fmt.Fprintf(stderr, format+"\n", values...)
	}
	handler, err := newExecutionHandler(cfg, credential, logf)
	if err != nil {
		return err
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", cfg.Directional.WorkerListen)
	if err != nil {
		return fmt.Errorf("listen for directional worker requests: %w", err)
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      workerWriteTimeout(cfg),
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
		BaseContext: func(net.Listener) context.Context {
			// Cancelling the process context also cancels an active CDO/science
			// calculation before Shutdown waits for the connection to drain.
			return ctx
		},
		ErrorLog: log.New(stderr, "directional-worker: ", log.LstdFlags|log.LUTC),
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	_, _ = fmt.Fprintf(stdout, "Directional worker listening on %s\n", listener.Addr())

	select {
	case serveErr := <-serveErrors:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("serve directional worker requests: %w", serveErr)
		}
		return nil
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), workerShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			_ = server.Close()
			return fmt.Errorf("shut down directional worker: %w", err)
		}
		serveErr := <-serveErrors
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("serve directional worker requests: %w", serveErr)
		}
		return nil
	}
}

func newExecutionHandler(cfg config.Config, credential []byte, logf func(string, ...any)) (http.Handler, error) {
	horizonSource := iconeu.NewHorizonStore(
		cfg.Paths.Data, filepath.Join(cfg.Paths.Temp, "horizon-batch"),
		cfg.HorizonAnalysis.CDOWorkers, logf,
	)
	horizonJobs, err := bot.NewHorizonJobs(bot.HorizonJobsConfig{
		QueueSize: cfg.HorizonAnalysis.QueueSize, Concurrency: cfg.HorizonAnalysis.Concurrency,
		JobTimeout: cfg.HorizonAnalysis.JobTimeout.Duration,
		// The worker uses HorizonJobs only as the established science/render
		// runner. Its unused admission/delivery cache must stay process-private:
		// startup cleanup of a shared cache could otherwise remove a bot lease.
		CacheRoot: filepath.Join(cfg.Paths.Temp, "horizon-runner-cache"),
		CacheTTL:  cfg.HorizonAnalysis.CacheTTL.Duration, CacheEntries: cfg.HorizonAnalysis.CacheEntries,
		EstimatedDuration:      cfg.HorizonAnalysis.EstimatedDuration.Duration,
		MaxStaleAge:            cfg.Providers.ICONEU.MaxStaleAge.Duration,
		RenderAlgorithmVersion: render.HorizonRenderVersion,
	}, horizonSource, func(renderContext context.Context, destination string, input bot.HorizonRenderInput, language string) error {
		if err := renderContext.Err(); err != nil {
			return err
		}
		if err := render.Horizon(renderContext, destination, render.HorizonInput{
			Location: input.Location, Provider: input.Provider, RunID: input.RunID,
			Grid: input.Grid, Frames: input.Frames,
		}, render.Options{Language: language}); err != nil {
			return err
		}
		return renderContext.Err()
	}, app.OverallCalibration(cfg.Algorithms), logf)
	if err != nil {
		return nil, fmt.Errorf("initialize worker Horizon runner: %w", err)
	}
	horizonRunner, err := horizonJobs.DirectionalRunner()
	if err != nil {
		return nil, fmt.Errorf("initialize worker Horizon runner: %w", err)
	}
	scienceCalibration, err := app.AstrodomeScienceCalibration(cfg.Algorithms)
	if err != nil {
		return nil, fmt.Errorf("initialize worker Astrodome science calibration: %w", err)
	}
	computer, err := astrodome.NewComputer(astrodome.ComputerConfig{
		DataRoot: cfg.Paths.Data, TempRoot: filepath.Join(cfg.Paths.Temp, "astrodome"),
		ECCodesWorkers: cfg.HorizonAnalysis.CDOWorkers, ResidentLimitBytes: uint64(cfg.Astrodome.ResidentLimit),
		ScienceCalibration: scienceCalibration, Logf: logf,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize worker Astrodome computer: %w", err)
	}
	astrodomeRunner, err := directional.NewAstrodomeDatasetRunner(computer)
	if err != nil {
		return nil, fmt.Errorf("initialize worker Astrodome runner: %w", err)
	}
	executionGate, err := directional.NewExecutionGate(
		filepath.Join(cfg.Paths.Data, "state", "run-leases"),
		cfg.Directional.Concurrency,
	)
	if err != nil {
		return nil, err
	}
	horizonRunner, err = executionGate.Wrap(horizonRunner)
	if err != nil {
		return nil, fmt.Errorf("serialize worker Horizon runner: %w", err)
	}
	astrodomeRunner, err = executionGate.Wrap(astrodomeRunner)
	if err != nil {
		return nil, fmt.Errorf("serialize worker Astrodome runner: %w", err)
	}
	handler, err := directional.NewWorkerHTTPHandler(directional.WorkerHTTPConfig{
		WorkspaceRoot:     filepath.Join(cfg.Paths.Data, "cache", "directional", "staging"),
		ServiceCredential: credential,
		Concurrency:       cfg.Directional.Concurrency,
		Logf:              logf,
		Runners: map[directional.Kind]directional.Runner{
			directional.KindHorizon: horizonRunner, directional.KindAstrodome: astrodomeRunner,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("initialize directional worker handler: %w", err)
	}
	return handler, nil
}

func workerWriteTimeout(cfg config.Config) time.Duration {
	if cfg.Directional.InternalRequestTimeout.Duration == 0 || cfg.Astrodome.JobTimeout.Duration == 0 {
		return 0
	}
	result := cfg.Directional.InternalRequestTimeout.Duration
	for _, duration := range []time.Duration{
		cfg.HorizonAnalysis.JobTimeout.Duration + time.Minute,
		cfg.Astrodome.JobTimeout.Duration + time.Minute,
	} {
		if duration > result {
			result = duration
		}
	}
	return result
}

func runHealthcheck(ctx context.Context, target string) error {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.Path != "/healthz" ||
		parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("healthcheck URL must be an absolute local HTTP URL")
	}
	host := parsed.Hostname()
	address := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (address == nil || !address.IsLoopback()) {
		return errors.New("healthcheck URL must use a loopback host")
	}
	requestContext, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return fmt.Errorf("construct directional worker health request: %w", err)
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: healthcheckTimeout}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("directional worker health request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("directional worker health endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}
