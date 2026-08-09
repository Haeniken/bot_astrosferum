package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunHealthcheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := runHealthcheck(context.Background(), server.URL); err != nil {
		t.Fatal(err)
	}
	unhealthy := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unhealthy.Close()
	if err := runHealthcheck(context.Background(), unhealthy.URL); err == nil {
		t.Fatal("unhealthy endpoint accepted")
	}
}

func TestReadSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("  opaque-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := readSecret(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != "opaque-secret" {
		t.Fatalf("secret = %q", value)
	}
}

func TestReadSecretRejectsEmptyAndOversizedFiles(t *testing.T) {
	for name, content := range map[string]string{
		"empty":     " \n\t",
		"oversized": strings.Repeat("x", maximumSecretBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "secret")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readSecret(path); err == nil {
				t.Fatal("invalid secret accepted")
			}
		})
	}
}

func TestValidateAstrodomeVisualizationRejectsUnsupportedScientificContract(t *testing.T) {
	old := `{"science_version":"astrodome-science-v28","frames":[]}`
	if err := validateAstrodomeVisualization([]byte(old)); err == nil {
		t.Fatal("unsupported saved Astrodome contract was accepted")
	}
}
