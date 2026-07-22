package vk

import (
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var (
	//go:embed certs/russian_trusted_root_ca_pem.crt
	russianTrustedRootCA []byte
	//go:embed certs/russian_trusted_sub_ca_pem.crt
	russianTrustedSubCA []byte
)

type restrictedTransport struct {
	base http.RoundTripper
}

func (transport restrictedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil || request.URL.Scheme != "https" || !isVKHost(request.URL.Hostname()) {
		return nil, errors.New("VK transport rejected a non-VK HTTPS endpoint")
	}
	return transport.base.RoundTrip(request)
}

func newRestrictedTransport() (http.RoundTripper, error) {
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system certificate pool: %w", err)
	}
	if !roots.AppendCertsFromPEM(russianTrustedRootCA) || !roots.AppendCertsFromPEM(russianTrustedSubCA) {
		return nil, errors.New("load embedded Russian trusted certificates")
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("default HTTP transport is unavailable")
	}
	cloned := base.Clone()
	cloned.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
	}
	return restrictedTransport{base: cloned}, nil
}

func newHTTPClient(transport http.RoundTripper, timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if request.URL == nil || request.URL.Scheme != "https" || !isVKHost(request.URL.Hostname()) {
				return errors.New("VK transport rejected a redirect outside VK domains")
			}
			return nil
		},
	}
}

func isVKHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "vk.ru" || strings.HasSuffix(host, ".vk.ru") ||
		host == "vk.com" || strings.HasSuffix(host, ".vk.com")
}

var _ http.RoundTripper = restrictedTransport{}
