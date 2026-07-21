// Package lightpollution provides location-specific artificial sky-brightness
// estimates. It deliberately keeps this static site property separate from the
// hourly weather and seeing score.
package lightpollution

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultBaseURL       = "https://djlorenz.github.io/astronomy/binary_tiles"
	defaultAtlasYear     = 2024
	tileCells            = 600
	tilePayloadBytes     = tileCells*tileCells + 1
	maximumDownloadBytes = 2 << 20
)

// Estimate describes modeled artificial zenith brightness at a coordinate.
// LPI is artificial brightness divided by a nominal natural-sky brightness.
type Estimate struct {
	Year          int     `json:"year"`
	LPI           float64 `json:"lpi"`
	SQM           float64 `json:"sqm_mag_arcsec2"`
	Zone          string  `json:"light_pollution_zone"`
	BortleDisplay string  `json:"approximate_bortle"`
}

// Options mainly exists so the downloader can be tested without public
// network access. Zero values select the production defaults.
type Options struct {
	BaseURL    string
	AtlasYear  int
	HTTPClient *http.Client
}

// Atlas reads David Lorenz's annual Light Pollution Atlas binary tiles. A tile
// is fetched on first use and then retained below the runtime data volume.
type Atlas struct {
	root    string
	baseURL string
	year    int
	client  *http.Client
	logf    func(string, ...any)

	mu    sync.Mutex
	tiles map[string][]int8
}

func NewAtlas(root string, options Options, logf func(string, ...any)) (*Atlas, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("light-pollution cache root is required")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create light-pollution cache root: %w", err)
	}
	if options.BaseURL == "" {
		options.BaseURL = defaultBaseURL
	}
	if options.AtlasYear == 0 {
		options.AtlasYear = defaultAtlasYear
	}
	if options.AtlasYear < 2012 || options.AtlasYear > time.Now().UTC().Year() {
		return nil, fmt.Errorf("invalid light-pollution atlas year %d", options.AtlasYear)
	}
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: 12 * time.Second}
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	result := &Atlas{
		root: root, baseURL: strings.TrimRight(options.BaseURL, "/"), year: options.AtlasYear,
		client: options.HTTPClient, logf: logf, tiles: make(map[string][]int8),
	}
	result.logf("light-pollution atlas configured for annual dataset %d", result.year)
	result.pruneOldYears(result.year)
	return result, nil
}

// At returns a bilinearly interpolated value from the four surrounding
// 30-arcsecond grid cells. The atlas covers 65 S through 75 N.
func (atlas *Atlas) At(ctx context.Context, latitude, longitude float64) (Estimate, error) {
	if math.IsNaN(latitude) || math.IsInf(latitude, 0) || latitude < -65 || latitude >= 75 {
		return Estimate{}, fmt.Errorf("latitude %.6f is outside Light Pollution Atlas coverage [-65, 75)", latitude)
	}
	if math.IsNaN(longitude) || math.IsInf(longitude, 0) || longitude < -180 || longitude > 180 {
		return Estimate{}, fmt.Errorf("invalid longitude %.6f", longitude)
	}

	atlas.mu.Lock()
	defer atlas.mu.Unlock()
	year := atlas.year

	// Grid-cell centres are offset half a cell from -180/-65. Interpolate the
	// physical LPI, not its logarithmically compressed integer representation.
	x := modulo(longitude+180, 360)*120 - 0.5
	y := (latitude+65)*120 - 0.5
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	fx, fy := x-float64(x0), y-float64(y0)
	if y0 < 0 {
		y0, fy = 0, 0
	}
	if y0 >= 16799 {
		y0, fy = 16799, 0
	}
	x0 = ((x0 % 43200) + 43200) % 43200
	x1 := (x0 + 1) % 43200
	y1 := y0 + 1
	if y1 > 16799 {
		y1 = 16799
	}

	v00, err := atlas.gridValue(ctx, year, x0, y0)
	if err != nil {
		return Estimate{}, err
	}
	v10, err := atlas.gridValue(ctx, year, x1, y0)
	if err != nil {
		return Estimate{}, err
	}
	v01, err := atlas.gridValue(ctx, year, x0, y1)
	if err != nil {
		return Estimate{}, err
	}
	v11, err := atlas.gridValue(ctx, year, x1, y1)
	if err != nil {
		return Estimate{}, err
	}
	lpi := (1-fy)*((1-fx)*v00+fx*v10) + fy*((1-fx)*v01+fx*v11)
	if lpi < 0 {
		lpi = 0
	}
	sqm := 22 - 2.5*math.Log10(1+lpi)
	return Estimate{
		Year: year, LPI: lpi, SQM: sqm, Zone: lightPollutionZone(lpi),
		BortleDisplay: approximateBortle(sqm),
	}, nil
}

func (atlas *Atlas) gridValue(ctx context.Context, year, globalX, globalY int) (float64, error) {
	tileX, tileY := globalX/tileCells+1, globalY/tileCells+1
	localX, localY := globalX%tileCells, globalY%tileCells
	payload, err := atlas.loadTile(ctx, year, tileX, tileY)
	if err != nil {
		return 0, err
	}
	compressed, err := decodeCompressedAt(payload, localX, localY)
	if err != nil {
		return 0, err
	}
	return compressedToLPI(compressed), nil
}

