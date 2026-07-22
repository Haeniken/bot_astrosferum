package vk

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bot_astrosferum/internal/app/bot"
)

func TestEmbeddedRussianCertificatesAreCurrent(t *testing.T) {
	for name, encoded := range map[string][]byte{
		"root": russianTrustedRootCA,
		"sub":  russianTrustedSubCA,
	} {
		block, _ := pem.Decode(encoded)
		if block == nil {
			t.Fatalf("%s certificate is not PEM", name)
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatalf("parse %s certificate: %v", name, err)
		}
		if certificate.NotAfter.Before(time.Now().Add(30 * 24 * time.Hour)) {
			t.Fatalf("%s certificate expires too soon: %s", name, certificate.NotAfter)
		}
	}
}

func TestNewClientValidation(t *testing.T) {
	for _, test := range []struct {
		token string
		group int64
	}{
		{token: "", group: 1},
		{token: "bad token", group: 1},
		{token: "valid", group: 0},
	} {
		if _, err := NewClient(test.token, test.group); err == nil {
			t.Fatalf("NewClient(%q, %d) unexpectedly succeeded", test.token, test.group)
		}
	}
}

func TestNewClientUsesVKRUAPI(t *testing.T) {
	client, err := NewClient("valid", 1)
	if err != nil {
		t.Fatal(err)
	}
	if client.apiEndpoint != "https://api.vk.ru/method/" {
		t.Fatalf("API endpoint = %q", client.apiEndpoint)
	}
}

func TestSendHTMLMessageUsesVKKeyboardAndPlainText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/method/messages.send" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := request.Form.Get("message"); got != "Bortle 8–9 & clear" {
			t.Errorf("message = %q", got)
		}
		if request.Form.Get("access_token") != "test-token" || request.Form.Get("v") != apiVersion {
			t.Error("VK credentials/version were not included")
		}
		var keyboard struct {
			Buttons [][]struct {
				Action map[string]string `json:"action"`
			} `json:"buttons"`
		}
		if err := json.Unmarshal([]byte(request.Form.Get("keyboard")), &keyboard); err != nil {
			t.Fatal(err)
		}
		if keyboard.Buttons[0][0].Action["type"] != "location" || keyboard.Buttons[1][0].Action["label"] != "Saved" {
			t.Fatalf("unexpected keyboard: %+v", keyboard)
		}
		writeAPIResponse(response, `1`)
	}))
	defer server.Close()
	client := testClient(server.URL)
	keyboard := bot.Keyboard{{{Text: "Share", RequestLocation: true}}, {{Text: "Saved"}}}
	if err := client.SendHTMLMessageWithKeyboard(context.Background(), 42, "Bortle <b>8–9</b> &amp; <i>clear</i>", keyboard); err != nil {
		t.Fatal(err)
	}
}

