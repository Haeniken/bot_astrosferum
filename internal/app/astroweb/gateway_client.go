package astroweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const serviceHeader = "X-Astrosferum-Service"

type GatewayClient struct {
	baseURL    *url.URL
	credential string
	httpClient *http.Client
}

func NewGatewayClient(baseURL string, credential []byte, httpClient *http.Client) (*GatewayClient, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("directional gateway URL must be an absolute internal HTTP URL")
	}
	if len(credential) < minimumOpaqueCredentialBytes {
		return nil, errors.New("directional gateway credential must contain at least 32 bytes")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &GatewayClient{baseURL: parsed, credential: string(credential), httpClient: httpClient}, nil
}

func (client *GatewayClient) Availability(ctx context.Context, userID int64) (AstrodomeAvailability, error) {
	var result AstrodomeAvailability
	response, err := client.do(ctx, http.MethodGet, "/internal/v1/directional/astrodome/availability", userID, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return result, gatewayStatusError(response.StatusCode)
	}
	if err := decodeGatewayJSON(response.Body, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (client *GatewayClient) Admit(ctx context.Context, admission AstrodomeAdmission) (AstrodomeJobStatus, error) {
	var result AstrodomeJobStatus
	body, err := json.Marshal(admission)
	if err != nil {
		return result, err
	}
	response, err := client.do(ctx, http.MethodPost, "/internal/v1/directional/astrodome/jobs", admission.TelegramUserID, bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusOK {
		return result, gatewayStatusError(response.StatusCode)
	}
	if err := decodeGatewayJSON(response.Body, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (client *GatewayClient) Status(ctx context.Context, userID int64, jobID string) (AstrodomeJobStatus, error) {
	var result AstrodomeJobStatus
	if err := validateJobID(jobID); err != nil {
		return result, ErrJobNotFound
	}
	response, err := client.do(ctx, http.MethodGet, "/internal/v1/directional/astrodome/jobs/"+url.PathEscape(jobID), userID, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return result, gatewayStatusError(response.StatusCode)
	}
	if err := decodeGatewayJSON(response.Body, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (client *GatewayClient) Dataset(ctx context.Context, userID int64, jobID string) (AstrodomeDataset, error) {
	if err := validateJobID(jobID); err != nil {
		return AstrodomeDataset{}, ErrJobNotFound
	}
	response, err := client.do(ctx, http.MethodGet, "/internal/v1/directional/astrodome/jobs/"+url.PathEscape(jobID)+"/dataset", userID, nil)
	if err != nil {
		return AstrodomeDataset{}, err
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return AstrodomeDataset{}, gatewayStatusError(response.StatusCode)
	}
	return AstrodomeDataset{
		Body: response.Body, Bytes: response.ContentLength, ETag: response.Header.Get("ETag"),
		ContentEncoding: response.Header.Get("Content-Encoding"),
	}, nil
}

func (client *GatewayClient) Cancel(ctx context.Context, userID int64, jobID string) error {
	if err := validateJobID(jobID); err != nil {
		return ErrJobNotFound
	}
	response, err := client.do(ctx, http.MethodDelete, "/internal/v1/directional/astrodome/jobs/"+url.PathEscape(jobID), userID, nil)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusOK {
		return gatewayStatusError(response.StatusCode)
	}
	return nil
}

func (client *GatewayClient) do(ctx context.Context, method, path string, userID int64, body io.Reader) (*http.Response, error) {
	target := *client.baseURL
	target.Path = strings.TrimRight(client.baseURL.Path, "/") + path
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, err
	}
	request.Header.Set(serviceHeader, client.credential)
	if strings.HasSuffix(path, "/dataset") {
		// Explicitly requesting gzip prevents net/http from transparently
		// decoding it; the public gateway can stream the immutable bytes and
		// preserve the science payload ETag verbatim.
		request.Header.Set("Accept-Encoding", "gzip")
	}
	if userID > 0 {
		request.Header.Set("X-Astrosferum-User-ID", strconv.FormatInt(userID, 10))
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, errors.New("directional gateway transport failed")
	}
	return response, nil
}

func gatewayStatusError(status int) error {
	switch status {
	case http.StatusNotFound:
		return ErrJobNotFound
	case http.StatusTooManyRequests:
		return ErrDirectionalBusy
	case http.StatusForbidden:
		return ErrAstrodomeDisabled
	case http.StatusServiceUnavailable:
		return ErrAstrodomeUnavailable
	default:
		return fmt.Errorf("directional gateway returned HTTP %d", status)
	}
}

func decodeGatewayJSON(reader io.Reader, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode directional gateway response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("directional gateway returned multiple JSON values")
	}
	return nil
}

var _ AstrodomeGateway = (*GatewayClient)(nil)
