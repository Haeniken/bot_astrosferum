package bot

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"bot_astrosferum/internal/app/directional"
	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/render"
)

const (
	HorizonActionID       ActionID = "horizon.v1"
	HorizonProviderICONEU string   = "icon-eu"

	horizonCacheSchema     = "horizon-cache-v7-glo30-informational-skyline"
	horizonActionKeyFile   = ".horizon-action-key"
	horizonActionTagBytes  = 8
	horizonActionCoreParts = 4
	horizonForecastHours   = 72
	horizonDeliveryTimeout = 90 * time.Second
	horizonDeliveryWorkers = 2
	horizonDeliveryBatch   = 5 * time.Minute
	horizonClickCooldown   = 3 * time.Second
	horizonCacheSweepMax   = time.Hour
)

var (
	ErrHorizonUnsupported = errors.New("horizon analysis is unsupported")
	ErrHorizonStaleAction = errors.New("horizon action is stale")
	ErrHorizonQueueFull   = errors.New("horizon queue is full")
	ErrHorizonUserBusy    = errors.New("horizon user already has an active job")

	horizonRunPattern      = regexp.MustCompile(`^[0-9]{10}$`)
	horizonPlatformPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

// HorizonSource is the consumer-owned subset implemented by
// iconeu.HorizonStore. Keeping it here avoids making the model adapter a
// dependency of the platform-neutral application workflow.
type HorizonSource interface {
	Supports(forecast.HorizonPlan) bool
	CurrentRunID() (string, error)
	Series(context.Context, string, forecast.HorizonPlan) ([]forecast.HorizonSnapshot, error)
}

type TerrainSkylineSource interface {
	CacheKey(forecast.Location) (string, bool, error)
	Resolve(context.Context, forecast.Location) (forecast.TerrainSkyline, error)
}

type disabledTerrainSkylineSource struct{}

func (disabledTerrainSkylineSource) Resolve(context.Context, forecast.Location) (forecast.TerrainSkyline, error) {
	return forecast.DisabledTerrainSkyline(), nil
}
func (disabledTerrainSkylineSource) CacheKey(forecast.Location) (string, bool, error) {
	return forecast.TerrainSkylineVersion + ":disabled", true, nil
}

// HorizonMessenger is the exact delivery/acknowledgement subset used by the
// heavy workflow. HorizonJobs never imports either platform package.
type HorizonMessenger interface {
	SendMessage(context.Context, int64, string, bool) error
	SendPhoto(context.Context, int64, string, string) error
	AnswerAction(context.Context, string, string) error
}

// HorizonCompletionMessenger is implemented by non-platform callers that
// need an explicit terminal signal instead of inferring it from localized
// status messages. Telegram and VK adapters do not need to implement it.
type HorizonCompletionMessenger interface {
	CompleteHorizon(error)
}

type HorizonDatasetMessenger interface {
	SendHorizonDataset(context.Context, []byte) error
}

// HorizonRenderInput contains only already-fetched and already-computed data.
// A render adapter can map it directly to render.HorizonInput.
type HorizonRenderInput struct {
	Location       forecast.Location
	Provider       string
	RunID          string
	Grid           string
	Frames         []forecast.HorizonFrame
	TerrainSkyline forecast.TerrainSkyline
}

// HorizonRenderFunc renders one PNG at destination. Implementations should
// honor ctx before expensive work and must not acquire model/network data.
type HorizonRenderFunc func(ctx context.Context, destination string, input HorizonRenderInput, language string) error

type HorizonJobsConfig struct {
	QueueSize              int
	Concurrency            int
	JobTimeout             time.Duration
	CacheRoot              string
	CacheTTL               time.Duration
	CacheEntries           int
	EstimatedDuration      time.Duration
	MaxStaleAge            time.Duration
	RenderAlgorithmVersion string
	Terrain                TerrainSkylineSource
}

// HorizonButtonRequest is built from the successfully delivered ordinary
// ICON-EU forecast. Values are quantized before signing, and the exact same
// quantized values are later used by the heavy calculation.
type HorizonButtonRequest struct {
	Provider                  string
	RunID                     string
	Location                  forecast.Location
	ObserverSurfaceElevationM float64
}

type horizonRequest struct {
	Provider                  string
	RunID                     string
	Location                  forecast.Location
	ObserverSurfaceElevationM float64
	Language                  userLanguage
	TerrainSkyline            forecast.TerrainSkyline
	TerrainPreparationKey     string
}

type horizonWaiter struct {
	identity  horizonUserIdentity
	chatID    int64
	messenger HorizonMessenger
	language  userLanguage
}

type horizonUserIdentity struct {
	platform string
	userID   int64
}

type horizonJob struct {
	key     string
	request horizonRequest
	plan    forecast.HorizonPlan
	waiters map[horizonUserIdentity]horizonWaiter
}

type horizonDelivery struct {
	key       string
	path      string
	dataset   []byte
	runID     string
	timeZone  string
	waiters   []horizonWaiter
	jobErr    error
	leased    bool
	cacheSlot bool
}

type horizonRecentClick struct {
	key string
	at  time.Time
}

// HorizonJobs owns platform-neutral Horizon admission and localized delivery.
// Production can attach the shared directional coordinator so Horizon and
// Astrodome use one FIFO and one heavy worker. A local composition is allowed
// because both runners execute the same versioned straight-ray plan.
type HorizonJobs struct {
	config             HorizonJobsConfig
	source             HorizonSource
	render             HorizonRenderFunc
	calibration        forecast.OverallIndexCalibration
	cache              *horizonCache
	actionKey          []byte
	resolver           *forecast.TimeZoneResolver
	logf               func(string, ...any)
	now                func() time.Time
	compute            func(context.Context, []forecast.HorizonSnapshot, forecast.HorizonPlan, forecast.OverallIndexCalibration) ([]forecast.HorizonFrame, error)
	terrain            TerrainSkylineSource
	mu                 sync.Mutex
	started            bool
	closed             bool
	running            int
	root               context.Context
	cancel             context.CancelFunc
	queue              chan *horizonJob
	delivery           chan horizonDelivery
	cacheDeliverySlots chan struct{}
	jobs               map[string]*horizonJob
	active             map[horizonUserIdentity]string
	recent             map[horizonUserIdentity]horizonRecentClick
	wait               sync.WaitGroup
	closeOne           sync.Once
	directional        *directional.Coordinator
	directionalTickets map[horizonUserIdentity]*directional.Ticket
}

func NewHorizonJobs(config HorizonJobsConfig, source HorizonSource, renderer HorizonRenderFunc, calibration forecast.OverallIndexCalibration, logf func(string, ...any)) (*HorizonJobs, error) {
	if source == nil {
		return nil, errors.New("horizon source is required")
	}
	if renderer == nil {
		return nil, errors.New("horizon renderer is required")
	}
	if config.QueueSize < 1 {
		return nil, errors.New("horizon queue size must be positive")
	}
	if config.Concurrency < 1 {
		return nil, errors.New("horizon concurrency must be positive")
	}
	if config.JobTimeout <= 0 || config.CacheTTL <= 0 || config.EstimatedDuration <= 0 || config.MaxStaleAge <= 0 {
		return nil, errors.New("horizon job, cache, and estimate durations must be positive")
	}
	if config.CacheEntries < 1 {
		return nil, errors.New("horizon cache entry limit must be positive")
	}
	if strings.TrimSpace(config.CacheRoot) == "" {
		return nil, errors.New("horizon cache root is required")
	}
	if strings.TrimSpace(config.RenderAlgorithmVersion) == "" {
		return nil, errors.New("horizon render algorithm version is required")
	}
	if err := calibration.Validate(); err != nil {
		return nil, fmt.Errorf("horizon calibration: %w", err)
	}
	cache, err := newHorizonCache(config.CacheRoot, config.CacheTTL, config.CacheEntries)
	if err != nil {
		return nil, err
	}
	actionKey, err := loadOrCreateHorizonActionKey(config.CacheRoot)
	if err != nil {
		return nil, err
	}
	resolver, err := forecast.NewTimeZoneResolver()
	if err != nil {
		return nil, fmt.Errorf("initialize horizon timezone resolver: %w", err)
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if config.Terrain == nil {
		config.Terrain = disabledTerrainSkylineSource{}
	}
	deliveryCapacity := max(16, config.QueueSize*8)
	return &HorizonJobs{
		config: config, source: source, terrain: config.Terrain, render: renderer, calibration: calibration,
		cache: cache, actionKey: actionKey, resolver: resolver, logf: logf, now: time.Now,
		compute: forecast.ComputeHorizonSeries, queue: make(chan *horizonJob, config.QueueSize),
		delivery:           make(chan horizonDelivery, deliveryCapacity),
		cacheDeliverySlots: make(chan struct{}, deliveryCapacity/2),
		jobs:               make(map[string]*horizonJob), active: make(map[horizonUserIdentity]string),
		recent:             make(map[horizonUserIdentity]horizonRecentClick),
		directionalTickets: make(map[horizonUserIdentity]*directional.Ticket),
	}, nil
}

// CancelUser cancels the actual shared directional ticket for one active
// caller. Platform update contexts never call this method; it exists for the
// owner-scoped website DELETE/timeout path.
func (jobs *HorizonJobs) CancelUser(platform string, userID int64) {
	if jobs == nil || !horizonPlatformPattern.MatchString(platform) || userID <= 0 {
		return
	}
	identity := horizonUserIdentity{platform: platform, userID: userID}
	jobs.mu.Lock()
	ticket := jobs.directionalTickets[identity]
	jobs.mu.Unlock()
	if ticket != nil {
		_ = ticket.Cancel()
	}
}

// Start binds all work to the process/root lifetime rather than to a
// short-lived webhook callback context. When a directional coordinator is
// configured, only lightweight delivery workers are started here.
func (jobs *HorizonJobs) Start(root context.Context) error {
	if root == nil {
		return errors.New("horizon root context is required")
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if jobs.started {
		return errors.New("horizon jobs already started")
	}
	if jobs.closed {
		return errors.New("horizon jobs are closed")
	}
	jobs.root, jobs.cancel = context.WithCancel(root)
	jobs.started = true
	calculationWorkers := jobs.config.Concurrency
	if jobs.directional != nil {
		calculationWorkers = 0
	}
	jobs.wait.Add(calculationWorkers + horizonDeliveryWorkers)
	for range calculationWorkers {
		go jobs.worker()
	}
	for range horizonDeliveryWorkers {
		go jobs.deliveryWorker()
	}
	return nil
}

// Close cancels running extraction, discards queued work, and waits for the
// single worker to stop. It is safe to call more than once.
func (jobs *HorizonJobs) Close() {
	jobs.closeOne.Do(func() {
		jobs.mu.Lock()
		jobs.closed = true
		cancel := jobs.cancel
		jobs.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		jobs.wait.Wait()
		jobs.discardDeliveries()
	})
}

// Button returns an ICON-EU-only, authenticated action button. Callers should
// simply omit it when this method returns ErrHorizonUnsupported or
// ErrHorizonStaleAction; the ordinary seven-chart forecast remains complete.
func (jobs *HorizonJobs) Button(request HorizonButtonRequest, languageCode string) (ActionButton, error) {
	canonical, plan, err := jobs.canonicalButtonRequest(request, languageFromCode(languageCode))
	if err != nil {
		return ActionButton{}, err
	}
	currentRun, err := jobs.source.CurrentRunID()
	if err != nil {
		return ActionButton{}, fmt.Errorf("read current ICON-EU run: %w", err)
	}
	if currentRun != canonical.RunID {
		return ActionButton{}, ErrHorizonStaleAction
	}
	if !jobs.source.Supports(plan) {
		return ActionButton{}, ErrHorizonUnsupported
	}
	payload, err := jobs.encodePayload(canonical)
	if err != nil {
		return ActionButton{}, err
	}
	data, err := EncodeAction(HorizonActionID, payload)
	if err != nil {
		return ActionButton{}, err
	}
	label := canonical.Language.text("🧭 Горизонт (~%s)", "🧭 Horizon (~%s)")
	return ActionButton{Text: fmt.Sprintf(label, compactHorizonDuration(jobs.config.EstimatedDuration, canonical.Language)), Data: data}, nil
}

// ActionHandler returns a thin per-platform closure. Pass the same
// HorizonJobs instance to both Telegram and VK, but a different platform name
// and adapter messenger to each closure.
func (jobs *HorizonJobs) ActionHandler(platform string, messenger HorizonMessenger) (ActionHandler, error) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if !horizonPlatformPattern.MatchString(platform) {
		return nil, errors.New("invalid horizon platform name")
	}
	if messenger == nil {
		return nil, errors.New("horizon messenger is required")
	}
	return func(ctx context.Context, invocation ActionInvocation, payload string) error {
		return jobs.handleAction(ctx, platform, messenger, invocation, payload)
	}, nil
}

func (jobs *HorizonJobs) handleAction(ctx context.Context, platform string, messenger HorizonMessenger, invocation ActionInvocation, payload string) error {
	request, err := jobs.decodePayload(payload)
	if err != nil {
		return ErrInvalidActionData
	}
	language := languageEnglish
	userID := invocation.Chat.ID
	if invocation.From != nil {
		language = languageFromCode(invocation.From.LanguageCode)
		if invocation.From.ID > 0 {
			userID = invocation.From.ID
		}
	}
	request.Language = language
	// Callback acknowledgement is intentionally before disk/model validation.
	// A failed ACK is not allowed to duplicate or cancel already-safe work.
	if err := messenger.AnswerAction(ctx, invocation.Token, language.text("Проверяю запрос…", "Checking request…")); err != nil {
		jobs.logf("horizon callback acknowledgement failed on %s", platform)
	}
	return jobs.deliverRequest(ctx, platform, messenger, userID, invocation.Chat.ID, request)
}

// Deliver submits the same pinned Horizon calculation used by the signed bot
// action, but from another authenticated application surface such as the web
// account. It deliberately reuses the same cache, per-user admission checks,
// shared directional FIFO, renderer, and Telegram delivery path.
func (jobs *HorizonJobs) Deliver(ctx context.Context, platform string, messenger HorizonMessenger, userID, chatID int64, input HorizonButtonRequest, languageCode string) error {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if !horizonPlatformPattern.MatchString(platform) || messenger == nil || userID <= 0 || chatID <= 0 {
		return errors.New("valid Horizon delivery platform, messenger, and user are required")
	}
	language := languageFromCode(languageCode)
	request, _, err := jobs.canonicalButtonRequest(input, language)
	if err != nil {
		return err
	}
	return jobs.deliverRequest(ctx, platform, messenger, userID, chatID, request)
}

func (jobs *HorizonJobs) deliverRequest(ctx context.Context, platform string, messenger HorizonMessenger, userID, chatID int64, request horizonRequest) error {
	language := request.Language
	if !jobs.isRunning() {
		jobs.sendStatus(messenger, chatID, language.text(
			"Анализ горизонта сейчас недоступен.", "Horizon analysis is currently unavailable."))
		completeHorizonMessenger(messenger, ErrHorizonUnsupported)
		return nil
	}

	currentRun, err := jobs.source.CurrentRunID()
	if err != nil {
		jobs.sendStatus(messenger, chatID, language.text(
			"Сейчас не удалось проверить актуальный ICON-EU run. Попробуйте позже.",
			"The current ICON-EU run could not be checked. Please try again later."))
		completeHorizonMessenger(messenger, err)
		return nil
	}
	if currentRun != request.RunID {
		jobs.sendStatus(messenger, chatID, language.text(
			"Кнопка относится к устаревшему run. Запросите обычный прогноз снова.",
			"This button belongs to an older run. Request the regular forecast again."))
		completeHorizonMessenger(messenger, ErrHorizonStaleAction)
		return nil
	}
	plan, err := forecast.NewHorizonPlan(request.Location, request.ObserverSurfaceElevationM)
	if err != nil || !jobs.source.Supports(plan) {
		jobs.sendStatus(messenger, chatID, language.text(
			"Для этой точки анализ горизонта ICON-EU недоступен.",
			"ICON-EU horizon analysis is unavailable for this location."))
		completeHorizonMessenger(messenger, ErrHorizonUnsupported)
		return nil
	}
	request.Location.TimeZone = jobs.resolveTimeZone(request.Location)
	terrainKey, ready, err := jobs.terrain.CacheKey(request.Location)
	if err != nil {
		jobs.sendStatus(messenger, chatID, language.text(
			"Не удалось подготовить профиль рельефа Copernicus DEM GLO-30.",
			"Could not prepare the Copernicus DEM GLO-30 terrain skyline."))
		completeHorizonMessenger(messenger, err)
		return nil
	}
	request.TerrainPreparationKey = terrainKey
	if ready {
		request.TerrainSkyline, err = jobs.terrain.Resolve(ctx, request.Location)
		if err != nil {
			jobs.sendStatus(messenger, chatID, language.text(
				"Не удалось прочитать профиль рельефа Copernicus DEM GLO-30.",
				"Could not read the Copernicus DEM GLO-30 terrain skyline."))
			completeHorizonMessenger(messenger, err)
			return nil
		}
	} else {
		request.TerrainSkyline = forecast.PendingTerrainSkyline(request.Location)
	}
	key, err := horizonCacheKey(request, jobs.calibration, jobs.config.RenderAlgorithmVersion)
	if err != nil {
		jobs.sendStatus(messenger, chatID, language.text(
			"Не удалось подготовить расчёт горизонта.", "Could not prepare the horizon calculation."))
		completeHorizonMessenger(messenger, err)
		return nil
	}
	waiter := horizonWaiter{
		identity: horizonUserIdentity{platform: platform, userID: userID},
		chatID:   chatID, messenger: messenger, language: language,
	}
	if path, ok := jobs.cache.load(key, jobs.now()); ok {
		if jobs.reserveCached(waiter.identity, key) {
			jobs.sendStatus(messenger, waiter.chatID, language.text(
				"Отправляю готовый расчёт из кэша.", "Sending the completed calculation from cache."))
			if !jobs.scheduleCachedDelivery(waiter, key, path, request.RunID, request.Location.TimeZone) {
				jobs.finishCached(waiter.identity, key)
				jobs.sendStatus(messenger, waiter.chatID, language.text(
					"Очередь отправки занята. Повторите запрос чуть позже.",
					"The delivery queue is busy. Please try again shortly."))
				completeHorizonMessenger(messenger, ErrHorizonQueueFull)
			}
		} else {
			jobs.sendStatus(messenger, waiter.chatID, language.text(
				"Этот запрос уже обрабатывается.", "This request is already being processed."))
			completeHorizonMessenger(messenger, ErrHorizonUserBusy)
		}
		return nil
	}
	if jobs.directional != nil {
		return jobs.handleDirectionalAdmission(ctx, &horizonJob{
			key: key, request: request, plan: plan,
			waiters: map[horizonUserIdentity]horizonWaiter{waiter.identity: waiter},
		}, waiter)
	}
	position, joined, enqueueErr := jobs.enqueue(&horizonJob{
		key: key, request: request, plan: plan,
		waiters: map[horizonUserIdentity]horizonWaiter{waiter.identity: waiter},
	}, waiter)
	switch {
	case errors.Is(enqueueErr, ErrHorizonUserBusy):
		jobs.sendStatus(messenger, waiter.chatID, language.text(
			"У вас уже выполняется анализ горизонта. Дождитесь результата.",
			"You already have a horizon analysis in progress. Please wait for it."))
		completeHorizonMessenger(messenger, enqueueErr)
	case errors.Is(enqueueErr, ErrHorizonQueueFull):
		jobs.sendStatus(messenger, waiter.chatID, language.text(
			"Очередь анализа горизонта заполнена. Попробуйте позже.",
			"The horizon-analysis queue is full. Please try again later."))
		completeHorizonMessenger(messenger, enqueueErr)
	case enqueueErr != nil:
		jobs.sendStatus(messenger, waiter.chatID, language.text(
			"Анализ горизонта сейчас недоступен.", "Horizon analysis is currently unavailable."))
		completeHorizonMessenger(messenger, enqueueErr)
	case joined:
		jobs.sendStatus(messenger, waiter.chatID, language.text(
			"Такой расчёт уже выполняется; результат будет отправлен и вам.",
			"The same calculation is already running; its result will also be sent to you."))
	default:
		eta := jobs.estimatedWait(position)
		jobs.sendStatus(messenger, waiter.chatID, fmt.Sprintf(language.text(
			"Анализ горизонта поставлен в очередь: позиция %d, ориентировочно %s.",
			"Horizon analysis queued: position %d, approximately %s."),
			position, compactHorizonDuration(eta, language)))
	}
	return nil
}

func (jobs *HorizonJobs) canonicalButtonRequest(input HorizonButtonRequest, language userLanguage) (horizonRequest, forecast.HorizonPlan, error) {
	if input.Provider != HorizonProviderICONEU {
		return horizonRequest{}, forecast.HorizonPlan{}, ErrHorizonUnsupported
	}
	request := horizonRequest{
		Provider: input.Provider, RunID: input.RunID,
		Location: forecast.Location{
			Latitude:  math.Round(input.Location.Latitude*1e5) / 1e5,
			Longitude: math.Round(input.Location.Longitude*1e5) / 1e5,
			TimeZone:  input.Location.TimeZone,
		},
		ObserverSurfaceElevationM: math.Round(input.ObserverSurfaceElevationM), Language: language,
	}
	if err := validateHorizonRequest(request); err != nil {
		return horizonRequest{}, forecast.HorizonPlan{}, err
	}
	plan, err := forecast.NewHorizonPlan(request.Location, request.ObserverSurfaceElevationM)
	if err != nil {
		return horizonRequest{}, forecast.HorizonPlan{}, fmt.Errorf("build horizon plan: %w", err)
	}
	return request, plan, nil
}

func validateHorizonRequest(request horizonRequest) error {
	if request.Provider != HorizonProviderICONEU {
		return ErrHorizonUnsupported
	}
	if !horizonRunPattern.MatchString(request.RunID) {
		return ErrInvalidActionData
	}
	runTime, err := time.Parse("2006010215", request.RunID)
	if err != nil || runTime.Format("2006010215") != request.RunID {
		return ErrInvalidActionData
	}
	if err := forecast.ValidateCoordinates(request.Location.Latitude, request.Location.Longitude); err != nil {
		return ErrInvalidActionData
	}
	if !finiteHorizon(request.ObserverSurfaceElevationM) || request.ObserverSurfaceElevationM < -1000 || request.ObserverSurfaceElevationM > 10000 || request.ObserverSurfaceElevationM != math.Round(request.ObserverSurfaceElevationM) {
		return ErrInvalidActionData
	}
	if request.TerrainSkyline.Version != "" {
		if err := request.TerrainSkyline.Validate(); err != nil {
			return ErrInvalidActionData
		}
		if !request.TerrainSkyline.MatchesLocation(request.Location) {
			return ErrInvalidActionData
		}
	}
	return nil
}

func (jobs *HorizonJobs) encodePayload(request horizonRequest) (string, error) {
	if err := validateHorizonRequest(request); err != nil {
		return "", err
	}
	latitude := int64(math.Round((request.Location.Latitude + 90) * 1e5))
	longitude := int64(math.Round((request.Location.Longitude + 180) * 1e5))
	elevation := int64(request.ObserverSurfaceElevationM) + 1000
	core := strings.Join([]string{
		request.RunID,
		fixedBase36(latitude, 5),
		fixedBase36(longitude, 5),
		fixedBase36(elevation, 3),
	}, ".")
	if strings.Contains(core, "!") {
		return "", ErrInvalidActionData
	}
	tag := jobs.payloadTag(core)
	return core + "." + tag, nil
}

func (jobs *HorizonJobs) decodePayload(payload string) (horizonRequest, error) {
	parts := strings.Split(payload, ".")
	if len(parts) != horizonActionCoreParts+1 {
		return horizonRequest{}, ErrInvalidActionData
	}
	core := strings.Join(parts[:horizonActionCoreParts], ".")
	if !hmac.Equal([]byte(parts[horizonActionCoreParts]), []byte(jobs.payloadTag(core))) {
		return horizonRequest{}, ErrInvalidActionData
	}
	if len(parts[0]) != 10 || len(parts[1]) != 5 || len(parts[2]) != 5 || len(parts[3]) != 3 {
		return horizonRequest{}, ErrInvalidActionData
	}
	latitude, ok := parseCanonicalBase36(parts[1])
	if !ok {
		return horizonRequest{}, ErrInvalidActionData
	}
	longitude, ok := parseCanonicalBase36(parts[2])
	if !ok {
		return horizonRequest{}, ErrInvalidActionData
	}
	elevation, ok := parseCanonicalBase36(parts[3])
	if !ok {
		return horizonRequest{}, ErrInvalidActionData
	}
	request := horizonRequest{
		Provider: HorizonProviderICONEU, RunID: parts[0],
		Location: forecast.Location{
			Latitude: float64(latitude)/1e5 - 90, Longitude: float64(longitude)/1e5 - 180,
		},
		ObserverSurfaceElevationM: float64(elevation - 1000),
	}
	if err := validateHorizonRequest(request); err != nil {
		return horizonRequest{}, err
	}
	return request, nil
}

func (jobs *HorizonJobs) payloadTag(core string) string {
	digest := hmac.New(sha256.New, jobs.actionKey)
	_, _ = digest.Write([]byte(core))
	return base64.RawURLEncoding.EncodeToString(digest.Sum(nil)[:horizonActionTagBytes])
}

func fixedBase36(value int64, width int) string {
	if value < 0 {
		return strings.Repeat("!", width)
	}
	encoded := strconv.FormatInt(value, 36)
	if len(encoded) > width {
		return strings.Repeat("!", width)
	}
	return strings.Repeat("0", width-len(encoded)) + encoded
}

func parseCanonicalBase36(value string) (int64, bool) {
	if value == "" || value != strings.ToLower(value) {
		return 0, false
	}
	parsed, err := strconv.ParseInt(value, 36, 64)
	if err != nil || fixedBase36(parsed, len(value)) != value {
		return 0, false
	}
	return parsed, true
}

func loadOrCreateHorizonActionKey(root string) ([]byte, error) {
	path := filepath.Join(root, horizonActionKeyFile)
	readExisting := func() ([]byte, error) {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("horizon action key is not a regular file")
		}
		key, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if len(key) != 32 {
			return nil, errors.New("horizon action key has invalid length")
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("restrict horizon action key permissions: %w", err)
		}
		return key, nil
	}
	if key, err := readExisting(); err == nil {
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read horizon action key: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate horizon action key: %w", err)
	}
	temporary, err := os.CreateTemp(root, ".horizon-action-key-")
	if err != nil {
		return nil, fmt.Errorf("create horizon action key: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() {
		_ = os.Remove(temporaryName)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return nil, err
	}
	if _, err := temporary.Write(key); err != nil {
		_ = temporary.Close()
		return nil, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return nil, err
	}
	if err := temporary.Close(); err != nil {
		return nil, err
	}
	// Link publishes the fully-synced temporary inode without replacing a key
	// that another concurrent initializer may already have created.
	if err := os.Link(temporaryName, path); err != nil {
		existing, readErr := readExisting()
		if readErr == nil {
			return existing, nil
		}
		return nil, fmt.Errorf("publish horizon action key: %w", err)
	}
	return key, nil
}

func (jobs *HorizonJobs) resolveTimeZone(location forecast.Location) string {
	if jobs.resolver == nil {
		return "UTC"
	}
	return jobs.resolver.Resolve(location.Latitude, location.Longitude)
}

func horizonCacheKey(request horizonRequest, calibration forecast.OverallIndexCalibration, renderAlgorithmVersion string) (string, error) {
	if err := validateHorizonRequest(request); err != nil {
		return "", err
	}
	if strings.TrimSpace(request.TerrainPreparationKey) == "" {
		return "", ErrInvalidActionData
	}
	calibrationJSON, err := json.Marshal(calibration)
	if err != nil {
		return "", err
	}
	identity := strings.Join([]string{
		horizonCacheSchema,
		"provider=" + HorizonProviderICONEU,
		"run=" + request.RunID,
		"window=f001-f072-hourly",
		fmt.Sprintf("lat_e5=%d", int64(math.Round(request.Location.Latitude*1e5))),
		fmt.Sprintf("lon_e5=%d", int64(math.Round(request.Location.Longitude*1e5))),
		forecastLocationCacheIdentity(request.Location),
		fmt.Sprintf("hhl_m=%d", int64(math.Round(request.ObserverSurfaceElevationM))),
		"timezone=" + strings.TrimSpace(request.Location.TimeZone),
		"forecast=" + forecast.HorizonAlgorithmVersion,
		"terrain_preparation=" + request.TerrainPreparationKey,
		"terrain_profile=" + horizonTerrainCacheIdentity(request.TerrainSkyline),
		"grid_profile=" + forecast.HorizonGridProfile,
		"render=" + strings.TrimSpace(renderAlgorithmVersion),
		fmt.Sprintf("geometric_elevation=%.6f", forecast.HorizonGeometricElevationDegrees),
		fmt.Sprintf("earth_radius=%.3f", forecast.HorizonEarthRadiusM),
		fmt.Sprintf("atmosphere_top=%.3f", forecast.HorizonAtmosphereTopM),
		fmt.Sprintf("segment=%.3f", forecast.HorizonSurfaceSegmentLengthM),
		"calibration=" + string(calibrationJSON),
		"language=" + request.Language.renderCode(),
	}, "|")
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:]), nil
}

