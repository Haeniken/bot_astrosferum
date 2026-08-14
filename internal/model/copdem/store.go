package copdem

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"bot_astrosferum/internal/forecast"
)

const (
	defaultBaseURL         = "https://copernicus-dem-30m.s3.eu-central-1.amazonaws.com"
	maximumTileBytes       = 128 << 20
	maximumProfileEntries  = 4096
	defaultCacheLimitBytes = 20 << 30
)

type Config struct {
	Enabled         bool
	Root            string
	BaseURL         string
	HTTPClient      *http.Client
	CacheLimitBytes int64
	Logf            func(string, ...any)
}

// CacheKey returns an existing immutable profile identity without performing
// network or GDAL work. A miss is represented explicitly and can be admitted
// to the shared directional queue before Resolve performs the cold build.
func (store *Store) CacheKey(location forecast.Location) (string, bool, error) {
	if store == nil {
		return "", false, errors.New("copernicus DEM store is required")
	}
	if !store.enabled {
		return forecast.TerrainSkylineVersion + ":disabled", true, nil
	}
	canonical, err := canonicalLocation(location)
	if err != nil {
		return "", false, err
	}
	key := profileKey(canonical)
	if _, err := loadProfile(filepath.Join(store.root, "profiles", key+".json")); err != nil {
		return key, false, nil
	}
	return key, true, nil
}

// Store owns immutable source tiles and static per-coordinate skylines. It is
// intentionally process-local; all expensive work happens before a
// directional request is submitted and never once per forecast hour.
type Store struct {
	enabled         bool
	root            string
	baseURL         string
	client          *http.Client
	cacheLimitBytes int64
	logf            func(string, ...any)
	buildMu         sync.Mutex
}

func NewStore(config Config) (*Store, error) {
	if strings.TrimSpace(config.Root) == "" {
		return nil, errors.New("copernicus DEM cache root is required")
	}
	if config.BaseURL == "" {
		config.BaseURL = defaultBaseURL
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 15 * time.Minute}
	}
	if config.CacheLimitBytes == 0 {
		config.CacheLimitBytes = defaultCacheLimitBytes
	}
	if config.CacheLimitBytes < 0 {
		return nil, errors.New("copernicus DEM cache limit must be non-negative")
	}
	if config.Logf == nil {
		config.Logf = func(string, ...any) {}
	}
	return &Store{
		enabled: config.Enabled, root: config.Root, baseURL: strings.TrimRight(config.BaseURL, "/"), cacheLimitBytes: config.CacheLimitBytes,
		client: config.HTTPClient, logf: config.Logf,
	}, nil
}

func (store *Store) Resolve(ctx context.Context, location forecast.Location) (forecast.TerrainSkyline, error) {
	if store == nil {
		return forecast.TerrainSkyline{}, errors.New("copernicus DEM store is required")
	}
	if !store.enabled {
		return forecast.DisabledTerrainSkyline(), nil
	}
	var err error
	location, err = canonicalLocation(location)
	if err != nil {
		return forecast.TerrainSkyline{}, err
	}
	key := profileKey(location)

	path := filepath.Join(store.root, "profiles", key+".json")
	if profile, err := loadProfile(path); err == nil {
		_ = os.Chtimes(path, time.Now(), time.Now())
		return profile, nil
	}
	started := time.Now()
	// GDAL sampling is the only expensive source operation. Serialize cache
	// misses so concurrent admitted jobs cannot multiply downloads, disk I/O,
	// or the native-cell scan inside the directional worker.
	store.buildMu.Lock()
	defer store.buildMu.Unlock()
	if profile, err := loadProfile(path); err == nil {
		_ = os.Chtimes(path, time.Now(), time.Now())
		return profile, nil
	}
	profile, err := store.build(ctx, location, key)
	if err != nil {
		return forecast.TerrainSkyline{}, err
	}
	if err := saveProfile(path, profile); err != nil {
		return forecast.TerrainSkyline{}, fmt.Errorf("publish Copernicus DEM skyline: %w", err)
	}
	if err := store.pruneProfiles(path); err != nil {
		store.logf("Copernicus DEM profile cache prune failed: %v", err)
	}
	if err := store.pruneTiles(nil); err != nil {
		store.logf("Copernicus DEM tile cache prune failed: %v", err)
	}
	store.logf("Copernicus DEM GLO-30 skyline ready: samples=%d digest=%s duration=%s",
		len(profile.Samples), profile.DigestSHA256, time.Since(started).Round(time.Millisecond))
	return profile, nil
}