// loadTile is called under atlas.mu, which also prevents duplicate concurrent
// downloads of the same tile.
func (atlas *Atlas) loadTile(ctx context.Context, year, tileX, tileY int) ([]int8, error) {
	key := fmt.Sprintf("%d/%d/%d", year, tileX, tileY)
	if payload, ok := atlas.tiles[key]; ok {
		return payload, nil
	}
	path := atlas.tilePath(year, tileX, tileY)
	compressed, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read cached light-pollution tile: %w", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		compressed, err = atlas.downloadTile(ctx, year, tileX, tileY)
		if err != nil {
			return nil, err
		}
		if err := atomicWrite(path, compressed); err != nil {
			return nil, fmt.Errorf("cache light-pollution tile: %w", err)
		}
	}
	payload, err := inflateTile(compressed)
	if err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("decode light-pollution tile %d/%d/%d: %w", year, tileX, tileY, err)
	}
	atlas.tiles[key] = payload
	return payload, nil
}

func (atlas *Atlas) downloadTile(ctx context.Context, year, tileX, tileY int) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, atlas.tileURL(year, tileX, tileY), nil)
	if err != nil {
		return nil, err
	}
	response, err := atlas.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download light-pollution tile: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download light-pollution tile: HTTP %d", response.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maximumDownloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read light-pollution tile: %w", err)
	}
	if len(content) == 0 || len(content) > maximumDownloadBytes {
		return nil, fmt.Errorf("light-pollution tile has invalid compressed size %d", len(content))
	}
	return content, nil
}

func inflateTile(compressed []byte) ([]int8, error) {
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, tilePayloadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) != tilePayloadBytes {
		return nil, fmt.Errorf("got %d bytes, want %d", len(raw), tilePayloadBytes)
	}
	payload := make([]int8, len(raw))
	for index, value := range raw {
		payload[index] = int8(value)
	}
	return payload, nil
}

func decodeCompressedAt(payload []int8, x, y int) (int, error) {
	if len(payload) != tilePayloadBytes || x < 0 || x >= tileCells || y < 0 || y >= tileCells {
		return 0, errors.New("invalid light-pollution tile coordinate")
	}
	value := 128*int(payload[0]) + int(payload[1])
	for row := 1; row <= y; row++ {
		value += int(payload[tileCells*row+1])
	}
	rowStart := tileCells*y + 1
	for column := 1; column <= x; column++ {
		value += int(payload[rowStart+column])
	}
	return value, nil
}

func compressedToLPI(value int) float64 {
	return (5.0 / 195.0) * (math.Exp(0.0195*float64(value)) - 1)
}

func lightPollutionZone(lpi float64) string {
	thresholds := []struct {
		upper float64
		zone  string
	}{{0.01, "0"}, {0.06, "1a"}, {0.11, "1b"}, {0.19, "2a"}, {0.33, "2b"}, {0.58, "3a"}, {1, "3b"}, {1.73, "4a"}, {3, "4b"}, {5.2, "5a"}, {9, "5b"}, {15.59, "6a"}, {27, "6b"}, {46.77, "7a"}}
	for _, threshold := range thresholds {
		if lpi < threshold.upper {
			return threshold.zone
		}
	}
	return "7b"
}

// Bortle is a subjective all-sky visual classification, while the atlas is a
// modeled zenith brightness. The conventional SQM correspondence therefore
// remains explicitly approximate; the brightest bin cannot reliably separate
// classes 8 and 9.
func approximateBortle(sqm float64) string {
	switch {
	case sqm >= 21.99:
		return "1"
	case sqm >= 21.89:
		return "2"
	case sqm >= 21.69:
		return "3"
	case sqm >= 20.49:
		return "4"
	case sqm >= 19.50:
		return "5"
	case sqm >= 18.94:
		return "6"
	case sqm >= 18.38:
		return "7"
	default:
		return "8–9"
	}
}

func (atlas *Atlas) tileURL(year, tileX, tileY int) string {
	return fmt.Sprintf("%s/%d/binary_tile_%d_%d.dat.gz", atlas.baseURL, year, tileX, tileY)
}

func (atlas *Atlas) tilePath(year, tileX, tileY int) string {
	return filepath.Join(atlas.root, strconv.Itoa(year), fmt.Sprintf("binary_tile_%d_%d.dat.gz", tileX, tileY))
}

func (atlas *Atlas) pruneOldYears(current int) {
	entries, err := os.ReadDir(atlas.root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		year, err := strconv.Atoi(entry.Name())
		if err == nil && entry.IsDir() && year != current && year != current-1 {
			_ = os.RemoveAll(filepath.Join(atlas.root, entry.Name()))
		}
	}
}

func atomicWrite(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".incoming-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o640); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func modulo(value, divisor float64) float64 {
	return math.Mod(math.Mod(value, divisor)+divisor, divisor)
}
