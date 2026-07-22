package iconglobal

import (
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

const DefaultBaseURL = "https://opendata.dwd.de/weather/nwp/icon/grib"

var (
	runPattern        = regexp.MustCompile(`_([0-9]{10})_180_T_2M\.grib2\.bz2`)
	cycles            = []string{"00", "06", "12", "18"}
	pressureLevelsHPA = []int{1000, 950, 925, 900, 850, 800, 700, 600, 500, 400, 300, 250, 200, 150, 100, 70, 50, 30}
)

type surfaceField struct {
	directory string
	code      string
}

var surfaceFields = []surfaceField{
	{directory: "t_2m", code: "T_2M"},
	{directory: "td_2m", code: "TD_2M"},
	{directory: "relhum_2m", code: "RELHUM_2M"},
	{directory: "clct", code: "CLCT"},
	{directory: "clcl", code: "CLCL"},
	{directory: "clcm", code: "CLCM"},
	{directory: "clch", code: "CLCH"},
	{directory: "tot_prec", code: "TOT_PREC"},
	{directory: "u_10m", code: "U_10M"},
	{directory: "v_10m", code: "V_10M"},
	{directory: "vmax_10m", code: "VMAX_10M"},
	{directory: "pmsl", code: "PMSL"},
	{directory: "tqv", code: "TQV"},
	{directory: "tqc", code: "TQC"},
	{directory: "tqi", code: "TQI"},
	{directory: "h_ml_lk", code: "H_ML_LK"},
}

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	Workers    int
	Progress   func(string, ...any)
}

func NewClient() *Client {
	return &Client{
		BaseURL: DefaultBaseURL, HTTPClient: &http.Client{Timeout: 2 * time.Minute}, Workers: 4,
		Progress: func(string, ...any) {},
	}
}

func Coverage() model.Coverage {
	return model.Coverage{
		GridType: "unstructured_grid", GridName: "ICON Global native ~13 km",
		MinLat: -90, MaxLat: 90, MinLon: -180, MaxLon: 180, Increment: 0.125,
	}
}

func (client *Client) ProbeLatest(ctx context.Context) (model.RemoteRun, error) {
	client.defaults()
	var candidates []model.RemoteRun
	for _, cycle := range cycles {
		body, err := client.get(ctx, fmt.Sprintf("%s/%s/t_2m/", strings.TrimRight(client.BaseURL, "/"), cycle))
		if err != nil {
			return model.RemoteRun{}, fmt.Errorf("probe ICON Global cycle %s: %w", cycle, err)
		}
		for _, match := range runPattern.FindAllSubmatch(body, -1) {
			baseTime, err := time.Parse("2006010215", string(match[1]))
			if err == nil {
				candidates = append(candidates, model.RemoteRun{ID: string(match[1]), BaseTime: baseTime})
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].BaseTime.After(candidates[j].BaseTime) })
	for _, candidate := range candidates {
		urls := []string{
			client.pressureFieldURL(candidate.ID, 72, 1000, "u"),
			client.pressureFieldURL(candidate.ID, 72, 1000, "fi"),
			client.surfaceFieldURL(candidate.ID, 78, surfaceFields[len(surfaceFields)-1]),
		}
		complete := true
		for _, url := range urls {
			request, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
			if err != nil {
				return model.RemoteRun{}, err
			}
			response, err := client.HTTPClient.Do(request)
			if err != nil || response.StatusCode != http.StatusOK {
				complete = false
			}
			if response != nil {
				_ = response.Body.Close()
			}
		}
		if complete {
			return candidate, nil
		}
	}
	return model.RemoteRun{}, errors.New("no complete ICON Global run through +72 h")
}

