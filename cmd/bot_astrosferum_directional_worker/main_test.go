package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bot_astrosferum/internal/config"
)

func TestExecutionHandlerExposesMetadataFreeHealthAndBothKinds(t *testing.T) {
	temporary := t.TempDir()
	cfg := config.Defaults()
	cfg.Paths.Data = filepath.Join(temporary, "data")
	cfg.Paths.Temp = filepath.Join(temporary, "tmp")
	cfg.Providers.ICONEU.MaxStaleAge = config.Duration{Duration: 12 * time.Hour}
	credential := bytes.Repeat([]byte("a"), 32)
	handler, err := newExecutionHandler(cfg, credential, nil)
	if err != nil {
		t.Fatalf("newExecutionHandler: %v", err)
	}

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK || health.Body.String() != "ok\n" {
		t.Fatalf("health response = HTTP %d %q", health.Code, health.Body.String())
	}
	if strings.Contains(strings.ToLower(health.Body.String()), "icon") || len(health.Header().Values("X-Astrosferum-Worker-Service")) != 0 {
		t.Fatalf("health response exposed worker metadata: headers=%v body=%q", health.Header(), health.Body.String())
	}

	for _, kind := range []string{"horizon", "astrodome"} {
		t.Run(kind, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/v1/execute/"+kind, strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Astrosferum-Worker-Service", string(credential))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("registered %s endpoint returned HTTP %d, want 400 for invalid payload", kind, response.Code)
			}
		})
	}
}

func TestRunHealthcheckUsesLocalUnauthenticatedEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/healthz" || request.Header.Get("X-Astrosferum-Worker-Service") != "" {
			http.Error(w, "unexpected health request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	}))
	defer server.Close()

	if err := run(context.Background(), []string{"--healthcheck", server.URL + "/healthz"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("healthcheck: %v", err)
	}
	if err := runHealthcheck(context.Background(), "http://example.com/healthz"); err == nil {
		t.Fatal("healthcheck unexpectedly allowed a non-loopback host")
	}
}

func TestWorkerWriteTimeoutCoversLongestKind(t *testing.T) {
	cfg := config.Defaults()
	cfg.Directional.InternalRequestTimeout = config.Duration{Duration: 30 * time.Minute}
	cfg.HorizonAnalysis.JobTimeout = config.Duration{Duration: 10 * time.Minute}
	cfg.Astrodome.JobTimeout = config.Duration{Duration: time.Hour}
	if got, want := workerWriteTimeout(cfg), time.Hour+time.Minute; got != want {
		t.Fatalf("workerWriteTimeout = %s, want %s", got, want)
	}
}

func TestWorkerWriteTimeoutCanBeDisabled(t *testing.T) {
	for _, mutate := range []func(*config.Config){
		func(cfg *config.Config) { cfg.Directional.InternalRequestTimeout = config.Duration{} },
		func(cfg *config.Config) { cfg.Astrodome.JobTimeout = config.Duration{} },
	} {
		cfg := config.Defaults()
		cfg.Directional.InternalRequestTimeout = config.Duration{Duration: 75 * time.Minute}
		cfg.Astrodome.JobTimeout = config.Duration{Duration: time.Hour}
		mutate(&cfg)
		if got := workerWriteTimeout(cfg); got != 0 {
			t.Fatalf("workerWriteTimeout = %s, want disabled", got)
		}
	}
}
