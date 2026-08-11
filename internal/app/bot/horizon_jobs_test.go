package bot

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"bot_astrosferum/internal/forecast"
)

const horizonTestRunID = "2026072206"

var horizonTestRunTime = time.Date(2026, 7, 22, 6, 0, 0, 0, time.UTC)

type horizonFakeSource struct {
	mu         sync.Mutex
	currentRun string
	supported  bool
	calls      int
	supports   int
	current    int
	active     int
	maxActive  int
	seriesFun  func(context.Context) error
	currentSeq []string
	currentAt  int
}

func (source *horizonFakeSource) Supports(forecast.HorizonPlan) bool {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.supports++
	return source.supported
}

func (source *horizonFakeSource) CurrentRunID() (string, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.current++
	if len(source.currentSeq) > 0 {
		index := min(source.currentAt, len(source.currentSeq)-1)
		source.currentAt++
		return source.currentSeq[index], nil
	}
	return source.currentRun, nil
}

func (source *horizonFakeSource) Series(ctx context.Context, _ string, _ forecast.HorizonPlan) ([]forecast.HorizonSnapshot, error) {
	source.mu.Lock()
	source.calls++
	source.active++
	if source.active > source.maxActive {
		source.maxActive = source.active
	}
	callback := source.seriesFun
	source.mu.Unlock()
	defer func() {
		source.mu.Lock()
		source.active--
		source.mu.Unlock()
	}()
	if callback != nil {
		if err := callback(ctx); err != nil {
			return nil, err
		}
	}
	return horizonHourlyTestSnapshots(), nil
}

func (source *horizonFakeSource) counts() (calls, maximum int) {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.calls, source.maxActive
}

func (source *horizonFakeSource) horizonCalls() (supports, current, series int) {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.supports, source.current, source.calls
}

type horizonFakeMessenger struct {
	mu       sync.Mutex
	messages []string
	photos   []string
	answers  []string
	changed  chan struct{}
}

type horizonDeliveryTestMessenger struct {
	*horizonFakeMessenger
	photoErr   error
	completion chan error
}

func (messenger *horizonDeliveryTestMessenger) SendPhoto(ctx context.Context, chatID int64, path, caption string) error {
	if messenger.photoErr != nil {
		return messenger.photoErr
	}
	return messenger.horizonFakeMessenger.SendPhoto(ctx, chatID, path, caption)
}

func (messenger *horizonDeliveryTestMessenger) CompleteHorizon(err error) {
	messenger.completion <- err
}

func newHorizonFakeMessenger() *horizonFakeMessenger {
	return &horizonFakeMessenger{changed: make(chan struct{}, 64)}
}

func (messenger *horizonFakeMessenger) signal() {
	select {
	case messenger.changed <- struct{}{}:
	default:
	}
}

func (messenger *horizonFakeMessenger) SendMessage(_ context.Context, _ int64, text string, _ bool) error {
	messenger.mu.Lock()
	messenger.messages = append(messenger.messages, text)
	messenger.mu.Unlock()
	messenger.signal()
	return nil
}

func (messenger *horizonFakeMessenger) SendPhoto(_ context.Context, _ int64, path, _ string) error {
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		return errors.New("missing test image")
	}
	messenger.mu.Lock()
	messenger.photos = append(messenger.photos, path)
	messenger.mu.Unlock()
	messenger.signal()
	return nil
}

func (messenger *horizonFakeMessenger) SendDocument(context.Context, int64, string, string) error {
	return nil
}

func (messenger *horizonFakeMessenger) SendMessageWithActions(context.Context, int64, string, ActionKeyboard) error {
	return nil
}

func (messenger *horizonFakeMessenger) AnswerAction(_ context.Context, _ string, text string) error {
	messenger.mu.Lock()
	messenger.answers = append(messenger.answers, text)
	messenger.mu.Unlock()
	messenger.signal()
	return nil
}

func (messenger *horizonFakeMessenger) snapshot() (messages, photos, answers []string) {
	messenger.mu.Lock()
	defer messenger.mu.Unlock()
	return append([]string(nil), messenger.messages...), append([]string(nil), messenger.photos...), append([]string(nil), messenger.answers...)
}

func TestHorizonDeliveryFailureIsScopedToOneWaiter(t *testing.T) {
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	jobs := newHorizonTestJobs(t, t.TempDir(), source, HorizonJobsConfig{})
	jobs.root = t.Context()
	path := filepath.Join(t.TempDir(), "horizon.png")
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\ntest"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := &horizonDeliveryTestMessenger{
		horizonFakeMessenger: newHorizonFakeMessenger(), photoErr: errors.New("first platform failed"), completion: make(chan error, 1),
	}
	second := &horizonDeliveryTestMessenger{horizonFakeMessenger: newHorizonFakeMessenger(), completion: make(chan error, 1)}
	jobs.performDelivery(horizonDelivery{
		key: "delivery-test", path: path, runID: horizonTestRunID, timeZone: "UTC",
		waiters: []horizonWaiter{
			{identity: horizonUserIdentity{platform: "telegram", userID: 1}, chatID: 1, messenger: first, language: languageEnglish},
			{identity: horizonUserIdentity{platform: "web", userID: 2}, chatID: 2, messenger: second, language: languageEnglish},
		},
	})
	if err := <-first.completion; err == nil {
		t.Fatal("first waiter delivery failure was not reported")
	}
	if err := <-second.completion; err != nil {
		t.Fatalf("first waiter poisoned second waiter: %v", err)
	}
	_, photos, _ := second.snapshot()
	if len(photos) != 1 {
		t.Fatalf("second waiter photos = %d, want 1", len(photos))
	}
}