func (client *Client) Sync(ctx context.Context, run model.RemoteRun, dataRoot string) (LoadedManifest, error) {
	client.defaults()
	if _, err := time.Parse("2006010215", run.ID); err != nil || run.BaseTime.IsZero() {
		return LoadedManifest{}, errors.New("invalid ICON Global run")
	}
	stateDirectory := filepath.Join(dataRoot, "state")
	if err := os.MkdirAll(stateDirectory, 0o750); err != nil {
		return LoadedManifest{}, err
	}
	lockPath := filepath.Join(stateDirectory, "icon-global-sync.lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return LoadedManifest{}, fmt.Errorf("create ICON Global sync lock: %w", err)
	}
	_, _ = fmt.Fprintf(lock, "run=%s\nstarted=%s\npid=%d\n", run.ID, time.Now().UTC().Format(time.RFC3339), os.Getpid())
	_ = lock.Close()
	defer func() { _ = os.Remove(lockPath) }()

	providerRoot := filepath.Join(dataRoot, "models", "icon-global")
	finalDirectory := filepath.Join(providerRoot, "runs", run.ID)
	if current, err := LoadManifest(filepath.Join(finalDirectory, "manifest.json")); err == nil {
		return current, nil
	}
	incoming := filepath.Join(providerRoot, "incoming", fmt.Sprintf("%s-%d", run.ID, time.Now().UnixNano()))
	pressureDirectory := filepath.Join(incoming, "pressure")
	surfaceDirectory := filepath.Join(incoming, "surface")
	if err := os.MkdirAll(pressureDirectory, 0o750); err != nil {
		return LoadedManifest{}, err
	}
	if err := os.MkdirAll(surfaceDirectory, 0o750); err != nil {
		return LoadedManifest{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(incoming)
		}
	}()

	type task struct {
		kind string
		hour int
	}
	tasks := make([]task, 0, 104)
	for hour := 0; hour <= 72; hour += 3 {
		tasks = append(tasks, task{kind: "pressure", hour: hour})
	}
	for hour := 0; hour <= 78; hour++ {
		tasks = append(tasks, task{kind: "surface", hour: hour})
	}
	type result struct {
		task task
		step StepFile
		err  error
	}
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan task)
	results := make(chan result, len(tasks))
	workers := max(1, client.Workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for item := range jobs {
				var step StepFile
				var err error
				if item.kind == "pressure" {
					step, err = client.downloadPressureStep(workContext, run, item.hour, pressureDirectory)
				} else {
					step, err = client.downloadSurfaceStep(workContext, run, item.hour, surfaceDirectory)
				}
				results <- result{task: item, step: step, err: err}
				if err != nil {
					cancel()
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, item := range tasks {
			select {
			case jobs <- item:
			case <-workContext.Done():
				return
			}
		}
	}()
	go func() { group.Wait(); close(results) }()
	pressure := make([]StepFile, 25)
	surface := make([]StepFile, 79)
	completed := 0
	for item := range results {
		if item.err != nil {
			return LoadedManifest{}, item.err
		}
		if item.task.kind == "pressure" {
			item.step.File = filepath.Join("pressure", filepath.Base(item.step.File))
			pressure[item.task.hour/3] = item.step
		} else {
			item.step.File = filepath.Join("surface", filepath.Base(item.step.File))
			surface[item.task.hour] = item.step
		}
		completed++
		client.Progress("ICON Global %s: completed %s f%03d (%d/%d)", run.ID, item.task.kind, item.task.hour, completed, len(tasks))
	}
	if completed != len(tasks) {
		return LoadedManifest{}, fmt.Errorf("ICON Global sync produced %d of %d bundles", completed, len(tasks))
	}
	manifest := Manifest{
		SchemaVersion: manifestSchemaVersion, Provider: "icon-global", Product: "native pressure/single-level",
		RunID: run.ID, BaseTime: run.BaseTime.UTC(), PublishedAt: time.Now().UTC(), Grid: Coverage(),
		PressureSteps: pressure, SurfaceSteps: surface, Complete: true,
	}
	if err := writeManifest(filepath.Join(incoming, "manifest.json"), manifest); err != nil {
		return LoadedManifest{}, err
	}
	if err := os.MkdirAll(filepath.Dir(finalDirectory), 0o750); err != nil {
		return LoadedManifest{}, err
	}
	if err := os.Rename(incoming, finalDirectory); err != nil {
		return LoadedManifest{}, err
	}
	published = true
	return LoadedManifest{Manifest: manifest, Directory: finalDirectory}, nil
}

func (client *Client) downloadPressureStep(ctx context.Context, run model.RemoteRun, hour int, directory string) (StepFile, error) {
	return client.downloadBundle(ctx, run, hour, directory, len(pressureLevelsHPA)*4, func(file *os.File) error {
		for _, level := range pressureLevelsHPA {
			for _, variable := range []string{"u", "v", "fi", "t"} {
				if err := client.appendField(ctx, file, client.pressureFieldURL(run.ID, hour, level, variable)); err != nil {
					return fmt.Errorf("pressure f%03d %dhPa %s: %w", hour, level, variable, err)
				}
			}
		}
		return nil
	})
}

func (client *Client) downloadSurfaceStep(ctx context.Context, run model.RemoteRun, hour int, directory string) (StepFile, error) {
	messageCount := len(surfaceFields)
	if hour == 0 {
		messageCount-- // VMAX_10M is an interval maximum and has no f000 product.
	}
	return client.downloadBundle(ctx, run, hour, directory, messageCount, func(file *os.File) error {
		for _, field := range surfaceFields {
			if hour == 0 && field.code == "VMAX_10M" {
				continue
			}
			if err := client.appendField(ctx, file, client.surfaceFieldURL(run.ID, hour, field)); err != nil {
				return fmt.Errorf("surface f%03d %s: %w", hour, field.code, err)
			}
		}
		return nil
	})
}

func (client *Client) downloadBundle(ctx context.Context, run model.RemoteRun, hour int, directory string, messages int, appendFields func(*os.File) error) (StepFile, error) {
	name := fmt.Sprintf("f%03d.grib2", hour)
	path := filepath.Join(directory, name)
	file, err := os.OpenFile(path+".part", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return StepFile{}, err
	}
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(path + ".part")
		}
	}()
	if err := appendFields(file); err != nil {
		return StepFile{}, err
	}
	if err := file.Sync(); err != nil {
		return StepFile{}, err
	}
	if err := file.Close(); err != nil {
		return StepFile{}, err
	}
	if err := validateMessageCount(ctx, path+".part", messages); err != nil {
		return StepFile{}, err
	}
	digest, size, err := fileDigest(path + ".part")
	if err != nil {
		return StepFile{}, err
	}
	if err := os.Rename(path+".part", path); err != nil {
		return StepFile{}, err
	}
	failed = false
	return StepFile{ForecastHour: hour, ValidAt: run.BaseTime.Add(time.Duration(hour) * time.Hour), File: path, Bytes: size, SHA256: digest, Messages: messages}, nil
}

