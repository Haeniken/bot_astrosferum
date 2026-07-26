package geoscf

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"bot_astrosferum/internal/forecast"
)

const (
	// A cold public OPeNDAP subset request is routinely slower than 30 s even
	// though the response is only a few dozen KiB. The bot joins this optional
	// result for a much shorter bounded interval; this HTTP deadline primarily
	// lets the in-flight request finish and populate the shared RAM cache.
	defaultRequestTimeout  = 60 * time.Second
	defaultMetadataTTL     = 10 * time.Minute
	defaultDataTTL         = 6 * time.Hour
	defaultMaximumRunAge   = 48 * time.Hour
	defaultCacheEntries    = 256
	defaultDataConcurrency = 2
	defaultMaxResponseSize = int64(4 << 20)
)

var variableNames = []string{
	"aod550_bc",
	"aod550_dust",
	"aod550_oc",
	"aod550_psc",
	"aod550_sla",
	"aod550_sna",
	"aod550_ss",
	"totcol_o3",
}

type Options struct {
	DatasetURL       string
	HTTPClient       *http.Client
	RequestTimeout   time.Duration
	MetadataTTL      time.Duration
	DataTTL          time.Duration
	MaximumRunAge    time.Duration
	CacheEntries     int
	DataConcurrency  int
	MaxResponseBytes int64
	Now              func() time.Time
}

type metadataCache struct {
	value     datasetMetadata
	expiresAt time.Time
}

type dataCacheEntry struct {
	value     rawPointSeries
	expiresAt time.Time
	used      uint64
}

type flight struct {
	done  chan struct{}
	value rawPointSeries
	err   error
}

// Client is safe for concurrent use. Metadata and point slabs are cached in a
// bounded in-memory LRU, and concurrent identical slab requests are coalesced.
type Client struct {
	datasetURL       string
	httpClient       *http.Client
	requestTimeout   time.Duration
	metadataTTL      time.Duration
	dataTTL          time.Duration
	maximumRunAge    time.Duration
	cacheEntries     int
	dataSlots        chan struct{}
	maxResponseBytes int64
	now              func() time.Time

	metadataMu sync.Mutex
	metadata   metadataCache
	metaFlight *metadataFlight

	cacheMu sync.Mutex
	clock   uint64
	cache   map[string]dataCacheEntry
	flights map[string]*flight
}

type metadataFlight struct {
	done  chan struct{}
	value datasetMetadata
	err   error
}

func New(options Options) *Client {
	datasetURL := strings.TrimSuffix(strings.TrimSpace(options.DatasetURL), "/")
	if datasetURL == "" {
		datasetURL = DefaultDatasetURL
	}
	requestTimeout := options.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = defaultRequestTimeout
	}
	metadataTTL := options.MetadataTTL
	if metadataTTL <= 0 {
		metadataTTL = defaultMetadataTTL
	}
	dataTTL := options.DataTTL
	if dataTTL <= 0 {
		dataTTL = defaultDataTTL
	}
	maximumRunAge := options.MaximumRunAge
	if maximumRunAge <= 0 {
		maximumRunAge = defaultMaximumRunAge
	}
	cacheEntries := options.CacheEntries
	if cacheEntries <= 0 {
		cacheEntries = defaultCacheEntries
	}
	dataConcurrency := options.DataConcurrency
	if dataConcurrency <= 0 {
		dataConcurrency = defaultDataConcurrency
	}
	maxResponseBytes := options.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultMaxResponseSize
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}
	return &Client{
		datasetURL:       datasetURL,
		httpClient:       httpClient,
		requestTimeout:   requestTimeout,
		metadataTTL:      metadataTTL,
		dataTTL:          dataTTL,
		maximumRunAge:    maximumRunAge,
		cacheEntries:     cacheEntries,
		dataSlots:        make(chan struct{}, dataConcurrency),
		maxResponseBytes: maxResponseBytes,
		now:              now,
		cache:            make(map[string]dataCacheEntry),
		flights:          make(map[string]*flight),
	}
}