func TestHorizonActionPayloadIsAuthenticatedCompactAndPersistent(t *testing.T) {
	root := t.TempDir()
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	jobs := newHorizonTestJobs(t, root, source, HorizonJobsConfig{})
	request := horizonTestButtonRequest()
	request.Location = forecast.Location{Latitude: 89.9, Longitude: -180, TimeZone: "UTC"}
	request.ObserverSurfaceElevationM = 10000
	button, err := jobs.Button(request, "en")
	if err != nil {
		t.Fatalf("Button: %v", err)
	}
	if len(button.Data) > maxActionDataBytes {
		t.Fatalf("action payload has %d bytes, maximum is %d", len(button.Data), maxActionDataBytes)
	}
	id, payload, err := DecodeAction(button.Data)
	if err != nil || id != HorizonActionID {
		t.Fatalf("DecodeAction = %q, %q, %v", id, payload, err)
	}
	decoded, err := jobs.decodePayload(payload)
	if err != nil {
		t.Fatalf("decodePayload: %v", err)
	}
	if decoded.Provider != HorizonProviderICONEU || decoded.RunID != request.RunID || decoded.ObserverSurfaceElevationM != 10000 {
		t.Fatalf("unexpected decoded request: %+v", decoded)
	}
	if decoded.Location.Latitude != 89.9 || decoded.Location.Longitude != -180 {
		t.Fatalf("unexpected decoded coordinates: %+v", decoded.Location)
	}

	tampered := []byte(payload)
	if tampered[12] == '0' {
		tampered[12] = '1'
	} else {
		tampered[12] = '0'
	}
	if _, err := jobs.decodePayload(string(tampered)); !errors.Is(err, ErrInvalidActionData) {
		t.Fatalf("tampered payload error = %v, want ErrInvalidActionData", err)
	}

	second := newHorizonTestJobs(t, root, source, HorizonJobsConfig{})
	if _, err := second.decodePayload(payload); err != nil {
		t.Fatalf("persisted action key did not validate an existing button: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, horizonActionKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("action key permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestHorizonActionKeyConcurrentInitializationPublishesOneKey(t *testing.T) {
	root := t.TempDir()
	const callers = 24
	keys := make(chan []byte, callers)
	errorsFound := make(chan error, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			key, err := loadOrCreateHorizonActionKey(root)
			if err != nil {
				errorsFound <- err
				return
			}
			keys <- key
		}()
	}
	wait.Wait()
	close(keys)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("concurrent key initialization: %v", err)
	}
	var first []byte
	for key := range keys {
		if first == nil {
			first = key
			continue
		}
		if !hmac.Equal(first, key) {
			t.Fatal("concurrent initializers returned different action keys")
		}
	}
	if len(first) != 32 {
		t.Fatalf("published key length = %d, want 32", len(first))
	}
}

func TestHorizonActionKeyRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, horizonActionKeyFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateHorizonActionKey(root); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlink action key error = %v", err)
	}
}

func TestHorizonCacheKeyCoversScientificAndPresentationIdentity(t *testing.T) {
	base := horizonRequest{
		Provider: HorizonProviderICONEU, RunID: horizonTestRunID,
		Location:                  forecast.Location{Latitude: 59.9386, Longitude: 30.3141, TimeZone: "Europe/Moscow"},
		ObserverSurfaceElevationM: 17, Language: languageEnglish,
	}
	calibration := forecast.DefaultOverallIndexCalibration()
	baseKey, err := horizonCacheKey(base, calibration, "render-v1")
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name    string
		request horizonRequest
		cal     forecast.OverallIndexCalibration
		render  string
	}{
		{name: "latitude", request: func() horizonRequest { value := base; value.Location.Latitude += 0.00001; return value }(), cal: calibration, render: "render-v1"},
		{name: "longitude", request: func() horizonRequest { value := base; value.Location.Longitude += 0.00001; return value }(), cal: calibration, render: "render-v1"},
		{name: "HHL", request: func() horizonRequest { value := base; value.ObserverSurfaceElevationM++; return value }(), cal: calibration, render: "render-v1"},
		{name: "run", request: func() horizonRequest { value := base; value.RunID = "2026072212"; return value }(), cal: calibration, render: "render-v1"},
		{name: "language", request: func() horizonRequest { value := base; value.Language = languageRussian; return value }(), cal: calibration, render: "render-v1"},
		{name: "timezone", request: func() horizonRequest { value := base; value.Location.TimeZone = "UTC"; return value }(), cal: calibration, render: "render-v1"},
		{name: "calibration", request: base, cal: func() forecast.OverallIndexCalibration { value := calibration; value.CloudWeight += 0.1; return value }(), render: "render-v1"},
		{name: "renderer", request: base, cal: calibration, render: "render-v2"},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			key, keyErr := horizonCacheKey(check.request, check.cal, check.render)
			if keyErr != nil {
				t.Fatal(keyErr)
			}
			if key == baseKey {
				t.Fatalf("cache key ignored %s", check.name)
			}
		})
	}
}

