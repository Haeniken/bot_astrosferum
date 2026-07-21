package telegram

import (
	"context"
	"errors"
	"fmt"
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

Как запросить прогноз:
• нажмите «📍 Отправить геопозицию»;
• отправьте текстом: 59.9386, 30.3141;
• используйте: /forecast 59.9386 30.3141.
• «💾 Сохранить координаты» хранит до 10 точек; выбор — «📌 Мои точки», удаление — /deletepoint N.

Как читать результат:

Высотные графики: по горизонтали — местное время; слева — давление, справа — высота ICON (850 hPa ≈ 1,5 км); светлее — больше, шкала под картой.

1. Почасовая погода на 72 часа: условия, ветер, влажность, T−Td, туман и события светил. Капля — защита от росы; она не означает плохой сиинг. «Прозрачность %» — сравнительная оценка по облакам, VIS и PWV, не экстинкция.

Облака Н/С/В и астрономия:
• нижние закрывают объект и отражают засветку;
• средние гасят свет и контраст, дают неоднородный фон;
• верхние повышают фон и портят длинные выдержки и фотометрию;
• в погодной таблице белый <10%, синий 10–49%, оранжевый ≥50%; это покрытие, не оптическая толщина;
• облачность не меняет wind-based seeing, но может исключить наблюдение или съёмку.

2. Overall Astronomy Index — пригодность 1…10: гибридный сиинг ICON (TKE до динамической MH 500–2000 м AGL + HMNSP99 выше), τ₀, эффективная облачная преграда и туман; приземный ветер штрафует слабо, роса не влияет. MH — почасовая высота перемешанного слоя ICON. Внутри: сиинг / τ₀, мс / T% пропускания; f/F — возможный/высокий риск тумана. Фон: день/сумерки/ночь.

3. Эффективная облачная преграда ICON — CLC+QC/QI и толщина слоя: 0% почти не мешает, 100% непрозрачно; тонкие верхние облака влияют слабее плотных нижних.

4. Wind Speed, m/s — ветер по высоте. Сильный ветер на 300–200 hPa часто означает струйное течение; у земли раскачивает телескоп.

5. Vector Wind Shear, m/s/km — векторный сдвиг с поправкой на фактическое расстояние; больше — выше риск турбулентности.

6. Wind Direction Delta, ° — поворот ветра; при скорости ниже 2 м/с показан как 0°, поскольку вклад почти штилевого потока мал.

7. Forecast Wind Seeing Index — оценка по ветру 1…10. Текст 96% — условная уверенность только по дальности срока; в Overall Index она не входит.

Засветка: LPI/SQM для координат по Atlas 2024 и отдельное сравнение с World Atlas 2015; Бортль — ориентир по зениту и в Overall Index не входит.

Время указано в часовой зоне координат. Расчётный сиинг — модельная оценка, не локальное измерение DIMM и не шкала Пикеринга.`

const NotReadyText = `Координаты распознаны, но выдача рабочего ICON-прогноза ещё разворачивается. Синтетические данные я пользователям не отправляю.`

const defaultForecastMaxStaleAge = 12 * time.Hour

type Messenger interface {
	SendMessage(ctx context.Context, chatID int64, text string, locationButton bool) error
	SendPhoto(ctx context.Context, chatID int64, path, caption string) error
}

type KeyboardMessenger interface {
	SendMessageWithKeyboard(context.Context, int64, string, Keyboard) error
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
}

type SurfaceProvider interface {
	Surface(ctx context.Context, location forecast.Location) (forecast.SurfaceSeries, error)
}

type CloudProvider interface {
	Cloud(ctx context.Context, location forecast.Location) (forecast.CloudSeries, error)
}

type LightPollutionProvider interface {
	At(ctx context.Context, latitude, longitude float64) (lightpollution.Estimate, error)
}

type Handler struct {
	messenger           Messenger
	resolver            *forecast.TimeZoneResolver
	provider            ForecastProvider
	renderRoot          string
	renderCacheRoot     string
	renderOptions       render.Options
	overallCalibration  forecast.OverallIndexCalibration
	forecastMaxStaleAge time.Duration
	lightPollution      LightPollutionProvider
	worldAtlas2015      LightPollutionProvider
	persistence         Persistence
	admins              map[int64]struct{}
	sessionMu           sync.Mutex
	sessions            map[int64]saveSession
	logf                func(string, ...any)
	requestSequence     atomic.Uint64
}

func (handler *Handler) EnableForecast(provider ForecastProvider, renderRoot string, options render.Options) error {
	if provider == nil {
		return fmt.Errorf("forecast provider is required")
	}
	if strings.TrimSpace(renderRoot) == "" {
		return fmt.Errorf("Telegram render root is required")
	}
	handler.provider = provider
	handler.renderRoot = renderRoot
	handler.renderOptions = options
	if err := os.MkdirAll(renderRoot, 0o750); err != nil {
		return fmt.Errorf("create Telegram render root: %w", err)
	}
	pruneRenderCache(renderRoot, time.Hour, 0)
	return nil
}

func (handler *Handler) EnableRenderCache(root string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("Telegram render cache root is required")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return fmt.Errorf("create Telegram render cache root: %w", err)
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
	return &Handler{messenger: messenger, resolver: resolver, overallCalibration: forecast.DefaultOverallIndexCalibration(), forecastMaxStaleAge: defaultForecastMaxStaleAge, logf: func(string, ...any) {}, admins: map[int64]struct{}{}, sessions: map[int64]saveSession{}}, nil
}

func (handler *Handler) SetForecastMaxStaleAge(maxAge time.Duration) error {
	if maxAge <= 0 {
		return errors.New("forecast maximum stale age must be positive")
	}
	handler.forecastMaxStaleAge = maxAge
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
		return errors.New("World Atlas 2015 provider is required")
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

func (handler *Handler) Handle(ctx context.Context, update Update) error {
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
			handler.logf("touch Telegram user %d: %v", userID, err)
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
		return handler.sendMainKeyboard(ctx, message.Chat.ID, startHelp(language), userID, language)
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
	if err := handler.sendUserMessage(ctx, chatID,
		fmt.Sprintf(language.text("Точка принята: %.4f, %.4f\nЧасовая зона: %s\nСтрою прогноз ICON-EU…", "Location accepted: %.4f, %.4f\nTime zone: %s\nBuilding the ICON-EU forecast…"), latitude, longitude, label), true, language); err != nil {
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
		return handler.sendUserMessage(ctx, chatID, language.text("Не удалось получить актуальный ICON-EU профиль: ", "Could not obtain a current ICON-EU profile: ")+safeForecastError(err, language), true, language)
	}
	var surfaceSeries forecast.SurfaceSeries
	var sky astronomy.Series
	hasWeather := false
	hasOverall := false
	var cloudSeries forecast.CloudSeries
	hasCloud := false
	if surfaceProvider, ok := handler.provider.(SurfaceProvider); ok {
		surface, surfaceError := surfaceProvider.Surface(ctx, location)
		if surfaceError == nil {
			surface = surface.Window(time.Now(), 72)
			if len(surface.Frames) >= 2 {
				surfaceSeries = surface
				var astronomyError error
				sky, astronomyError = astronomy.Compute(location, surface.Frames[0].ValidAt, surface.Frames[len(surface.Frames)-1].ValidAt)
				if astronomyError == nil {
					hasWeather = true
				}
			}
		}
	}
	if cloudProvider, ok := handler.provider.(CloudProvider); ok {
		cloud, cloudError := cloudProvider.Cloud(ctx, location)
		if cloudError == nil {
			cloud = cloud.Window(time.Now(), 72)
			if len(cloud.Frames) >= 2 {
				cloudSeries, hasCloud = cloud, true
			}
		}
	}
	dataDuration := time.Since(dataStarted)
	renderStarted := time.Now()
	requestRenderOptions := handler.renderOptions
	requestRenderOptions.Language = language.renderCode()
	renderCacheHit := false
	cacheKey := ""
	var charts render.Result
	if handler.renderCacheRoot != "" && hasWeather && hasCloud {
		cacheKey = forecastRenderCacheKey(series, surfaceSeries, cloudSeries, sky, requestRenderOptions, handler.overallCalibration)
		charts, renderCacheHit = loadRenderCache(handler.renderCacheRoot, cacheKey)
		hasOverall = renderCacheHit
	}
	if !renderCacheHit {
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
		defer os.RemoveAll(requestDirectory)
		charts, err = render.All(requestDirectory, series, requestRenderOptions)
		if err != nil {
			return handler.sendUserMessage(ctx, chatID, language.text("Не удалось построить графики прогноза.", "Could not render the forecast charts."), true, language)
		}
		if hasWeather {
			charts.Weather = filepath.Join(requestDirectory, "weather-hourly.png")
			if err := render.Weather(charts.Weather, surfaceSeries, sky, requestRenderOptions); err != nil {
				charts.Weather, hasWeather = "", false
			}
		}
		if hasWeather {
			overallFrames, overallError := forecast.ComputeHourlyOverallIndex(series, surfaceSeries, cloudSeries, handler.overallCalibration)
			if overallError != nil {
				handler.logf("forecast request %d overall index calculation failed: %v", requestID, overallError)
			} else {
				charts.OverallIndex = filepath.Join(requestDirectory, "overall-astronomy-index-hourly.png")
				if renderError := render.OverallIndex(charts.OverallIndex, series, overallFrames, sky, render.Options{Width: 3200, Height: 960, Language: language.renderCode()}); renderError == nil {
					hasOverall = true
				} else {
					handler.logf("forecast request %d overall index render failed: %v", requestID, renderError)
				}
			}
		}
		if hasCloud {
			charts.CloudObstruction = filepath.Join(requestDirectory, "cloud-obstruction-height-hourly.png")
			if err := render.CloudObstruction(charts.CloudObstruction, cloudSeries, handler.overallCalibration, render.Options{Width: 3200, Height: 1100, Language: language.renderCode()}); err != nil {
				charts.CloudObstruction, hasCloud = "", false
			}
		}
		if handler.renderCacheRoot != "" && hasWeather && hasCloud && hasOverall {
			published, publishError := publishRenderCache(handler.renderCacheRoot, cacheKey, requestDirectory)
			if publishError == nil {
				charts = published
			} else {
				handler.logf("forecast request %d render cache publish failed: %v", requestID, publishError)
			}
		}
	}
	renderDuration := time.Since(renderStarted)
	validUntil := series.Frames[len(series.Frames)-1].ValidAt
	locationZone, zoneError := time.LoadLocation(series.Location.TimeZone)
	if zoneError != nil {
		locationZone = time.UTC
	}
	summary := fmt.Sprintf(language.text(
		"ICON-EU run %s UTC\n%s\nПериод: %s — %s\nСетка: %s\nОптическая турбулентность: %s; гибридная модельная оценка ICON TKE до динамической MH 500–2000 м AGL + HMNSP99 выше, сиинг и τ₀ на 500 нм.",
		"ICON-EU run %s UTC\n%s\nPeriod: %s — %s\nGrid: %s\nOptical turbulence: %s; hybrid ICON model estimate using TKE up to dynamic MH 500–2000 m AGL and HMNSP99 above, with seeing and τ₀ at 500 nm."),
		series.RunID, forecastFreshnessText(series.BaseTime, time.Now(), handler.forecastMaxStaleAge, language), series.Frames[0].ValidAt.In(locationZone).Format("02.01 15:04"),
		validUntil.In(locationZone).Format("02.01 15:04"), series.Grid, series.AlgorithmVersion)
	if lightPollutionChannel != nil {
		select {
		case result := <-lightPollutionChannel:
			if result.err != nil {
				handler.logf("forecast request %d light-pollution lookup failed: %v", requestID, result.err)
				summary += language.text("\nЗасветка: оценка временно недоступна.", "\nLight pollution: estimate temporarily unavailable.")
			} else {
				summary += fmt.Sprintf(language.text("\nЗасветка: LPI %.2f, SQM %.2f mag/arcsec², ориентир Бортля %s (Light Pollution Atlas %d, зенит, интерполяция 30″).", "\nLight pollution: LPI %.2f, SQM %.2f mag/arcsec², Bortle reference %s (Light Pollution Atlas %d, zenith, 30″ interpolation)."),
					result.estimate.LPI, result.estimate.SQM, result.estimate.BortleDisplay, result.estimate.Year)
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
				summary += language.text("\nСравнение World Atlas 2015: оценка временно недоступна.", "\nWorld Atlas 2015 comparison: estimate temporarily unavailable.")
			} else {
				summary += fmt.Sprintf(language.text("\nСравнение World Atlas 2015: LPI %.2f, SQM %.2f mag/arcsec², ориентир Бортля %s (зенит, интерполяция 30″).", "\nWorld Atlas 2015 comparison: LPI %.2f, SQM %.2f mag/arcsec², Bortle reference %s (zenith, 30″ interpolation)."), result.estimate.LPI, result.estimate.SQM, result.estimate.BortleDisplay)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := handler.sendUserMessage(ctx, chatID, summary, true, language); err != nil {
		return err
	}
	photos := make([]struct {
		path, caption string
	}, 0, 7)
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
		photos = append(photos, struct{ path, caption string }{charts.Weather, fmt.Sprintf(language.text("%d/%d · Почасовая погода и небесные события на 72 часа", "%d/%d · Hourly weather and celestial events for 72 hours"), number, total)})
		number++
	}
	if hasOverall {
		photos = append(photos, struct{ path, caption string }{charts.OverallIndex, fmt.Sprintf(language.text("%d/%d · Общий почасовой индекс: ICON TKE до динамической MH 500–2000 м AGL + HMNSP99 выше, τ₀, облачная преграда и туман", "%d/%d · Overall hourly index: ICON TKE up to dynamic MH 500–2000 m AGL + HMNSP99 above, τ₀, cloud obstruction, and fog"), number, total)})
		number++
	}
	if hasCloud {
		photos = append(photos, struct{ path, caption string }{charts.CloudObstruction, fmt.Sprintf(language.text("%d/%d · Эффективная облачная преграда ICON: покрытие и жидкий/ледяной конденсат по фактической высоте", "%d/%d · ICON effective cloud obstruction: cover and liquid/ice condensate by actual height"), number, total)})
		number++
	}
	photos = append(photos,
		struct{ path, caption string }{charts.WindSpeed, fmt.Sprintf(language.text("%d/%d · Скорость ветра по уровням давления", "%d/%d · Wind speed by pressure level"), number, total)},
		struct{ path, caption string }{charts.VectorShear, fmt.Sprintf(language.text("%d/%d · Вертикальный векторный сдвиг ветра, м/с на км", "%d/%d · Vertical vector wind shear, m/s per km"), number+1, total)},
		struct{ path, caption string }{charts.DirectionDelta, fmt.Sprintf(language.text("%d/%d · Изменение направления между соседними уровнями", "%d/%d · Wind direction change between adjacent levels"), number+2, total)},
		struct{ path, caption string }{charts.SeeingIndex, fmt.Sprintf(language.text("%d/%d · Прогнозный индекс сиинга по ветру; уверенность — только по дальности срока и в Overall Index не входит", "%d/%d · Forecast wind seeing index; confidence depends only on lead time and is not part of the Overall Index"), number+3, total)},
	)
	sendStarted := time.Now()
	failed := 0
	for _, photo := range photos {
		if err := handler.messenger.SendPhoto(ctx, chatID, photo.path, photo.caption); err != nil {
			failed++
		}
	}
	if failed > 0 {
		return handler.sendUserMessage(ctx, chatID,
			fmt.Sprintf(language.text("Не удалось отправить %d из %d графиков. Попробуйте повторить запрос позже.", "Could not send %d of %d charts. Please try again later."), failed, len(photos)), true, language)
	}
	handler.logf("forecast request %d complete data=%s render=%s render_cache_hit=%t send=%s total=%s",
		requestID, dataDuration.Round(time.Millisecond), renderDuration.Round(time.Millisecond), renderCacheHit,
		time.Since(sendStarted).Round(time.Millisecond), time.Since(requestStarted).Round(time.Millisecond))
	successful = true
	return nil
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
	return fmt.Sprintf(language.text("Актуальность данных (freshness): run актуален, возраст %s (порог %s).", "Data freshness: current run, age %s (threshold %s)."), ageText, thresholdText)
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

func (handler *Handler) sendUserMessage(ctx context.Context, chatID int64, text string, locationButton bool, language userLanguage) error {
	if locationButton {
		if messenger, ok := handler.messenger.(KeyboardMessenger); ok {
			return messenger.SendMessageWithKeyboard(ctx, chatID, text, defaultKeyboard(language))
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
	defer os.RemoveAll(dir)
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