func horizonTerrainCacheIdentity(profile forecast.TerrainSkyline) string {
	switch profile.Source {
	case "disabled":
		return profile.Version + ":disabled"
	case "pending":
		return profile.Version + ":pending"
	default:
		return profile.Version + ":profile_sha256=" + profile.DigestSHA256 + ":manifest_sha256=" + profile.InputManifestSHA256
	}
}

func horizonRequestFamilyKey(request horizonRequest, calibration forecast.OverallIndexCalibration, renderAlgorithmVersion string) (string, error) {
	request.TerrainSkyline = forecast.PendingTerrainSkyline(request.Location)
	return horizonCacheKey(request, calibration, renderAlgorithmVersion)
}

func (jobs *HorizonJobs) enqueue(candidate *horizonJob, waiter horizonWaiter) (position int, joined bool, err error) {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if !jobs.started || jobs.closed || jobs.root == nil || jobs.root.Err() != nil {
		return 0, false, errors.New("horizon jobs are not running")
	}
	jobs.pruneRecentLocked(jobs.now())
	if activeKey, ok := jobs.active[waiter.identity]; ok {
		if activeKey == candidate.key {
			return 0, true, ErrHorizonUserBusy
		}
		return 0, false, ErrHorizonUserBusy
	}
	if recent, ok := jobs.recent[waiter.identity]; ok && recent.key == candidate.key && jobs.now().Sub(recent.at) < horizonClickCooldown {
		return 0, true, ErrHorizonUserBusy
	}
	if existing, ok := jobs.jobs[candidate.key]; ok {
		if len(existing.waiters) >= jobs.maximumWaiters() {
			return 0, false, ErrHorizonQueueFull
		}
		existing.waiters[waiter.identity] = waiter
		jobs.active[waiter.identity] = candidate.key
		return 0, true, nil
	}
	jobs.jobs[candidate.key] = candidate
	jobs.active[waiter.identity] = candidate.key
	position = len(jobs.queue) + 1
	select {
	case jobs.queue <- candidate:
		return position, false, nil
	default:
		delete(jobs.jobs, candidate.key)
		delete(jobs.active, waiter.identity)
		return 0, false, ErrHorizonQueueFull
	}
}

