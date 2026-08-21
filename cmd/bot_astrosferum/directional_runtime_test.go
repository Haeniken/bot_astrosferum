package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bot_astrosferum/internal/config"
)

func TestDirectionalRuntimeStartDoesNotProbeRemoteWorker(t *testing.T) {
	temporary := t.TempDir()
	credentialPath := filepath.Join(temporary, "directional_credential")
	if err := os.WriteFile(credentialPath, bytes.Repeat([]byte("a"), 32), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Paths.Data = filepath.Join(temporary, "data")
	cfg.Paths.Temp = filepath.Join(temporary, "tmp")
	cfg.Directional.CredentialFile = credentialPath
	cfg.Directional.Listen = "127.0.0.1:0"
	// No process is listening. Composition and startup must still succeed;
	// reachability is handled as a per-job fail-fast error by RemoteRunner.
	cfg.Directional.WorkerURL = "http://127.0.0.1:1"
	cfg.Directional.InternalRequestTimeout = config.Duration{Duration: time.Second}
	cfg.Providers.ICONEU.MaxStaleAge = config.Duration{Duration: 24 * time.Hour}

	ctx, cancel := context.WithCancel(t.Context())
	runtime, err := newDirectionalRuntime(ctx, cfg, nil, nil, nil, nil, nil)
	if err != nil {
		cancel()
		t.Fatalf("newDirectionalRuntime: %v", err)
	}
	if err := runtime.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	cancel()
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