func validateMessageCount(ctx context.Context, path string, expected int) error {
	output, err := exec.CommandContext(ctx, "grib_count", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("validate %s: %s", filepath.Base(path), strings.TrimSpace(string(output)))
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || count != expected {
		return fmt.Errorf("%s contains %d messages, expected %d", filepath.Base(path), count, expected)
	}
	return nil
}

func (client *Client) appendField(ctx context.Context, destination *os.File, url string) error {
	start, err := destination.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	var lastError error
	for attempt := 0; attempt < 3; attempt++ {
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
			lastError = err
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
	}
	return lastError
}

func (client *Client) pressureFieldURL(runID string, hour, level int, variable string) string {
	cycle := runID[len(runID)-2:]
	code := strings.ToUpper(variable)
	name := fmt.Sprintf("icon_global_icosahedral_pressure-level_%s_%03d_%d_%s.grib2.bz2", runID, hour, level, code)
	return fmt.Sprintf("%s/%s/%s/%s", strings.TrimRight(client.BaseURL, "/"), cycle, variable, name)
}

func (client *Client) surfaceFieldURL(runID string, hour int, field surfaceField) string {
	cycle := runID[len(runID)-2:]
	name := fmt.Sprintf("icon_global_icosahedral_single-level_%s_%03d_%s.grib2.bz2", runID, hour, field.code)
	return fmt.Sprintf("%s/%s/%s/%s", strings.TrimRight(client.BaseURL, "/"), cycle, field.directory, name)
}

func (client *Client) get(ctx context.Context, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.HTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 8<<20))
}

func (client *Client) defaults() {
	if client.BaseURL == "" {
		client.BaseURL = DefaultBaseURL
	}
	if client.HTTPClient == nil {
		client.HTTPClient = &http.Client{Timeout: 2 * time.Minute}
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
	file, err := os.OpenFile(path+".part", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
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
	return os.Rename(path+".part", path)
}

func publishCurrent(root, runID string) error {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return err
	}
	temporary := filepath.Join(root, fmt.Sprintf(".current-%d", time.Now().UnixNano()))
	if err := os.Symlink(filepath.Join("runs", runID), temporary); err != nil {
		return err
	}
	if err := os.Rename(temporary, filepath.Join(root, "current")); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

// PublishCurrent exposes a Global run only after its pressure, surface, and
// native model-level cloud/PBL sections are all complete.
func PublishCurrent(dataRoot string, loaded LoadedManifest) error {
	if !loaded.HasHourlyCloud() {
		return errors.New("refuse to publish ICON Global run without hourly model-level cloud data")
	}
	return publishCurrent(filepath.Join(dataRoot, "models", "icon-global"), loaded.RunID)
}
