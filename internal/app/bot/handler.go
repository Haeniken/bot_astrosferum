package bot

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"bot_astrosferum/internal/astronomy"
	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/lightpollution"
	"bot_astrosferum/internal/render"
	"bot_astrosferum/internal/store"
)

const StartHelp = `Привет! Я строю астрономический прогноз для выбранной точки.

Как запросить:
• нажмите «📍 Отправить геопозицию»;
• отправьте 59.9386, 30.3141 или /forecast 59.9386 30.3141;
• можно сохранить до 10 точек: «💾 Сохранить координаты», «📌 Мои точки», /deletepoint N.

Вне ICON-EU используется ICON Global. Там нативный TKE доступен до +48 ч, поэтому Overall короче остальных графиков.

Как читать результат:

Высотные карты: по горизонтали — местное время; слева давление, справа высота ICON (850 hPa ≈ 1,5 км); светлее — больше, шкала снизу.

1. Погода на 72 часа: облака, ветер, влажность, T−Td, эвристика тумана и светила. Капля — защита от росы; это не означает плохой сиинг. «Эвристика прозрачности» сортирует часы по облакам, VIS и PWV; это не оптическое пропускание.

Облака: нижние закрывают объект и отражают засветку; средние гасят сигнал и контраст; верхние повышают фон и портят длинные выдержки/фотометрию. Покрытие: белое <10%, синее 10–49%, оранжевое ≥50%; облачность не меняет wind-based seeing.

2. Overall Astronomy Index, 1…10: снизу сохранившаяся пригодность; цветные сегменты точно разлагают потери от сиинга, облаков, ветра, эвристики тумана и осадков; сумма до 10. Осадки выше порога дают индекс 1; «!» — неполные данные.

Кольцо «Эталон V, зенит» — только при Солнце <−18°: PWV/AOD/O₃/Луна/PSF-сиинг, без засветки. Нет GEOS-CF — нет кольца; Overall доступен.

3. Эффективная облачная преграда ICON — CLC+QC/QI и толщина: 0% почти ясно, 100% непрозрачно; тонкие верхние облака слабее плотных нижних.

4. Wind Speed — ветер по высоте; сильный поток на 300–200 hPa часто означает струйное течение, у земли может раскачивать телескоп.

5. Vector Wind Shear, m/s/km — векторный сдвиг на фактический километр высоты; больше — выше риск турбулентности.

6. Wind Direction Delta — поворот ветра; ниже 2 м/с показан 0°, потому что направление почти штилевого потока неустойчиво и малозначимо.

7. Forecast Wind Seeing Index, 1…10 — оценка только по ветру. Процент — эвристика качества по сроку, не статистическая уверенность; в Overall Index она не входит.

Засветка: LPI/SQM по Atlas 2024 и World Atlas 2015; Бортль — ориентир по зениту, в Overall не входит.

Время — в часовой зоне точки. Сиинг — модельная оценка, не DIMM и не шкала Пикеринга.`

const NotReadyText = `Координаты распознаны, но выдача рабочего ICON-прогноза ещё разворачивается. Синтетические данные я пользователям не отправляю.`

const (
	defaultForecastMaxStaleAge = 12 * time.Hour
	// GEOS-CF is an independent, optional enrichment. A cold public OPeNDAP
	// request may continue in the background and warm its bounded RAM cache,
	// but it must not add an unbounded delay to the primary ICON forecast.
	atmosphericCompositionJoinTimeout      = 5 * time.Second
	atmosphericCompositionOperationTimeout = 65 * time.Second
)

type Messenger interface {
	SendMessage(ctx context.Context, chatID int64, text string, locationButton bool) error
	SendPhoto(ctx context.Context, chatID int64, path, caption string) error
	SendDocument(ctx context.Context, chatID int64, path, caption string) error
}

// ForecastDatasetMessenger is implemented only by the website result sink.
// Platform adapters continue receiving the original PNG files.
type ForecastDatasetMessenger interface {
	SendForecastDataset(context.Context, string) error
}

// ForecastStatusMessenger is implemented by the website capture only. It
// switches the presentation ETA to the configured warm duration after an
// exact immutable render-cache hit has been established.
type ForecastStatusMessenger interface {
	UpdateForecastCacheStatus(bool)
}

type KeyboardMessenger interface {
	SendMessageWithKeyboard(context.Context, int64, string, Keyboard) error
}

type HTMLKeyboardMessenger interface {
	SendHTMLMessageWithKeyboard(context.Context, int64, string, Keyboard) error
}

type Persistence interface {
	TouchUser(context.Context, int64) error
	SavePoint(context.Context, int64, string, float64, float64) error
	Points(context.Context, int64) ([]store.Point, error)
	DeletePoint(context.Context, int64, int64) (bool, error)
	RecordForecast(context.Context, int64, bool) error
	Stats(context.Context) (int64, []store.DailyUsage, error)
}

type saveSession struct {
	Stage               int
	Latitude, Longitude float64
	UpdatedAt           time.Time
}

type ForecastProvider interface {
	Vertical(ctx context.Context, location forecast.Location) (forecast.VerticalSeries, error)
	Surface(ctx context.Context, location forecast.Location) (forecast.SurfaceSeries, error)
	Cloud(ctx context.Context, location forecast.Location) (forecast.CloudSeries, error)
}

type LightPollutionProvider interface {
	At(ctx context.Context, latitude, longitude float64) (lightpollution.Estimate, error)
}

type AtmosphericCompositionProvider interface {
	AtmosphericComposition(ctx context.Context, location forecast.Location, validTimes []time.Time) (forecast.AtmosphericCompositionSeries, error)
}

type compositionResult struct {
	series forecast.AtmosphericCompositionSeries
	err    error
}

type Handler struct {
	messenger                     Messenger
	resolver                      *forecast.TimeZoneResolver
	provider                      ForecastProvider
	renderRoot                    string
	renderCacheRoot               string
	renderOptions                 render.Options
	overallCalibration            forecast.OverallIndexCalibration
	forecastMaxStaleAge           time.Duration
	fallbackMaxStaleAge           time.Duration
	lightPollution                LightPollutionProvider
	worldAtlas2015                LightPollutionProvider
	atmosphericComposition        AtmosphericCompositionProvider
	atmosphericCompositionContext context.Context
	atmosphericCompositionJoin    time.Duration
	atmosphericCompositionTimeout time.Duration
	persistence                   Persistence
	admins                        map[int64]struct{}
	actions                       ActionRouter
	horizon                       *HorizonJobs
	forecastQueue                 *ForecastQueue
	sessionMu                     sync.Mutex
	sessions                      map[int64]saveSession
	logf                          func(string, ...any)
	requestSequence               atomic.Uint64
}

func (handler *Handler) EnableForecast(provider ForecastProvider, renderRoot string, options render.Options) error {
	if provider == nil {
		return fmt.Errorf("forecast provider is required")
	}
	if strings.TrimSpace(renderRoot) == "" {
		return fmt.Errorf("render root is required")
	}
	handler.provider = provider
	handler.renderRoot = renderRoot
	handler.renderOptions = options
	if err := os.MkdirAll(renderRoot, 0o750); err != nil {
		return fmt.Errorf("create render root: %w", err)
	}
	pruneRenderCache(renderRoot, time.Hour, 0)
	return nil
}