func (jobs *HorizonJobs) reserveCached(identity horizonUserIdentity, key string) bool {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if !jobs.started || jobs.closed || jobs.root == nil || jobs.root.Err() != nil {
		return false
	}
	now := jobs.now()
	jobs.pruneRecentLocked(now)
	if _, exists := jobs.active[identity]; exists {
		return false
	}
	if recent, exists := jobs.recent[identity]; exists && recent.key == key && now.Sub(recent.at) < horizonClickCooldown {
		return false
	}
	jobs.active[identity] = key
	return true
}

func (jobs *HorizonJobs) isRunning() bool {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	return jobs.started && !jobs.closed && jobs.root != nil && jobs.root.Err() == nil
}

func (jobs *HorizonJobs) maximumWaiters() int {
	maximum := jobs.config.QueueSize * 32
	if maximum < 32 {
		return 32
	}
	return maximum
}

func (jobs *HorizonJobs) estimatedWait(position int) time.Duration {
	jobs.mu.Lock()
	running := jobs.running
	jobs.mu.Unlock()
	jobsThroughCompletion := running + position
	batches := (jobsThroughCompletion + jobs.config.Concurrency - 1) / jobs.config.Concurrency
	if batches < 1 {
		batches = 1
	}
	return time.Duration(batches) * jobs.config.EstimatedDuration
}

