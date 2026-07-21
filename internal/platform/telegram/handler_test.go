package telegram

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

type sentMessage struct {
	chatID         int64
	text           string
	locationButton bool
}

type fakeMessenger struct {
	messages []sentMessage
	photos   []string
}

func (messenger *fakeMessenger) SendPhoto(_ context.Context, _ int64, path, _ string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	messenger.photos = append(messenger.photos, path)
	return nil
}

func (messenger *fakeMessenger) SendMessage(_ context.Context, chatID int64, text string, locationButton bool) error {
	messenger.messages = append(messenger.messages, sentMessage{chatID: chatID, text: text, locationButton: locationButton})
	return nil
}

func TestStartProvidesUsageAndInterpretation(t *testing.T) {
	messenger := &fakeMessenger{}
	handler, err := NewHandler(messenger)
	if err != nil {
		t.Fatal(err)
	}
	err = handler.Handle(context.Background(), Update{Message: &Message{Chat: Chat{ID: 42}, Text: "/start"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(messenger.messages) != 1 || !messenger.messages[0].locationButton {
		t.Fatalf("unexpected messages: %#v", messenger.messages)
	}
	for _, expected := range []string{
		"/forecast 59.9386 30.3141", "Forecast Wind Seeing Index", "1…10", "часовой зоне",
		"по горизонтали", "сдвиг", "облачность не меняет wind-based seeing", "850 hPa ≈ 1,5 км",
		"нижние", "средние", "верхние", "длинные выдержки", "не означает плохой сиинг",
		"ICON (TKE до динамической MH 500–2000 м AGL + HMNSP99 выше)", "MH — почасовая", "τ₀", "f/F", "в Overall Index она не входит",
	} {
		if !strings.Contains(messenger.messages[0].text, expected) {
			t.Fatalf("help does not contain %q", expected)
		}
	}
	if len([]byte(messenger.messages[0].text)) > 4096 {
		t.Fatalf("Telegram help is too long: %d bytes", len([]byte(messenger.messages[0].text)))
	}
}

func TestNativeLocationResolvesCoordinateTimezone(t *testing.T) {
	messenger := &fakeMessenger{}
	handler, err := NewHandler(messenger)
	if err != nil {
		t.Fatal(err)
	}
	err = handler.Handle(context.Background(), Update{Message: &Message{
		Chat: Chat{ID: 42}, Location: &Location{Latitude: 59.9386, Longitude: 30.3141},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(messenger.messages) != 1 || !strings.Contains(messenger.messages[0].text, "Europe/Moscow · MSK (UTC+3)") {
		t.Fatalf("unexpected reply: %#v", messenger.messages)
	}
}

func TestForecastCommandValidationError(t *testing.T) {
	messenger := &fakeMessenger{}
	handler, err := NewHandler(messenger)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.Handle(context.Background(), Update{Message: &Message{Chat: Chat{ID: 42}, Text: "/forecast nope"}}); err != nil {
		t.Fatal(err)
	}
	if len(messenger.messages) != 1 || !strings.Contains(messenger.messages[0].text, "Не удалось") {
		t.Fatalf("unexpected reply: %#v", messenger.messages)
	}
}

func TestForecastFreshnessText(t *testing.T) {
	now := time.Date(2026, time.July, 21, 15, 30, 0, 0, time.UTC)

	fresh := forecastFreshnessText(now.Add(-9*time.Hour-17*time.Minute), now, 12*time.Hour)
	for _, expected := range []string{"Актуальность данных (freshness)", "run актуален", "9 ч 17 мин", "порог 12 ч 0 мин"} {
		if !strings.Contains(fresh, expected) {
			t.Fatalf("fresh status %q does not contain %q", fresh, expected)
		}
	}

	stale := forecastFreshnessText(now.Add(-14*time.Hour-2*time.Minute), now, 12*time.Hour)
	for _, expected := range []string{"⚠️", "Данные устарели (stale run)", "14 ч 2 мин", "порог 12 ч 0 мин", "последние изменения атмосферы"} {
		if !strings.Contains(stale, expected) {
			t.Fatalf("stale status %q does not contain %q", stale, expected)
		}
	}
}

func TestForecastFreshnessTreatsClockSkewAsZeroAge(t *testing.T) {
	now := time.Date(2026, time.July, 21, 15, 30, 0, 0, time.UTC)
	status := forecastFreshnessText(now.Add(time.Minute), now, 12*time.Hour)
	if !strings.Contains(status, "run актуален, возраст 0 мин") {
		t.Fatalf("unexpected clock-skew status: %q", status)
	}
}
