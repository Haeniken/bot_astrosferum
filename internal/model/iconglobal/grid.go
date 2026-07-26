package iconglobal

import (
	"compress/bzip2"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const gridURL = "https://opendata.dwd.de/weather/lib/cdo/icon_grid_0026_R03B07_G.nc.bz2"

func GridPath(dataRoot string) string {
	return filepath.Join(dataRoot, "models", "icon-global", "static", "icon_grid_0026_R03B07_G.nc")
}

func EnsureGrid(ctx context.Context, dataRoot string, logf func(string, ...any), httpClient *http.Client) error {
	path := GridPath(dataRoot)
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() > 100<<20 {
		return nil
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	lockPath := path + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > 2*time.Hour {
				if removeErr := os.Remove(lockPath); removeErr == nil {
					return EnsureGrid(ctx, dataRoot, logf, httpClient)
				}
			}
			return errors.New("ICON Global grid download is already running")
		}
		return err
	}
	_ = lock.Close()
	defer func() { _ = os.Remove(lockPath) }()

	logf("ICON Global static grid is missing; downloading official DWD geometry")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, gridURL, nil)
	if err != nil {
		return err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Minute}
	} else if httpClient.Timeout > 0 && httpClient.Timeout < 30*time.Minute {
		copy := *httpClient
		copy.Timeout = 30 * time.Minute
		httpClient = &copy
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("download ICON Global grid: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download ICON Global grid: HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(path+".part", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(path + ".part")
		}
	}()
	if _, err := io.Copy(file, bzip2.NewReader(response.Body)); err != nil {
		return fmt.Errorf("decompress ICON Global grid: %w", err)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	info, err := os.Stat(path + ".part")
	if err != nil || info.Size() <= 100<<20 {
		return errors.New("downloaded ICON Global grid is incomplete")
	}
	if err := os.Rename(path+".part", path); err != nil {
		return err
	}
	failed = false
	logf("ICON Global static grid ready size=%d", info.Size())
	return nil
}