func (jobs *HorizonJobs) worker() {
	defer jobs.wait.Done()
	sweepInterval := jobs.config.CacheTTL / 4
	if sweepInterval <= 0 || sweepInterval > horizonCacheSweepMax {
		sweepInterval = horizonCacheSweepMax
	}
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-jobs.root.Done():
			jobs.discardQueued()
			return
		case <-ticker.C:
			if err := jobs.cache.cleanup(jobs.now()); err != nil {
				jobs.logf("horizon cache cleanup failed")
			}
		case job := <-jobs.queue:
			if job == nil {
				continue
			}
			jobs.mu.Lock()
			jobs.running++
			jobs.mu.Unlock()
			jobs.process(job)
			jobs.mu.Lock()
			jobs.running--
			jobs.mu.Unlock()
		}
	}
}

func (jobs *HorizonJobs) process(job *horizonJob) {
	currentRun, err := jobs.source.CurrentRunID()
	if err != nil || currentRun != job.request.RunID {
		if err == nil {
			err = ErrHorizonStaleAction
		}
		jobs.logJobError("run_precheck", job, err)
		jobs.deliverJob(job, "", err)
		return
	}
	if path, ok := jobs.cache.load(job.key, jobs.now()); ok {
		jobs.deliverJob(job, path, nil)
		return
	}
	ctx, cancel := context.WithTimeout(jobs.root, jobs.config.JobTimeout)
	defer cancel()
	if job.request.TerrainSkyline.Source == "pending" {
		key, _, keyErr := jobs.terrain.CacheKey(job.request.Location)
		if keyErr != nil || key != job.request.TerrainPreparationKey {
			err := errors.New("terrain preparation identity differs from runner configuration")
			jobs.logJobError("terrain_identity", job, err)
			jobs.deliverJob(job, "", err)
			return
		}
		resolved, resolveErr := jobs.terrain.Resolve(ctx, job.request.Location)
		if resolveErr != nil {
			jobs.logJobError("terrain_preparation", job, resolveErr)
			jobs.deliverJob(job, "", resolveErr)
			return
		}
		job.request.TerrainSkyline = resolved
	}
	finalKey, keyErr := horizonCacheKey(job.request, jobs.calibration, jobs.config.RenderAlgorithmVersion)
	if keyErr != nil {
		jobs.logJobError("terrain_identity", job, keyErr)
		jobs.deliverJob(job, "", keyErr)
		return
	}
	publicationKey := finalKey
	if path, ok := jobs.cache.load(publicationKey, jobs.now()); ok {
		jobs.deliverJob(job, path, nil)
		return
	}
	snapshots, err := jobs.source.Series(ctx, job.request.RunID, job.plan)
	if err != nil {
		jobs.logJobError("source", job, err)
		jobs.deliverJob(job, "", err)
		return
	}
	if err := ctx.Err(); err != nil {
		jobs.logJobError("source", job, err)
		jobs.deliverJob(job, "", err)
		return
	}
	if err := validateHorizonSeries(job.request.RunID, snapshots); err != nil {
		jobs.logJobError("series_validation", job, err)
		jobs.deliverJob(job, "", err)
		return
	}
	frames, err := jobs.compute(ctx, snapshots, job.plan, jobs.calibration)
	if err != nil {
		jobs.logJobError("calculation", job, err)
		jobs.deliverJob(job, "", err)
		return
	}
	if err := forecast.ApplyTerrainSkylineToHorizon(frames, job.request.TerrainSkyline); err != nil {
		jobs.logJobError("terrain", job, err)
		jobs.deliverJob(job, "", err)
		return
	}
	if err := ctx.Err(); err != nil {
		jobs.logJobError("calculation", job, err)
		jobs.deliverJob(job, "", err)
		return
	}
	path, err := jobs.cache.publishBundle(publicationKey, jobs.now(), func(destination, datasetDestination string) error {
		if renderErr := jobs.render(ctx, destination, HorizonRenderInput{
			Location: job.request.Location, Provider: "ICON-EU",
			RunID: job.request.RunID, Grid: "ICON-EU 0.0625°", Frames: frames,
			TerrainSkyline: job.request.TerrainSkyline,
		}, job.request.Language.renderCode()); renderErr != nil {
			return renderErr
		}
		dataset, datasetErr := render.PrepareHorizonInteractiveDataset(render.HorizonInput{
			Location: job.request.Location, Provider: "ICON-EU", RunID: job.request.RunID,
			Grid: "ICON-EU 0.0625°", Frames: frames, TerrainSkyline: job.request.TerrainSkyline,
		}, publicationKey, job.request.ObserverSurfaceElevationM, jobs.calibration)
		if datasetErr != nil {
			return datasetErr
		}
		if datasetErr := render.SaveHorizonInteractiveDataset(datasetDestination, dataset); datasetErr != nil {
			return datasetErr
		}
		return ctx.Err()
	})
	if err != nil {
		jobs.logJobError("render", job, err)
		jobs.deliverJob(job, "", err)
		return
	}
	currentRun, err = jobs.source.CurrentRunID()
	if err != nil || currentRun != job.request.RunID {
		if err == nil {
			err = ErrHorizonStaleAction
		}
		jobs.logJobError("run_recheck", job, err)
		jobs.deliverJob(job, "", err)
		return
	}
	jobs.deliverJob(job, path, nil)
}

