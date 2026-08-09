package astroweb

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayClientUsesServiceAndUserHeaders(t *testing.T) {
	credential := strings.Repeat("s", 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get(serviceHeader) != credential {
			t.Errorf("service credential missing")
		}
		switch request.URL.Path {
		case "/internal/v1/directional/astrodome/availability":
			if request.Header.Get("X-Astrosferum-User-ID") != "42" {
				t.Errorf("availability user header missing")
			}
			_, _ = io.WriteString(w, `{"enabled":true,"available":true,"worker_available":true,"queue_length":0,"running":false}`)
		case "/internal/v1/directional/astrodome/jobs":
			if request.Header.Get("X-Astrosferum-User-ID") != "42" || request.Method != http.MethodPost {
				t.Errorf("unexpected admission identity/method")
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"id":"job_abcdefghijklmnopqrstuvwxyz","state":"queued","queue_position":1,"created_at":"2026-07-28T12:00:00Z","updated_at":"2026-07-28T12:00:00Z"}`)
		case "/internal/v1/directional/astrodome/jobs/job_abcdefghijklmnopqrstuvwxyz":
			if request.Header.Get("X-Astrosferum-User-ID") != "42" {
				t.Errorf("status user header missing")
			}
			_, _ = io.WriteString(w, `{"id":"job_abcdefghijklmnopqrstuvwxyz","state":"ready","queue_position":null,"created_at":"2026-07-28T12:00:00Z","updated_at":"2026-07-28T12:01:00Z"}`)
		case "/internal/v1/directional/astrodome/jobs/job_abcdefghijklmnopqrstuvwxyz/dataset":
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("ETag", `"abc"`)
			_, _ = io.WriteString(w, "payload")
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	client, err := NewGatewayClient(server.URL, []byte(credential), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	availability, err := client.Availability(context.Background(), 42)
	if err != nil || !availability.Available || !availability.WorkerAvailable {
		t.Fatalf("availability = %+v, %v", availability, err)
	}
	status, err := client.Admit(context.Background(), AstrodomeAdmission{
		TelegramUserID: 42, Point: SavedPoint{ID: 7}, Language: "ru", IdempotencyKey: "0123456789abcdef",
	})
	if err != nil || status.State != "queued" {
		t.Fatalf("admission = %+v, %v", status, err)
	}
	status, err = client.Status(context.Background(), 42, "job_abcdefghijklmnopqrstuvwxyz")
	if err != nil || status.State != "ready" {
		t.Fatalf("status = %+v, %v", status, err)
	}
	dataset, err := client.Dataset(context.Background(), 42, "job_abcdefghijklmnopqrstuvwxyz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dataset.Body.Close() }()
	body, _ := io.ReadAll(dataset.Body)
	if string(body) != "payload" || dataset.ContentEncoding != "gzip" || dataset.ETag != `"abc"` {
		t.Fatalf("dataset = %q %+v", body, dataset)
	}
}

func TestGatewayClientMapsPrivateErrors(t *testing.T) {
	for _, test := range []struct {
		status int
		want   error
	}{
		{status: http.StatusNotFound, want: ErrJobNotFound},
		{status: http.StatusTooManyRequests, want: ErrDirectionalBusy},
		{status: http.StatusForbidden, want: ErrAstrodomeDisabled},
		{status: http.StatusServiceUnavailable, want: ErrAstrodomeUnavailable},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(test.status)
		}))
		client, err := NewGatewayClient(server.URL, []byte(strings.Repeat("s", 32)), server.Client())
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Availability(context.Background(), 42)
		server.Close()
		if !errors.Is(err, test.want) {
			t.Fatalf("HTTP %d error = %v, want %v", test.status, err, test.want)
		}
	}
}

func TestGatewayClientRejectsMalformedResponseAndJobID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"enabled":true} {"second":true}`)
	}))
	defer server.Close()
	client, err := NewGatewayClient(server.URL, []byte(strings.Repeat("s", 32)), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Availability(context.Background(), 42); err == nil {
		t.Fatal("multiple JSON values accepted")
	}
	if _, err := client.Status(context.Background(), 42, "short"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("invalid job ID error = %v", err)
	}
}
