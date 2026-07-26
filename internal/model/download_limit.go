package model

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"time"

	"golang.org/x/time/rate"
)

const limitedReadChunk = 32 << 10

const limitedDownloadTimeout = 30 * time.Minute

// DownloadLimiter applies one aggregate limit to all wrapped HTTP clients.
// Sharing one instance between ICON-EU and ICON Global prevents each provider
// from independently consuming the configured full bandwidth.
type DownloadLimiter struct {
	limiter *rate.Limiter
}

func NewDownloadLimiter(megabitsPerSecond float64) (*DownloadLimiter, error) {
	if math.IsNaN(megabitsPerSecond) || math.IsInf(megabitsPerSecond, 0) || megabitsPerSecond < 0 {
		return nil, errors.New("ICON download limit must be finite and non-negative")
	}
	if megabitsPerSecond == 0 {
		return &DownloadLimiter{}, nil
	}
	bytesPerSecond := megabitsPerSecond * 1_000_000 / 8
	if math.IsInf(bytesPerSecond, 0) {
		return nil, errors.New("ICON download limit is too large")
	}
	return &DownloadLimiter{limiter: rate.NewLimiter(rate.Limit(bytesPerSecond), limitedReadChunk)}, nil
}

func (limiter *DownloadLimiter) WrapHTTPClient(client *http.Client) *http.Client {
	if limiter == nil || limiter.limiter == nil {
		return client
	}
	if client == nil {
		client = &http.Client{}
	}
	wrapped := *client
	if wrapped.Timeout > 0 && wrapped.Timeout < limitedDownloadTimeout {
		wrapped.Timeout = limitedDownloadTimeout
	}
	transport := wrapped.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	wrapped.Transport = downloadLimitTransport{base: transport, limiter: limiter.limiter}
	return &wrapped
}

type downloadLimitTransport struct {
	base    http.RoundTripper
	limiter *rate.Limiter
}

func (transport downloadLimitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	response.Body = &downloadLimitedBody{
		ReadCloser: response.Body,
		ctx:        request.Context(),
		limiter:    transport.limiter,
	}
	return response, nil
}

type downloadLimitedBody struct {
	io.ReadCloser
	ctx     context.Context
	limiter *rate.Limiter
}

func (body *downloadLimitedBody) Read(buffer []byte) (int, error) {
	if len(buffer) > limitedReadChunk {
		buffer = buffer[:limitedReadChunk]
	}
	count, err := body.ReadCloser.Read(buffer)
	if count > 0 {
		if waitError := body.limiter.WaitN(body.ctx, count); waitError != nil {
			return 0, waitError
		}
	}
	return count, err
}
