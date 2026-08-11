package bot

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	horizonBundleImageLimit   = 64 << 20
	horizonBundleDatasetLimit = 16 << 20
)

func writeHorizonBundle(destination, imagePath, datasetPath string) error {
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	writer := tar.NewWriter(output)
	writeErr := writeHorizonBundleFile(writer, imagePath, horizonCacheImage, horizonBundleImageLimit)
	if writeErr == nil {
		writeErr = writeHorizonBundleFile(writer, datasetPath, horizonCacheDataset, horizonBundleDatasetLimit)
	}
	closeTarErr := writer.Close()
	syncErr := output.Sync()
	closeErr := output.Close()
	if writeErr != nil {
		_ = os.Remove(destination)
		return writeErr
	}
	for _, candidate := range []error{closeTarErr, syncErr, closeErr} {
		if candidate != nil {
			_ = os.Remove(destination)
			return candidate
		}
	}
	return nil
}

func writeHorizonBundleFile(writer *tar.Writer, sourcePath, name string, limit int64) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return fmt.Errorf("invalid Horizon bundle member %s", name)
	}
	if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o640, Size: info.Size()}); err != nil {
		return err
	}
	written, err := io.Copy(writer, source)
	if err != nil || written != info.Size() {
		return errors.New("horizon bundle source changed while being copied")
	}
	return nil
}

func readHorizonBundle(path, temporaryRoot string) (imagePath string, dataset []byte, cleanup func(), err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", nil, func() {}, err
	}
	defer func() { _ = file.Close() }()
	reader := tar.NewReader(file)
	var image []byte
	seen := map[string]bool{}
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil || header == nil || header.Typeflag != tar.TypeReg || seen[header.Name] {
			return "", nil, func() {}, errors.New("invalid Horizon result bundle")
		}
		seen[header.Name] = true
		switch header.Name {
		case horizonCacheImage:
			if header.Size <= 0 || header.Size > horizonBundleImageLimit {
				return "", nil, func() {}, errors.New("invalid Horizon PNG in result bundle")
			}
			image, err = io.ReadAll(io.LimitReader(reader, horizonBundleImageLimit+1))
		case horizonCacheDataset:
			if header.Size <= 0 || header.Size > horizonBundleDatasetLimit {
				return "", nil, func() {}, errors.New("invalid Horizon JSON in result bundle")
			}
			dataset, err = io.ReadAll(io.LimitReader(reader, horizonBundleDatasetLimit+1))
		default:
			return "", nil, func() {}, errors.New("unexpected Horizon result bundle member")
		}
		if err != nil {
			return "", nil, func() {}, err
		}
	}
	if !bytes.HasPrefix(image, []byte("\x89PNG\r\n\x1a\n")) || !bytes.HasPrefix(dataset, []byte("{")) {
		return "", nil, func() {}, errors.New("incomplete Horizon result bundle")
	}
	temporary, err := os.CreateTemp(temporaryRoot, horizonCacheLease+"*.png")
	if err != nil {
		return "", nil, func() {}, err
	}
	imagePath = temporary.Name()
	cleanup = func() { _ = os.Remove(imagePath) }
	if err := temporary.Chmod(0o640); err != nil {
		_ = temporary.Close()
		cleanup()
		return "", nil, func() {}, err
	}
	if _, err := temporary.Write(image); err != nil {
		_ = temporary.Close()
		cleanup()
		return "", nil, func() {}, err
	}
	if err := temporary.Close(); err != nil {
		cleanup()
		return "", nil, func() {}, err
	}
	return filepath.Clean(imagePath), dataset, cleanup, nil
}