func TestHorizonJobsSingleflightFansOutAcrossPlatforms(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	source.seriesFun = func(ctx context.Context) error {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	jobs := newHorizonTestJobs(t, t.TempDir(), source, HorizonJobsConfig{QueueSize: 2})
	startHorizonTestJobs(t, jobs)
	telegram := newHorizonFakeMessenger()
	vk := newHorizonFakeMessenger()
	telegramHandler := mustHorizonActionHandler(t, jobs, "telegram", telegram)
	vkHandler := mustHorizonActionHandler(t, jobs, "vk", vk)
	payload := horizonTestPayload(t, jobs, horizonTestButtonRequest())

	invokeHorizonAction(t, telegramHandler, payload, 101, "en")
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first horizon job did not start")
	}
	invokeHorizonAction(t, vkHandler, payload, 202, "en")
	close(release)
	waitHorizonTest(t, 2*time.Second, func() bool {
		_, telegramPhotos, _ := telegram.snapshot()
		_, vkPhotos, _ := vk.snapshot()
		return len(telegramPhotos) == 1 && len(vkPhotos) == 1
	})
	calls, maximum := source.counts()
	if calls != 1 {
		t.Fatalf("Series calls = %d, want one shared calculation", calls)
	}
	if maximum != 1 {
		t.Fatalf("maximum concurrent Series calls = %d, want 1", maximum)
	}
	_, _, telegramAnswers := telegram.snapshot()
	_, _, vkAnswers := vk.snapshot()
	if len(telegramAnswers) != 1 || len(vkAnswers) != 1 {
		t.Fatalf("callbacks were not acknowledged immediately: telegram=%d vk=%d", len(telegramAnswers), len(vkAnswers))
	}
	if telegramAnswers[0] != "Checking request…" || vkAnswers[0] != "Checking request…" {
		t.Fatalf("callback acknowledgement should be neutral: telegram=%q vk=%q", telegramAnswers, vkAnswers)
	}
}

func TestHorizonJobsEnforcesPerUserActivityAndRepeatedClickProtection(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	source.seriesFun = func(ctx context.Context) error {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	jobs := newHorizonTestJobs(t, t.TempDir(), source, HorizonJobsConfig{QueueSize: 2})
	startHorizonTestJobs(t, jobs)
	messenger := newHorizonFakeMessenger()
	handler := mustHorizonActionHandler(t, jobs, "telegram", messenger)
	firstPayload := horizonTestPayload(t, jobs, horizonTestButtonRequest())
	secondRequest := horizonTestButtonRequest()
	secondRequest.Location.Longitude += 0.01
	secondPayload := horizonTestPayload(t, jobs, secondRequest)

	invokeHorizonAction(t, handler, firstPayload, 303, "en")
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first horizon job did not start")
	}
	invokeHorizonAction(t, handler, secondPayload, 303, "en")
	messages, _, _ := messenger.snapshot()
	if !containsHorizonText(messages, "already have a horizon analysis") {
		t.Fatalf("missing per-user active-job status: %q", messages)
	}
	close(release)
	waitHorizonTest(t, 2*time.Second, func() bool {
		_, photos, _ := messenger.snapshot()
		return len(photos) == 1
	})
	time.Sleep(20 * time.Millisecond)
	invokeHorizonAction(t, handler, firstPayload, 303, "en")
	time.Sleep(80 * time.Millisecond)
	_, photos, _ := messenger.snapshot()
	if len(photos) != 1 {
		t.Fatalf("repeated click sent %d images, want 1", len(photos))
	}
	calls, _ := source.counts()
	if calls != 1 {
		t.Fatalf("repeated click caused %d calculations, want 1", calls)
	}
}

func TestHorizonJobsQueueIsBoundedAndWorkerIsSerial(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	source.seriesFun = func(ctx context.Context) error {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	jobs := newHorizonTestJobs(t, t.TempDir(), source, HorizonJobsConfig{QueueSize: 1})
	startHorizonTestJobs(t, jobs)
	messengers := []*horizonFakeMessenger{newHorizonFakeMessenger(), newHorizonFakeMessenger(), newHorizonFakeMessenger()}
	handlers := []ActionHandler{
		mustHorizonActionHandler(t, jobs, "telegram", messengers[0]),
		mustHorizonActionHandler(t, jobs, "vk", messengers[1]),
		mustHorizonActionHandler(t, jobs, "telegram", messengers[2]),
	}
	requests := []HorizonButtonRequest{horizonTestButtonRequest(), horizonTestButtonRequest(), horizonTestButtonRequest()}
	requests[1].Location.Longitude += 0.02
	requests[2].Location.Longitude += 0.04
	invokeHorizonAction(t, handlers[0], horizonTestPayload(t, jobs, requests[0]), 1, "en")
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first horizon job did not start")
	}
	invokeHorizonAction(t, handlers[1], horizonTestPayload(t, jobs, requests[1]), 2, "en")
	invokeHorizonAction(t, handlers[2], horizonTestPayload(t, jobs, requests[2]), 3, "en")
	messages, _, _ := messengers[2].snapshot()
	if !containsHorizonText(messages, "queue is full") {
		t.Fatalf("missing queue-full status: %q", messages)
	}
	close(release)
	waitHorizonTest(t, 2*time.Second, func() bool {
		_, first, _ := messengers[0].snapshot()
		_, second, _ := messengers[1].snapshot()
		return len(first) == 1 && len(second) == 1
	})
	calls, maximum := source.counts()
	if calls != 2 || maximum != 1 {
		t.Fatalf("source calls/max = %d/%d, want 2/1", calls, maximum)
	}
	_, third, _ := messengers[2].snapshot()
	if len(third) != 0 {
		t.Fatalf("queue-rejected request received %d images", len(third))
	}
}

