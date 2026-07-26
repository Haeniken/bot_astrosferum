package iconeu

import (
	"bufio"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"bot_astrosferum/internal/model"
)

const (
	DefaultBaseURL = "https://opendata.dwd.de/weather/nwp/icon-eu/grib"
	ProductName    = "europe_regular-lat-lon_pressure-level"
)

var (
	DefaultPressureLevelsHPA = []int{1000, 950, 925, 900, 875, 850, 825, 800, 775, 700, 600, 500, 400, 300, 250, 200, 150, 100, 70, 50}
	fullCycles               = []string{"00", "06", "12", "18"}
	runPattern               = regexp.MustCompile(`_([0-9]{10})_120_T_2M\.grib2\.bz2`)
)

var pressureLevelDownloadVariables = []string{"u", "v", "fi", "t"}

type CommandRunner interface {
	CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	Runner     CommandRunner
	Workers    int
	Progress   func(string, ...any)
}

func NewClient() *Client {
	return &Client{
		BaseURL:    DefaultBaseURL,
		HTTPClient: &http.Client{Timeout: 2 * time.Minute},
		Runner:     execRunner{},
		Workers:    4,
		Progress:   func(string, ...any) {},
	}
}

func Coverage() model.Coverage {
	return model.Coverage{
		GridType: "regular_ll", GridName: "ICON-EU 0.0625°",
		MinLat: 29.5, MaxLat: 70.5, MinLon: -23.5, MaxLon: 62.5, Increment: 0.0625,
	}
}

func (client *Client) ProbeLatest(ctx context.Context) (model.RemoteRun, error) {
	client.defaults()
	var candidates []model.RemoteRun
	for _, cycle := range fullCycles {
		body, err := client.get(ctx, fmt.Sprintf("%s/%s/t_2m/", strings.TrimRight(client.BaseURL, "/"), cycle))
		if err != nil {
			return model.RemoteRun{}, fmt.Errorf("probe ICON-EU cycle %s: %w", cycle, err)
		}
		matches := runPattern.FindAllSubmatch(body, -1)
		for _, match := range matches {
			id := string(match[1])
			baseTime, err := time.Parse("2006010215", id)
			if err == nil {
				candidates = append(candidates, model.RemoteRun{ID: id, BaseTime: baseTime})
			}
		}
	}
	if len(candidates) == 0 {
		return model.RemoteRun{}, errors.New("no complete ICON-EU cycle discovered")
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].BaseTime.After(candidates[j].BaseTime) })
	for _, candidate := range candidates {
		available := true
		for _, variable := range pressureLevelDownloadVariables {
			url := client.fieldURL(candidate.ID, 72, 1000, variable)
			request, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
			if err != nil {
				return model.RemoteRun{}, err
			}
			response, err := client.HTTPClient.Do(request)
			if err != nil || response.StatusCode != http.StatusOK {
				available = false
			}
			if response != nil {
				_ = response.Body.Close()
			}
		}
		if available {
			return candidate, nil
		}
	}
	return model.RemoteRun{}, errors.New("latest discovered ICON-EU cycles are not complete through +72 h")
}

