package telegram

import (
	"context"
	"os"
	"strings"
	"testing"
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