func (handler *Handler) EnableForecastQueue(queue *ForecastQueue) error {
	if queue == nil {
		return errors.New("forecast queue is required")
	}
	handler.forecastQueue = queue
	return nil
}

func (handler *Handler) EnableRenderCache(root string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("render cache root is required")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return fmt.Errorf("create render cache root: %w", err)
	}
	pruneRenderCache(root, 48*time.Hour, 256)
	handler.renderCacheRoot = root
	return nil
}

func (handler *Handler) SetLogger(logf func(string, ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	handler.logf = logf
}

func NewHandler(messenger Messenger) (*Handler, error) {
	resolver, err := forecast.NewTimeZoneResolver()
	if err != nil {
		return nil, err
	}
	return &Handler{
		messenger: messenger, resolver: resolver,
		overallCalibration:  forecast.DefaultOverallIndexCalibration(),
		forecastMaxStaleAge: defaultForecastMaxStaleAge, fallbackMaxStaleAge: 18 * time.Hour,
		atmosphericCompositionJoin:    atmosphericCompositionJoinTimeout,
		atmosphericCompositionTimeout: atmosphericCompositionOperationTimeout,
		logf:                          func(string, ...any) {}, admins: map[int64]struct{}{}, actions: ActionRouter{}, sessions: map[int64]saveSession{},
	}, nil
}

func (handler *Handler) EnableActions(actions ActionRouter) error {
	if len(actions) == 0 {
		return errors.New("at least one action is required")
	}
	for id, action := range actions {
		if !validActionID(id) || action == nil {
			return fmt.Errorf("invalid action registration %q", id)
		}
		if _, exists := handler.actions[id]; exists {
			return fmt.Errorf("action %q is already registered", id)
		}
		handler.actions[id] = action
	}
	return nil
}

// EnableHorizon attaches one shared heavy-job service to this platform's thin
// handler. Telegram and VK call it with the same HorizonJobs instance and
// their own platform name/messenger adapter.
func (handler *Handler) EnableHorizon(platform string, jobs *HorizonJobs) error {
	if jobs == nil {
		return errors.New("horizon jobs are required")
	}
	messenger, ok := handler.messenger.(HorizonMessenger)
	if !ok {
		return errors.New("messenger does not support horizon actions")
	}
	action, err := jobs.ActionHandler(platform, messenger)
	if err != nil {
		return err
	}
	if err := handler.EnableActions(ActionRouter{HorizonActionID: action}); err != nil {
		return err
	}
	handler.horizon = jobs
	return nil
}

func (handler *Handler) SetForecastMaxStaleAge(maxAge time.Duration) error {
	if maxAge <= 0 {
		return errors.New("forecast maximum stale age must be positive")
	}
	handler.forecastMaxStaleAge = maxAge
	return nil
}

func (handler *Handler) SetFallbackForecastMaxStaleAge(maxAge time.Duration) error {
	if maxAge <= 0 {
		return errors.New("fallback forecast maximum stale age must be positive")
	}
	handler.fallbackMaxStaleAge = maxAge
	return nil
}

func (handler *Handler) EnablePersistence(p Persistence, adminIDs []int64) error {
	if p == nil {
		return errors.New("persistence is required")
	}
	handler.persistence = p
	for _, id := range adminIDs {
		handler.admins[id] = struct{}{}
	}
	return nil
}

func (handler *Handler) EnableWorldAtlas2015(p LightPollutionProvider) error {
	if p == nil {
		return errors.New("world atlas 2015 provider is required")
	}
	handler.worldAtlas2015 = p
	return nil
}

func (handler *Handler) SetOverallIndexCalibration(calibration forecast.OverallIndexCalibration) error {
	if err := calibration.Validate(); err != nil {
		return err
	}
	handler.overallCalibration = calibration
	return nil
}

func (handler *Handler) EnableLightPollution(provider LightPollutionProvider) error {
	if provider == nil {
		return fmt.Errorf("light-pollution provider is required")
	}
	handler.lightPollution = provider
	return nil
}

func (handler *Handler) EnableAtmosphericComposition(rootContext context.Context, provider AtmosphericCompositionProvider) error {
	if rootContext == nil {
		return fmt.Errorf("atmospheric-composition root context is required")
	}
	if provider == nil {
		return fmt.Errorf("atmospheric-composition provider is required")
	}
	handler.atmosphericComposition = provider
	handler.atmosphericCompositionContext = rootContext
	return nil
}

func (handler *Handler) Handle(ctx context.Context, update Update) error {
	if update.Action != nil {
		return handler.handleAction(ctx, *update.Action)
	}
	if update.Message == nil {
		return nil
	}
	message := update.Message
	userID := message.Chat.ID
	language := languageEnglish
	if message.From != nil {
		language = languageFromCode(message.From.LanguageCode)
		if message.From.ID > 0 {
			userID = message.From.ID
		}
	}
	if handler.persistence != nil {
		if err := handler.persistence.TouchUser(ctx, userID); err != nil {
			handler.logf("touch platform user: %v", err)
		}
	}
	text := strings.TrimSpace(message.Text)
	command := ""
	if fields := strings.Fields(text); len(fields) > 0 {
		command = strings.ToLower(fields[0])
	}
	if at := strings.IndexByte(command, '@'); at >= 0 {
		command = command[:at]
	}
	if command == "/start" || command == "/help" {
		return handler.sendMainKeyboard(ctx, message.Chat.ID, startHelp(language, handler.horizon != nil), userID, language)
	}
	if command == "/cancel" {
		handler.clearSession(userID)
		return handler.sendMainKeyboard(ctx, message.Chat.ID, language.text("Сохранение отменено.", "Saving cancelled."), userID, language)
	}
	if isButton(text, "⬅️ Назад", "⬅️ Back") {
		handler.clearSession(userID)
		return handler.sendMainKeyboard(ctx, message.Chat.ID, language.text("Главное меню.", "Main menu."), userID, language)
	}
	if command == "/admin" || command == "/stats" || isButton(text, "📊 Статистика", "📊 Statistics") {
		return handler.replyAdminStats(ctx, message.Chat.ID, userID, language)
	}
	if command == "/points" || isButton(text, "📌 Мои точки", "📌 My locations") {
		return handler.replyPoints(ctx, message.Chat.ID, userID, language)
	}
	if command == "/deletepoint" {
		return handler.deletePoint(ctx, message.Chat.ID, userID, text, language)
	}
	if command == "/savepoint" || isButton(text, "💾 Сохранить координаты", "💾 Save coordinates") {
		handler.setSession(userID, saveSession{Stage: 1})
		return handler.sendSaveKeyboard(ctx, message.Chat.ID, language.text(
			"Отправьте геопозицию или координаты текстом (например, 59.9386, 30.3141). Для отмены: /cancel",
			"Share a location or send coordinates as text (for example, 59.9386, 30.3141). To cancel: /cancel"), language)
	}
	if point, ok, err := handler.selectedPoint(ctx, userID, text); err != nil {
		return err
	} else if ok {
		return handler.replyToLocation(ctx, message.Chat.ID, userID, point.Latitude, point.Longitude, language)
	}
	if session, ok := handler.getSession(userID); ok {
		return handler.handleSaveSession(ctx, message, userID, session, language)
	}

	if message.Location != nil {
		return handler.replyToLocation(ctx, message.Chat.ID, userID, message.Location.Latitude, message.Location.Longitude, language)
	}
	if text == "" {
		return nil
	}
	latitude, longitude, err := forecast.ParseLocationText(text)
	if err != nil {
		if command == "/forecast" {
			return handler.sendUserMessage(ctx, message.Chat.ID, language.text(
				"Не удалось прочитать координаты. Пример: /forecast 59.9386 30.3141",
				"Could not parse the coordinates. Example: /forecast 59.9386 30.3141"), true, language)
		}
		return nil
	}
	return handler.replyToLocation(ctx, message.Chat.ID, userID, latitude, longitude, language)
}

func (handler *Handler) handleAction(ctx context.Context, invocation ActionInvocation) error {
	messenger, ok := handler.messenger.(ActionMessenger)
	if !ok {
		return nil
	}
	language := languageEnglish
	userID := invocation.Chat.ID
	if invocation.From != nil {
		language = languageFromCode(invocation.From.LanguageCode)
		if invocation.From.ID > 0 {
			userID = invocation.From.ID
		}
	}
	err := handler.actions.Route(ctx, invocation)
	if err == nil {
		handler.touchActionUser(ctx, userID)
		return nil
	}
	if errors.Is(err, ErrInvalidActionData) || errors.Is(err, ErrUnsupportedAction) || errors.Is(err, ErrUnregisteredAction) {
		handler.logf("rejected action: %v", err)
		answerErr := messenger.AnswerAction(ctx, invocation.Token, language.text(
			"Кнопка устарела или недоступна. Запросите прогноз снова.",
			"This button is stale or unavailable. Request the forecast again."))
		handler.touchActionUser(ctx, userID)
		return answerErr
	}
	handler.touchActionUser(ctx, userID)
	return err
}

func (handler *Handler) touchActionUser(ctx context.Context, userID int64) {
	if handler.persistence == nil {
		return
	}
	if err := handler.persistence.TouchUser(ctx, userID); err != nil {
		handler.logf("touch platform user from action: %v", err)
	}
}

func (handler *Handler) replyToLocation(ctx context.Context, chatID, userID int64, latitude, longitude float64, language userLanguage) error {
	successful := false
	if handler.persistence != nil {
		defer func() {
			recordContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			if err := handler.persistence.RecordForecast(recordContext, userID, successful); err != nil {
				handler.logf("record forecast usage: %v", err)
			}
		}()
	}
	requestID := handler.requestSequence.Add(1)
	requestStarted := time.Now()
	timeZone := handler.resolver.Resolve(latitude, longitude)
	label := forecast.TimeZoneLabel(timeZone, time.Now())
	location, err := forecast.NewLocation(latitude, longitude, timeZone)
	if err != nil {
		return handler.sendUserMessage(ctx, chatID, language.text("Координаты не прошли проверку.", "The coordinates failed validation."), true, language)
	}
	if handler.provider == nil {
		text := fmt.Sprintf(language.text("Точка принята: %.4f, %.4f\nЧасовая зона: %s\n\n%s", "Location accepted: %.4f, %.4f\nTime zone: %s\n\n%s"), latitude, longitude, label, language.text(NotReadyText, NotReadyTextEN))
		return handler.sendUserMessage(ctx, chatID, text, true, language)
	}
	if handler.forecastQueue != nil {
		release, queueError := handler.forecastQueue.Wait(ctx, func(position int) error {
			return handler.sendUserMessage(ctx, chatID, fmt.Sprintf(language.text(
				"Прогноз поставлен в очередь: ваше место — %d.",
				"Forecast queued: your position is %d."), position), true, language)
		})
		if queueError != nil {
			return queueError
		}
		defer release()
	}
	if err := handler.sendUserMessage(ctx, chatID,
		fmt.Sprintf(language.text("Точка принята: %.4f, %.4f\nЧасовая зона: %s\nРассчитываю прогноз по свежим доступным данным…", "Location accepted: %.4f, %.4f\nTime zone: %s\nCalculating the forecast from the freshest available data…"), latitude, longitude, label), true, language); err != nil {
		return err
	}
	type lightPollutionResult struct {
		estimate lightpollution.Estimate
		err      error
	}
	var lightPollutionChannel chan lightPollutionResult
	if handler.lightPollution != nil {
		lightPollutionChannel = make(chan lightPollutionResult, 1)
		go func() {
			estimate, lookupError := handler.lightPollution.At(ctx, latitude, longitude)
			lightPollutionChannel <- lightPollutionResult{estimate: estimate, err: lookupError}
		}()
	}
	var worldAtlasChannel chan lightPollutionResult
	if handler.worldAtlas2015 != nil {
		worldAtlasChannel = make(chan lightPollutionResult, 1)
		go func() {
			estimate, e := handler.worldAtlas2015.At(ctx, latitude, longitude)
			worldAtlasChannel <- lightPollutionResult{estimate: estimate, err: e}
		}()
	}
	dataStarted := time.Now()
	series, err := handler.provider.Vertical(ctx, location)
	if err != nil {
		return handler.sendUserMessage(ctx, chatID, language.text("Не удалось получить актуальный профиль ICON: ", "Could not obtain a current ICON profile: ")+safeForecastError(err, language), true, language)
	}
	var surfaceSeries forecast.SurfaceSeries
	var sky astronomy.Series
	var celestialTracks []astronomy.CelestialTrack
	var compositionChannel chan compositionResult
	hasWeather := false
	hasOverall := false
	var cloudSeries forecast.CloudSeries
	hasCloud := false
	surface, surfaceError := handler.provider.Surface(ctx, location)
	if surfaceError != nil {
		handler.logf("forecast request %d surface data unavailable: %v", requestID, surfaceError)
	} else {
		surface = surface.Window(time.Now(), 72)
		if len(surface.Frames) >= 2 {
			surfaceSeries = surface
			var astronomyError error
			sky, astronomyError = astronomy.Compute(location, surface.Frames[0].ValidAt, surface.Frames[len(surface.Frames)-1].ValidAt)
			if astronomyError != nil {
				handler.logf("forecast request %d astronomy calculation failed: %v", requestID, astronomyError)
			} else {
				validTimes := make([]time.Time, len(surface.Frames))
				for index := range surface.Frames {
					validTimes[index] = surface.Frames[index].ValidAt
				}
				celestialTracks, astronomyError = astronomy.ComputeCelestialTracks(location, validTimes)
				if astronomyError != nil {
					handler.logf("forecast request %d celestial tracks failed: %v", requestID, astronomyError)
				} else {
					hasWeather = true
					if handler.atmosphericComposition != nil {
						compositionChannel = handler.startAtmosphericComposition(location, validTimes)
					}
				}
			}
		}
	}
	cloud, cloudError := handler.provider.Cloud(ctx, location)
	if cloudError != nil {
		handler.logf("forecast request %d cloud data unavailable: %v", requestID, cloudError)
	} else {
		cloud = cloud.Window(time.Now(), 72)
		if len(cloud.Frames) >= 2 {
			cloudSeries, hasCloud = cloud, true
		}
	}
	if !forecastInputsShareRun(series, surface, surfaceError, cloud, cloudError) {
		handler.logf("forecast request %d rejected mixed model runs during acquisition", requestID)
		return handler.sendUserMessage(ctx, chatID, language.text(
			"Во время расчёта появился новый model run. Повторите запрос — прогноз будет построен уже по свежим данным.",
			"A new model run appeared while the data were being read. Repeat the request to use the fresh run."),
			true, language)
	}
	var compositionSeries forecast.AtmosphericCompositionSeries
	if compositionChannel != nil {
		select {
		case composition := <-compositionChannel:
			if composition.err != nil {
				handler.logf("forecast request %d GEOS-CF composition unavailable; Reference V-band remains partial: %v", requestID, composition.err)
			} else {
				compositionSeries = composition.series
			}
		case <-time.After(handler.atmosphericCompositionJoin):
			handler.logf("forecast request %d GEOS-CF composition did not finish within the %s join budget; ICON rendering continues while the bounded service operation may warm the shared RAM cache", requestID, handler.atmosphericCompositionJoin)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	dataDuration := time.Since(dataStarted)
	renderStarted := time.Now()
	requestRenderOptions := handler.renderOptions
	requestRenderOptions.Language = language.renderCode()
	var overallFrames []forecast.OverallIndexFrame
	var interactiveDataset render.ForecastInteractiveDataset
	var upperAirDiagnostics forecast.Diagnostics
	var cloudDiagnostics forecast.CloudDiagnostics
	var cloudObstruction [][]float64
	_, structuredOnly := handler.messenger.(ForecastDatasetMessenger)
	renderCacheHit := false
	cacheKey := ""
	var charts render.Result
	if hasWeather && hasCloud {
		cacheKey = forecastRenderCacheKey(series, surfaceSeries, cloudSeries, compositionSeries, sky, requestRenderOptions, handler.overallCalibration)
		if handler.renderCacheRoot != "" {
			charts, renderCacheHit = loadRenderCache(handler.renderCacheRoot, cacheKey)
			hasOverall = renderCacheHit
		}
	}
	if sink, ok := handler.messenger.(ForecastStatusMessenger); ok {
		sink.UpdateForecastCacheStatus(renderCacheHit)
	}
	if !renderCacheHit {
		if hasWeather && hasCloud {
			overallFrames, err = forecast.ComputeHourlyOverallIndex(series, surfaceSeries, cloudSeries, handler.overallCalibration)
			if err != nil {
				handler.logf("forecast request %d overall index calculation failed: %v", requestID, err)
			} else {
				if referenceError := AttachReferenceVBand(overallFrames, surfaceSeries, compositionSeries, sky); referenceError != nil {
					handler.logf("forecast request %d Reference V-band calculation failed: %v", requestID, referenceError)
				}
				hasOverall = true
				interactiveDataset, upperAirDiagnostics, cloudDiagnostics, cloudObstruction, err = render.PrepareForecastInteractiveDataset(
					series, surfaceSeries, cloudSeries, sky, celestialTracks, overallFrames, handler.overallCalibration,
				)
				if err != nil {
					handler.logf("forecast request %d interactive dataset preparation failed: %v", requestID, err)
				} else {
					interactiveDataset.ArtifactKey = cacheKey
				}
			}
		}
		if len(upperAirDiagnostics.Times) == 0 {
			upperAirDiagnostics, err = forecast.ComputeDiagnostics(series)
			if err != nil {
				return handler.sendUserMessage(ctx, chatID, language.text("Не удалось рассчитать высотные характеристики.", "Could not calculate the upper-air diagnostics."), true, language)
			}
		}
		if hasCloud && len(cloudDiagnostics.Times) == 0 {
			cloudDiagnostics, err = forecast.ComputeCloudDiagnostics(cloudSeries)
			if err == nil {
				cloudObstruction, err = forecast.ComputeCloudObstruction(cloudDiagnostics, handler.overallCalibration)
			}
			if err != nil {
				handler.logf("forecast request %d cloud obstruction calculation failed: %v", requestID, err)
				hasCloud = false
			}
		}
		root := handler.renderRoot
		if handler.renderCacheRoot != "" && hasWeather && hasCloud {
			root = handler.renderCacheRoot
		}
		if err := os.MkdirAll(root, 0o750); err != nil {
			return handler.sendUserMessage(ctx, chatID, language.text("Не удалось подготовить каталог графиков.", "Could not prepare the chart directory."), true, language)
		}
		requestDirectory, directoryError := os.MkdirTemp(root, ".incoming-")
		if directoryError != nil {
			return handler.sendUserMessage(ctx, chatID, language.text("Не удалось подготовить временный каталог графиков.", "Could not prepare the temporary chart directory."), true, language)
		}
		defer func() { _ = os.RemoveAll(requestDirectory) }()
		if !structuredOnly {
			charts, err = render.AllDiagnostics(requestDirectory, series, upperAirDiagnostics, requestRenderOptions)
			if err != nil {
				return handler.sendUserMessage(ctx, chatID, language.text("Не удалось построить графики прогноза.", "Could not render the forecast charts."), true, language)
			}
			if hasWeather {
				charts.Weather = filepath.Join(requestDirectory, "weather-hourly.png")
				if err := render.Weather(charts.Weather, surfaceSeries, sky, celestialTracks, requestRenderOptions); err != nil {
					charts.Weather, hasWeather = "", false
				}
			}
			if hasWeather && hasCloud && hasOverall {
				charts.OverallIndex = filepath.Join(requestDirectory, "overall-astronomy-index-hourly.png")
				if renderError := render.OverallIndex(charts.OverallIndex, series, overallFrames, sky, render.Options{Width: render.OverallWidth, Height: render.OverallHeight, Language: language.renderCode()}); renderError != nil {
					handler.logf("forecast request %d overall index render failed: %v", requestID, renderError)
					hasOverall = false
				}
			}
			if hasCloud {
				charts.CloudObstruction = filepath.Join(requestDirectory, "cloud-obstruction-height-hourly.png")
				if err := render.CloudObstructionDiagnostics(charts.CloudObstruction, cloudSeries, cloudDiagnostics, cloudObstruction, render.Options{Width: 3200, Height: 1100, Language: language.renderCode()}); err != nil {
					charts.CloudObstruction, hasCloud = "", false
				}
			}
		}
		if hasWeather && hasCloud && hasOverall && interactiveDataset.SchemaVersion == render.ForecastInteractiveSchema {
			charts.Dataset = filepath.Join(requestDirectory, "forecast.json")
			if err := render.SaveForecastInteractiveDataset(charts.Dataset, interactiveDataset); err != nil {
				handler.logf("forecast request %d interactive dataset publication failed: %v", requestID, err)
				charts.Dataset = ""
			}
		}
		if !structuredOnly && handler.renderCacheRoot != "" && hasWeather && hasCloud && hasOverall && charts.Dataset != "" {
			published, publishError := publishRenderCache(handler.renderCacheRoot, cacheKey, requestDirectory)
			if publishError == nil {
				charts = published
			} else {
				handler.logf("forecast request %d render cache publish failed: %v", requestID, publishError)
			}
		}
	}
	if sink, ok := handler.messenger.(ForecastDatasetMessenger); ok {
		if charts.Dataset == "" {
			return errors.New("interactive forecast dataset is unavailable")
		}
		if err := sink.SendForecastDataset(ctx, charts.Dataset); err != nil {
			return err
		}
	}
	renderDuration := time.Since(renderStarted)
	validUntil := series.Frames[len(series.Frames)-1].ValidAt
	locationZone, zoneError := time.LoadLocation(series.Location.TimeZone)
	if zoneError != nil {
		locationZone = time.UTC
	}
	modelName := forecastModelName(series.Provider)
	maxStaleAge := handler.forecastMaxStaleAge
	if series.Provider == "icon-global" {
		maxStaleAge = handler.fallbackMaxStaleAge
	}
	summary := fmt.Sprintf(language.text(
		"%s %s UTC\n%s\n%s\nПериод: %s — %s\nСетка: %s",
		"%s %s UTC\n%s\n%s\nPeriod: %s — %s\nGrid: %s"),
		modelName, series.RunID,
		synScanCoordinatesText(series.Location.Latitude, series.Location.Longitude, language),
		forecastFreshnessText(series.BaseTime, time.Now(), maxStaleAge, language),
		series.Frames[0].ValidAt.In(locationZone).Format("02.01 15:04"),
		validUntil.In(locationZone).Format("02.01 15:04"), series.Grid)
	if hasCloud {
		summary += fmt.Sprintf(language.text(
			"\nМодельная высота поверхности: %.0f м над уровнем моря (ICON HHL).",
			"\nModel surface elevation: %.0f m above mean sea level (ICON HHL)."),
			cloudSeries.SurfaceElevationM)
	}
	if lightPollutionChannel != nil {
		select {
		case result := <-lightPollutionChannel:
			if result.err != nil {
				handler.logf("forecast request %d light-pollution lookup failed: %v", requestID, result.err)
				summary += language.text("\nЗасветка: оценка временно недоступна.", "\nLight pollution: estimate temporarily unavailable.")
			} else {
				summary += fmt.Sprintf(language.text("\nЗасветка: ориентир Бортля <b>%s</b> (LPI %.2f, SQM %.2f mag/arcsec², Light Pollution Atlas %d).", "\nLight pollution: Bortle reference <b>%s</b> (LPI %.2f, SQM %.2f mag/arcsec², Light Pollution Atlas %d)."),
					result.estimate.BortleDisplay, result.estimate.LPI, result.estimate.SQM, result.estimate.Year)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if worldAtlasChannel != nil {
		select {
		case result := <-worldAtlasChannel:
			if result.err != nil {
				handler.logf("forecast request %d World Atlas 2015 lookup failed: %v", requestID, result.err)
				summary += language.text("\nСравнение засветки: оценка World Atlas 2015 временно недоступна.", "\nLight pollution comparison: World Atlas 2015 estimate temporarily unavailable.")
			} else {
				summary += fmt.Sprintf(language.text("\nСравнение засветки: ориентир Бортля <b>%s</b> (LPI %.2f, SQM %.2f mag/arcsec², World Atlas 2015).", "\nLight pollution comparison: Bortle reference <b>%s</b> (LPI %.2f, SQM %.2f mag/arcsec², World Atlas 2015)."), result.estimate.BortleDisplay, result.estimate.LPI, result.estimate.SQM)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := handler.sendHTMLUserMessage(ctx, chatID, summary, true, language); err != nil {
		return err
	}
	if structuredOnly {
		handler.logf("forecast request %d complete data=%s structured=%s render_cache_hit=%t total=%s",
			requestID, dataDuration.Round(time.Millisecond), renderDuration.Round(time.Millisecond), renderCacheHit,
			time.Since(requestStarted).Round(time.Millisecond))
		successful = true
		return nil
	}
	type chartDelivery struct {
		path, caption string
		asDocument    bool
	}
	deliveries := make([]chartDelivery, 0, 7)
	total := 4
	if hasWeather {
		total++
	}
	if hasCloud {
		total++
	}
	if hasOverall {
		total++
	}
	number := 1
	if hasWeather {
		deliveries = append(deliveries, chartDelivery{charts.Weather, fmt.Sprintf(language.text("<b>%d/%d · Почасовая погода и небесные события на 72 часа.</b>\n<i>Показывает облака, осадки, температуру, ветер, влажность, риск росы и эвристику тумана, а также события Солнца, Луны и планет. Помогает выбрать тёмные часы без мешающей погоды и заранее подготовить оборудование.</i>", "<b>%d/%d · Hourly weather and celestial events for 72 hours.</b>\n<i>Shows clouds, precipitation, temperature, wind, humidity, dew risk and the fog heuristic, plus Sun, Moon, and planet events. Use it to select dark hours without obstructive weather and prepare equipment in advance.</i>"), number, total), true})
		number++
	}
	if hasOverall {
		deliveries = append(deliveries, chartDelivery{charts.OverallIndex, overallChartCaption(language, number, total), true})
		number++
	}
	if hasCloud {
		deliveries = append(deliveries, chartDelivery{charts.CloudObstruction, fmt.Sprintf(language.text("<b>%d/%d · Эффективная облачная преграда:</b> покрытие и жидкий/ледяной конденсат по фактической высоте.\n<i>Карта показывает, когда и на какой высоте ожидается оптически значимая облачность. Она помогает отличить плотные нижние облака от менее мешающих верхних и оценить пригодность окна для съёмки или фотометрии.</i>", "<b>%d/%d · Effective cloud obstruction:</b> cover and liquid/ice condensate by actual height.\n<i>This chart shows when and at what altitude optically significant cloud is expected. It helps distinguish dense low cloud from less obstructive high cloud and assess whether imaging or photometry is practical.</i>"), number, total), true})
		number++
	}
	deliveries = append(deliveries,
		chartDelivery{charts.WindSpeed, fmt.Sprintf(language.text("<b>%d/%d · Скорость ветра по уровням давления.</b>\n<i>Вертикальный профиль выявляет слои сильного ветра и струйное течение. Они могут ухудшать стабильность изображения и ведение, хотя одна скорость ветра не является прямым измерением seeing.</i>", "<b>%d/%d · Wind speed by pressure level.</b>\n<i>The vertical profile exposes strong-flow layers and the jet stream. These can degrade image stability or tracking, although wind speed alone is not a direct seeing measurement.</i>"), number, total), false},
		chartDelivery{charts.VectorShear, fmt.Sprintf(language.text("<b>%d/%d · Вертикальный векторный сдвиг ветра, м/с на км.</b>\n<i>Показывает, насколько быстро полный вектор ветра меняется на километр высоты. Яркие слои отмечают вероятные границы генерации турбулентности и часы с менее устойчивым мелкомасштабным изображением.</i>", "<b>%d/%d · Vertical vector wind shear, m/s per km.</b>\n<i>Shows how quickly the full wind vector changes per kilometre. Bright layers identify likely turbulence-producing boundaries and hours with less stable fine-detail imaging.</i>"), number+1, total), false},
		chartDelivery{charts.DirectionDelta, fmt.Sprintf(language.text("<b>%d/%d · Изменение направления между соседними уровнями.</b>\n<i>График выделяет поворот ветра между соседними уровнями. Его нужно читать вместе со скоростью и векторным сдвигом: большой поворот при почти полном штиле значительно менее важен, чем при сильном потоке.</i>", "<b>%d/%d · Wind direction change between adjacent levels.</b>\n<i>This chart highlights turning between adjacent levels. Read it together with wind speed and vector shear: a large turn in near-calm air matters much less than the same change in a strong flow.</i>"), number+2, total), false},
		chartDelivery{charts.SeeingIndex, fmt.Sprintf(language.text("<b>%d/%d · Прогнозный индекс сиинга по ветру.</b>\n<i>Компактный индекс ранжирует часы только по модельному профилю ветра. Для окончательного выбора используйте «Общий астрономический индекс», потому что облака и туман сюда намеренно не входят.</i>", "<b>%d/%d · Forecast wind seeing index.</b>\n<i>This compact index ranks hours using the modeled wind profile only. Use the Overall Astronomy Index for final planning because cloud and fog are deliberately excluded here.</i>"), number+3, total), false},
	)
	sendStarted := time.Now()
	failed := 0
	for _, delivery := range deliveries {
		var err error
		if delivery.asDocument {
			err = handler.messenger.SendDocument(ctx, chatID, delivery.path, delivery.caption)
		} else {
			err = handler.messenger.SendPhoto(ctx, chatID, delivery.path, delivery.caption)
		}
		if err != nil {
			handler.logf("forecast request %d chart %q failed: %v", requestID, filepath.Base(delivery.path), err)
			failed++
		}
	}
	if failed > 0 {
		return handler.sendUserMessage(ctx, chatID,
			fmt.Sprintf(language.text("Не удалось отправить %d из %d графиков. Попробуйте повторить запрос позже.", "Could not send %d of %d charts. Please try again later."), failed, len(deliveries)), true, language)
	}
	handler.logf("forecast request %d complete data=%s render=%s render_cache_hit=%t send=%s total=%s",
		requestID, dataDuration.Round(time.Millisecond), renderDuration.Round(time.Millisecond), renderCacheHit,
		time.Since(sendStarted).Round(time.Millisecond), time.Since(requestStarted).Round(time.Millisecond))
	successful = true
	if hasWeather && hasCloud && hasOverall &&
		series.RunID == surfaceSeries.RunID && series.RunID == cloudSeries.RunID {
		handler.offerHorizon(ctx, chatID, requestID, series.Provider, series.RunID, location, cloudSeries.SurfaceElevationM, language)
	}
	return nil
}

func overallChartCaption(language userLanguage, number, total int) string {
	return fmt.Sprintf(language.text(
		"<b>%d/%d · Общий астрономический индекс (Overall Astronomy Index).</b>\n<i>Снизу — сохранившаяся пригодность; цветные сегменты точно разлагают потери и вместе заполняют столбец до 10. Красный сегмент — жёсткий запрет из-за осадков (индекс 1), «!» — неполные данные. Кольцо — эталон V в зените только астрономической ночью: PWV/AOD/O₃/Луна/PSF-сиинг. Без GEOS-CF кольцо пропускается, но Overall остаётся доступен.</i>",
		"<b>%d/%d · Overall Astronomy Index.</b>\n<i>The lower segment is retained suitability; the colored segments are an exact additive loss decomposition that closes each column at 10. Red is a hard precipitation veto (index 1), while “!” marks incomplete inputs. The ring is the zenith Reference V-band value shown only during astronomical night: PWV/AOD/O₃/Moon/PSF seeing. If GEOS-CF is unavailable, the ring is omitted but Overall remains available.</i>",
	), number, total)
}

func forecastInputsShareRun(vertical forecast.VerticalSeries, surface forecast.SurfaceSeries, surfaceErr error, cloud forecast.CloudSeries, cloudErr error) bool {
	if surfaceErr == nil && (surface.Provider != vertical.Provider || surface.RunID != vertical.RunID || !surface.BaseTime.Equal(vertical.BaseTime)) {
		return false
	}
	if cloudErr == nil && (cloud.Provider != vertical.Provider || cloud.RunID != vertical.RunID || !cloud.BaseTime.Equal(vertical.BaseTime)) {
		return false
	}
	return true
}

// AttachReferenceVBand enriches already computed Overall frames without
// changing their generic score. It is shared by live messaging and the
// render-point verification command so their scientific output cannot drift.
func AttachReferenceVBand(overall []forecast.OverallIndexFrame, surface forecast.SurfaceSeries, composition forecast.AtmosphericCompositionSeries, sky astronomy.Series) error {
	surfaceByTime := make(map[time.Time]forecast.SurfaceFrame, len(surface.Frames))
	for _, frame := range surface.Frames {
		surfaceByTime[frame.ValidAt] = frame
	}
	for index := range overall {
		at := overall[index].ValidAt
		surfaceFrame, ok := surfaceByTime[at]
		if !ok {
			return fmt.Errorf("surface frame is unavailable for Reference V-band at %s", at.Format(time.RFC3339))
		}
		moon := astronomy.MoonStateAt(surface.Location, at)
		geometry := forecast.ReferenceVBandSkyGeometry{
			ValidAt: at, SunAltitudeDegrees: sky.SunAltitudeDegrees(at),
			MoonAltitudeDegrees:   moon.TopocentricGeometricAltitudeDegrees,
			MoonZenithDistanceDeg: moon.TopocentricZenithDistanceDegrees,
			MoonPhaseAngleDegrees: moon.PhaseAngleDegrees,
			MoonEarthDistanceKM:   moon.EarthMoonDistanceKM,
			MoonGeometryAvailable: moon.Valid,
		}
		compositionFrame, available := composition.FrameAt(at)
		if !available {
			compositionFrame = nil
		}
		diagnostic, err := forecast.ComputeReferenceVBandAtmosphere(surfaceFrame, overall[index], compositionFrame, geometry)
		if err != nil {
			return fmt.Errorf("compute Reference V-band at %s: %w", at.Format(time.RFC3339), err)
		}
		overall[index].ReferenceVBand = &diagnostic
	}
	return nil
}

func (handler *Handler) startAtmosphericComposition(location forecast.Location, validTimes []time.Time) chan compositionResult {
	result := make(chan compositionResult, 1)
	operationContext, cancel := context.WithTimeout(handler.atmosphericCompositionContext, handler.atmosphericCompositionTimeout)
	go func() {
		defer cancel()
		composition, err := handler.atmosphericComposition.AtmosphericComposition(operationContext, location, validTimes)
		result <- compositionResult{series: composition, err: err}
	}()
	return result
}

func (handler *Handler) offerHorizon(ctx context.Context, chatID int64, requestID uint64, provider, runID string, location forecast.Location, surfaceElevationM float64, language userLanguage) {
	if handler.horizon == nil || provider != HorizonProviderICONEU {
		return
	}
	button, err := handler.horizon.Button(HorizonButtonRequest{
		Provider: provider, RunID: runID, Location: location,
		ObserverSurfaceElevationM: surfaceElevationM,
	}, language.renderCode())
	if err != nil {
		if !errors.Is(err, ErrHorizonUnsupported) && !errors.Is(err, ErrHorizonStaleAction) {
			handler.logf("forecast request %d horizon action unavailable: %v", requestID, err)
		}
		return
	}
	messenger, ok := handler.messenger.(ActionMessenger)
	if !ok {
		return
	}
	prompt := language.text(
		"Дополнительный анализ: 72 физических почасовых срока ICON-EU f001…f072, восемь прямых лучей на геометрической высоте 10°. Первые сроки уже могут быть в прошлом — ориентируйтесь на подписанную шкалу времени.",
		"Optional analysis: 72 physical hourly ICON-EU terms f001…f072, eight straight rays at 10° geometric elevation. The earliest terms may already be in the past; use the labeled time axis.",
	)
	if err := messenger.SendMessageWithActions(ctx, chatID, prompt, ActionKeyboard{{button}}); err != nil {
		handler.logf("forecast request %d horizon action prompt failed: %v", requestID, err)
	}
}

func forecastFreshnessText(baseTime, now time.Time, maxAge time.Duration, language userLanguage) string {
	if maxAge <= 0 {
		maxAge = defaultForecastMaxStaleAge
	}
	age := now.UTC().Sub(baseTime.UTC())
	if age < 0 {
		age = 0
	}
	ageText := formatForecastAge(age, language)
	thresholdText := formatForecastAge(maxAge, language)
	if age > maxAge {
		return fmt.Sprintf(language.text("⚠️ Данные устарели (stale run): возраст run %s, порог %s. Прогноз может не учитывать последние изменения атмосферы.", "⚠️ Stale run: run age %s exceeds the %s threshold. The forecast may not reflect recent atmospheric changes."), ageText, thresholdText)
	}
	return fmt.Sprintf(language.text("Актуальность данных: %s", "Data freshness: %s"), ageText)
}

func formatForecastAge(age time.Duration, language userLanguage) string {
	if age < 0 {
		age = 0
	}
	totalMinutes := int64(age / time.Minute)
	days := totalMinutes / (24 * 60)
	hours := totalMinutes/60 - days*24
	minutes := totalMinutes % 60
	if language == languageEnglish {
		if days > 0 {
			return fmt.Sprintf("%dd %dh %dmin", days, hours, minutes)
		}
		if hours > 0 {
			return fmt.Sprintf("%dh %dmin", hours, minutes)
		}
		return fmt.Sprintf("%dmin", minutes)
	}
	if days > 0 {
		return fmt.Sprintf("%d д %d ч %d мин", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%d ч %d мин", hours, minutes)
	}
	return fmt.Sprintf("%d мин", minutes)
}

func safeForecastError(err error, language userLanguage) string {
	text := strings.TrimSpace(err.Error())
	if strings.Contains(text, "outside the current ICON-EU domain") {
		return language.text("точка находится вне домена ICON-EU; fallback ICON Global ещё не подключён.", "the location is outside the ICON-EU domain; the ICON Global fallback is not connected yet.")
	}
	if strings.Contains(text, "current ICON-EU run") {
		return language.text("текущий run ещё не опубликован.", "the current run has not been published yet.")
	}
	return language.text("внутренняя ошибка данных.", "internal data error.")
}

func forecastModelName(provider string) string {
	if provider == "icon-global" {
		return "ICON Global"
	}
	return "ICON-EU"
}

func synScanCoordinatesText(latitude, longitude float64, language userLanguage) string {
	return fmt.Sprintf(
		language.text(
			"SynScan: долгота %s, широта %s",
			"SynScan: longitude %s, latitude %s",
		),
		formatSynScanAngle(longitude, 3, "E", "W"),
		formatSynScanAngle(latitude, 2, "N", "S"),
	)
}

func formatSynScanAngle(value float64, degreeDigits int, positiveHemisphere, negativeHemisphere string) string {
	hemisphere := positiveHemisphere
	if value < 0 {
		hemisphere = negativeHemisphere
	}
	totalMinutes := int(math.Round(math.Abs(value) * 60))
	degrees := totalMinutes / 60
	minutes := totalMinutes % 60
	return fmt.Sprintf("%0*d°%02d′ %s", degreeDigits, degrees, minutes, hemisphere)
}

func (handler *Handler) sendUserMessage(ctx context.Context, chatID int64, text string, locationButton bool, language userLanguage) error {
	if locationButton {
		if messenger, ok := handler.messenger.(KeyboardMessenger); ok {
			return messenger.SendMessageWithKeyboard(ctx, chatID, text, defaultKeyboard(language))
		}
	}
	return handler.messenger.SendMessage(ctx, chatID, text, locationButton)
}

func (handler *Handler) sendHTMLUserMessage(ctx context.Context, chatID int64, text string, locationButton bool, language userLanguage) error {
	if locationButton {
		if messenger, ok := handler.messenger.(HTMLKeyboardMessenger); ok {
			return messenger.SendHTMLMessageWithKeyboard(ctx, chatID, text, defaultKeyboard(language))
		}
	}
	return handler.messenger.SendMessage(ctx, chatID, text, locationButton)
}

func (handler *Handler) sendMainKeyboard(ctx context.Context, chatID int64, text string, userID int64, language userLanguage) error {
	k := defaultKeyboard(language)
	if _, ok := handler.admins[userID]; ok {
		k = append(k, []Button{{Text: language.text("📊 Статистика", "📊 Statistics")}})
	}
	if m, ok := handler.messenger.(KeyboardMessenger); ok {
		return m.SendMessageWithKeyboard(ctx, chatID, text, k)
	}
	return handler.messenger.SendMessage(ctx, chatID, text, true)
}

func (handler *Handler) sendSaveKeyboard(ctx context.Context, chatID int64, text string, language userLanguage) error {
	k := Keyboard{{{Text: language.text("📍 Отправить геопозицию", "📍 Share location"), RequestLocation: true}}, {{Text: language.text("⬅️ Назад", "⬅️ Back")}, {Text: language.text("❌ Отмена", "❌ Cancel")}}}
	if m, ok := handler.messenger.(KeyboardMessenger); ok {
		return m.SendMessageWithKeyboard(ctx, chatID, text, k)
	}
	return handler.messenger.SendMessage(ctx, chatID, text, true)
}
func (handler *Handler) setSession(id int64, s saveSession) {
	handler.sessionMu.Lock()
	defer handler.sessionMu.Unlock()
	now := time.Now()
	for key, value := range handler.sessions {
		if now.Sub(value.UpdatedAt) > 2*time.Hour {
			delete(handler.sessions, key)
		}
	}
	s.UpdatedAt = now
	handler.sessions[id] = s
}
func (handler *Handler) getSession(id int64) (saveSession, bool) {
	handler.sessionMu.Lock()
	defer handler.sessionMu.Unlock()
	s, ok := handler.sessions[id]
	if ok && time.Since(s.UpdatedAt) > 2*time.Hour {
		delete(handler.sessions, id)
		return saveSession{}, false
	}
	return s, ok
}
func (handler *Handler) clearSession(id int64) {
	handler.sessionMu.Lock()
	defer handler.sessionMu.Unlock()
	delete(handler.sessions, id)
}

func (handler *Handler) handleSaveSession(ctx context.Context, m *Message, userID int64, s saveSession, language userLanguage) error {
	if isButton(strings.TrimSpace(m.Text), "❌ Отмена", "❌ Cancel") {
		handler.clearSession(userID)
		return handler.sendMainKeyboard(ctx, m.Chat.ID, language.text("Сохранение отменено.", "Saving cancelled."), userID, language)
	}
	if handler.persistence == nil {
		handler.clearSession(userID)
		return handler.sendMainKeyboard(ctx, m.Chat.ID, language.text("Хранилище точек временно недоступно.", "Saved locations are temporarily unavailable."), userID, language)
	}
	if s.Stage == 1 {
		var lat, lon float64
		var err error
		if m.Location != nil {
			lat, lon = m.Location.Latitude, m.Location.Longitude
		} else {
			lat, lon, err = forecast.ParseLocationText(strings.TrimSpace(m.Text))
		}
		if err != nil || forecast.ValidateCoordinates(lat, lon) != nil {
			return handler.sendSaveKeyboard(ctx, m.Chat.ID, language.text("Координаты не распознаны. Отправьте геопозицию или, например: 59.9386, 30.3141", "Coordinates not recognized. Share a location or send, for example: 59.9386, 30.3141"), language)
		}
		handler.setSession(userID, saveSession{Stage: 2, Latitude: lat, Longitude: lon})
		return handler.sendUserMessage(ctx, m.Chat.ID, language.text("Введите короткое название точки (до 64 символов).", "Enter a short location name (up to 64 characters)."), false, language)
	}
	name := strings.TrimSpace(m.Text)
	if name == "" || len([]rune(name)) > 64 || strings.HasPrefix(name, "/") {
		return handler.sendUserMessage(ctx, m.Chat.ID, language.text("Название должно содержать от 1 до 64 символов.", "The name must contain 1 to 64 characters."), false, language)
	}
	err := handler.persistence.SavePoint(ctx, userID, name, s.Latitude, s.Longitude)
	if errors.Is(err, store.ErrPointLimit) {
		return handler.sendMainKeyboard(ctx, m.Chat.ID, language.text("Уже сохранено 10 точек. Удалите ненужную командой /deletepoint N.", "You already have 10 saved locations. Delete one with /deletepoint N."), userID, language)
	}
	if err != nil {
		handler.logf("save point: %v", err)
		return handler.sendMainKeyboard(ctx, m.Chat.ID, language.text("Не удалось сохранить точку. Возможно, такое название уже используется.", "Could not save the location. That name may already be in use."), userID, language)
	}
	handler.clearSession(userID)
	return handler.sendMainKeyboard(ctx, m.Chat.ID, fmt.Sprintf(language.text("Точка «%s» сохранена: %.4f, %.4f.", "Location “%s” saved: %.4f, %.4f."), name, s.Latitude, s.Longitude), userID, language)
}

func (handler *Handler) replyPoints(ctx context.Context, chatID, userID int64, language userLanguage) error {
	if handler.persistence == nil {
		return handler.sendMainKeyboard(ctx, chatID, language.text("Хранилище точек временно недоступно.", "Saved locations are temporarily unavailable."), userID, language)
	}
	points, err := handler.persistence.Points(ctx, userID)
	if err != nil {
		return err
	}
	if len(points) == 0 {
		return handler.sendMainKeyboard(ctx, chatID, language.text("Сохранённых точек пока нет. Нажмите «💾 Сохранить координаты».", "There are no saved locations yet. Tap “💾 Save coordinates”."), userID, language)
	}
	var b strings.Builder
	b.WriteString(language.text("Сохранённые точки:\n", "Saved locations:\n"))
	k := Keyboard{}
	for i, p := range points {
		fmt.Fprintf(&b, "%d. %s — %.4f, %.4f\n", i+1, p.Name, p.Latitude, p.Longitude)
		name := []rune(p.Name)
		if len(name) > 48 {
			name = append(name[:47], '…')
		}
		k = append(k, []Button{{Text: fmt.Sprintf("📌 %d. %s", i+1, string(name))}})
	}
	b.WriteString(language.text("\nДля удаления: /deletepoint N", "\nTo delete: /deletepoint N"))
	k = append(k, defaultKeyboard(language)...)
	k = append(k, []Button{{Text: language.text("⬅️ Назад", "⬅️ Back")}})
	if m, ok := handler.messenger.(KeyboardMessenger); ok {
		return m.SendMessageWithKeyboard(ctx, chatID, b.String(), k)
	}
	return handler.sendUserMessage(ctx, chatID, b.String(), true, language)
}

func (handler *Handler) selectedPoint(ctx context.Context, userID int64, text string) (store.Point, bool, error) {
	if !strings.HasPrefix(text, "📌 ") || handler.persistence == nil {
		return store.Point{}, false, nil
	}
	part := strings.TrimPrefix(text, "📌 ")
	dot := strings.IndexByte(part, '.')
	if dot < 1 {
		return store.Point{}, false, nil
	}
	n, err := strconv.Atoi(part[:dot])
	if err != nil {
		return store.Point{}, false, nil
	}
	points, err := handler.persistence.Points(ctx, userID)
	if err != nil {
		return store.Point{}, false, err
	}
	if n < 1 || n > len(points) {
		return store.Point{}, false, nil
	}
	return points[n-1], true, nil
}

func (handler *Handler) deletePoint(ctx context.Context, chatID, userID int64, text string, language userLanguage) error {
	fields := strings.Fields(text)
	if len(fields) != 2 {
		return handler.sendUserMessage(ctx, chatID, language.text("Использование: /deletepoint N", "Usage: /deletepoint N"), true, language)
	}
	n, err := strconv.Atoi(fields[1])
	if err != nil || n < 1 {
		return handler.sendUserMessage(ctx, chatID, language.text("Номер точки указан неверно.", "Invalid location number."), true, language)
	}
	points, err := handler.persistence.Points(ctx, userID)
	if err != nil {
		return err
	}
	if n > len(points) {
		return handler.sendUserMessage(ctx, chatID, language.text("Такой точки нет.", "That saved location does not exist."), true, language)
	}
	ok, err := handler.persistence.DeletePoint(ctx, userID, points[n-1].ID)
	if err != nil {
		return err
	}
	if !ok {
		return handler.sendUserMessage(ctx, chatID, language.text("Точка уже удалена.", "The location has already been deleted."), true, language)
	}
	return handler.sendMainKeyboard(ctx, chatID, language.text("Точка удалена.", "Location deleted."), userID, language)
}

func (handler *Handler) replyAdminStats(ctx context.Context, chatID, userID int64, language userLanguage) error {
	if _, ok := handler.admins[userID]; !ok {
		return handler.sendUserMessage(ctx, chatID, language.text("Команда доступна только администратору.", "This command is available only to an administrator."), true, language)
	}
	if handler.persistence == nil {
		return handler.sendUserMessage(ctx, chatID, language.text("Статистика временно недоступна.", "Statistics are temporarily unavailable."), true, language)
	}
	users, days, err := handler.persistence.Stats(ctx)
	if err != nil {
		return err
	}
	if len(days) == 0 {
		return handler.sendUserMessage(ctx, chatID, language.text("Статистика пока пуста.", "There are no statistics yet."), true, language)
	}
	var total, successful, failed int64
	for _, d := range days {
		total += d.Requests
		successful += d.Successful
		failed += d.Failed
	}
	dir, err := os.MkdirTemp(handler.renderRoot, "admin-stats-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "requests-30d.png")
	if err := render.UsageStats(path, days, language.renderCode()); err != nil {
		return err
	}
	text := fmt.Sprintf(language.text("Пользователей бота: %d\nЗапросов прогноза за 30 дней: %d\nУспешно: %d · с ошибкой: %d\nСегодня: %d", "Bot users: %d\nForecast requests in the last 30 days: %d\nSuccessful: %d · failed: %d\nToday: %d"), users, total, successful, failed, days[len(days)-1].Requests)
	if err := handler.sendUserMessage(ctx, chatID, text, true, language); err != nil {
		return err
	}
	return handler.messenger.SendPhoto(ctx, chatID, path, language.text("Запросы прогноза: зелёный — успешно, красный — с ошибкой · MSK (UTC+3)", "Forecast requests: green — successful, red — failed · MSK (UTC+3)"))
}
