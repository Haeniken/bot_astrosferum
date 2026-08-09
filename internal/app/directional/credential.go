package directional

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const maximumServiceCredentialFileBytes = 16 << 10

// ReadServiceCredentialFile reads a mounted internal-service credential with
// a strict size bound. The returned slice is independent from the temporary
// read buffer, which is wiped before the function returns.
func ReadServiceCredentialFile(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("credential file path is empty")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("credential path is not a regular file")
	}
	content, err := io.ReadAll(io.LimitReader(file, maximumServiceCredentialFileBytes+1))
	if err != nil {
		return nil, err
	}
	defer clear(content)
	if len(content) > maximumServiceCredentialFileBytes {
		return nil, errors.New("credential file is too large")
	}
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) < minimumServiceCredentialBytes {
		return nil, fmt.Errorf("credential must contain at least %d bytes", minimumServiceCredentialBytes)
	}
	if bytes.ContainsAny(trimmed, "\r\n") {
		return nil, errors.New("credential contains an embedded line break")
	}
	return append([]byte(nil), trimmed...), nil
}