func canonicalLocation(location forecast.Location) (forecast.Location, error) {
	if err := forecast.ValidateCoordinates(location.Latitude, location.Longitude); err != nil || math.Abs(location.Latitude) == 90 {
		return forecast.Location{}, errors.New("copernicus DEM skyline location is invalid")
	}
	location.Latitude = math.Round(location.Latitude/forecast.TerrainSkylineCoordinateStepDeg) * forecast.TerrainSkylineCoordinateStepDeg
	location.Longitude = normalizeLongitude(math.Round(location.Longitude/forecast.TerrainSkylineCoordinateStepDeg) * forecast.TerrainSkylineCoordinateStepDeg)
	return location, nil
}

type samplePoint struct {
	latitude  float64
	longitude float64
}

type tileArtifact struct {
	path     string
	manifest forecast.TerrainSkylineInput
}

func (store *Store) build(ctx context.Context, location forecast.Location, key string) (forecast.TerrainSkyline, error) {
	tiles := make(map[string]string)
	for _, name := range terrainTileNames(location) {
		tiles[name] = filepath.Join(store.root, "tiles", name, name+".tif")
	}
	names := make([]string, 0, len(tiles))
	for name := range tiles {
		names = append(names, name)
	}
	sort.Strings(names)
	paths := make([]string, 0, len(names))
	inputs := make([]forecast.TerrainSkylineInput, 0, len(names))
	for _, name := range names {
		path := tiles[name]
		artifact, err := store.ensureTile(ctx, name, path)
		if err != nil {
			return forecast.TerrainSkyline{}, err
		}
		paths = append(paths, artifact.path)
		inputs = append(inputs, artifact.manifest)
	}
	workRoot := filepath.Join(store.root, "work")
	if err := os.MkdirAll(workRoot, 0o750); err != nil {
		return forecast.TerrainSkyline{}, err
	}
	work, err := os.MkdirTemp(workRoot, key+"-")
	if err != nil {
		return forecast.TerrainSkyline{}, err
	}
	defer func() { _ = os.RemoveAll(work) }()
	vrt := filepath.Join(work, "terrain.vrt")
	arguments := append([]string{"-q", "-resolution", "highest", "-srcnodata", "-32767", "-vrtnodata", "-32767", vrt}, paths...)
	if output, err := exec.CommandContext(ctx, "gdalbuildvrt", arguments...).CombinedOutput(); err != nil {
		return forecast.TerrainSkyline{}, fmt.Errorf("build Copernicus DEM VRT: %w: %s", err, strings.TrimSpace(string(output)))
	}
	observerHeight, err := sampleHeights(ctx, vrt, []samplePoint{{latitude: location.Latitude, longitude: location.Longitude}})
	if err != nil {
		return forecast.TerrainSkyline{}, err
	}
	// The terrain angle is referenced to the same 2 m aperture height used by
	// the Astrodome direction contract. The stored observer elevation remains
	// the DSM surface so provenance is not silently redefined.
	observerSurfaceHeight := observerHeight[0]
	observerApertureHeight := observerSurfaceHeight + forecast.TerrainSkylineApertureHeightAGLM
	maxima, err := nativeCellSkyline(ctx, paths, location, observerApertureHeight)
	if err != nil {
		return forecast.TerrainSkyline{}, err
	}
	profile, err := forecast.NewTerrainSkyline(location.Latitude, location.Longitude, observerSurfaceHeight, inputs, maxima)
	if err != nil {
		return forecast.TerrainSkyline{}, err
	}
	protected := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		protected[path] = struct{}{}
	}
	if err := store.pruneTiles(protected); err != nil {
		return forecast.TerrainSkyline{}, err
	}
	return profile, nil
}

