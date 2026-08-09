package iconeu

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"bot_astrosferum/internal/model"
)

var domeTestForecastHour = regexp.MustCompile(`f([0-9]{3})\.grib2`)

type domeTestTransport struct {
	mu                sync.Mutex
	counts            map[string]int
	failURL           string
	failAttempts      int
	compressedPayload []byte
}

func newDomeTestTransport(t *testing.T) *domeTestTransport {
	t.Helper()
	payload, err := hex.DecodeString("425a6839314159265359774bb01400000000800040200021184682ee48a70a120ee9760280")
	if err != nil {
		t.Fatal(err)
	}
	return &domeTestTransport{counts: make(map[string]int), compressedPayload: payload}
}

func (transport *domeTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.mu.Lock()
	transport.counts[request.URL.String()]++
	count := transport.counts[request.URL.String()]
	fail := request.URL.String() == transport.failURL && count <= transport.failAttempts
	transport.mu.Unlock()
	if fail {
		return nil, errors.New("injected network failure")
	}
	return &http.Response{
		StatusCode: http.StatusOK, Header: make(http.Header), Request: request,
		Body: io.NopCloser(bytes.NewReader(transport.compressedPayload)),
	}, nil
}

func (transport *domeTestTransport) count(url string) int {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.counts[url]
}

func (transport *domeTestTransport) countContaining(fragment string) int {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	total := 0
	for url, count := range transport.counts {
		if strings.Contains(url, fragment) {
			total += count
		}
	}
	return total
}

type domeTestRunner struct {
	baseTime          time.Time
	surfaceIdentities map[string]domeMessage
	mismatchContains  string
	mutateContains    string
	mutate            func() error
	mutateOnce        sync.Once
}

func (runner *domeTestRunner) CombinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	path := args[len(args)-1]
	inventory, err := runner.inventory(path)
	if err != nil {
		return []byte(err.Error()), err
	}
	if name == "grib_count" {
		return []byte(strconv.Itoa(len(inventory))), nil
	}
	if name != "grib_get" {
		return nil, fmt.Errorf("unexpected command %s", name)
	}
	if runner.mutate != nil && strings.Contains(path, runner.mutateContains) {
		var mutateErr error
		runner.mutateOnce.Do(func() { mutateErr = runner.mutate() })
		if mutateErr != nil {
			return []byte(mutateErr.Error()), mutateErr
		}
	}
	messages := make([]domeMessage, 0, len(inventory))
	for message := range inventory {
		messages = append(messages, message)
	}
	sort.Slice(messages, func(i, j int) bool {
		return fmt.Sprintf("%+v", messages[i]) < fmt.Sprintf("%+v", messages[j])
	})
	if runner.mismatchContains != "" && strings.Contains(path, runner.mismatchContains) && len(messages) > 0 {
		messages[0].ShortName = "wrong"
	}
	var output strings.Builder
	for _, message := range messages {
		_, _ = fmt.Fprintf(&output, "%s %s %s %s %d %d\n",
			message.ShortName, message.TypeOfLevel, message.Level, message.StepRange,
			message.ValidityDate, message.ValidityTime)
	}
	return []byte(output.String()), nil
}

func (runner *domeTestRunner) inventory(path string) (domeInventory, error) {
	hour, err := domeTestHour(path)
	if err != nil && !strings.Contains(path, "geometry.grib2") {
		return nil, err
	}
	switch {
	case strings.Contains(path, "cloud-hourly-v5-full-hhl") && strings.Contains(path, "geometry.grib2"):
		return domeGeometryInventory(runner.baseTime), nil
	case strings.Contains(path, "cloud-hourly-v5-full-hhl"):
		return domeBaseCloudInventory(runner.baseTime, hour), nil
	case strings.Contains(path, "surface-hourly-v17"):
		return domeSurfaceInventory(runner.baseTime, hour, runner.surfaceIdentities), nil
	case strings.Contains(path, string(filepath.Separator)+"model"+string(filepath.Separator)):
		inventory := domeTargetModelInventory(runner.baseTime, hour)
		if hour <= 78 {
			for message := range domeBaseCloudInventory(runner.baseTime, hour) {
				delete(inventory, message)
			}
		}
		return inventory, nil
	case strings.Contains(path, string(filepath.Separator)+"surface"+string(filepath.Separator)):
		return domeSurfaceInventory(runner.baseTime, hour, runner.surfaceIdentities), nil
	default:
		return nil, fmt.Errorf("no fake inventory for %s", path)
	}
}