func validateHorizonSeries(runID string, snapshots []forecast.HorizonSnapshot) error {
	runTime, err := time.Parse("2006010215", runID)
	if err != nil {
		return ErrInvalidActionData
	}
	if len(snapshots) != horizonForecastHours {
		return fmt.Errorf("horizon series must contain %d hourly snapshots (f001..f072)", horizonForecastHours)
	}
	for index, snapshot := range snapshots {
		expected := runTime.Add(time.Duration(index+1) * time.Hour)
		if !snapshot.ValidAt.Equal(expected) {
			return fmt.Errorf("horizon series frame %d is not the required hourly run term", index)
		}
	}
	return nil
}

func (jobs *HorizonJobs) deliverJob(job *horizonJob, path string, jobErr error) {
	jobs.mu.Lock()
	if jobs.jobs[job.key] == job {
		delete(jobs.jobs, job.key)
	}
	waiters := make([]horizonWaiter, 0, len(job.waiters))
	for _, waiter := range job.waiters {
		waiters = append(waiters, waiter)
	}
	jobs.mu.Unlock()
	leased := false
	var dataset []byte
	if jobErr == nil {
		if filepath.Base(path) == horizonCacheImage {
			dataset, jobErr = os.ReadFile(filepath.Join(filepath.Dir(path), horizonCacheDataset))
			if jobErr == nil {
				path, jobErr = jobs.cache.lease(path)
			}
		} else {
			var cleanup func()
			path, dataset, cleanup, jobErr = readHorizonBundle(path, jobs.cache.root)
			_ = cleanup
		}
		leased = jobErr == nil
	}
	delivery := horizonDelivery{
		key: job.key, path: path, dataset: dataset, runID: job.request.RunID,
		timeZone: job.request.Location.TimeZone, waiters: waiters, jobErr: jobErr, leased: leased,
	}
	select {
	case jobs.delivery <- delivery:
	case <-jobs.root.Done():
		if leased {
			_ = os.Remove(path)
		}
		jobs.finishJob(job.key, waiters)
	default:
		if leased {
			_ = os.Remove(path)
		}
		jobs.logf("horizon delivery queue is full; completed result remains available in cache")
		jobs.finishJob(job.key, waiters)
	}
}