func TestHorizonJobsUsesConfiguredConcurrency(t *testing.T) {
	release := make(chan struct{})
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	source.seriesFun = func(ctx context.Context) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	jobs := newHorizonTestJobs(t, t.TempDir(), source, HorizonJobsConfig{QueueSize: 2, Concurrency: 2})
	startHorizonTestJobs(t, jobs)
	firstMessenger := newHorizonFakeMessenger()
	secondMessenger := newHorizonFakeMessenger()
	firstHandler := mustHorizonActionHandler(t, jobs, "telegram", firstMessenger)
	secondHandler := mustHorizonActionHandler(t, jobs, "vk", secondMessenger)
	firstRequest := horizonTestButtonRequest()
	secondRequest := horizonTestButtonRequest()
	secondRequest.Location.Longitude += 0.02

	invokeHorizonAction(t, firstHandler, horizonTestPayload(t, jobs, firstRequest), 101, "en")
	invokeHorizonAction(t, secondHandler, horizonTestPayload(t, jobs, secondRequest), 202, "en")
	waitHorizonTest(t, 2*time.Second, func() bool {
		calls, maximum := source.counts()
		return calls == 2 && maximum == 2
	})
	close(release)
	waitHorizonTest(t, 2*time.Second, func() bool {
		_, firstPhotos, _ := firstMessenger.snapshot()
		_, secondPhotos, _ := secondMessenger.snapshot()
		return len(firstPhotos) == 1 && len(secondPhotos) == 1
	})
}

func TestHorizonJobsTimesOutAndDoesNotPublishPartialResult(t *testing.T) {
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	source.seriesFun = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	root := t.TempDir()
	jobs := newHorizonTestJobs(t, root, source, HorizonJobsConfig{JobTimeout: 35 * time.Millisecond})
	var logsMu sync.Mutex
	var logs []string
	jobs.logf = func(format string, values ...any) {
		logsMu.Lock()
		logs = append(logs, fmt.Sprintf(format, values...))
		logsMu.Unlock()
	}
	startHorizonTestJobs(t, jobs)
	messenger := newHorizonFakeMessenger()
	handler := mustHorizonActionHandler(t, jobs, "telegram", messenger)
	invokeHorizonAction(t, handler, horizonTestPayload(t, jobs, horizonTestButtonRequest()), 404, "en")
	waitHorizonTest(t, 2*time.Second, func() bool {
		messages, _, _ := messenger.snapshot()
		return containsHorizonText(messages, "exceeded its time limit")
	})
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && (validHorizonCacheKey(entry.Name()) || strings.HasPrefix(entry.Name(), ".incoming-")) {
			t.Fatalf("timeout left cache artifact %q", entry.Name())
		}
	}
	logsMu.Lock()
	joinedLogs := strings.Join(logs, "\n")
	logsMu.Unlock()
	if !strings.Contains(joinedLogs, "stage=source") || !strings.Contains(joinedLogs, "timeout=true") || !strings.Contains(joinedLogs, "error_type=") {
		t.Fatalf("timeout log lacks safe diagnostics: %q", joinedLogs)
	}
	if strings.Contains(joinedLogs, "59.9386") || strings.Contains(joinedLogs, "30.3141") || strings.Contains(joinedLogs, "user") || strings.Contains(joinedLogs, "payload") {
		t.Fatalf("timeout log contains sensitive action data: %q", joinedLogs)
	}
}

func TestHorizonJobsTimeoutCancelsCalculation(t *testing.T) {
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	jobs := newHorizonTestJobs(t, t.TempDir(), source, HorizonJobsConfig{JobTimeout: 35 * time.Millisecond})
	entered := make(chan struct{})
	jobs.compute = func(ctx context.Context, _ []forecast.HorizonSnapshot, _ forecast.HorizonPlan, _ forecast.OverallIndexCalibration) ([]forecast.HorizonFrame, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	startHorizonTestJobs(t, jobs)
	messenger := newHorizonFakeMessenger()
	handler := mustHorizonActionHandler(t, jobs, "telegram", messenger)
	invokeHorizonAction(t, handler, horizonTestPayload(t, jobs, horizonTestButtonRequest()), 405, "en")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("horizon calculation did not start")
	}
	waitHorizonTest(t, 2*time.Second, func() bool {
		messages, _, _ := messenger.snapshot()
		return containsHorizonText(messages, "exceeded its time limit")
	})
}