func domeTestHour(path string) (int, error) {
	match := domeTestForecastHour.FindStringSubmatch(path)
	if len(match) != 2 {
		return 0, fmt.Errorf("forecast hour missing from %s", path)
	}
	return strconv.Atoi(match[1])
}

func TestParseDomeInventoryCanonicalizesECCodesZeroMinuteStep(t *testing.T) {
	metadata := []byte("HHL generalVertical 16 0m 20260728 1200\n")
	inventory, err := parseDomeInventory(metadata)
	if err != nil {
		t.Fatal(err)
	}
	want := newDomeMessage("HHL", "generalVertical", 16, 0, time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC))
	if _, ok := inventory[want]; !ok {
		t.Fatalf("zero-minute HHL step was not canonicalized: %+v", inventory)
	}
	if got := canonicalDomeStepRange("15m"); got != "15m" {
		t.Fatalf("non-zero minute step = %q, want exact source value", got)
	}
}

func TestDomeSurfaceStepRangePreservesAccumulationWindows(t *testing.T) {
	tests := []struct {
		shortName string
		hour      int
		want      string
	}{
		{shortName: "tp", hour: 3, want: "0-3"},
		{shortName: "VMAX_10M", hour: 1, want: "0-1"},
		{shortName: "VMAX_10M", hour: 2, want: "1-2"},
		{shortName: "VMAX_10M", hour: 78, want: "77-78"},
		{shortName: "VMAX_10M", hour: 81, want: "80-81"},
		{shortName: "VMAX_10M", hour: 84, want: "83-84"},
		{shortName: "VMAX_10M", hour: 0, want: "0"},
		{shortName: "10u", hour: 3, want: "3"},
	}
	for _, test := range tests {
		if got := domeSurfaceStepRange(test.shortName, test.hour); got != test.want {
			t.Errorf("%s f%03d step range = %q, want %q", test.shortName, test.hour, got, test.want)
		}
	}
}

func TestSyncDomeResumesWithoutRedownloadAndPublishesAtomically(t *testing.T) {
	root := t.TempDir()
	loaded, runner := writeDomeTestBase(t, root)
	transport := newDomeTestTransport(t)
	client := domeTestClient(transport, runner)
	client.Workers = 1
	transport.failURL = client.modelLevelFieldURL(loaded.RunID, 1, 1, "p", "P")
	transport.failAttempts = 3
	budget := newDomeTestBudget(t, root, model.HardDiskProjectCapBytes)
	oldTarget := installOldDomeCurrent(t, root)

	if _, err := client.SyncDome(context.Background(), root, loaded, budget); err == nil {
		t.Fatal("injected network failure did not stop acquisition")
	}
	assertDomeCurrentTarget(t, root, oldTarget)
	staging := filepath.Join(root, "models", "icon-eu", "dome-staging", loaded.RunID, DomeInputContractDigest())
	if _, err := os.Stat(filepath.Join(staging, "model", "f000.grib2")); err != nil {
		t.Fatalf("verified f000 did not survive failure: %v", err)
	}
	if _, err := os.Stat(domeCheckpointPath(filepath.Join(staging, "model", "f000.grib2"))); err != nil {
		t.Fatalf("verified f000 checkpoint did not survive failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "model", "f001.grib2.part")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed object's .part survived: %v", err)
	}
	f000Requests := transport.countContaining("_000_")
	if f000Requests != domeModelMessagesPerStep-cloudStepMessageCount() {
		t.Fatalf("f000 extension requests = %d, want %d", f000Requests, domeModelMessagesPerStep-cloudStepMessageCount())
	}

	ready, err := client.SyncDome(context.Background(), root, loaded, budget)
	if err != nil {
		t.Fatal(err)
	}
	if transport.countContaining("_000_") != f000Requests {
		t.Fatal("completed f000 was downloaded again during resume")
	}
	if got := len(ready.ModelSteps); got != 81 {
		t.Fatalf("model steps = %d, want native 81 (not 72 or 73)", got)
	}
	if ready.ModelSteps[79].ForecastHour != 81 || ready.ModelSteps[80].ForecastHour != 84 || len(ready.SurfaceExtensionSteps) != 2 ||
		ready.SurfaceExtensionSteps[0].ForecastHour != 81 || ready.SurfaceExtensionSteps[1].ForecastHour != 84 {
		t.Fatalf("native brackets are incomplete: %+v / %+v", ready.ModelSteps[79:], ready.SurfaceExtensionSteps)
	}
	if ready.GridProfile != model.StorageProfileDense {
		t.Fatalf("grid profile = %q, want dense", ready.GridProfile)
	}
	wantTarget := filepath.Join("dome-runs", loaded.RunID, DomeInputContractDigest())
	assertDomeCurrentTarget(t, root, wantTarget)
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging still exists after atomic rename: %v", err)
	}
	if got := transport.count(client.hhlFieldURL(loaded.RunID, 1)); got != 0 {
		t.Fatalf("HHL was redownloaded %d times", got)
	}
	if got := transport.count(client.modelLevelFieldURL(loaded.RunID, 0, 25, "clc", "CLC")); got != 0 {
		t.Fatalf("base CLC message was redownloaded %d times", got)
	}
	if got := transport.count(client.modelLevelFieldURL(loaded.RunID, 0, 58, "tke", "TKE")); got != 0 {
		t.Fatalf("base TKE message was redownloaded %d times", got)
	}
	if got := transport.count(client.modelLevelFieldURL(loaded.RunID, 0, 25, "qv", "QV")); got != 1 {
		t.Fatalf("missing QV extension message requests = %d, want 1", got)
	}
	if got := transport.count(client.surfaceFieldURL(loaded.RunID, 81, surfaceFields[0])); got != 1 {
		t.Fatalf("surface f081 was requested %d times, want 1", got)
	}
}

