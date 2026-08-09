package directional

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestReadServiceCredentialFileTrimsMountedSecretNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "directional_credential")
	want := bytes.Repeat([]byte("a"), minimumServiceCredentialBytes)
	contents := append([]byte(" \t"), want...)
	contents = append(contents, '\n')
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ReadServiceCredentialFile(path)
	if err != nil {
		t.Fatalf("ReadServiceCredentialFile: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("credential = %q, want %q", got, want)
	}
}

func TestReadServiceCredentialFileRejectsUnsafeInput(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
	}{
		{name: "empty", content: []byte(" \n")},
		{name: "too short", content: bytes.Repeat([]byte("x"), minimumServiceCredentialBytes-1)},
		{name: "embedded newline", content: []byte("0123456789abcdef\n0123456789abcdef")},
		{name: "too large", content: bytes.Repeat([]byte("x"), maximumServiceCredentialFileBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "directional_credential")
			if err := os.WriteFile(path, test.content, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadServiceCredentialFile(path); err == nil {
				t.Fatal("ReadServiceCredentialFile unexpectedly accepted unsafe input")
			}
		})
	}
}

func TestReadServiceCredentialFileRejectsNonRegularFile(t *testing.T) {
	if _, err := ReadServiceCredentialFile(t.TempDir()); err == nil {
		t.Fatal("ReadServiceCredentialFile unexpectedly accepted a directory")
	}
}