func (client *Client) Sync(ctx context.Context, run model.RemoteRun, dataRoot string) (LoadedManifest, error) {
	client.defaults()
	if _, err := time.Parse("2006010215", run.ID); err != nil || run.BaseTime.IsZero() {
		return LoadedManifest{}, errors.New("invalid ICON-EU run")
	}
	stateDirectory := filepath.Join(dataRoot, "state")
	if err := os.MkdirAll(stateDirectory, 0o750); err != nil {
		return LoadedManifest{}, fmt.Errorf("create state directory: %w", err)
	}
	lockPath := filepath.Join(stateDirectory, "icon-eu-sync.lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return LoadedManifest{}, fmt.Errorf("ICON-EU sync lock exists: %s", lockPath)
		}
		return LoadedManifest{}, fmt.Errorf("create ICON-EU sync lock: %w", err)
	}
	_, _ = fmt.Fprintf(lock, "run=%s\nstarted=%s\npid=%d\n", run.ID, time.Now().UTC().Format(time.RFC3339), os.Getpid())
	_ = lock.Close()
	defer func() { _ = os.Remove(lockPath) }()

	providerRoot := filepath.Join(dataRoot, "models", "icon-eu")
	finalDirectory := filepath.Join(providerRoot, "runs", run.ID)
	if current, err := LoadManifest(filepath.Join(finalDirectory, "manifest.json")); err == nil {
		return current, nil
	}
	incomingDirectory := filepath.Join(providerRoot, "incoming", fmt.Sprintf("%s-%d", run.ID, time.Now().UnixNano()))
	stepsDirectory := filepath.Join(incomingDirectory, "steps-v4")
	if err := os.MkdirAll(stepsDirectory, 0o750); err != nil {
		return LoadedManifest{}, fmt.Errorf("create incoming run: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(incomingDirectory)
		}
	}()

	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	results := make(chan StepFile, 25)
	errorsChannel := make(chan error, 1)
	workers := client.Workers
	if workers < 1 {
		workers = 1
	}
	var waitGroup sync.WaitGroup
	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for forecastHour := range jobs {
				step, err := client.downloadStep(workContext, run, forecastHour, stepsDirectory)
				if err != nil {
					select {
					case errorsChannel <- err:
					default:
					}
					cancel()
					return
				}
				results <- step
			}
		}()
	}
	go func() {
		defer close(jobs)
		for forecastHour := 0; forecastHour <= 72; forecastHour += 3 {
			select {
			case jobs <- forecastHour:
			case <-workContext.Done():
				return
			}
		}
	}()
	go func() {
		waitGroup.Wait()
		close(results)
	}()

	steps := make([]StepFile, 0, 25)
	for step := range results {
		steps = append(steps, step)
		client.Progress("ICON-EU %s: completed f%03d (%d/25)", run.ID, step.ForecastHour, len(steps))
	}
	select {
	case syncError := <-errorsChannel:
		return LoadedManifest{}, syncError
	default:
	}
	if len(steps) != 25 {
		return LoadedManifest{}, fmt.Errorf("ICON-EU sync produced %d of 25 steps", len(steps))
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].ForecastHour < steps[j].ForecastHour })
	levels := make([]float64, len(DefaultPressureLevelsHPA))
	for index, level := range DefaultPressureLevelsHPA {
		levels[index] = float64(level)
	}
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion, Provider: "icon-eu", Product: ProductName,
		RunID: run.ID, BaseTime: run.BaseTime.UTC(), PublishedAt: time.Now().UTC(),
		Grid: Coverage(), PressureLevelsHPA: levels, Variables: []string{"u", "v", "z", "t"}, Steps: steps, Complete: true,
	}
	if err := writeManifest(filepath.Join(incomingDirectory, "manifest.json"), manifest); err != nil {
		return LoadedManifest{}, err
	}
	if err := os.MkdirAll(filepath.Dir(finalDirectory), 0o750); err != nil {
		return LoadedManifest{}, fmt.Errorf("create run directory: %w", err)
	}
	if err := os.Rename(incomingDirectory, finalDirectory); err != nil {
		return LoadedManifest{}, fmt.Errorf("publish ICON-EU run: %w", err)
	}
	published = true
	return LoadedManifest{Manifest: manifest, Directory: finalDirectory}, nil
}

func (client *Client) downloadStep(ctx context.Context, run model.RemoteRun, forecastHour int, directory string) (StepFile, error) {
	name := fmt.Sprintf("f%03d.grib2", forecastHour)
	destination := filepath.Join(directory, name)
	temporary := destination + ".part"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return StepFile{}, fmt.Errorf("create %s: %w", name, err)
	}
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(temporary)
		}
	}()
	for _, level := range DefaultPressureLevelsHPA {
		for _, variable := range pressureLevelDownloadVariables {
			if err := client.appendField(ctx, file, client.fieldURL(run.ID, forecastHour, level, variable)); err != nil {
				return StepFile{}, fmt.Errorf("download f%03d %dhPa %s: %w", forecastHour, level, variable, err)
			}
		}
	}
	if err := file.Sync(); err != nil {
		return StepFile{}, fmt.Errorf("sync %s: %w", name, err)
	}
	if err := file.Close(); err != nil {
		return StepFile{}, fmt.Errorf("close %s: %w", name, err)
	}
	if err := client.validateBundle(ctx, temporary); err != nil {
		return StepFile{}, err
	}
	digest, size, err := fileDigest(temporary)
	if err != nil {
		return StepFile{}, err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return StepFile{}, fmt.Errorf("publish %s: %w", name, err)
	}
	failed = false
	return StepFile{
		ForecastHour: forecastHour, ValidAt: run.BaseTime.Add(time.Duration(forecastHour) * time.Hour),
		File: filepath.Join("steps-v4", name), Bytes: size, SHA256: digest,
		Messages: len(DefaultPressureLevelsHPA) * len(pressureLevelDownloadVariables),
	}, nil
}

