package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"bot_astrosferum/internal/app/astroweb"
	"bot_astrosferum/internal/app/directional"
	"bot_astrosferum/internal/app/webui"
	"bot_astrosferum/internal/config"
	pgstore "bot_astrosferum/internal/store"
)

const maximumSecretBytes = 16 << 10

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("bot_astrosferum_web", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "/app/config/config.yaml", "configuration file")
	healthcheckURL := flags.String("healthcheck", "", "probe a local health URL and exit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("web server accepts flags only")
	}
	if *healthcheckURL != "" {
		return runHealthcheck(ctx, *healthcheckURL)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if !cfg.Web.Enabled {
		return errors.New("web application is disabled")
	}

	oidcSecret, err := readSecret(cfg.Web.OIDCClientSecretFile)
	if err != nil {
		return fmt.Errorf("read Telegram OIDC client secret: %w", err)
	}
	csrfKey, err := readSecret(cfg.Web.CSRFKeyFile)
	if err != nil {
		return fmt.Errorf("read web CSRF key: %w", err)
	}
	edgeCredential, err := readSecret(cfg.Web.EdgeCredentialFile)
	if err != nil {
		return fmt.Errorf("read edge credential: %w", err)
	}
	directionalCredential, err := readSecret(cfg.Web.DirectionalCredentialFile)
	if err != nil {
		return fmt.Errorf("read directional gateway credential: %w", err)
	}
	databasePassword, err := readSecret(cfg.Web.DatabasePasswordFile)
	if err != nil {
		return fmt.Errorf("read web database password: %w", err)
	}

	database, err := pgstore.OpenExisting(
		ctx,
		cfg.Database.Host,
		cfg.Database.Port,
		cfg.Database.Name,
		cfg.Web.DatabaseUser,
		string(databasePassword),
		cfg.Web.DatabaseMaxConns,
	)
	if err != nil {
		return err
	}
	defer database.Close()

	authenticator, err := astroweb.NewOIDCAuthenticator(ctx, astroweb.OIDCConfig{
		Issuer:              "https://oauth.telegram.org",
		ClientID:            cfg.Web.OIDCClientID,
		ClientSecret:        string(oidcSecret),
		RedirectURL:         strings.TrimRight(cfg.Web.PublicOrigin, "/") + "/auth/telegram/callback",
		PublicOrigin:        cfg.Web.PublicOrigin,
		TransactionLifetime: cfg.Web.TransactionTTL.Duration,
		SessionLifetime:     cfg.Web.SessionTTL.Duration,
		CSRFKey:             csrfKey,
		HTTPClient:          oidcHTTPClient(),
	}, database)
	if err != nil {
		return err
	}

	gateway, err := astroweb.NewGatewayClient(
		cfg.Web.DirectionalGatewayURL,
		directionalCredential,
		internalHTTPClient(),
	)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	visualizations, err := astroweb.NewVisualizationCatalog(
		filepath.Join(cfg.Paths.Data, "web", "astrodome"), database, gateway, nil,
		validateAstrodomeVisualization,
	)
	if err != nil {
		return err
	}
	go visualizations.RunMaintenance(ctx, logger)
	application, err := astroweb.NewServer(astroweb.ServerConfig{
		PublicOrigin:      cfg.Web.PublicOrigin,
		EdgeCredential:    edgeCredential,
		AstrodomePublic:   cfg.Astrodome.Enabled,
		AstrodomeAdminIDs: cfg.Platforms.Telegram.AdminIDs,
		Authenticator:     authenticator,
		Points:            database,
		Gateway:           gateway,
		Visualizations:    visualizations,
		Readiness:         database,
		Static:            webui.NewHandler(),
		Logger:            logger,
	})
	if err != nil {
		return err
	}

	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", cfg.Web.Listen)
	if err != nil {
		return fmt.Errorf("listen for web requests: %w", err)
	}
	server := &http.Server{
		Handler:           application,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      15 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
	}

	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()
	logger.Info("Astrosferum web server started", "listen", listener.Addr().String())
	_, _ = fmt.Fprintf(stdout, "Astrosferum web server listening on %s\n", listener.Addr())

	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			_ = server.Close()
			return fmt.Errorf("shut down web server: %w", err)
		}
		err = <-serveErrors
	case err = <-serveErrors:
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve web requests: %w", err)
	}
	return nil
}

func validateAstrodomeVisualization(source []byte) error {
	_, err := directional.DecodeAstrodomeDatasetBytes(source)
	return err
}

func runHealthcheck(ctx context.Context, target string) error {
	requestContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("construct health request: %w", err)
	}
	client := &http.Client{Transport: hardenedTransport(), Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("health request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}

func readSecret(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, maximumSecretBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maximumSecretBytes {
		return nil, errors.New("secret file is too large")
	}
	content = []byte(strings.TrimSpace(string(content)))
	if len(content) == 0 {
		return nil, errors.New("secret file is empty")
	}
	return content, nil
}

func oidcHTTPClient() *http.Client {
	return &http.Client{
		Transport: hardenedTransport(),
		Timeout:   30 * time.Second,
	}
}

func internalHTTPClient() *http.Client {
	return &http.Client{
		Transport: hardenedTransport(),
		Timeout:   15 * time.Minute,
	}
}

func hardenedTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 16
	transport.IdleConnTimeout = 90 * time.Second
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.ExpectContinueTimeout = time.Second
	return transport
}
