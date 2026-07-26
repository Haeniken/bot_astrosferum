package model

import (
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

type downloadRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip downloadRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestDownloadLimiterDisabledLeavesClientUnchanged(t *testing.T) {
	limiter, err := NewDownloadLimiter(0)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{}
	if wrapped := limiter.WrapHTTPClient(client); wrapped != client {
		t.Fatal("disabled limiter wrapped the HTTP client")
	}
}

func TestDownloadLimiterIsSharedAcrossWrappedClients(t *testing.T) {
	limiter, err := NewDownloadLimiter(100)
	if err != nil {
		t.Fatal(err)
	}
	base := downloadRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ICON")),
		}, nil
	})
	first := limiter.WrapHTTPClient(&http.Client{Transport: base, Timeout: 2 * time.Minute})
	second := limiter.WrapHTTPClient(&http.Client{Transport: base})
	firstTransport := first.Transport.(downloadLimitTransport)
	secondTransport := second.Transport.(downloadLimitTransport)
	if firstTransport.limiter != secondTransport.limiter {
		t.Fatal("ICON clients received independent bandwidth limits")
	}
	if first.Timeout != limitedDownloadTimeout {
		t.Fatalf("limited client timeout = %s", first.Timeout)
	}
}

func TestDownloadLimiterRejectsInvalidValues(t *testing.T) {
	for _, value := range []float64{-1, math.NaN(), math.Inf(1)} {
		if _, err := NewDownloadLimiter(value); err == nil {
			t.Fatalf("limit %v was accepted", value)
		}
	}
}
