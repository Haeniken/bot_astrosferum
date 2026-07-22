package lightpollution

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const WorldAtlas2015URL = "https://datapub.gfz-potsdam.de/download/10.5880.GFZ.1.4.2016.001/World_Atlas_2015.zip"

type WorldAtlas2015 struct {
	path          string
	mu            sync.Mutex
	cache         map[string]Estimate
	width, height int
	geo           [6]float64
}

// EnsureWorldAtlas2015 checks the runtime volume, downloads the official GFZ
// archive when absent, extracts only the GeoTIFF, and publishes it atomically.
func EnsureWorldAtlas2015(ctx context.Context, root string, logf func(string, ...any)) (string, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", err
	}
	target := filepath.Join(root, "World_Atlas_2015.tif")
	if info, err := os.Stat(target); err == nil && info.Size() > 1<<30 {
		return target, nil
	}
	archivePath := filepath.Join(root, "World_Atlas_2015.zip")
	if info, statErr := os.Stat(archivePath); statErr != nil || info.Size() == 0 {
		partial := archivePath + ".partial"
		_ = os.Remove(partial)
		logf("World Atlas 2015 missing; downloading official GFZ archive")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, WorldAtlas2015URL, nil)
		if err != nil {
			return "", err
		}
		client := &http.Client{Timeout: 2 * time.Hour}
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("download World Atlas 2015: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("download World Atlas 2015: HTTP %d", resp.StatusCode)
		}
		out, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
		if err != nil {
			return "", err
		}
		n, copyErr := io.Copy(out, io.LimitReader(resp.Body, (1<<30)+1))
		closeErr := out.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		if n > 1<<30 {
			return "", errors.New("world atlas archive exceeds 1 GiB safety limit")
		}
		if err := os.Rename(partial, archivePath); err != nil {
			return "", err
		}
	}
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("open World Atlas archive: %w", err)
	}
	defer func() { _ = zr.Close() }()
	var entry *zip.File
	for _, f := range zr.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".tif") {
			entry = f
			break
		}
	}
	if entry == nil {
		return "", errors.New("world atlas archive has no GeoTIFF")
	}
	if entry.UncompressedSize64 > 5<<30 {
		return "", errors.New("world atlas GeoTIFF exceeds 5 GiB safety limit")
	}
	in, err := entry.Open()
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }()
	incoming := target + ".incoming"
	_ = os.Remove(incoming)
	tif, err := os.OpenFile(incoming, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return "", err
	}
	written, copyErr := io.Copy(tif, io.LimitReader(in, (5<<30)+1))
	syncErr := tif.Sync()
	closeErr := tif.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || written != int64(entry.UncompressedSize64) {
		_ = os.Remove(incoming)
		return "", fmt.Errorf("extract World Atlas GeoTIFF: incomplete output")
	}
	if err := os.Rename(incoming, target); err != nil {
		return "", err
	}
	_ = os.Remove(archivePath)
	logf("World Atlas 2015 ready: %s", target)
	return target, nil
}

func OpenWorldAtlas2015(ctx context.Context, path string) (*WorldAtlas2015, error) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < 1<<30 {
		return nil, errors.New("world atlas 2015 GeoTIFF is missing or incomplete")
	}
	if _, err := exec.LookPath("gdal_translate"); err != nil {
		return nil, errors.New("gdal_translate is required for World Atlas 2015")
	}
	output, err := exec.CommandContext(ctx, "gdalinfo", "-json", path).Output()
	if err != nil {
		return nil, fmt.Errorf("validate World Atlas 2015: %w", err)
	}
	var metadata struct {
		Size [2]int     `json:"size"`
		Geo  [6]float64 `json:"geoTransform"`
	}
	if err := json.Unmarshal(output, &metadata); err != nil {
		return nil, fmt.Errorf("decode World Atlas metadata: %w", err)
	}
	if metadata.Size[0] < 2 || metadata.Size[1] < 2 || metadata.Geo[1] <= 0 || metadata.Geo[5] >= 0 || metadata.Geo[2] != 0 || metadata.Geo[4] != 0 {
		return nil, errors.New("unsupported World Atlas geotransform")
	}
	return &WorldAtlas2015{path: path, cache: make(map[string]Estimate), width: metadata.Size[0], height: metadata.Size[1], geo: metadata.Geo}, nil
}
func (a *WorldAtlas2015) Close() error { return nil }
func (a *WorldAtlas2015) At(ctx context.Context, lat, lon float64) (Estimate, error) {
	key := fmt.Sprintf("%.5f,%.5f", lat, lon)
	a.mu.Lock()
	if value, ok := a.cache[key]; ok {
		a.mu.Unlock()
		return value, nil
	}
	a.mu.Unlock()
	x := (lon-a.geo[0])/a.geo[1] - 0.5
	y := (lat-a.geo[3])/a.geo[5] - 0.5
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	fx, fy := x-float64(x0), y-float64(y0)
	if x0 < 0 || y0 < 0 || x0+1 >= a.width || y0+1 >= a.height {
		return Estimate{}, errors.New("coordinate is outside World Atlas 2015 coverage")
	}
	output, err := exec.CommandContext(ctx, "gdal_translate", "-q", "-srcwin", strconv.Itoa(x0), strconv.Itoa(y0), "2", "2", "-of", "XYZ", a.path, "/vsistdout/").Output()
	if err != nil {
		return Estimate{}, fmt.Errorf("sample World Atlas 2015: %w", err)
	}
	lines := strings.Fields(string(output))
	if len(lines) != 12 {
		return Estimate{}, fmt.Errorf("invalid World Atlas sample window")
	}
	values := make([]float64, 4)
	for i := range values {
		value, parseErr := strconv.ParseFloat(lines[i*3+2], 64)
		if parseErr != nil || math.IsNaN(value) || value < 0 {
			return Estimate{}, errors.New("invalid World Atlas 2015 sample")
		}
		values[i] = value
	}
	mcd := (1-fy)*((1-fx)*values[0]+fx*values[1]) + fy*((1-fx)*values[2]+fx*values[3])
	if math.IsNaN(mcd) || mcd < 0 {
		return Estimate{}, errors.New("invalid World Atlas 2015 sample")
	}
	lpi := mcd / 0.171168465
	sqm := 22 - 2.5*math.Log10(1+lpi)
	result := Estimate{Year: 2015, LPI: lpi, SQM: sqm, Zone: lightPollutionZone(lpi), BortleDisplay: approximateBortle(sqm)}
	a.mu.Lock()
	if len(a.cache) >= 4096 {
		a.cache = make(map[string]Estimate)
	}
	a.cache[key] = result
	a.mu.Unlock()
	return result, nil
}