// AtmosphericComposition retrieves and interpolates GEOS-CF composition for
// each requested valid time. The requested order is retained.
func (client *Client) AtmosphericComposition(ctx context.Context, location forecast.Location, validTimes []time.Time) (Series, error) {
	if err := validateRequest(location, validTimes); err != nil {
		return Series{}, err
	}
	metadata, err := client.loadMetadata(ctx)
	if err != nil {
		return Series{}, err
	}
	now := client.now().UTC()
	if metadata.runTime.After(now) {
		return Series{}, fmt.Errorf("%w: derived run time %s is in the future", ErrMalformedData, metadata.runTime.Format(time.RFC3339))
	}
	age := now.Sub(metadata.runTime)
	if age > client.maximumRunAge {
		return Series{}, fmt.Errorf("%w: run %s is %s old (maximum %s)", ErrStaleDataset, metadata.runID, age.Round(time.Minute), client.maximumRunAge)
	}

	timeWindow, err := interpolationWindow(metadata, validTimes)
	if err != nil {
		return Series{}, err
	}
	grid := locateGridCell(metadata, location)
	raw, err := client.loadPointSeries(ctx, metadata, grid, timeWindow)
	if err != nil {
		return Series{}, err
	}
	frames := make([]Frame, len(validTimes))
	for index, validTime := range validTimes {
		frame, interpolateErr := interpolateFrame(raw, metadata, grid, validTime.UTC())
		if interpolateErr != nil {
			return Series{}, fmt.Errorf("interpolate %s: %w", validTime.UTC().Format(time.RFC3339), interpolateErr)
		}
		frames[index] = frame
	}

	return Series{
		Location: location,
		Source: SourceMetadata{
			RunID:                  metadata.runID,
			RunTimeUTC:             metadata.runTime,
			ForecastWindowStartUTC: metadata.firstValid,
			ForecastWindowEndUTC:   metadata.lastValid,
			PublicationTimeUTC:     metadata.publicationTime,
			RetrievedAtUTC:         raw.retrievedAt,
			GridResolutionDegrees:  metadata.lonResolution,
			TemporalResolution:     metadata.timeStep,
			SpatialSupport:         grid.support(metadata),
			Freshness: Freshness{
				ReferenceTimeUTC: metadata.runTime,
				Age:              age,
				MaximumAge:       client.maximumRunAge,
				Stale:            false,
			},
			Provenance: Provenance{
				Institution:           "NASA Global Modeling and Assimilation Office",
				System:                ProviderName,
				Product:               ProductName,
				DatasetURL:            client.datasetURL,
				Variables:             append([]string(nil), variableNames...),
				SpatialInterpolation:  "bilinear on the native periodic longitude/latitude grid",
				TemporalInterpolation: "linear between hourly-mean midpoint samples",
				RunTimeDerivation:     "first hourly-mean midpoint minus 30 minutes",
			},
		},
		Frames: frames,
	}, nil
}

func validateRequest(location forecast.Location, validTimes []time.Time) error {
	if math.IsNaN(location.Latitude) || math.IsInf(location.Latitude, 0) || location.Latitude < -90 || location.Latitude > 90 {
		return fmt.Errorf("%w: latitude must be finite and within [-90, 90]", ErrInvalidRequest)
	}
	if math.IsNaN(location.Longitude) || math.IsInf(location.Longitude, 0) || location.Longitude < -180 || location.Longitude > 180 {
		return fmt.Errorf("%w: longitude must be finite and within [-180, 180]", ErrInvalidRequest)
	}
	if len(validTimes) == 0 {
		return fmt.Errorf("%w: at least one valid time is required", ErrInvalidRequest)
	}
	for _, validTime := range validTimes {
		if validTime.IsZero() {
			return fmt.Errorf("%w: valid times must not be zero", ErrInvalidRequest)
		}
	}
	return nil
}

func (client *Client) loadMetadata(ctx context.Context) (datasetMetadata, error) {
	now := client.now().UTC()
	client.metadataMu.Lock()
	if !client.metadata.value.firstValid.IsZero() && now.Before(client.metadata.expiresAt) {
		value := client.metadata.value
		client.metadataMu.Unlock()
		return value, nil
	}
	if client.metaFlight != nil {
		flight := client.metaFlight
		client.metadataMu.Unlock()
		select {
		case <-flight.done:
			return flight.value, flight.err
		case <-ctx.Done():
			return datasetMetadata{}, ctx.Err()
		}
	}
	flight := &metadataFlight{done: make(chan struct{})}
	client.metaFlight = flight
	client.metadataMu.Unlock()

	body, err := client.get(ctx, client.datasetURL+".das")
	if err == nil {
		flight.value, err = parseDAS(body)
	}
	if err != nil {
		err = fmt.Errorf("load GEOS-CF metadata: %w", err)
	}
	flight.err = err

	client.metadataMu.Lock()
	if err == nil {
		client.metadata = metadataCache{value: flight.value, expiresAt: now.Add(client.metadataTTL)}
	}
	client.metaFlight = nil
	close(flight.done)
	client.metadataMu.Unlock()
	return flight.value, err
}