func (jobs *HorizonJobs) scheduleCachedDelivery(waiter horizonWaiter, key, path, runID, timeZone string) bool {
	jobs.mu.Lock()
	root := jobs.root
	closed := jobs.closed
	jobs.mu.Unlock()
	if closed || root == nil || root.Err() != nil {
		return false
	}
	select {
	case jobs.cacheDeliverySlots <- struct{}{}:
	default:
		return false
	}
	dataset, err := os.ReadFile(filepath.Join(filepath.Dir(path), horizonCacheDataset))
	if err != nil {
		<-jobs.cacheDeliverySlots
		return false
	}
	lease, err := jobs.cache.lease(path)
	if err != nil {
		<-jobs.cacheDeliverySlots
		return false
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if jobs.closed || jobs.root != root || root.Err() != nil {
		_ = os.Remove(lease)
		<-jobs.cacheDeliverySlots
		return false
	}
	select {
	case jobs.delivery <- horizonDelivery{
		key: key, path: lease, dataset: dataset, runID: runID, timeZone: timeZone,
		waiters: []horizonWaiter{waiter}, leased: true, cacheSlot: true,
	}:
		return true
	default:
		_ = os.Remove(lease)
		<-jobs.cacheDeliverySlots
		return false
	}
}

func (jobs *HorizonJobs) deliveryWorker() {
	defer jobs.wait.Done()
	for {
		select {
		case <-jobs.root.Done():
			return
		case delivery := <-jobs.delivery:
			jobs.performDelivery(delivery)
		}
	}
}

func (jobs *HorizonJobs) performDelivery(delivery horizonDelivery) {
	if delivery.leased {
		defer func() { _ = os.Remove(delivery.path) }()
	}
	if delivery.cacheSlot {
		defer func() { <-jobs.cacheDeliverySlots }()
	}
	batchContext, cancelBatch := context.WithTimeout(jobs.root, horizonDeliveryBatch)
	defer cancelBatch()
	sourceError := delivery.jobErr
	for _, waiter := range delivery.waiters {
		if batchContext.Err() != nil {
			jobs.logf("horizon delivery batch exceeded its aggregate time limit")
			break
		}
		waiterError := sourceError
		if waiterError == nil {
			currentRun, err := jobs.source.CurrentRunID()
			if err != nil || currentRun != delivery.runID {
				waiterError = ErrHorizonStaleAction
				jobs.logf("horizon result rejected immediately before delivery because the current run could not be confirmed")
			}
		}
		ctx, cancel := context.WithTimeout(batchContext, horizonDeliveryTimeout)
		if waiterError != nil {
			text := waiter.language.text(
				"Не удалось рассчитать условия у горизонта. Обычный прогноз остаётся доступен.",
				"Horizon conditions could not be calculated. The regular forecast remains available.")
			switch {
			case errors.Is(waiterError, ErrHorizonStaleAction):
				text = waiter.language.text(
					"Расчёт относится к устаревшему run. Запросите обычный прогноз снова.",
					"This calculation belongs to an older run. Request the regular forecast again.")
			case errors.Is(waiterError, context.DeadlineExceeded), errors.Is(waiterError, context.Canceled):
				text = waiter.language.text(
					"Расчёт условий у горизонта превысил лимит времени. Попробуйте позже.",
					"The horizon calculation exceeded its time limit. Please try again later.")
			}
			if err := waiter.messenger.SendMessage(ctx, waiter.chatID, text, false); err != nil {
				jobs.logf("horizon failure notification failed on %s", waiter.identity.platform)
			}
			completeHorizonMessenger(waiter.messenger, waiterError)
		} else {
			if sink, ok := waiter.messenger.(HorizonDatasetMessenger); ok {
				waiterError = sink.SendHorizonDataset(ctx, delivery.dataset)
			}
			if waiterError == nil {
				waiterError = waiter.messenger.SendPhoto(ctx, waiter.chatID, delivery.path, horizonCaption(
					delivery.runID, delivery.timeZone, jobs.now(), jobs.config.MaxStaleAge, waiter.language,
				))
			}
			if waiterError != nil {
				jobs.logf("horizon result delivery failed on %s", waiter.identity.platform)
			}
			completeHorizonMessenger(waiter.messenger, waiterError)
		}
		cancel()
	}
	jobs.finishJob(delivery.key, delivery.waiters)
}

func completeHorizonMessenger(messenger HorizonMessenger, err error) {
	if completion, ok := messenger.(HorizonCompletionMessenger); ok {
		completion.CompleteHorizon(err)
	}
}

func (jobs *HorizonJobs) finishJob(key string, waiters []horizonWaiter) {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	now := jobs.now()
	for _, waiter := range waiters {
		if jobs.active[waiter.identity] == key {
			delete(jobs.active, waiter.identity)
			jobs.recent[waiter.identity] = horizonRecentClick{key: key, at: now}
		}
	}
	jobs.pruneRecentLocked(now)
}

func (jobs *HorizonJobs) finishCached(identity horizonUserIdentity, key string) {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if jobs.active[identity] == key {
		delete(jobs.active, identity)
		jobs.recent[identity] = horizonRecentClick{key: key, at: jobs.now()}
	}
}

func (jobs *HorizonJobs) discardQueued() {
	for {
		select {
		case job := <-jobs.queue:
			if job == nil {
				continue
			}
			jobs.mu.Lock()
			delete(jobs.jobs, job.key)
			for identity := range job.waiters {
				delete(jobs.active, identity)
			}
			jobs.mu.Unlock()
		default:
			return
		}
	}
}

func (jobs *HorizonJobs) discardDeliveries() {
	for {
		select {
		case delivery := <-jobs.delivery:
			if delivery.leased {
				_ = os.Remove(delivery.path)
			}
			if delivery.cacheSlot {
				<-jobs.cacheDeliverySlots
			}
			jobs.finishJob(delivery.key, delivery.waiters)
		default:
			return
		}
	}
}

func (jobs *HorizonJobs) pruneRecentLocked(now time.Time) {
	for identity, recent := range jobs.recent {
		if now.Sub(recent.at) >= horizonClickCooldown {
			delete(jobs.recent, identity)
		}
	}
}

func (jobs *HorizonJobs) sendStatus(messenger HorizonMessenger, chatID int64, text string) {
	jobs.mu.Lock()
	root := jobs.root
	jobs.mu.Unlock()
	if root == nil {
		root = context.Background()
	}
	ctx, cancel := context.WithTimeout(root, 30*time.Second)
	defer cancel()
	if err := messenger.SendMessage(ctx, chatID, text, false); err != nil {
		jobs.logf("horizon status delivery failed")
	}
}

func horizonCaption(runID, timeZone string, now time.Time, maxStaleAge time.Duration, language userLanguage) string {
	zone, err := time.LoadLocation(strings.TrimSpace(timeZone))
	if err != nil {
		zone = time.UTC
	}
	runTime, err := time.Parse("2006010215", runID)
	if err != nil {
		return language.text("Условия у горизонта на 10°", "Horizon conditions at 10°")
	}
	start := runTime.Add(time.Hour)
	end := runTime.Add(horizonForecastHours * time.Hour)
	caption := fmt.Sprintf(language.text(
		"Условия у горизонта на 10° · почасово: %s — %s",
		"Horizon conditions at 10° · hourly: %s — %s"),
		start.In(zone).Format("02.01 15:04 MST"), end.In(zone).Format("02.01 15:04 MST"))
	return caption + fmt.Sprintf(language.text(
		"\nICON-EU run %s UTC · %s",
		"\nICON-EU run %s UTC · %s"), runID, forecastFreshnessText(runTime, now, maxStaleAge, language))
}

func (jobs *HorizonJobs) logJobError(stage string, job *horizonJob, err error) {
	jobs.logf("horizon job stage=%s run=%s timeout=%t error_type=%T",
		stage, job.request.RunID,
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled), err)
}

func compactHorizonDuration(duration time.Duration, language userLanguage) string {
	minutes := int(math.Ceil(duration.Minutes()))
	if minutes < 1 {
		minutes = 1
	}
	if language == languageEnglish {
		return fmt.Sprintf("%d min", minutes)
	}
	return fmt.Sprintf("%d мин", minutes)
}

func finiteHorizon(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
