package geoscf

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
)

var constraintPattern = regexp.MustCompile(`([a-z0-9_]+)\[(\d+):1:(\d+)\]\[(\d+):1:(\d+)\]\[(\d+):1:(\d+)\]`)

const testDAS = `Attributes {
    NC_GLOBAL {
        String title "GEOS-CF (Composition Forecast)";
        String history "Sat Jul 25 17:14:22 EDT 2026 : imported";
    }
    lon {
        String grads_size "4";
        Float64 minimum -180;
        Float64 maximum 90;
        Float32 resolution 90;
    }
    lat {
        String grads_size "3";
        Float64 minimum -90;
        Float64 maximum 90;
        Float32 resolution 90;
    }
    time {
        String grads_size "4";
        String grads_min "09:30z25jul2026";
        String grads_step "60mn";
        String minimum "09:30z25jul2026";
        String maximum "12:30z25jul2026";
    }
}`

type fixtureOptions struct {
	omitVariable string
	missingValue string
	blockASCII   <-chan struct{}
}

type fixtureServer struct {
	server        *httptest.Server
	dasRequests   atomic.Int32
	asciiRequests atomic.Int32
	asciiStarted  chan struct{}
	startOnce     sync.Once
}

func newFixtureServer(t *testing.T, options fixtureOptions) *fixtureServer {
	t.Helper()
	fixture := &fixtureServer{asciiStarted: make(chan struct{})}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/dataset.das":
			fixture.dasRequests.Add(1)
			_, _ = writer.Write([]byte(testDAS))
		case "/dataset.ascii":
			fixture.asciiRequests.Add(1)
			fixture.startOnce.Do(func() { close(fixture.asciiStarted) })
			if options.blockASCII != nil {
				select {
				case <-options.blockASCII:
				case <-request.Context().Done():
					return
				}
			}
			query, err := url.QueryUnescape(request.URL.RawQuery)
			if err != nil {
				t.Errorf("decode query: %v", err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = writer.Write([]byte(asciiFixture(t, query, options)))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func asciiFixture(t *testing.T, query string, options fixtureOptions) string {
	t.Helper()
	matches := constraintPattern.FindAllStringSubmatch(query, -1)
	if len(matches) != len(variableNames) {
		t.Errorf("got %d constraints in %q, want %d", len(matches), query, len(variableNames))
	}
	var output strings.Builder
	for _, match := range matches {
		variable := match[1]
		if variable == options.omitVariable {
			continue
		}
		indices := make([]int, 6)
		for index := range indices {
			indices[index], _ = strconv.Atoi(match[index+2])
		}
		t0, t1, lat0, lat1, lon0, lon1 := indices[0], indices[1], indices[2], indices[3], indices[4], indices[5]
		fmt.Fprintf(&output, "%s, [%d][%d][%d]\n", variable, t1-t0+1, lat1-lat0+1, lon1-lon0+1)
		for timeIndex := t0; timeIndex <= t1; timeIndex++ {
			for latitudeIndex := lat0; latitudeIndex <= lat1; latitudeIndex++ {
				fmt.Fprintf(&output, "[%d][%d]", timeIndex-t0, latitudeIndex-lat0)
				for longitudeIndex := lon0; longitudeIndex <= lon1; longitudeIndex++ {
					value := sourceFixtureValue(variable, timeIndex, latitudeIndex, longitudeIndex)
					if variable == options.missingValue {
						value = 1e15
					}
					fmt.Fprintf(&output, ", %.9g", value)
				}
				output.WriteByte('\n')
			}
		}
		output.WriteByte('\n')
	}
	return output.String()
}

func sourceFixtureValue(variable string, timeIndex, latitudeIndex, longitudeIndex int) float64 {
	if variable == "totcol_o3" {
		return 300 + float64(timeIndex)*10 + float64(latitudeIndex) + periodicLongitude(longitudeIndex)*0.1
	}
	variableIndex := 0
	for index, candidate := range variableNames {
		if candidate == variable {
			variableIndex = index
			break
		}
	}
	return float64(variableIndex+1)*0.01 + float64(timeIndex)*0.001 + float64(latitudeIndex)*0.0001 + periodicLongitude(longitudeIndex)*0.00001
}

func periodicLongitude(index int) float64 {
	coordinates := []float64{180, -90, 0, 90}
	return coordinates[index]
}

func fixtureClient(server *fixtureServer, now time.Time) *Client {
	return New(Options{
		DatasetURL:     server.server.URL + "/dataset",
		RequestTimeout: 2 * time.Second,
		MetadataTTL:    time.Hour,
		DataTTL:        time.Hour,
		MaximumRunAge:  72 * time.Hour,
		CacheEntries:   4,
		Now:            func() time.Time { return now },
	})
}

func TestAtmosphericCompositionInterpolatesSpaceAndTime(t *testing.T) {
	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	server := newFixtureServer(t, fixtureOptions{})
	client := fixtureClient(server, now)
	location := forecast.Location{Latitude: -45, Longitude: -135}
	requested := []time.Time{
		time.Date(2026, 7, 25, 11, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC),
	}

	series, err := client.AtmosphericComposition(context.Background(), location, requested)
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Frames) != 2 || !series.Frames[0].ValidTimeUTC.Equal(requested[0]) || !series.Frames[1].ValidTimeUTC.Equal(requested[1]) {
		t.Fatalf("requested order was not retained: %+v", series.Frames)
	}
	// At 10:00 UTC all interpolation weights are 0.5: time 0/1,
	// latitude index 0/1 and longitude index 0/1.
	wantBlackCarbon := 0.01 + 0.0005 + 0.00005 + (180-90)*0.5*0.00001
	assertClose(t, series.Frames[1].AOD550Components.BlackCarbon, wantBlackCarbon, 1e-12)
	wantOzone := 300 + 5 + 0.5 + (180-90)*0.5*0.1
	assertClose(t, series.Frames[1].TotalColumnOzoneDU, wantOzone, 1e-9)
	if !series.Source.RunTimeUTC.Equal(time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC)) || series.Source.RunID != "geos-cf-20260725T0900Z" {
		t.Fatalf("unexpected run identity: %s %s", series.Source.RunID, series.Source.RunTimeUTC)
	}
	if !series.Source.PublicationTimeUTC.Equal(time.Date(2026, 7, 25, 21, 14, 22, 0, time.UTC)) {
		t.Fatalf("unexpected publication time: %s", series.Source.PublicationTimeUTC)
	}
	if series.Source.Freshness.Age != 24*time.Hour || series.Source.Freshness.Stale {
		t.Fatalf("unexpected freshness: %+v", series.Source.Freshness)
	}
	if len(series.Source.Provenance.Variables) != len(variableNames) {
		t.Fatalf("incomplete provenance: %+v", series.Source.Provenance)
	}
	if server.dasRequests.Load() != 1 || server.asciiRequests.Load() != 1 {
		t.Fatalf("unexpected requests: DAS=%d ASCII=%d", server.dasRequests.Load(), server.asciiRequests.Load())
	}

	if _, err = client.AtmosphericComposition(context.Background(), location, requested); err != nil {
		t.Fatal(err)
	}
	if server.dasRequests.Load() != 1 || server.asciiRequests.Load() != 1 {
		t.Fatalf("cache miss: DAS=%d ASCII=%d", server.dasRequests.Load(), server.asciiRequests.Load())
	}
}

func TestAtmosphericCompositionInterpolatesAcrossDateline(t *testing.T) {
	server := newFixtureServer(t, fixtureOptions{})
	client := fixtureClient(server, time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC))
	validTime := time.Date(2026, 7, 25, 9, 30, 0, 0, time.UTC)
	series, err := client.AtmosphericComposition(context.Background(), forecast.Location{Latitude: 0, Longitude: 170}, []time.Time{validTime})
	if err != nil {
		t.Fatal(err)
	}
	// Grid indices 3 (90 E) and 0 (180 E) must form a periodic pair.
	want := 0.01 + 0.0001 + 170*0.00001
	assertClose(t, series.Frames[0].AOD550Components.BlackCarbon, want, 1e-12)
	if server.asciiRequests.Load() != 2 {
		t.Fatalf("dateline fetch used %d requests, want two minimal longitude slabs", server.asciiRequests.Load())
	}
	if len(series.Source.SpatialSupport) != 4 || series.Source.SpatialSupport[1].Longitude != -180 {
		t.Fatalf("unexpected dateline support: %+v", series.Source.SpatialSupport)
	}
}