func (client *Client) loadPointSeries(ctx context.Context, metadata datasetMetadata, grid gridCell, window indexWindow) (rawPointSeries, error) {
	key := fmt.Sprintf("%s/%d:%d/%d:%d/%d:%d", metadata.runID, grid.lat0, grid.lat1, grid.lon0, grid.lon1, window.first, window.last)
	now := client.now().UTC()
	client.cacheMu.Lock()
	if entry, ok := client.cache[key]; ok && now.Before(entry.expiresAt) {
		client.clock++
		entry.used = client.clock
		client.cache[key] = entry
		client.cacheMu.Unlock()
		return entry.value, nil
	}
	delete(client.cache, key)
	if existing, ok := client.flights[key]; ok {
		client.cacheMu.Unlock()
		select {
		case <-existing.done:
			return existing.value, existing.err
		case <-ctx.Done():
			return rawPointSeries{}, ctx.Err()
		}
	}
	// Only the goroutine that creates a new unique point-slab flight consumes
	// a slot. Identical callers still coalesce onto the existing flight above.
	// The optional enrichment is fail-open, so a full gate returns immediately
	// instead of accumulating detached waiters behind a slow public service.
	select {
	case client.dataSlots <- struct{}{}:
		defer func() { <-client.dataSlots }()
	default:
		client.cacheMu.Unlock()
		return rawPointSeries{}, ErrBusy
	}
	requestFlight := &flight{done: make(chan struct{})}
	client.flights[key] = requestFlight
	client.cacheMu.Unlock()

	value, err := client.fetchPointSeries(ctx, metadata, grid, window)
	requestFlight.value = value
	if err != nil {
		requestFlight.err = fmt.Errorf("load GEOS-CF point slab: %w", err)
	}

	client.cacheMu.Lock()
	if requestFlight.err == nil {
		client.clock++
		client.cache[key] = dataCacheEntry{value: value, expiresAt: now.Add(client.dataTTL), used: client.clock}
		client.pruneCacheLocked()
	}
	delete(client.flights, key)
	close(requestFlight.done)
	client.cacheMu.Unlock()
	return requestFlight.value, requestFlight.err
}

func (client *Client) pruneCacheLocked() {
	for len(client.cache) > client.cacheEntries {
		var oldestKey string
		oldestUse := uint64(math.MaxUint64)
		for key, entry := range client.cache {
			if entry.used < oldestUse {
				oldestKey, oldestUse = key, entry.used
			}
		}
		delete(client.cache, oldestKey)
	}
}

func (client *Client) get(ctx context.Context, target string) ([]byte, error) {
	requestContext, cancel := context.WithTimeout(ctx, client.requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "text/plain")
	request.Header.Set("User-Agent", "bot_astrosferum/GEOS-CF")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", target, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return nil, fmt.Errorf("request %s: HTTP %s", target, response.Status)
	}
	limited := io.LimitReader(response.Body, client.maxResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", target, err)
	}
	if int64(len(body)) > client.maxResponseBytes {
		return nil, fmt.Errorf("request %s: response exceeds %d bytes", target, client.maxResponseBytes)
	}
	return body, nil
}

func (client *Client) fetchPointSeries(ctx context.Context, metadata datasetMetadata, grid gridCell, window indexWindow) (rawPointSeries, error) {
	lonSlices := [][2]int{{grid.lon0, grid.lon1}}
	if grid.lon1 < grid.lon0 {
		lonSlices = [][2]int{{grid.lon0, grid.lon0}, {grid.lon1, grid.lon1}}
	}
	parts := make([]rawSlab, 0, len(lonSlices))
	for _, longitudeRange := range lonSlices {
		constraint := buildConstraint(window, grid.lat0, grid.lat1, longitudeRange[0], longitudeRange[1])
		body, err := client.get(ctx, client.datasetURL+".ascii?"+constraint)
		if err != nil {
			return rawPointSeries{}, err
		}
		part, err := parseASCII(body, window.count(), grid.latCount(), longitudeRange[1]-longitudeRange[0]+1)
		if err != nil {
			return rawPointSeries{}, err
		}
		parts = append(parts, part)
	}
	series, err := mergeSlabs(parts, window, grid)
	if err != nil {
		return rawPointSeries{}, err
	}
	series.retrievedAt = client.now().UTC()
	return series, nil
}

func buildConstraint(window indexWindow, lat0, lat1, lon0, lon1 int) string {
	constraints := make([]string, 0, len(variableNames))
	for _, variable := range variableNames {
		constraints = append(constraints, fmt.Sprintf("%s[%d:1:%d][%d:1:%d][%d:1:%d]", variable, window.first, window.last, lat0, lat1, lon0, lon1))
	}
	return strings.Join(constraints, ",")
}

func interpolationWindow(metadata datasetMetadata, times []time.Time) (indexWindow, error) {
	positions := make([]float64, len(times))
	for index, requested := range times {
		validTime := requested.UTC()
		if validTime.Before(metadata.firstValid) || validTime.After(metadata.lastValid) {
			return indexWindow{}, fmt.Errorf("%w: %s is not within %s to %s", ErrOutsideCoverage, validTime.Format(time.RFC3339), metadata.firstValid.Format(time.RFC3339), metadata.lastValid.Format(time.RFC3339))
		}
		positions[index] = float64(validTime.Sub(metadata.firstValid)) / float64(metadata.timeStep)
	}
	sort.Float64s(positions)
	first := int(math.Floor(positions[0] + 1e-9))
	last := int(math.Ceil(positions[len(positions)-1] - 1e-9))
	if first < 0 || last >= metadata.timeCount || first > last {
		return indexWindow{}, fmt.Errorf("%w: interpolation indices %d:%d", ErrOutsideCoverage, first, last)
	}
	return indexWindow{first: first, last: last}, nil
}