func TestHorizonJobsTimeoutCancelsRendering(t *testing.T) {
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	jobs := newHorizonTestJobs(t, t.TempDir(), source, HorizonJobsConfig{JobTimeout: 35 * time.Millisecond})
	entered := make(chan struct{})
	jobs.render = func(ctx context.Context, _ string, _ HorizonRenderInput, _ string) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	startHorizonTestJobs(t, jobs)
	messenger := newHorizonFakeMessenger()
	handler := mustHorizonActionHandler(t, jobs, "telegram", messenger)
	invokeHorizonAction(t, handler, horizonTestPayload(t, jobs, horizonTestButtonRequest()), 406, "en")
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("horizon render did not start")
	}
	waitHorizonTest(t, 2*time.Second, func() bool {
		messages, _, _ := messenger.snapshot()
		return containsHorizonText(messages, "exceeded its time limit")
	})
}

func TestHorizonComputedDeliveryReleasesUserWhenChannelIsFull(t *testing.T) {
	root := t.TempDir()
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	jobs := newHorizonTestJobs(t, root, source, HorizonJobsConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	jobs.mu.Lock()
	jobs.root, jobs.cancel, jobs.started = ctx, cancel, true
	jobs.mu.Unlock()
	t.Cleanup(jobs.Close)
	key := strings.Repeat("6", sha256HexLength)
	path, err := jobs.cache.publish(key, time.Now(), func(destination string) error {
		return os.WriteFile(destination, []byte("computed-image"), 0o640)
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := horizonUserIdentity{platform: "telegram", userID: 9001}
	waiter := horizonWaiter{identity: identity, chatID: 9001, messenger: newHorizonFakeMessenger(), language: languageEnglish}
	job := &horizonJob{key: key, request: horizonRequest{RunID: horizonTestRunID, Location: forecast.Location{TimeZone: "UTC"}}, waiters: map[horizonUserIdentity]horizonWaiter{identity: waiter}}
	jobs.mu.Lock()
	jobs.jobs[key] = job
	jobs.active[identity] = key
	jobs.mu.Unlock()
	for range cap(jobs.delivery) {
		jobs.delivery <- horizonDelivery{}
	}
	started := time.Now()
	jobs.deliverJob(job, path, nil)
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("computed delivery blocked on a full delivery channel")
	}
	jobs.mu.Lock()
	_, active := jobs.active[identity]
	jobs.mu.Unlock()
	if active {
		t.Fatal("full delivery channel retained the user's active-job state")
	}
	assertNoHorizonLeases(t, root)
}

func TestHorizonCachedDeliveryReservationLeavesCapacityAndCloseDrainsLeases(t *testing.T) {
	root := t.TempDir()
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	jobs := newHorizonTestJobs(t, root, source, HorizonJobsConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	jobs.mu.Lock()
	jobs.root, jobs.cancel, jobs.started = ctx, cancel, true
	jobs.mu.Unlock()
	key := strings.Repeat("7", sha256HexLength)
	path, err := jobs.cache.publish(key, time.Now(), func(destination string) error {
		return os.WriteFile(destination, []byte("cached-image"), 0o640)
	})
	if err != nil {
		t.Fatal(err)
	}
	reserved := cap(jobs.cacheDeliverySlots)
	for index := range reserved {
		identity := horizonUserIdentity{platform: "telegram", userID: int64(index + 1)}
		if !jobs.reserveCached(identity, key) {
			t.Fatalf("cache reservation %d was rejected", index)
		}
		waiter := horizonWaiter{identity: identity, chatID: identity.userID, messenger: newHorizonFakeMessenger(), language: languageEnglish}
		if !jobs.scheduleCachedDelivery(waiter, key, path, horizonTestRunID, "UTC") {
			t.Fatalf("cache delivery %d was rejected", index)
		}
	}
	overflowIdentity := horizonUserIdentity{platform: "telegram", userID: 999}
	if !jobs.reserveCached(overflowIdentity, key) {
		t.Fatal("overflow user could not reserve active state")
	}
	overflow := horizonWaiter{identity: overflowIdentity, chatID: 999, messenger: newHorizonFakeMessenger(), language: languageEnglish}
	if jobs.scheduleCachedDelivery(overflow, key, path, horizonTestRunID, "UTC") {
		t.Fatal("cache-hit delivery exceeded its reserved half of the channel")
	}
	jobs.finishCached(overflowIdentity, key)

	computedKey := strings.Repeat("8", sha256HexLength)
	computedIdentity := horizonUserIdentity{platform: "vk", userID: 1000}
	computedWaiter := horizonWaiter{identity: computedIdentity, chatID: 1000, messenger: newHorizonFakeMessenger(), language: languageEnglish}
	computedJob := &horizonJob{key: computedKey, request: horizonRequest{RunID: horizonTestRunID, Location: forecast.Location{TimeZone: "UTC"}}, waiters: map[horizonUserIdentity]horizonWaiter{computedIdentity: computedWaiter}}
	jobs.mu.Lock()
	jobs.jobs[computedKey] = computedJob
	jobs.active[computedIdentity] = computedKey
	jobs.mu.Unlock()
	jobs.deliverJob(computedJob, path, nil)
	if len(jobs.delivery) != reserved+1 {
		t.Fatalf("computed result did not retain reserved delivery capacity: queue=%d reserved-cache=%d", len(jobs.delivery), reserved)
	}

	jobs.Close()
	if len(jobs.delivery) != 0 || len(jobs.cacheDeliverySlots) != 0 {
		t.Fatalf("Close left queued deliveries or cache slots: deliveries=%d slots=%d", len(jobs.delivery), len(jobs.cacheDeliverySlots))
	}
	jobs.mu.Lock()
	active := len(jobs.active)
	jobs.mu.Unlock()
	if active != 0 {
		t.Fatalf("Close retained %d active users", active)
	}
	assertNoHorizonLeases(t, root)
}

func TestHorizonActionsRejectGlobalCoverageForgeryAndStaleRun(t *testing.T) {
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	jobs := newHorizonTestJobs(t, t.TempDir(), source, HorizonJobsConfig{})
	global := horizonTestButtonRequest()
	global.Provider = "icon-global"
	if _, err := jobs.Button(global, "en"); !errors.Is(err, ErrHorizonUnsupported) {
		t.Fatalf("Global Button error = %v, want ErrHorizonUnsupported", err)
	}

	source.mu.Lock()
	source.supported = false
	source.mu.Unlock()
	if _, err := jobs.Button(horizonTestButtonRequest(), "en"); !errors.Is(err, ErrHorizonUnsupported) {
		t.Fatalf("uncovered Button error = %v, want ErrHorizonUnsupported", err)
	}
	source.mu.Lock()
	source.supported = true
	source.mu.Unlock()
	payload := horizonTestPayload(t, jobs, horizonTestButtonRequest())
	startHorizonTestJobs(t, jobs)
	messenger := newHorizonFakeMessenger()
	handler := mustHorizonActionHandler(t, jobs, "telegram", messenger)
	forged := []byte(payload)
	forged[len(forged)-1] ^= 1
	err := handler(context.Background(), ActionInvocation{Token: "forged", Chat: Chat{ID: 5}, From: &User{ID: 5, LanguageCode: "en"}}, string(forged))
	if !errors.Is(err, ErrInvalidActionData) {
		t.Fatalf("forged callback error = %v, want ErrInvalidActionData", err)
	}
	_, _, answers := messenger.snapshot()
	if len(answers) != 0 {
		t.Fatal("forged callback was acknowledged as accepted")
	}

	source.mu.Lock()
	source.currentRun = "2026072212"
	source.mu.Unlock()
	invokeHorizonAction(t, handler, payload, 6, "en")
	messages, _, answers := messenger.snapshot()
	if !containsHorizonText(messages, "older run") || len(answers) != 1 {
		t.Fatalf("stale callback result messages=%q answers=%q", messages, answers)
	}
	calls, _ := source.counts()
	if calls != 0 {
		t.Fatalf("rejected callbacks caused %d source calls", calls)
	}
}

func TestHorizonJobRejectsRunChangedDuringRendering(t *testing.T) {
	source := &horizonFakeSource{
		currentRun: horizonTestRunID, supported: true,
		currentSeq: []string{horizonTestRunID, horizonTestRunID, horizonTestRunID, "2026072212"},
	}
	jobs := newHorizonTestJobs(t, t.TempDir(), source, HorizonJobsConfig{})
	startHorizonTestJobs(t, jobs)
	messenger := newHorizonFakeMessenger()
	handler := mustHorizonActionHandler(t, jobs, "telegram", messenger)
	payload := horizonTestPayload(t, jobs, horizonTestButtonRequest())
	invokeHorizonAction(t, handler, payload, 55, "en")
	waitHorizonTest(t, 3*time.Second, func() bool {
		messages, _, _ := messenger.snapshot()
		return containsHorizonText(messages, "older run")
	})
	_, photos, _ := messenger.snapshot()
	if len(photos) != 0 {
		t.Fatalf("result from a superseded run delivered %d images", len(photos))
	}
}

func TestHorizonActionBeforeWorkerStartIsAcknowledgedButNotQueued(t *testing.T) {
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	jobs := newHorizonTestJobs(t, t.TempDir(), source, HorizonJobsConfig{})
	payload := horizonTestPayload(t, jobs, horizonTestButtonRequest())
	messenger := newHorizonFakeMessenger()
	handler := mustHorizonActionHandler(t, jobs, "telegram", messenger)
	invokeHorizonAction(t, handler, payload, 7, "en")
	messages, _, answers := messenger.snapshot()
	if len(answers) != 1 || !containsHorizonText(messages, "currently unavailable") {
		t.Fatalf("pre-start action messages=%q answers=%q", messages, answers)
	}
	calls, _ := source.counts()
	if calls != 0 {
		t.Fatalf("pre-start action caused %d calculations", calls)
	}
	jobs.Close()
	if err := jobs.Start(context.Background()); err == nil {
		t.Fatal("closed HorizonJobs restarted")
	}
}

func TestHorizonPersistentCacheAvoidsCalculationAfterRestart(t *testing.T) {
	root := t.TempDir()
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	first := newHorizonTestJobs(t, root, source, HorizonJobsConfig{})
	startHorizonTestJobs(t, first)
	firstMessenger := newHorizonFakeMessenger()
	firstHandler := mustHorizonActionHandler(t, first, "telegram", firstMessenger)
	payload := horizonTestPayload(t, first, horizonTestButtonRequest())
	invokeHorizonAction(t, firstHandler, payload, 11, "en")
	waitHorizonTest(t, 2*time.Second, func() bool {
		_, photos, _ := firstMessenger.snapshot()
		return len(photos) == 1
	})
	first.Close()

	second := newHorizonTestJobs(t, root, source, HorizonJobsConfig{})
	startHorizonTestJobs(t, second)
	secondMessenger := newHorizonFakeMessenger()
	secondHandler := mustHorizonActionHandler(t, second, "vk", secondMessenger)
	invokeHorizonAction(t, secondHandler, payload, 12, "en")
	waitHorizonTest(t, 2*time.Second, func() bool {
		_, photos, _ := secondMessenger.snapshot()
		return len(photos) == 1
	})
	calls, _ := source.counts()
	if calls != 1 {
		t.Fatalf("persistent cache resulted in %d calculations, want 1", calls)
	}
}

func TestHorizonCachedResultIsRecheckedImmediatelyBeforeDelivery(t *testing.T) {
	root := t.TempDir()
	source := &horizonFakeSource{currentRun: horizonTestRunID, supported: true}
	first := newHorizonTestJobs(t, root, source, HorizonJobsConfig{})
	startHorizonTestJobs(t, first)
	firstMessenger := newHorizonFakeMessenger()
	firstHandler := mustHorizonActionHandler(t, first, "telegram", firstMessenger)
	payload := horizonTestPayload(t, first, horizonTestButtonRequest())
	invokeHorizonAction(t, firstHandler, payload, 21, "en")
	waitHorizonTest(t, 2*time.Second, func() bool {
		_, photos, _ := firstMessenger.snapshot()
		return len(photos) == 1
	})
	first.Close()

	source.mu.Lock()
	source.currentSeq = []string{horizonTestRunID, "2026072212"}
	source.currentAt = 0
	source.mu.Unlock()
	second := newHorizonTestJobs(t, root, source, HorizonJobsConfig{})
	startHorizonTestJobs(t, second)
	secondMessenger := newHorizonFakeMessenger()
	secondHandler := mustHorizonActionHandler(t, second, "telegram", secondMessenger)
	invokeHorizonAction(t, secondHandler, payload, 22, "en")
	waitHorizonTest(t, 2*time.Second, func() bool {
		messages, _, _ := secondMessenger.snapshot()
		return containsHorizonText(messages, "older run")
	})
	_, photos, _ := secondMessenger.snapshot()
	if len(photos) != 0 {
		t.Fatalf("cached result from a superseded run delivered %d images", len(photos))
	}
	calls, _ := source.counts()
	if calls != 1 {
		t.Fatalf("cached stale-run check caused %d calculations, want the original one only", calls)
	}
}

func TestHorizonCaptionIncludesRunAndCurrentFreshness(t *testing.T) {
	now := time.Date(2026, 7, 22, 14, 17, 0, 0, time.UTC)
	caption := horizonCaption(horizonTestRunID, "Europe/Moscow", now, 12*time.Hour, languageEnglish)
	if !strings.Contains(caption, "ICON-EU run 2026072206 UTC") || !strings.Contains(caption, "Data freshness: 8h 17min") {
		t.Fatalf("caption lacks run freshness: %q", caption)
	}
	if !strings.Contains(caption, "22.07 09:00 MSK — 25.07 09:00 MSK") {
		t.Fatalf("caption lacks the actual f000..f072 run period: %q", caption)
	}
	russian := horizonCaption(horizonTestRunID, "Europe/Moscow", now, 12*time.Hour, languageRussian)
	if !strings.Contains(russian, "Актуальность данных: 8 ч 17 мин") {
		t.Fatalf("Russian caption lacks localized freshness: %q", russian)
	}
	configured := horizonCaption(horizonTestRunID, "Europe/Moscow", now, 6*time.Hour, languageEnglish)
	if !strings.Contains(configured, "Stale run") || !strings.Contains(configured, "6h 0min threshold") {
		t.Fatalf("Horizon caption ignored configured stale threshold: %q", configured)
	}
}

func TestValidateHorizonSeriesRequiresCompleteHourlyRunPeriod(t *testing.T) {
	valid := horizonHourlyTestSnapshots()
	if err := validateHorizonSeries(horizonTestRunID, valid); err != nil {
		t.Fatalf("valid full series: %v", err)
	}
	tests := []struct {
		name      string
		snapshots []forecast.HorizonSnapshot
	}{
		{name: "missing final term", snapshots: append([]forecast.HorizonSnapshot(nil), valid[:len(valid)-1]...)},
		{name: "starts after run", snapshots: func() []forecast.HorizonSnapshot {
			result := append([]forecast.HorizonSnapshot(nil), valid...)
			for index := range result {
				result[index].ValidAt = result[index].ValidAt.Add(time.Hour)
			}
			return result
		}()},
		{name: "three-hour gap", snapshots: func() []forecast.HorizonSnapshot {
			result := append([]forecast.HorizonSnapshot(nil), valid...)
			result[20].ValidAt = result[19].ValidAt.Add(3 * time.Hour)
			return result
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateHorizonSeries(horizonTestRunID, test.snapshots); err == nil {
				t.Fatal("incomplete/non-hourly series was accepted")
			}
		})
	}
}

func newHorizonTestJobs(t *testing.T, root string, source *horizonFakeSource, override HorizonJobsConfig) *HorizonJobs {
	t.Helper()
	config := HorizonJobsConfig{
		QueueSize: 2, Concurrency: 1, JobTimeout: 2 * time.Second, CacheRoot: root,
		CacheTTL: time.Hour, CacheEntries: 8, EstimatedDuration: time.Minute,
		MaxStaleAge:            12 * time.Hour,
		RenderAlgorithmVersion: "horizon-test-render-v1",
	}
	if override.QueueSize > 0 {
		config.QueueSize = override.QueueSize
	}
	if override.Concurrency > 0 {
		config.Concurrency = override.Concurrency
	}
	if override.JobTimeout > 0 {
		config.JobTimeout = override.JobTimeout
	}
	jobs, err := NewHorizonJobs(config, source, func(ctx context.Context, destination string, _ HorizonRenderInput, _ string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return os.WriteFile(destination, []byte("\x89PNG\r\n\x1a\ntest-png"), 0o640)
	}, forecast.DefaultOverallIndexCalibration(), nil)
	if err != nil {
		t.Fatalf("NewHorizonJobs: %v", err)
	}
	jobs.compute = func(_ context.Context, snapshots []forecast.HorizonSnapshot, _ forecast.HorizonPlan, _ forecast.OverallIndexCalibration) ([]forecast.HorizonFrame, error) {
		frames := make([]forecast.HorizonFrame, len(snapshots))
		for index, snapshot := range snapshots {
			results := make([]forecast.HorizonResult, forecast.HorizonDirectionCount)
			for directionIndex, direction := range forecast.HorizonDirections() {
				results[directionIndex] = forecast.HorizonResult{
					ValidAt: snapshot.ValidAt, Direction: direction, AzimuthDegrees: float64(directionIndex) * 45,
					GeometricElevationDegrees: forecast.HorizonGeometricElevationDegrees,
					DataQuality:               forecast.HorizonDataUnavailable, LimitingFactor: forecast.HorizonFactorUnavailable,
				}
			}
			frames[index] = forecast.HorizonFrame{ValidAt: snapshot.ValidAt, Results: results}
		}
		return frames, nil
	}
	return jobs
}

func startHorizonTestJobs(t *testing.T, jobs *HorizonJobs) {
	t.Helper()
	if err := jobs.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(jobs.Close)
}

func horizonTestButtonRequest() HorizonButtonRequest {
	return HorizonButtonRequest{
		Provider: HorizonProviderICONEU, RunID: horizonTestRunID,
		Location:                  forecast.Location{Latitude: 59.9386, Longitude: 30.3141, TimeZone: "Europe/Moscow"},
		ObserverSurfaceElevationM: 17,
	}
}

func horizonHourlyTestSnapshots() []forecast.HorizonSnapshot {
	snapshots := make([]forecast.HorizonSnapshot, horizonForecastHours+1)
	for index := range snapshots {
		snapshots[index].ValidAt = horizonTestRunTime.Add(time.Duration(index) * time.Hour)
	}
	return snapshots
}

func horizonTestPayload(t *testing.T, jobs *HorizonJobs, request HorizonButtonRequest) string {
	t.Helper()
	button, err := jobs.Button(request, "en")
	if err != nil {
		t.Fatalf("Button: %v", err)
	}
	id, payload, err := DecodeAction(button.Data)
	if err != nil || id != HorizonActionID {
		t.Fatalf("DecodeAction = %q, %q, %v", id, payload, err)
	}
	return payload
}

func mustHorizonActionHandler(t *testing.T, jobs *HorizonJobs, platform string, messenger HorizonMessenger) ActionHandler {
	t.Helper()
	handler, err := jobs.ActionHandler(platform, messenger)
	if err != nil {
		t.Fatalf("ActionHandler: %v", err)
	}
	return handler
}

func invokeHorizonAction(t *testing.T, handler ActionHandler, payload string, userID int64, language string) {
	t.Helper()
	err := handler(context.Background(), ActionInvocation{
		Token: "callback", Chat: Chat{ID: userID}, From: &User{ID: userID, LanguageCode: language},
	}, payload)
	if err != nil {
		t.Fatalf("horizon action: %v", err)
	}
}

func waitHorizonTest(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for horizon test condition")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func containsHorizonText(values []string, fragment string) bool {
	for _, value := range values {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}