func TestAtmosphericCompositionHandlesPole(t *testing.T) {
	server := newFixtureServer(t, fixtureOptions{})
	client := fixtureClient(server, time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC))
	series, err := client.AtmosphericComposition(context.Background(), forecast.Location{Latitude: 90, Longitude: 0}, []time.Time{time.Date(2026, 7, 25, 9, 30, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Source.SpatialSupport) != 2 {
		t.Fatalf("pole should use one latitude row: %+v", series.Source.SpatialSupport)
	}
	assertClose(t, series.Frames[0].AOD550Components.BlackCarbon, 0.01+0.0002, 1e-12)
}

func TestAtmosphericCompositionRejectsOutsideAndStaleCoverage(t *testing.T) {
	server := newFixtureServer(t, fixtureOptions{})
	client := fixtureClient(server, time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC))
	_, err := client.AtmosphericComposition(context.Background(), forecast.Location{}, []time.Time{time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC)})
	if !errors.Is(err, ErrOutsideCoverage) {
		t.Fatalf("got %v, want outside-coverage error", err)
	}
	if server.asciiRequests.Load() != 0 {
		t.Fatal("outside coverage should not fetch a data slab")
	}

	stale := fixtureClient(server, time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC))
	stale.maximumRunAge = 48 * time.Hour
	_, err = stale.AtmosphericComposition(context.Background(), forecast.Location{}, []time.Time{time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)})
	if !errors.Is(err, ErrStaleDataset) {
		t.Fatalf("got %v, want stale-dataset error", err)
	}
	if server.asciiRequests.Load() != 0 {
		t.Fatal("stale coverage should not fetch a data slab")
	}
}

