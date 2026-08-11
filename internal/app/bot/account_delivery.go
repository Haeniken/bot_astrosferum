package bot

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"bot_astrosferum/internal/app/directional"
	"bot_astrosferum/internal/forecast"
)

// AccountDeliveryDispatcher is the narrow bridge from an authenticated
// first-party account client to the existing Telegram delivery workflows. It does not calculate
// or render anything itself: the configured Telegram Handler, forecast queue,
// Horizon FIFO, caches, and persistence remain authoritative.
type AccountDeliveryDispatcher struct {
	root           context.Context
	logf           func(string, ...any)
	requestTimeout time.Duration
	forecastSlots  chan struct{}
	horizonSlots   chan struct{}

	mu      sync.Mutex
	handler *Handler
	active  map[string]struct{}
}

func NewAccountDeliveryDispatcher(root context.Context, requestTimeout time.Duration, forecastCapacity, horizonCapacity int, logf func(string, ...any)) (*AccountDeliveryDispatcher, error) {
	if root == nil {
		return nil, errors.New("account delivery root context is required")
	}
	if requestTimeout <= 0 {
		return nil, errors.New("account delivery request timeout must be positive")
	}
	if forecastCapacity < 1 || horizonCapacity < 1 {
		return nil, errors.New("account delivery capacities must be positive")
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &AccountDeliveryDispatcher{
		root: root, requestTimeout: requestTimeout, logf: logf,
		forecastSlots: make(chan struct{}, forecastCapacity), horizonSlots: make(chan struct{}, horizonCapacity),
		active: make(map[string]struct{}),
	}, nil
}

// SetTelegramHandler is a one-time composition hook. The dispatcher is
// created before the internal HTTP listener and becomes available as soon as
// the Telegram adapter has been fully configured.
func (dispatcher *AccountDeliveryDispatcher) SetTelegramHandler(handler *Handler) error {
	if handler == nil {
		return errors.New("telegram handler is required")
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if dispatcher.handler != nil {
		return errors.New("telegram handler is already configured")
	}
	dispatcher.handler = handler
	return nil
}

func (dispatcher *AccountDeliveryDispatcher) AdmitDelivery(ctx context.Context, kind directional.DeliveryKind, admission directional.DeliveryAdmission) error {
	if err := admission.Validate(); err != nil {
		return err
	}
	slots, err := dispatcher.slots(kind)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%s:%d", kind, admission.TelegramUserID)
	dispatcher.mu.Lock()
	if dispatcher.root.Err() != nil || dispatcher.handler == nil {
		dispatcher.mu.Unlock()
		return directional.ErrDeliveryUnavailable
	}
	if _, exists := dispatcher.active[key]; exists {
		dispatcher.mu.Unlock()
		return directional.ErrOwnerBusy
	}
	select {
	case slots <- struct{}{}:
	default:
		dispatcher.mu.Unlock()
		return directional.ErrOwnerBusy
	}
	handler := dispatcher.handler
	dispatcher.active[key] = struct{}{}
	dispatcher.mu.Unlock()
	release := func() {
		dispatcher.mu.Lock()
		delete(dispatcher.active, key)
		dispatcher.mu.Unlock()
		<-slots
	}
	if err := handler.acknowledgeAccountDelivery(ctx, kind, admission); err != nil {
		release()
		return directional.ErrDeliveryUnavailable
	}

	go func() {
		defer release()
		operationContext, cancel := context.WithTimeout(dispatcher.root, dispatcher.requestTimeout)
		defer cancel()
		var deliveryErr error
		switch kind {
		case directional.DeliveryForecast:
			deliveryErr = handler.DeliverForecast(operationContext, admission.TelegramUserID, admission.Latitude, admission.Longitude, admission.Language)
		case directional.DeliveryHorizon:
			deliveryErr = handler.DeliverHorizon(operationContext, admission.TelegramUserID, admission.Latitude, admission.Longitude, admission.Language)
		default:
			deliveryErr = errors.New("unsupported account delivery kind")
		}
		if deliveryErr != nil && dispatcher.root.Err() == nil {
			dispatcher.logf("account %s delivery for Telegram user failed: %v", kind, deliveryErr)
			handler.reportAccountDeliveryFailure(admission.TelegramUserID, admission.Language)
		}
	}()
	return nil
}

func (dispatcher *AccountDeliveryDispatcher) slots(kind directional.DeliveryKind) (chan struct{}, error) {
	switch kind {
	case directional.DeliveryForecast:
		return dispatcher.forecastSlots, nil
	case directional.DeliveryHorizon:
		return dispatcher.horizonSlots, nil
	default:
		return nil, errors.New("unsupported account delivery kind")
	}
}

func (handler *Handler) acknowledgeAccountDelivery(ctx context.Context, kind directional.DeliveryKind, admission directional.DeliveryAdmission) error {
	language := languageFromCode(admission.Language)
	label := language.text("прогноза", "forecast")
	if kind == directional.DeliveryHorizon {
		label = language.text("анализа горизонта", "Horizon analysis")
	}
	return handler.sendUserMessage(ctx, admission.TelegramUserID, fmt.Sprintf(language.text(
		"Запрос %s с сайта принят. Очередь, ход расчёта и возможная ошибка будут показаны в этом диалоге.",
		"Your website request for %s was accepted. Queue position, progress, and any error will be reported in this conversation."), label), true, language)
}

func (handler *Handler) reportAccountDeliveryFailure(telegramUserID int64, languageCode string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	language := languageFromCode(languageCode)
	if err := handler.sendUserMessage(ctx, telegramUserID, language.text(
		"Запрос с сайта не удалось завершить. Попробуйте повторить позже.",
		"The website request could not be completed. Please try again later."), true, language); err != nil {
		handler.logf("report account delivery failure to Telegram user: %v", err)
	}
}

// DeliverForecast invokes the exact ordinary bot workflow and sends all seven
// charts to the authenticated user's Telegram conversation.
func (handler *Handler) DeliverForecast(ctx context.Context, telegramUserID int64, latitude, longitude float64, languageCode string) error {
	if telegramUserID <= 0 {
		return errors.New("positive Telegram user ID is required")
	}
	return handler.replyToLocation(ctx, telegramUserID, telegramUserID, latitude, longitude, languageFromCode(languageCode))
}

// DeliverHorizon resolves the current ICON-EU run and model-surface height at
// the requested point, then submits the same Horizon job used by the signed
// in-bot button. ICON Global remains unsupported by the scientific contract.
func (handler *Handler) DeliverHorizon(ctx context.Context, telegramUserID int64, latitude, longitude float64, languageCode string) error {
	if telegramUserID <= 0 {
		return errors.New("positive Telegram user ID is required")
	}
	language := languageFromCode(languageCode)
	if handler.provider == nil || handler.horizon == nil {
		return handler.sendUserMessage(ctx, telegramUserID, language.text(
			"Анализ горизонта сейчас недоступен.",
			"Horizon analysis is currently unavailable."), true, language)
	}
	messenger, ok := handler.messenger.(HorizonMessenger)
	if !ok {
		return errors.New("telegram messenger does not support Horizon delivery")
	}
	location, err := forecast.NewLocation(latitude, longitude, handler.resolver.Resolve(latitude, longitude))
	if err != nil {
		return err
	}
	if err := handler.sendUserMessage(ctx, telegramUserID, language.text(
		"Готовлю актуальные данные для расчёта горизонта…",
		"Preparing current data for the Horizon calculation…"), true, language); err != nil {
		return err
	}
	if handler.forecastQueue != nil {
		release, queueErr := handler.forecastQueue.Wait(ctx, func(position int) error {
			return handler.sendUserMessage(ctx, telegramUserID, fmt.Sprintf(language.text(
				"Подготовка данных горизонта поставлена в очередь: ваше место — %d.",
				"Horizon data preparation queued: your position is %d."), position), true, language)
		})
		if queueErr != nil {
			return queueErr
		}
		defer release()
	}
	cloud, err := handler.provider.Cloud(ctx, location)
	if err != nil {
		return handler.sendUserMessage(ctx, telegramUserID, language.text(
			"Не удалось получить актуальные данные ICON-EU для горизонта.",
			"Could not obtain current ICON-EU data for Horizon."), true, language)
	}
	if cloud.Provider != HorizonProviderICONEU || cloud.RunID == "" {
		return handler.sendUserMessage(ctx, telegramUserID, language.text(
			"Для этой точки «Горизонт» недоступен: функция требует ICON-EU.",
			"Horizon is unavailable at this point because it requires ICON-EU."), true, language)
	}
	return handler.horizon.Deliver(ctx, "telegram", messenger, telegramUserID, telegramUserID, HorizonButtonRequest{
		Provider: HorizonProviderICONEU, RunID: cloud.RunID, Location: location,
		ObserverSurfaceElevationM: cloud.SurfaceElevationM,
	}, language.renderCode())
}

var _ directional.DeliveryBackend = (*AccountDeliveryDispatcher)(nil)
