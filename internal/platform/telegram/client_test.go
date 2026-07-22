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
)

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
	if err := client.SendHTMLMessageWithKeyboard(context.Background(), 42, "Bortle <b>8–9</b>", DefaultKeyboard()); err != nil {
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