func (client *Client) appendField(ctx context.Context, destination *os.File, url string) error {
	start, err := destination.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	var lastError error
	for attempt := 1; attempt <= 3; attempt++ {
		if err := destination.Truncate(start); err != nil {
			return err
		}
		if _, err := destination.Seek(start, io.SeekStart); err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		response, err := client.HTTPClient.Do(request)
		if err != nil {
			lastError = errors.New("HTTP transport failed")
			continue
		}
		if response.StatusCode != http.StatusOK {
			lastError = fmt.Errorf("HTTP %d", response.StatusCode)
			_ = response.Body.Close()
			continue
		}
		_, copyError := io.Copy(destination, bzip2.NewReader(response.Body))
		closeError := response.Body.Close()
		if copyError == nil && closeError == nil {
			return nil
		}
		lastError = errors.New("decompression or copy failed")
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return lastError
}

func (client *Client) validateBundle(ctx context.Context, path string) error {
	countOutput, err := client.Runner.CombinedOutput(ctx, "grib_count", path)
	if err != nil {
		return fmt.Errorf("grib_count %s failed: %s", filepath.Base(path), strings.TrimSpace(string(countOutput)))
	}
	expectedCount := len(DefaultPressureLevelsHPA) * len(pressureLevelDownloadVariables)
	count, err := strconv.Atoi(strings.TrimSpace(string(countOutput)))
	if err != nil || count != expectedCount {
		return fmt.Errorf("%s contains %d messages, expected %d", filepath.Base(path), count, expectedCount)
	}
	metadata, err := client.Runner.CombinedOutput(ctx, "grib_get", "-p", "shortName,typeOfLevel,level", path)
	if err != nil {
		return fmt.Errorf("grib_get %s failed: %s", filepath.Base(path), strings.TrimSpace(string(metadata)))
	}
	seen := make(map[string]bool, expectedCount)
	scanner := bufio.NewScanner(strings.NewReader(string(metadata)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 || (fields[0] != "u" && fields[0] != "v" && fields[0] != "z" && fields[0] != "t") || fields[1] != "isobaricInhPa" {
			return fmt.Errorf("unexpected metadata in %s: %q", filepath.Base(path), scanner.Text())
		}
		seen[fields[0]+":"+fields[2]] = true
	}
	for _, level := range DefaultPressureLevelsHPA {
		for _, variable := range []string{"u", "v", "z", "t"} {
			if !seen[fmt.Sprintf("%s:%d", variable, level)] {
				return fmt.Errorf("%s is missing %s at %d hPa", filepath.Base(path), variable, level)
			}
		}
	}
	return nil
}

func (client *Client) fieldURL(runID string, forecastHour, level int, variable string) string {
	cycle := runID[len(runID)-2:]
	code := strings.ToUpper(variable)
	name := fmt.Sprintf("icon-eu_europe_regular-lat-lon_pressure-level_%s_%03d_%d_%s.grib2.bz2", runID, forecastHour, level, code)
	return fmt.Sprintf("%s/%s/%s/%s", strings.TrimRight(client.BaseURL, "/"), cycle, variable, name)
}

func (client *Client) get(ctx context.Context, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.HTTPClient.Do(request)
	if err != nil {
		return nil, errors.New("HTTP transport failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 16<<20))
}

func (client *Client) defaults() {
	if client.BaseURL == "" {
		client.BaseURL = DefaultBaseURL
	}
	if client.HTTPClient == nil {
		client.HTTPClient = &http.Client{Timeout: 2 * time.Minute}
	}
	if client.Runner == nil {
		client.Runner = execRunner{}
	}
	if client.Progress == nil {
		client.Progress = func(string, ...any) {}
	}
}

func fileDigest(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func writeManifest(path string, manifest Manifest) error {
	temporary := path + ".part"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func publishCurrent(providerRoot, runID string) error {
	temporary := filepath.Join(providerRoot, fmt.Sprintf(".current-%d", time.Now().UnixNano()))
	if err := os.Symlink(filepath.Join("runs", runID), temporary); err != nil {
		return fmt.Errorf("create current ICON-EU link: %w", err)
	}
	if err := os.Rename(temporary, filepath.Join(providerRoot, "current")); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("publish current ICON-EU link: %w", err)
	}
	return nil
}

// PublishCurrent exposes an ICON-EU run only after every dataset required by
// the seven-chart forecast has been published. While a new run is downloading,
// readers therefore continue using the previous complete run.
func PublishCurrent(dataRoot string, loaded LoadedManifest) error {
	if !loaded.HasWindThermodynamics() || !loaded.HasHourlySurface() || !loaded.HasHourlyCloud() {
		return errors.New("refuse to publish incomplete ICON-EU run")
	}
	return publishCurrent(filepath.Join(dataRoot, "models", "icon-eu"), loaded.RunID)
}