func TestSyncDomeRejectsMetadataMismatchWithoutPublishingPartial(t *testing.T) {
	root := t.TempDir()
	loaded, runner := writeDomeTestBase(t, root)
	runner.mismatchContains = filepath.Join("model", "f000.grib2.part")
	transport := newDomeTestTransport(t)
	client := domeTestClient(transport, runner)
	client.Workers = 1
	budget := newDomeTestBudget(t, root, model.HardDiskProjectCapBytes)
	oldTarget := installOldDomeCurrent(t, root)

	if _, err := client.SyncDome(context.Background(), root, loaded, budget); err == nil || !strings.Contains(err.Error(), "missing canonical message") {
		t.Fatalf("metadata mismatch error = %v", err)
	}
	assertDomeCurrentTarget(t, root, oldTarget)
	staging := filepath.Join(root, "models", "icon-eu", "dome-staging", loaded.RunID, DomeInputContractDigest())
	if _, err := os.Stat(filepath.Join(staging, "model", "f000.grib2")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid final object exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "model", "f000.grib2.part")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid partial object exists: %v", err)
	}
}

func TestSyncDomeRefusesChangedBaseManifest(t *testing.T) {
	root := t.TempDir()
	loaded, runner := writeDomeTestBase(t, root)
	prepopulateDomeStaging(t, root, loaded)
	manifestPath := filepath.Join(loaded.Directory, "manifest.json")
	runner.mutateContains = filepath.Join("surface", "f084.grib2")
	runner.mutate = func() error {
		file, err := os.OpenFile(manifestPath, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		if _, err := file.WriteString("\n"); err != nil {
			_ = file.Close()
			return err
		}
		return file.Close()
	}
	client := domeTestClient(newDomeTestTransport(t), runner)
	budget := newDomeTestBudget(t, root, model.HardDiskProjectCapBytes)
	oldTarget := installOldDomeCurrent(t, root)

	if _, err := client.SyncDome(context.Background(), root, loaded, budget); err == nil || !strings.Contains(err.Error(), "base manifest changed") {
		t.Fatalf("base mutation error = %v", err)
	}
	assertDomeCurrentTarget(t, root, oldTarget)
	if _, err := os.Stat(filepath.Join(root, "models", "icon-eu", "dome-staging", loaded.RunID, DomeInputContractDigest(), "model", "f000.grib2")); err != nil {
		t.Fatalf("verified staging was removed after base mutation: %v", err)
	}
}

func TestSyncDomeRejectsChangedVerifiedObjectByCheckpoint(t *testing.T) {
	root := t.TempDir()
	loaded, runner := writeDomeTestBase(t, root)
	prepopulateDomeStaging(t, root, loaded)
	staging := filepath.Join(root, "models", "icon-eu", "dome-staging", loaded.RunID, DomeInputContractDigest())
	file, err := os.OpenFile(filepath.Join(staging, "model", "f000.grib2"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("tampered")
	_ = file.Close()
	transport := newDomeTestTransport(t)
	client := domeTestClient(transport, runner)
	budget := newDomeTestBudget(t, root, model.HardDiskProjectCapBytes)
	oldTarget := installOldDomeCurrent(t, root)

	if _, err := client.SyncDome(context.Background(), root, loaded, budget); err == nil || !strings.Contains(err.Error(), "checkpoint") {
		t.Fatalf("changed verified object error = %v", err)
	}
	assertDomeCurrentTarget(t, root, oldTarget)
	if got := transport.countContaining(client.BaseURL); got != 0 {
		t.Fatalf("changed verified object was redownloaded with %d HTTP requests", got)
	}
}

func TestSyncDomeCapRejectionLeavesCurrentAndDoesNotCreateStaging(t *testing.T) {
	root := t.TempDir()
	loaded, runner := writeDomeTestBase(t, root)
	transport := newDomeTestTransport(t)
	client := domeTestClient(transport, runner)
	budget := newDomeTestBudget(t, root, 1)
	oldTarget := installOldDomeCurrent(t, root)

	if _, err := client.SyncDome(context.Background(), root, loaded, budget); !errors.Is(err, model.ErrDiskProjectCap) {
		t.Fatalf("cap error = %v, want ErrDiskProjectCap", err)
	}
	assertDomeCurrentTarget(t, root, oldTarget)
	if got := transport.countContaining(client.BaseURL); got != 0 {
		t.Fatalf("cap rejection made %d HTTP requests", got)
	}
	staging := filepath.Join(root, "models", "icon-eu", "dome-staging", loaded.RunID, DomeInputContractDigest())
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cap rejection created staging: %v", err)
	}
}

func TestDomeProjectionDiffersOnlyByResultCacheAllowance(t *testing.T) {
	tasks := []domeSyncTask{
		{destination: filepath.Join(t.TempDir(), "missing-a"), expected: make(domeInventory, 555)},
		{destination: filepath.Join(t.TempDir(), "missing-b"), expected: make(domeInventory, 742)},
	}
	// Maps cannot contain a requested cardinality without keys; populate exact
	// distinct synthetic metadata to exercise the projection arithmetic.
	for taskIndex := range tasks {
		count := 555
		if taskIndex == 1 {
			count = 742
		}
		for index := range count {
			tasks[taskIndex].expected[domeMessage{ShortName: strconv.Itoa(index)}] = struct{}{}
		}
	}
	dense, sparse, err := domeSyncProjections(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if dense.Bytes-sparse.Bytes != domeDenseResultCacheBytes-domeSparseResultCacheBytes ||
		dense.Inodes-sparse.Inodes != domeDenseResultCacheFiles-domeSparseResultCacheFiles {
		t.Fatalf("profile projections differ outside the documented cache allowance: dense=%+v sparse=%+v", dense, sparse)
	}
	capBetweenProfiles := sparse.Bytes + (dense.Bytes-sparse.Bytes)/2
	if got := model.SelectStorageProfile(dense.Bytes, sparse.Bytes, capBetweenProfiles); got != model.StorageProfileSparse {
		t.Fatalf("profile at cap %d = %q, want sparse", capBetweenProfiles, got)
	}
}

func domeTestClient(transport *domeTestTransport, runner *domeTestRunner) *Client {
	return &Client{
		BaseURL: "https://dwd.invalid/icon-eu", HTTPClient: &http.Client{Transport: transport},
		Runner: runner, Workers: 4, Progress: func(string, ...any) {},
	}
}

func writeDomeTestBase(t *testing.T, root string) (LoadedManifest, *domeTestRunner) {
	t.Helper()
	baseTime := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	runID := baseTime.Format("2006010215")
	directory := filepath.Join(root, "models", "icon-eu", "runs", runID)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	now := baseTime.Add(5 * time.Hour)
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion, Provider: "icon-eu", Product: ProductName,
		RunID: runID, BaseTime: baseTime, PublishedAt: now, Grid: Coverage(),
		PressureLevelsHPA: []float64{1000, 500}, Variables: []string{"u", "v", "z", "t"},
		SurfaceVariables: make([]string, len(surfaceFields)), SurfacePublishedAt: &now,
		SurfaceSteps:     make([]SurfaceStepFile, HourlySurfaceStepCount),
		CloudVariables:   []string{"ccl", "pres", "qc", "qi", "t", "u", "v", "tke", "HHL"},
		CloudModelLevels: append([]int(nil), DefaultCloudModelLevels...), CloudPublishedAt: &now,
		CloudSteps: make([]SurfaceStepFile, HourlySurfaceStepCount), Complete: true,
	}
	for index, field := range surfaceFields {
		manifest.SurfaceVariables[index] = field.ShortName
	}
	for _, hour := range []int{0, 3} {
		relative := filepath.Join("steps-v4", fmt.Sprintf("f%03d.grib2", hour))
		bundle := writeDomeTestFile(t, directory, relative, fmt.Sprintf("pressure-%d", hour))
		manifest.Steps = append(manifest.Steps, StepFile{
			ForecastHour: hour, ValidAt: baseTime.Add(time.Duration(hour) * time.Hour), File: relative,
			Bytes: bundle.Bytes, SHA256: bundle.SHA256, Messages: len(DefaultPressureLevelsHPA) * 4,
		})
	}
	geometryRelative := filepath.Join("cloud-hourly-v5-full-hhl", "geometry.grib2")
	geometry := writeDomeTestFile(t, directory, geometryRelative, "full-hhl")
	geometry.Messages = domeHalfLevelCount
	manifest.CloudGeometry = &geometry
	for hour := range HourlySurfaceStepCount {
		cloudRelative := filepath.Join("cloud-hourly-v5-full-hhl", fmt.Sprintf("f%03d.grib2", hour))
		cloud := writeDomeTestFile(t, directory, cloudRelative, fmt.Sprintf("cloud-%03d", hour))
		manifest.CloudSteps[hour] = SurfaceStepFile{
			ForecastHour: hour, ValidAt: baseTime.Add(time.Duration(hour) * time.Hour), File: cloudRelative,
			Bytes: cloud.Bytes, SHA256: cloud.SHA256, Messages: cloudStepMessageCount(),
		}
		surfaceRelative := filepath.Join("surface-hourly-v17", fmt.Sprintf("f%03d.grib2", hour))
		surface := writeDomeTestFile(t, directory, surfaceRelative, fmt.Sprintf("surface-%03d", hour))
		manifest.SurfaceSteps[hour] = SurfaceStepFile{
			ForecastHour: hour, ValidAt: baseTime.Add(time.Duration(hour) * time.Hour), File: surfaceRelative,
			Bytes: surface.Bytes, SHA256: surface.SHA256, Messages: SurfaceBundleSchemaVersion,
		}
	}
	if err := writeManifest(filepath.Join(directory, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadManifest(filepath.Join(directory, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	identities := domeTestSurfaceIdentities()
	return loaded, &domeTestRunner{baseTime: baseTime, surfaceIdentities: identities}
}

func writeDomeTestFile(t *testing.T, root, relative, content string) BundleFile {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	digest, size, err := fileDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	return BundleFile{File: relative, Bytes: size, SHA256: digest, Messages: 1}
}

func domeTestSurfaceIdentities() map[string]domeMessage {
	identities := make(map[string]domeMessage, len(surfaceFields))
	for _, field := range surfaceFields {
		levelType, level := "surface", "0"
		switch field.ShortName {
		case "2t", "2d", "2r":
			levelType, level = "heightAboveGround", "2"
		case "10u", "10v", "VMAX_10M":
			levelType, level = "heightAboveGround", "10"
		case "prmsl":
			levelType = "meanSea"
		case "CLCT", "TQV", "TQC", "TQI":
			levelType = "entireAtmosphere"
		}
		identities[field.ShortName] = domeMessage{ShortName: field.ShortName, TypeOfLevel: levelType, Level: level}
	}
	return identities
}

func prepopulateDomeStaging(t *testing.T, root string, loaded LoadedManifest) {
	t.Helper()
	manifestSHA, _, err := fileDigest(filepath.Join(loaded.Directory, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(root, "models", "icon-eu", "dome-staging", loaded.RunID, DomeInputContractDigest())
	if err := os.MkdirAll(filepath.Join(staging, "model"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(staging, "surface"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "base-manifest.sha256"), []byte(manifestSHA+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	for _, hour := range DomeNativeForecastHours() {
		relative := filepath.Join("model", fmt.Sprintf("f%03d.grib2", hour))
		bundle := writeDomeTestFile(t, staging, relative, fmt.Sprintf("dome-%03d", hour))
		messages := domeModelMessagesPerStep
		if hour <= 78 {
			messages -= cloudStepMessageCount()
		}
		writeDomeTestCheckpoint(t, filepath.Join(staging, relative), domeObjectCheckpoint{
			Schema: domeCheckpointSchema, BaseManifestSHA256: manifestSHA, InputContractSHA256: DomeInputContractDigest(),
			File: DomeStepFile{
				Source: DomeFileSourceDomeRun, ForecastHour: hour, ValidAt: loaded.BaseTime.Add(time.Duration(hour) * time.Hour),
				File:  filepath.Join("dome-runs", loaded.RunID, DomeInputContractDigest(), relative),
				Bytes: bundle.Bytes, SHA256: bundle.SHA256, Messages: messages,
			},
		})
	}
	for _, hour := range []int{81, 84} {
		relative := filepath.Join("surface", fmt.Sprintf("f%03d.grib2", hour))
		bundle := writeDomeTestFile(t, staging, relative, fmt.Sprintf("surface-extension-%03d", hour))
		writeDomeTestCheckpoint(t, filepath.Join(staging, relative), domeObjectCheckpoint{
			Schema: domeCheckpointSchema, BaseManifestSHA256: manifestSHA, InputContractSHA256: DomeInputContractDigest(),
			File: DomeStepFile{
				Source: DomeFileSourceDomeRun, ForecastHour: hour, ValidAt: loaded.BaseTime.Add(time.Duration(hour) * time.Hour),
				File:  filepath.Join("dome-runs", loaded.RunID, DomeInputContractDigest(), relative),
				Bytes: bundle.Bytes, SHA256: bundle.SHA256, Messages: SurfaceBundleSchemaVersion,
			},
		})
	}
}

func writeDomeTestCheckpoint(t *testing.T, path string, checkpoint domeObjectCheckpoint) {
	t.Helper()
	allocated, err := domeAllocatedBytes(path)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.File.AllocatedBytes = allocated
	if err := writeDomeCheckpoint(domeCheckpointPath(path), checkpoint); err != nil {
		t.Fatal(err)
	}
}

func newDomeTestBudget(t *testing.T, root string, capBytes uint64) *model.DiskBudget {
	t.Helper()
	providerRoot := filepath.Join(root, "models", "icon-eu")
	budget, err := model.NewDiskBudget(model.DiskBudgetConfig{
		LockPath:       filepath.Join(root, "state", "disk-budget.lock"),
		ReservationDir: filepath.Join(root, "state", "disk-reservations"), FilesystemPath: root,
		PublishedRoots: []string{filepath.Join(providerRoot, "runs"), filepath.Join(providerRoot, "dome-runs")},
		StagingRoots:   []string{filepath.Join(providerRoot, "dome-staging")},
		LeasedRoots:    []string{filepath.Join(root, "state", "run-leases")},
		CacheRoots:     []string{filepath.Join(root, "cache")}, TemporaryRoots: []string{filepath.Join(root, "tmp")},
		ProjectCapBytes: capBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return budget
}

func installOldDomeCurrent(t *testing.T, root string) string {
	t.Helper()
	providerRoot := filepath.Join(root, "models", "icon-eu")
	if err := os.MkdirAll(providerRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join("dome-runs", "old-run", "old-contract")
	if err := os.Symlink(target, filepath.Join(providerRoot, "dome-ready-current")); err != nil {
		t.Fatal(err)
	}
	return target
}

func assertDomeCurrentTarget(t *testing.T, root, want string) {
	t.Helper()
	got, err := os.Readlink(filepath.Join(root, "models", "icon-eu", "dome-ready-current"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("dome-ready-current = %q, want %q", got, want)
	}
}
