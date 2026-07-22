package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"bot_astrosferum/internal/app/bot"
)

func TestUpdateShardPreservesChatAffinity(t *testing.T) {
	first := bot.Update{ID: 1, Message: &bot.Message{Chat: bot.Chat{ID: -12345}}}
	second := bot.Update{ID: 2, Message: &bot.Message{Chat: bot.Chat{ID: -12345}}}
	if updateShard(first, 6) != updateShard(second, 6) {
		t.Fatal("updates for one chat must use one worker")
	}
	if updateShard(bot.Update{ID: 3}, 6) != 0 {
		t.Fatal("updates without messages must use shard zero")
	}
	action := bot.Update{ID: 4, Action: &bot.ActionInvocation{Chat: bot.Chat{ID: -12345}}}
	if updateShard(action, 6) != updateShard(first, 6) {
		t.Fatal("callbacks and messages for one chat must use one worker")
	}
}

func TestGetUpdatesConvertsCallbackAndRequestsIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/getUpdates" {
			t.Errorf("request path = %q, want /getUpdates", request.URL.Path)
		}
		var payload struct {
			AllowedUpdates []string `json:"allowed_updates"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.AllowedUpdates) != 2 || payload.AllowedUpdates[0] != "message" || payload.AllowedUpdates[1] != "callback_query" {
			t.Fatalf("allowed_updates = %v", payload.AllowedUpdates)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":true,"result":[{"update_id":7,"callback_query":{"id":"callback-1","from":{"id":9,"language_code":"en"},"message":{"message_id":5,"chat":{"id":42}},"data":"v1:horizon.v1:point"}}]}`))
	}))
	defer server.Close()

	client := &Client{httpClient: server.Client(), endpoint: server.URL + "/"}
	updates, err := client.getUpdates(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || updates[0].Action == nil {
		t.Fatalf("updates = %+v", updates)
	}
	action := updates[0].Action
	if action.Token != "callback-1" || action.Data != "v1:horizon.v1:point" || action.Chat.ID != 42 || action.From == nil || action.From.ID != 9 {
		t.Fatalf("action = %+v", action)
	}
}

func TestSendMessageWithActionsAndAnswerAction(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/sendMessage":
			var payload struct {
				ChatID      int64 `json:"chat_id"`
				ReplyMarkup struct {
					InlineKeyboard [][]struct {
						Text string `json:"text"`
						Data string `json:"callback_data"`
					} `json:"inline_keyboard"`
				} `json:"reply_markup"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.ChatID != 42 || len(payload.ReplyMarkup.InlineKeyboard) != 1 || payload.ReplyMarkup.InlineKeyboard[0][0].Text != "Horizon" || payload.ReplyMarkup.InlineKeyboard[0][0].Data != "v1:horizon.v1:point" {
				t.Fatalf("send payload = %+v", payload)
			}
		case "/answerCallbackQuery":
			var payload struct {
				ID   string `json:"callback_query_id"`
				Text string `json:"text"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload.ID != "callback-1" || payload.Text != "Queued" {
				t.Fatalf("answer payload = %+v", payload)
			}
		default:
			t.Errorf("unexpected path %q", request.URL.Path)
		}
		_, _ = response.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer server.Close()

	client := &Client{httpClient: server.Client(), endpoint: server.URL + "/"}
	keyboard := bot.ActionKeyboard{{{Text: "Horizon", Data: "v1:horizon.v1:point"}}}
	if err := client.SendMessageWithActions(context.Background(), 42, "Choose", keyboard); err != nil {
		t.Fatal(err)
	}
	if err := client.AnswerAction(context.Background(), "callback-1", "Queued"); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestActionKeyboardRejectsOversizedCallbackData(t *testing.T) {
	_, err := encodeActionKeyboard(bot.ActionKeyboard{{{Text: "Horizon", Data: string(make([]byte, 65))}}})
	if err == nil {
		t.Fatal("oversized callback_data unexpectedly accepted")
	}
}

func TestSendHTMLMessageWithKeyboardUsesHTMLParseMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/sendMessage" {
			t.Errorf("request path = %q, want /sendMessage", request.URL.Path)
		}
		var payload struct {
			Text      string `json:"text"`
			ParseMode string `json:"parse_mode"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode sendMessage payload: %v", err)
		}
		if payload.Text != "Bortle <b>8–9</b>" || payload.ParseMode != "HTML" {
			t.Errorf("unexpected payload: %+v", payload)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer server.Close()

	client := &Client{httpClient: server.Client(), endpoint: server.URL + "/"}
	if err := client.SendHTMLMessageWithKeyboard(context.Background(), 42, "Bortle <b>8–9</b>", bot.DefaultKeyboard()); err != nil {
		t.Fatal(err)
	}
}

func TestSendDocumentUploadsOriginalFile(t *testing.T) {
	payload := []byte("lossless-png-payload")
	path := filepath.Join(t.TempDir(), "chart.png")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/sendDocument" {
			t.Errorf("request path = %q, want /sendDocument", request.URL.Path)
		}
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart form: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := request.FormValue("chat_id"); got != "42" {
			t.Errorf("chat_id = %q, want 42", got)
		}
		if got := request.FormValue("caption"); got != "<b>localized</b> <i>caption</i>" {
			t.Errorf("caption = %q", got)
		}
		if got := request.FormValue("parse_mode"); got != "HTML" {
			t.Errorf("parse_mode = %q, want HTML", got)
		}
		if got := request.FormValue("disable_content_type_detection"); got != "true" {
			t.Errorf("disable_content_type_detection = %q, want true", got)
		}
		file, header, err := request.FormFile("document")
		if err != nil {
			t.Errorf("document part: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		defer func() { _ = file.Close() }()
		content, err := io.ReadAll(file)
		if err != nil {
			t.Errorf("read document: %v", err)
		}
		if header.Filename != "chart.png" {
			t.Errorf("filename = %q, want chart.png", header.Filename)
		}
		if string(content) != string(payload) {
			t.Errorf("uploaded document was changed")
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer server.Close()

	client := &Client{httpClient: server.Client(), endpoint: server.URL + "/"}
	if err := client.SendDocument(context.Background(), 42, path, "<b>localized</b> <i>caption</i>"); err != nil {
		t.Fatal(err)
	}
}

func TestSendPhotoUsesHTMLCaption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chart.png")
	if err := os.WriteFile(path, []byte("png-payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/sendPhoto" {
			t.Errorf("request path = %q, want /sendPhoto", request.URL.Path)
		}
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart form: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := request.FormValue("caption"); got != "<b>title</b>\n<i>comment</i>" {
			t.Errorf("caption = %q", got)
		}
		if got := request.FormValue("parse_mode"); got != "HTML" {
			t.Errorf("parse_mode = %q, want HTML", got)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer server.Close()

	client := &Client{httpClient: server.Client(), endpoint: server.URL + "/"}
	if err := client.SendPhoto(context.Background(), 42, path, "<b>title</b>\n<i>comment</i>"); err != nil {
		t.Fatal(err)
	}
}