func terrainTileNames(location forecast.Location) []string {
	// Analytic bounding coordinates of the spherical search cap guarantee that
	// even a tile touched only at a narrow corner is included. Longitude is kept
	// unwrapped around the observer while enumerating and normalized by tileName.
	angularRadius := forecast.TerrainSkylineMaximumDistanceM / forecast.HorizonEarthRadiusM
	latitude := location.Latitude * math.Pi / 180
	minimumLatitude := math.Max(-math.Pi/2, latitude-angularRadius) * 180 / math.Pi
	maximumLatitude := math.Min(math.Pi/2, latitude+angularRadius) * 180 / math.Pi
	longitudeRadius := math.Pi
	if math.Abs(latitude)+angularRadius < math.Pi/2 {
		longitudeRadius = math.Asin(math.Sin(angularRadius) / math.Cos(latitude))
	}
	minimumLongitude := location.Longitude - longitudeRadius*180/math.Pi
	maximumLongitude := location.Longitude + longitudeRadius*180/math.Pi

	names := make(map[string]struct{})
	for latitudeDegree := int(math.Floor(minimumLatitude)); latitudeDegree <= int(math.Floor(maximumLatitude)); latitudeDegree++ {
		if latitudeDegree < -90 || latitudeDegree > 89 {
			continue
		}
		for longitudeDegree := int(math.Floor(minimumLongitude)); longitudeDegree <= int(math.Floor(maximumLongitude)); longitudeDegree++ {
			names[tileName(float64(latitudeDegree), float64(longitudeDegree))] = struct{}{}
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func nativeCellSkyline(ctx context.Context, paths []string, location forecast.Location, observerApertureHeight float64) ([]forecast.TerrainSkylineSample, error) {
	if len(paths) == 0 {
		return nil, errors.New("scan native Copernicus DEM cells: source tiles are required")
	}
	arguments := []string{
		"-c", nativeCellSkylineScript,
		strconv.FormatFloat(location.Latitude, 'g', 17, 64), strconv.FormatFloat(location.Longitude, 'g', 17, 64),
		strconv.FormatFloat(observerApertureHeight, 'g', 17, 64), strconv.FormatFloat(forecast.TerrainSkylineMaximumDistanceM, 'g', 17, 64),
		"-32767",
	}
	arguments = append(arguments, paths...)
	command := exec.CommandContext(ctx, "python3", arguments...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("scan native Copernicus DEM cells: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	var result []forecast.TerrainSkylineSample
	if err := decoder.Decode(&result); err != nil || len(result) != forecast.TerrainSkylineAzimuthCount {
		return nil, fmt.Errorf("decode native Copernicus DEM skyline: %w", err)
	}
	return result, nil
}

const nativeCellSkylineScript = `
import json, math, sys
import numpy as np
from osgeo import gdal

gdal.UseExceptions()

observer_lat, observer_lon, observer_h, maximum_distance, nodata = sys.argv[1:6]
paths = sys.argv[6:]
observer_lat = float(observer_lat); observer_lon = float(observer_lon)
observer_h = float(observer_h); maximum_distance = float(maximum_distance)
nodata = float(nodata)
radius = 6371008.8
angular = maximum_distance / radius
lat = math.radians(observer_lat); lon = math.radians(observer_lon)
boundary = []
for azimuth_degrees in range(360):
    azimuth = math.radians(azimuth_degrees)
    target_lat = math.asin(math.sin(lat)*math.cos(angular) + math.cos(lat)*math.sin(angular)*math.cos(azimuth))
    target_lon = lon + math.atan2(math.sin(azimuth)*math.sin(angular)*math.cos(lat), math.cos(angular)-math.sin(lat)*math.sin(target_lat))
    boundary.append((math.degrees(target_lon), math.degrees(target_lat)))
maxima = [-90.0] * 360; distances = [0.0] * 360
chunk_rows = 256
for path in paths:
    dataset = gdal.Open(path, gdal.GA_ReadOnly)
    if dataset is None or dataset.RasterCount != 1:
        raise RuntimeError("invalid source COG")
    band = dataset.GetRasterBand(1)
    transform = dataset.GetGeoTransform()
    inverse = gdal.InvGeoTransform(transform)
    pixels = [gdal.ApplyGeoTransform(inverse, x, y) for x, y in boundary + [(observer_lon, observer_lat)]]
    min_x = max(0, math.floor(min(p[0] for p in pixels)) - 1); max_x = min(dataset.RasterXSize, math.ceil(max(p[0] for p in pixels)) + 1)
    min_y = max(0, math.floor(min(p[1] for p in pixels)) - 1); max_y = min(dataset.RasterYSize, math.ceil(max(p[1] for p in pixels)) + 1)
    if min_x >= max_x or min_y >= max_y:
        continue
    for y0 in range(min_y, max_y, chunk_rows):
        rows = min(chunk_rows, max_y-y0)
        heights = band.ReadAsArray(min_x, y0, max_x-min_x, rows).astype(np.float64, copy=False)
        columns = np.arange(min_x, max_x, dtype=np.float64) + .5
        row_values = np.arange(y0, y0+rows, dtype=np.float64) + .5
        longitudes = transform[0] + columns * transform[1] + row_values[:,None] * transform[2]
        latitudes = transform[3] + columns[None,:] * transform[4] + row_values[:,None] * transform[5]
        if transform[2] == 0:
            longitudes = np.broadcast_to(longitudes, heights.shape)
        if transform[4] == 0:
            latitudes = np.broadcast_to(latitudes, heights.shape)
        phi = np.radians(latitudes); dphi = phi-lat
        dlambda = np.radians(((longitudes-observer_lon+180.0)%360.0)-180.0)
        hav = np.sin(dphi/2)**2 + math.cos(lat)*np.cos(phi)*np.sin(dlambda/2)**2
        central = 2*np.arctan2(np.sqrt(np.maximum(0, hav)), np.sqrt(np.maximum(0, 1-hav)))
        distance = radius * central
        mask = (distance > 0) & (distance <= maximum_distance) & np.isfinite(heights) & (heights != nodata) & (heights >= -500) & (heights <= 10500)
        if not np.any(mask): continue
        bearing = np.degrees(np.arctan2(np.sin(dlambda)*np.cos(phi), math.cos(lat)*np.sin(phi)-math.sin(lat)*np.cos(phi)*np.cos(dlambda))) % 360
        azimuth_index = np.floor(bearing + .5).astype(np.int64) % 360
        terrain_radius = radius + heights
        elevation = np.degrees(np.arctan2(terrain_radius*np.cos(central)-(radius+observer_h), terrain_radius*np.sin(central)))
        valid = np.flatnonzero(mask)
        bins = azimuth_index.flat[valid]
        values = elevation.flat[valid]
        chunk_maxima = np.full(360, -np.inf, dtype=np.float64)
        np.maximum.at(chunk_maxima, bins, values)
        # Equality selects at least one exact source-cell value per populated
        # bin; choose the first deterministic cell in raster traversal order.
        winners = np.flatnonzero(values == chunk_maxima[bins])
        for winner in winners:
            azimuth = int(bins[winner]); value = float(values[winner])
            if value > maxima[azimuth]:
                maxima[azimuth] = value
                distances[azimuth] = float(distance.flat[valid[winner]])
if any(value == -90.0 for value in maxima):
    raise RuntimeError("native grid did not cover every azimuth")
json.dump([{"azimuth_degrees": float(i), "elevation_degrees": maxima[i], "obstacle_surface_distance_m": distances[i]} for i in range(360)], sys.stdout, separators=(",",":"), allow_nan=False)
`

func sampleHeights(ctx context.Context, vrt string, points []samplePoint) ([]float64, error) {
	command := exec.CommandContext(ctx, "gdallocationinfo", "-wgs84", "-r", "bilinear", "-valonly", vrt)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start Copernicus DEM sampler: %w", err)
	}
	writeErr := make(chan error, 1)
	go func() {
		writer := bufio.NewWriterSize(stdin, 1<<20)
		for _, point := range points {
			if _, err := fmt.Fprintf(writer, "%.10f %.10f\n", point.longitude, point.latitude); err != nil {
				writeErr <- err
				_ = stdin.Close()
				return
			}
		}
		err := writer.Flush()
		closeErr := stdin.Close()
		writeErr <- errors.Join(err, closeErr)
	}()
	heights := make([]float64, 0, len(points))
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		value, err := strconv.ParseFloat(strings.TrimSpace(scanner.Text()), 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < -500 || value > 10_000 {
			_ = command.Process.Kill()
			_ = command.Wait()
			return nil, fmt.Errorf("copernicus DEM returned invalid height %q", scanner.Text())
		}
		heights = append(heights, value)
	}
	if err := scanner.Err(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, err
	}
	if err := <-writeErr; err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, err
	}
	if err := command.Wait(); err != nil {
		return nil, fmt.Errorf("sample Copernicus DEM: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if len(heights) != len(points) {
		return nil, fmt.Errorf("copernicus DEM returned %d heights for %d points", len(heights), len(points))
	}
	return heights, nil
}

func (store *Store) ensureTile(ctx context.Context, name, path string) (tileArtifact, error) {
	if metadata, err := validateTile(ctx, name, path); err == nil {
		_ = os.Chtimes(path, time.Now(), time.Now())
		return tileArtifact{path: path, manifest: metadata}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return tileArtifact{}, err
	}
	url := store.baseURL + "/" + name + "/" + name + ".tif"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return tileArtifact{}, err
	}
	response, err := store.client.Do(request)
	if err != nil {
		return tileArtifact{}, fmt.Errorf("download Copernicus DEM tile %s: %w", name, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return tileArtifact{}, fmt.Errorf("download Copernicus DEM tile %s: HTTP %d", name, response.StatusCode)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".download-*.tif")
	if err != nil {
		return tileArtifact{}, err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	written, copyErr := io.Copy(temporary, io.LimitReader(response.Body, maximumTileBytes+1))
	closeErr := temporary.Close()
	if copyErr != nil || closeErr != nil || written <= 16 || written > maximumTileBytes {
		return tileArtifact{}, fmt.Errorf("download Copernicus DEM tile %s is incomplete or too large", name)
	}
	metadata, err := validateTile(ctx, name, temporaryPath)
	if err != nil {
		return tileArtifact{}, fmt.Errorf("validate downloaded Copernicus DEM tile %s: %w", name, err)
	}
	metadata.ObjectETag = strings.Trim(response.Header.Get("ETag"), "\"")
	metadata.ObjectBytes = written
	if metadata.ObjectETag == "" {
		metadata.ObjectETag = "unavailable"
	}
	if err := os.Chmod(temporaryPath, 0o640); err != nil {
		return tileArtifact{}, err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return tileArtifact{}, err
	}
	if err := saveTileManifest(path+".manifest.json", metadata); err != nil {
		return tileArtifact{}, err
	}
	return tileArtifact{path: path, manifest: metadata}, nil
}

type cacheFile struct {
	path    string
	size    int64
	modTime time.Time
}

func (store *Store) pruneTiles(protected map[string]struct{}) error {
	root := filepath.Join(store.root, "tiles")
	files, total, err := cacheFiles(root, ".tif")
	if err != nil {
		return err
	}
	for _, file := range files {
		if total <= store.cacheLimitBytes {
			break
		}
		if _, keep := protected[file.path]; keep {
			continue
		}
		if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Remove(file.path + ".manifest.json"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		total -= file.size
	}
	if total > store.cacheLimitBytes {
		return fmt.Errorf("copernicus DEM tile cache needs %d bytes but its limit is %d bytes", total, store.cacheLimitBytes)
	}
	return nil
}

func (store *Store) pruneProfiles(protected string) error {
	files, _, err := cacheFiles(filepath.Join(store.root, "profiles"), ".json")
	if err != nil {
		return err
	}
	for len(files) > maximumProfileEntries {
		file := files[0]
		files = files[1:]
		if file.path == protected {
			files = append(files, file)
			continue
		}
		if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func cacheFiles(root, extension string) ([]cacheFile, int64, error) {
	var files []cacheFile
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != extension {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, cacheFile{path: path, size: info.Size(), modTime: info.ModTime()})
		total += info.Size()
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	sort.Slice(files, func(left, right int) bool {
		if files[left].modTime.Equal(files[right].modTime) {
			return files[left].path < files[right].path
		}
		return files[left].modTime.Before(files[right].modTime)
	})
	return files, total, err
}

type gdalInfo struct {
	DriverShortName string                       `json:"driverShortName"`
	Size            [2]int                       `json:"size"`
	GeoTransform    [6]float64                   `json:"geoTransform"`
	Metadata        map[string]map[string]string `json:"metadata"`
	Bands           []struct {
		Band        int      `json:"band"`
		Type        string   `json:"type"`
		NoDataValue *float64 `json:"noDataValue"`
		Unit        string   `json:"unit"`
		Scale       *float64 `json:"scale"`
		Offset      *float64 `json:"offset"`
	} `json:"bands"`
	STAC struct {
		EPSG int `json:"proj:epsg"`
	} `json:"stac"`
}

func validateTile(ctx context.Context, name, path string) (forecast.TerrainSkylineInput, error) {
	file, err := os.Open(path)
	if err != nil {
		return forecast.TerrainSkylineInput{}, err
	}
	defer func() { _ = file.Close() }()
	header := make([]byte, 4)
	if _, err := io.ReadFull(file, header); err != nil {
		return forecast.TerrainSkylineInput{}, err
	}
	if string(header) != "II*\x00" && string(header) != "MM\x00*" {
		return forecast.TerrainSkylineInput{}, errors.New("file is not a TIFF")
	}
	output, err := exec.CommandContext(ctx, "gdalinfo", "-json", path).CombinedOutput()
	if err != nil {
		return forecast.TerrainSkylineInput{}, fmt.Errorf("inspect GeoTIFF: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var info gdalInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return forecast.TerrainSkylineInput{}, fmt.Errorf("decode GDAL metadata: %w", err)
	}
	latitudeDegree, longitudeDegree, ok := parseTileName(name)
	if !ok {
		return forecast.TerrainSkylineInput{}, errors.New("invalid GLO-30 tile name")
	}
	wantWidth, validWidth := forecast.GLO30TileRasterWidth(name)
	if !validWidth {
		return forecast.TerrainSkylineInput{}, errors.New("invalid GLO-30 tile dimensions")
	}
	wantTransform := [6]float64{float64(longitudeDegree) - 0.5/float64(wantWidth), 1 / float64(wantWidth), 0,
		float64(latitudeDegree+1) + 0.5/3600, 0, -1.0 / 3600}
	if info.DriverShortName != "GTiff" || info.Size != [2]int{wantWidth, 3600} || info.STAC.EPSG != 4326 ||
		len(info.Bands) != 1 || info.Bands[0].Band != 1 || info.Bands[0].Type != "Float32" ||
		info.Bands[0].NoDataValue != nil || info.Bands[0].Scale != nil || info.Bands[0].Offset != nil || info.Bands[0].Unit != "" ||
		info.Metadata[""]["AREA_OR_POINT"] != "Point" || info.Metadata["IMAGE_STRUCTURE"]["LAYOUT"] != "COG" {
		return forecast.TerrainSkylineInput{}, errors.New("GeoTIFF violates the GLO-30 raster contract")
	}
	for index := range wantTransform {
		if math.Abs(info.GeoTransform[index]-wantTransform[index]) > 2e-14 {
			return forecast.TerrainSkylineInput{}, errors.New("GeoTIFF violates the GLO-30 geotransform contract")
		}
	}
	digest, bytes, err := hashFile(path)
	if err != nil {
		return forecast.TerrainSkylineInput{}, err
	}
	// The AWS COG adaptation omits optional GeoTIFF band metadata. Its source
	// DGED contract is unpacked Float32 metres with NoData=-32767, scale=1 and
	// offset=0; these canonical semantics are explicit in the manifest and the
	// scanner masks the source sentinel before all physical range checks.
	metadata := forecast.TerrainSkylineInput{
		TileID: name, ObjectBytes: bytes, SHA256: digest, RasterWidth: wantWidth, RasterHeight: 3600,
		NoDataValue: -32767, Scale: 1, Offset: 0, Unit: "m",
	}
	if cached, err := loadTileManifest(path + ".manifest.json"); err == nil && cached.TileID == name && cached.SHA256 == digest && cached.ObjectBytes == bytes {
		metadata.ObjectETag = cached.ObjectETag
	}
	if metadata.ObjectETag == "" {
		metadata.ObjectETag = "unavailable"
	}
	return metadata, nil
}

func tileName(latitude, longitude float64) string {
	latitudeDegree := int(math.Floor(latitude))
	longitudeDegree := int(math.Floor(normalizeLongitude(longitude)))
	latPrefix, lonPrefix := "N", "E"
	if latitudeDegree < 0 {
		latPrefix = "S"
		latitudeDegree = -latitudeDegree
	}
	if longitudeDegree < 0 {
		lonPrefix = "W"
		longitudeDegree = -longitudeDegree
	}
	return fmt.Sprintf("Copernicus_DSM_COG_10_%s%02d_00_%s%03d_00_DEM", latPrefix, latitudeDegree, lonPrefix, longitudeDegree)
}

func parseTileName(name string) (int, int, bool) {
	var latitudePrefix, longitudePrefix byte
	var latitude, longitude int
	if _, err := fmt.Sscanf(name, "Copernicus_DSM_COG_10_%c%02d_00_%c%03d_00_DEM", &latitudePrefix, &latitude, &longitudePrefix, &longitude); err != nil {
		return 0, 0, false
	}
	if latitudePrefix == 'S' {
		latitude = -latitude
	} else if latitudePrefix != 'N' {
		return 0, 0, false
	}
	if longitudePrefix == 'W' {
		longitude = -longitude
	} else if longitudePrefix != 'E' {
		return 0, 0, false
	}
	if latitude < -90 || latitude > 89 || longitude < -180 || longitude > 179 || tileName(float64(latitude), float64(longitude)) != name {
		return 0, 0, false
	}
	return latitude, longitude, true
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	written, err := io.Copy(digest, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(digest.Sum(nil)), written, nil
}

func saveTileManifest(path string, manifest forecast.TerrainSkylineInput) error {
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tile-manifest-*.json")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o640); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
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

func loadTileManifest(path string) (forecast.TerrainSkylineInput, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return forecast.TerrainSkylineInput{}, err
	}
	var result forecast.TerrainSkylineInput
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return forecast.TerrainSkylineInput{}, err
	}
	return result, nil
}

func destination(latitude, longitude, azimuthDegrees, distanceM float64) (float64, float64) {
	angular := distanceM / forecast.HorizonEarthRadiusM
	lat := latitude * math.Pi / 180
	lon := longitude * math.Pi / 180
	azimuth := azimuthDegrees * math.Pi / 180
	destinationLatitude := math.Asin(math.Sin(lat)*math.Cos(angular) + math.Cos(lat)*math.Sin(angular)*math.Cos(azimuth))
	destinationLongitude := lon + math.Atan2(math.Sin(azimuth)*math.Sin(angular)*math.Cos(lat), math.Cos(angular)-math.Sin(lat)*math.Sin(destinationLatitude))
	return destinationLatitude * 180 / math.Pi, normalizeLongitude(destinationLongitude * 180 / math.Pi)
}

func directSphericalElevation(observerHeightM, terrainHeightM, surfaceDistanceM float64) float64 {
	centralAngle := surfaceDistanceM / forecast.HorizonEarthRadiusM
	observerRadius := forecast.HorizonEarthRadiusM + observerHeightM
	terrainRadius := forecast.HorizonEarthRadiusM + terrainHeightM
	radial := terrainRadius*math.Cos(centralAngle) - observerRadius
	tangential := terrainRadius * math.Sin(centralAngle)
	return math.Atan2(radial, tangential) * 180 / math.Pi
}

func profileKey(location forecast.Location) string {
	canonical := fmt.Sprintf("%s|%.5f|%.5f", forecast.TerrainSkylineVersion, location.Latitude, location.Longitude)
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:])
}

func loadProfile(path string) (forecast.TerrainSkyline, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return forecast.TerrainSkyline{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	var profile forecast.TerrainSkyline
	if err := decoder.Decode(&profile); err != nil {
		return forecast.TerrainSkyline{}, err
	}
	if err := profile.Validate(); err != nil {
		return forecast.TerrainSkyline{}, err
	}
	return profile, nil
}

func saveProfile(path string, profile forecast.TerrainSkyline) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".profile-*.json")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o640); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
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

func normalizeLongitude(value float64) float64 {
	value = math.Mod(value+180, 360)
	if value < 0 {
		value += 360
	}
	return value - 180
}