func TestAtmosphericCompositionRejectsMissingDataAndVariables(t *testing.T) {
	tests := []struct {
		name      string
		options   fixtureOptions
		wantError error
	}{
		{name: "fill value", options: fixtureOptions{missingValue: "aod550_dust"}, wantError: ErrMissingData},
		{name: "missing variable", options: fixtureOptions{omitVariable: "totcol_o3"}, wantError: ErrMalformedData},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newFixtureServer(t, test.options)
			client := fixtureClient(server, time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC))
			_, err := client.AtmosphericComposition(context.Background(), forecast.Location{}, []time.Time{time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)})
			if !errors.Is(err, test.wantError) {
				t.Fatalf("got %v, want %v", err, test.wantError)
			}
		})
	}
}

func TestAtmosphericCompositionCoalescesConcurrentRequests(t *testing.T) {
	release := make(chan struct{})
	server := newFixtureServer(t, fixtureOptions{blockASCII: release})
	client := fixtureClient(server, time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC))
	location := forecast.Location{Latitude: -45, Longitude: -135}
	validTimes := []time.Time{time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)}

	const callers = 12
	results := make(chan error, callers)
	for range callers {
		go func() {
			_, err := client.AtmosphericComposition(context.Background(), location, validTimes)
			results <- err
		}()
	}
	select {
	case <-server.asciiStarted:
	case <-time.After(time.Second):
		t.Fatal("data request did not start")
	}
	close(release)
	for range callers {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if server.dasRequests.Load() != 1 || server.asciiRequests.Load() != 1 {
		t.Fatalf("requests were not coalesced: DAS=%d ASCII=%d", server.dasRequests.Load(), server.asciiRequests.Load())
	}
}

func TestAtmosphericCompositionRejectsNewUniqueFlightWhenGateIsFull(t *testing.T) {
	release := make(chan struct{})
	server := newFixtureServer(t, fixtureOptions{blockASCII: release})
	client := fixtureClient(server, time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC))
	client.dataSlots = make(chan struct{}, 1)
	validTimes := []time.Time{time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)}

	firstResult := make(chan error, 1)
	go func() {
		_, err := client.AtmosphericComposition(context.Background(), forecast.Location{Latitude: -45, Longitude: -135}, validTimes)
		firstResult <- err
	}()
	select {
	case <-server.asciiStarted:
	case <-time.After(time.Second):
		t.Fatal("first data request did not start")
	}

	_, err := client.AtmosphericComposition(context.Background(), forecast.Location{Latitude: 45, Longitude: 45}, validTimes)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("second unique request error = %v, want %v", err, ErrBusy)
	}
	close(release)
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
}

func TestAtmosphericCompositionValidatesRequest(t *testing.T) {
	client := New(Options{})
	tests := []struct {
		location forecast.Location
		times    []time.Time
	}{
		{location: forecast.Location{Latitude: 91}, times: []time.Time{time.Now()}},
		{location: forecast.Location{Longitude: -181}, times: []time.Time{time.Now()}},
		{times: nil},
		{times: []time.Time{{}}},
	}
	for _, test := range tests {
		_, err := client.AtmosphericComposition(context.Background(), test.location, test.times)
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("got %v, want invalid-request error", err)
		}
	}
}

func TestParseDASRejectsInconsistentGrid(t *testing.T) {
	invalid := strings.Replace(testDAS, `Float64 maximum 90;`, `Float64 maximum 80;`, 1)
	_, err := parseDAS([]byte(invalid))
	if !errors.Is(err, ErrMalformedData) {
		t.Fatalf("got %v, want malformed-data error", err)
	}
}

func assertClose(t *testing.T, got, want, tolerance float64) {
	t.Helper()
	if math.Abs(got-want) > tolerance {
		t.Fatalf("got %.12g, want %.12g (tolerance %.3g)", got, want, tolerance)
	}
}