func TestSendMessageWithActionsAndAnswerAction(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		switch request.URL.Path {
		case "/method/messages.send":
			if request.Form.Get("peer_id") != "42" || request.Form.Get("message") != "Choose horizon" {
				t.Fatalf("unexpected send form: %v", request.Form)
			}
			var keyboard struct {
				Inline  bool `json:"inline"`
				Buttons [][]struct {
					Action struct {
						Type    string `json:"type"`
						Label   string `json:"label"`
						Payload string `json:"payload"`
					} `json:"action"`
				} `json:"buttons"`
			}
			if err := json.Unmarshal([]byte(request.Form.Get("keyboard")), &keyboard); err != nil {
				t.Fatal(err)
			}
			if !keyboard.Inline || len(keyboard.Buttons) != 1 {
				t.Fatalf("unexpected action keyboard: %+v", keyboard)
			}
			action := keyboard.Buttons[0][0].Action
			if action.Type != "callback" || action.Label != "Horizon" || action.Payload != `{"action":"v1:horizon.v1:point"}` {
				t.Fatalf("unexpected callback action: %+v", action)
			}
			writeAPIResponse(response, `1`)
		case "/method/messages.sendMessageEventAnswer":
			if request.Form.Get("event_id") != "event-1" || request.Form.Get("user_id") != "9" || request.Form.Get("peer_id") != "42" {
				t.Fatalf("unexpected answer form: %v", request.Form)
			}
			var eventData struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal([]byte(request.Form.Get("event_data")), &eventData); err != nil {
				t.Fatal(err)
			}
			if eventData.Type != "show_snackbar" || eventData.Text != "Queued" {
				t.Fatalf("event_data = %+v", eventData)
			}
			writeAPIResponse(response, `1`)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	client := testClient(server.URL)
	keyboard := bot.ActionKeyboard{{{Text: "Horizon", Data: "v1:horizon.v1:point"}}}
	if err := client.SendMessageWithActions(context.Background(), 42, "<b>Choose</b> <i>horizon</i>", keyboard); err != nil {
		t.Fatal(err)
	}
	token, err := encodeActionToken("event-1", 9, 42)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.AnswerAction(context.Background(), token, "Queued"); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestActionTokenValidation(t *testing.T) {
	if _, err := decodeActionToken("not-a-token"); err == nil {
		t.Fatal("invalid token unexpectedly accepted")
	}
	if _, err := encodeActionToken("", 1, 1); err == nil {
		t.Fatal("empty event id unexpectedly accepted")
	}
}

func TestEncodeKeyboardFitsTenSavedLocations(t *testing.T) {
	keyboard := bot.Keyboard{{{Text: "location", RequestLocation: true}}}
	for index := range 10 {
		keyboard = append(keyboard, []bot.Button{{Text: fmt.Sprintf("Point %d", index+1)}})
	}
	keyboard = append(keyboard,
		[]bot.Button{{Text: "Save"}, {Text: "My locations"}},
		[]bot.Button{{Text: "Back"}},
	)
	encoded, err := encodeKeyboard(keyboard)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Buttons [][]json.RawMessage `json:"buttons"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Buttons) > 10 {
		t.Fatalf("keyboard has %d rows, want at most 10", len(payload.Buttons))
	}
	buttons := 0
	for _, row := range payload.Buttons {
		buttons += len(row)
	}
	if buttons != 14 {
		t.Fatalf("keyboard has %d buttons, want 14", buttons)
	}
}

func TestMediaDeliveryDelayDoesNotSerializeConcurrentRequests(t *testing.T) {
	client := &Client{mediaDelay: 20 * time.Millisecond}
	started := time.Now()
	done := make(chan error, 2)
	for range 2 {
		go func() { done <- client.waitMediaDelay(context.Background()) }()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(started); elapsed < 15*time.Millisecond || elapsed >= 35*time.Millisecond {
		t.Fatalf("concurrent media delay = %s, want one independent 20ms interval", elapsed)
	}
}

func TestSendPhotoUploadsAndAttachesPNG(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/method/photos.getMessagesUploadServer":
			writeAPIResponse(response, `{"upload_url":"`+server.URL+`/upload/photo"}`)
		case "/upload/photo":
			assertUploadedFile(t, request, "photo", "chart.png", "png-data")
			_, _ = io.WriteString(response, `{"server":7,"photo":"photo-token","hash":"hash-token"}`)
		case "/method/photos.saveMessagesPhoto":
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if request.Form.Get("photo") != "photo-token" || request.Form.Get("hash") != "hash-token" {
				t.Fatalf("unexpected save form: %v", request.Form)
			}
			writeAPIResponse(response, `[{"owner_id":-240376006,"id":15,"access_key":"key"}]`)
		case "/method/messages.send":
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if request.Form.Get("attachment") != "photo-240376006_15_key" || request.Form.Get("message") != "Chart caption" {
				t.Fatalf("unexpected send form: %v", request.Form)
			}
			writeAPIResponse(response, `99`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "chart.png")
	if err := os.WriteFile(path, []byte("png-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := testClient(server.URL).SendPhoto(context.Background(), 42, path, "<b>Chart</b> <i>caption</i>"); err != nil {
		t.Fatal(err)
	}
}

func TestSendDocumentUploadsAndAttachesPNG(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/method/docs.getMessagesUploadServer":
			writeAPIResponse(response, `{"upload_url":"`+server.URL+`/upload/doc"}`)
		case "/upload/doc":
			assertUploadedFile(t, request, "file", "large.png", "lossless-png")
			_, _ = io.WriteString(response, `{"file":"document-token"}`)
		case "/method/docs.save":
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if request.Form.Get("file") != "document-token" || request.Form.Get("title") != "large.png" {
				t.Fatalf("unexpected save form: %v", request.Form)
			}
			writeAPIResponse(response, `{"type":"doc","doc":{"owner_id":-240376006,"id":16}}`)
		case "/method/messages.send":
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if request.Form.Get("attachment") != "doc-240376006_16" {
				t.Fatalf("unexpected attachment: %v", request.Form)
			}
			writeAPIResponse(response, `100`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "large.png")
	if err := os.WriteFile(path, []byte("lossless-png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := testClient(server.URL).SendDocument(context.Background(), 42, path, "caption"); err != nil {
		t.Fatal(err)
	}
}

func TestSendDocumentRetriesWithFreshUploadServer(t *testing.T) {
	var uploadRequests int
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/method/docs.getMessagesUploadServer":
			writeAPIResponse(response, `{"upload_url":"`+server.URL+`/upload/doc"}`)
		case "/upload/doc":
			uploadRequests++
			if uploadRequests == 1 {
				response.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			assertUploadedFile(t, request, "file", "large.png", "lossless-png")
			_, _ = io.WriteString(response, `{"file":"document-token"}`)
		case "/method/docs.save":
			writeAPIResponse(response, `{"type":"doc","doc":{"owner_id":-1,"id":16}}`)
		case "/method/messages.send":
			writeAPIResponse(response, `100`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "large.png")
	if err := os.WriteFile(path, []byte("lossless-png"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := testClient(server.URL)
	client.retryDelay = time.Millisecond
	if err := client.SendDocument(context.Background(), 42, path, "caption"); err != nil {
		t.Fatal(err)
	}
	if uploadRequests != 2 {
		t.Fatalf("upload requests = %d, want 2", uploadRequests)
	}
}

func TestAPIErrorsDoNotExposeToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(response, `{"error":{"error_code":5,"error_msg":"authorization failed"}}`)
	}))
	defer server.Close()
	client := testClient(server.URL)
	err := client.call(context.Background(), "groups.getLongPollServer", url.Values{}, nil)
	if err == nil || strings.Contains(err.Error(), client.token) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUploadTransportErrorDoesNotExposeSignedURL(t *testing.T) {
	const signedURL = "https://upload.vk.ru/document?sig=upload-secret"
	path := filepath.Join(t.TempDir(), "chart.png")
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := testClient("https://example.invalid")
	client.apiHTTP = &http.Client{Transport: failingRoundTripper(func(request *http.Request) error {
		return fmt.Errorf("dial failed for %s", request.URL.String())
	})}
	var result map[string]any
	err := client.uploadFile(context.Background(), signedURL, "file", path, &result)
	if err == nil || err.Error() != "VK upload transport failed" {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if strings.Contains(err.Error(), "upload-secret") || strings.Contains(err.Error(), signedURL) {
		t.Fatalf("upload error exposed signed URL: %v", err)
	}
}

type failingRoundTripper func(*http.Request) error

func (transport failingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return nil, transport(request)
}

func testClient(serverURL string) *Client {
	client, err := NewClient("test-token", 240376006)
	if err != nil {
		panic(err)
	}
	client.apiEndpoint = serverURL + "/method/"
	client.apiHTTP = http.DefaultClient
	client.longPollHTTP = http.DefaultClient
	client.mediaDelay = time.Nanosecond
	return client
}

func writeAPIResponse(response http.ResponseWriter, body string) {
	response.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(response, `{"response":`+body+`}`)
}

func assertUploadedFile(t *testing.T, request *http.Request, field, filename, content string) {
	t.Helper()
	if err := request.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}
	file, header, err := request.FormFile(field)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if header.Filename != filename || string(data) != content {
		t.Fatalf("upload = %q %q", header.Filename, data)
	}
}
