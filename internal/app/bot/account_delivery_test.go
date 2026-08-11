package bot

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"bot_astrosferum/internal/app/directional"
	"bot_astrosferum/internal/forecast"
	"bot_astrosferum/internal/render"
)

type accountDeliveryMessenger struct {
	mu       sync.Mutex
	messages []string
	err      error
}

func (messenger *accountDeliveryMessenger) SendMessage(_ context.Context, _ int64, text string, _ bool) error {
	messenger.mu.Lock()
	defer messenger.mu.Unlock()
	messenger.messages = append(messenger.messages, text)
	return messenger.err
}

func (*accountDeliveryMessenger) SendPhoto(context.Context, int64, string, string) error { return nil }
func (*accountDeliveryMessenger) SendDocument(context.Context, int64, string, string) error {
	return nil
}

type blockingAccountDeliveryProvider struct {
	started chan struct{}
}

func (provider *blockingAccountDeliveryProvider) Vertical(ctx context.Context, _ forecast.Location) (forecast.VerticalSeries, error) {
	select {
	case provider.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return forecast.VerticalSeries{}, ctx.Err()
}

func (*blockingAccountDeliveryProvider) Surface(context.Context, forecast.Location) (forecast.SurfaceSeries, error) {
	return forecast.SurfaceSeries{}, nil
}

func (*blockingAccountDeliveryProvider) Cloud(context.Context, forecast.Location) (forecast.CloudSeries, error) {
	return forecast.CloudSeries{}, nil
}

func TestAccountDeliveryDispatcherFailsClosedUntilTelegramHandlerIsConfigured(t *testing.T) {
	dispatcher, err := NewAccountDeliveryDispatcher(t.Context(), time.Minute, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = dispatcher.AdmitDelivery(t.Context(), directional.DeliveryForecast, directional.DeliveryAdmission{
		TelegramUserID: 42, Latitude: 59.9, Longitude: 30.2, Language: "ru",
	})
	if !errors.Is(err, directional.ErrDeliveryUnavailable) {
		t.Fatalf("unconfigured dispatcher error = %v", err)
	}
}

func TestAccountDeliveryDispatcherConfigurationValidation(t *testing.T) {
	var nilContext context.Context
	if _, err := NewAccountDeliveryDispatcher(nilContext, time.Minute, 1, 1, nil); err == nil {
		t.Fatal("nil root accepted")
	}
	if _, err := NewAccountDeliveryDispatcher(context.Background(), 0, 1, 1, nil); err == nil {
		t.Fatal("zero request timeout accepted")
	}
	if _, err := NewAccountDeliveryDispatcher(context.Background(), time.Minute, 0, 1, nil); err == nil {
		t.Fatal("zero forecast capacity accepted")
	}
	dispatcher, err := NewAccountDeliveryDispatcher(context.Background(), time.Minute, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.SetTelegramHandler(nil); err == nil {
		t.Fatal("nil Telegram handler accepted")
	}
}

func TestAccountDeliveryDispatcherAcknowledgesAndBoundsOutstandingWork(t *testing.T) {
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	messenger := &accountDeliveryMessenger{}
	handler, err := NewHandler(messenger)
	if err != nil {
		t.Fatal(err)
	}
	provider := &blockingAccountDeliveryProvider{started: make(chan struct{}, 1)}
	if err := handler.EnableForecast(provider, t.TempDir(), render.Options{Width: 3200, Height: 960}); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewAccountDeliveryDispatcher(root, time.Minute, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.SetTelegramHandler(handler); err != nil {
		t.Fatal(err)
	}
	first := directional.DeliveryAdmission{TelegramUserID: 42, Latitude: 59.9, Longitude: 30.2, Language: "en"}
	if err := dispatcher.AdmitDelivery(t.Context(), directional.DeliveryForecast, first); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("forecast delivery did not start")
	}
	second := first
	second.TelegramUserID = 43
	if err := dispatcher.AdmitDelivery(t.Context(), directional.DeliveryForecast, second); !errors.Is(err, directional.ErrOwnerBusy) {
		t.Fatalf("capacity error = %v, want owner busy", err)
	}
	messenger.mu.Lock()
	messageCount := len(messenger.messages)
	messenger.mu.Unlock()
	if messageCount < 2 {
		t.Fatalf("Telegram messages = %d, want acknowledgement and workflow progress", messageCount)
	}
}

func TestAccountDeliveryDispatcherRejectsWhenTelegramAcknowledgementFails(t *testing.T) {
	messenger := &accountDeliveryMessenger{err: errors.New("chat unavailable")}
	handler, err := NewHandler(messenger)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewAccountDeliveryDispatcher(t.Context(), time.Minute, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.SetTelegramHandler(handler); err != nil {
		t.Fatal(err)
	}
	err = dispatcher.AdmitDelivery(t.Context(), directional.DeliveryForecast, directional.DeliveryAdmission{
		TelegramUserID: 42, Latitude: 59.9, Longitude: 30.2, Language: "ru",
	})
	if !errors.Is(err, directional.ErrDeliveryUnavailable) {
		t.Fatalf("acknowledgement error = %v, want unavailable", err)
	}
}
