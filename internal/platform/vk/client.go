package vk

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"bot_astrosferum/internal/app/bot"
)

const apiVersion = "5.199"

type Client struct {
	token        string
	groupID      int64
	apiEndpoint  string
	apiHTTP      *http.Client
	longPollHTTP *http.Client
	retryDelay   time.Duration
	messageDelay time.Duration
	randomID     atomic.Uint32
}

var (
	_ bot.Messenger             = (*Client)(nil)
	_ bot.KeyboardMessenger     = (*Client)(nil)
	_ bot.HTMLKeyboardMessenger = (*Client)(nil)
	_ bot.ActionMessenger       = (*Client)(nil)
)

type actionToken struct {
	Version int    `json:"v"`
	EventID string `json:"event_id"`
	UserID  int64  `json:"user_id"`
	PeerID  int64  `json:"peer_id"`
}

type apiError struct {
	Code    int    `json:"error_code"`
	Message string `json:"error_msg"`
}

func (err apiError) Error() string {
	return fmt.Sprintf("VK API error %d: %s", err.Code, err.Message)
}

func NewClient(token string, groupID int64) (*Client, error) {
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, "\r\n\t ") {
		return nil, errors.New("invalid VK token")
	}
	if groupID <= 0 {
		return nil, errors.New("VK group ID must be positive")
	}
	transport, err := newRestrictedTransport()
	if err != nil {
		return nil, err
	}
	client := &Client{
		token:        token,
		groupID:      groupID,
		apiEndpoint:  "https://api.vk.ru/method/",
		apiHTTP:      newHTTPClient(transport, 60*time.Second),
		longPollHTTP: newHTTPClient(transport, 35*time.Second),
		retryDelay:   time.Second,
		messageDelay: 250 * time.Millisecond,
	}
	client.randomID.Store(uint32(time.Now().UnixNano()) & 0x7fffffff)
	return client, nil
}

func (client *Client) GroupID() int64 { return client.groupID }

func (client *Client) call(ctx context.Context, method string, values url.Values, result any) error {
	if values == nil {
		values = make(url.Values)
	}
	values.Set("access_token", client.token)
	values.Set("v", apiVersion)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.apiEndpoint+method, strings.NewReader(values.Encode()))
	if err != nil {
		return fmt.Errorf("create VK %s request: %w", method, err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.apiHTTP.Do(request)
	if err != nil {
		return fmt.Errorf("call VK %s: %w", method, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("VK %s returned HTTP %s", method, response.Status)
	}
	var envelope struct {
		Response json.RawMessage `json:"response"`
		Error    *apiError       `json:"error"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	if err := decoder.Decode(&envelope); err != nil {
		return fmt.Errorf("decode VK %s response: %w", method, err)
	}
	if envelope.Error != nil {
		return *envelope.Error
	}
	if len(envelope.Response) == 0 {
		return fmt.Errorf("VK %s returned no response", method)
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Response, result); err != nil {
		return fmt.Errorf("decode VK %s result: %w", method, err)
	}
	return nil
}

func (client *Client) SendMessage(ctx context.Context, peerID int64, text string, locationButton bool) error {
	if locationButton {
		return client.SendMessageWithKeyboard(ctx, peerID, text, bot.DefaultKeyboard())
	}
	return client.sendWithDelay(ctx, peerID, text, "", nil)
}

func (client *Client) SendMessageWithKeyboard(ctx context.Context, peerID int64, text string, keyboard bot.Keyboard) error {
	encoded, err := encodeKeyboard(keyboard)
	if err != nil {
		return err
	}
	return client.sendWithDelay(ctx, peerID, text, "", encoded)
}

func (client *Client) SendHTMLMessageWithKeyboard(ctx context.Context, peerID int64, text string, keyboard bot.Keyboard) error {
	return client.SendMessageWithKeyboard(ctx, peerID, plainText(text), keyboard)
}

func (client *Client) SendMessageWithActions(ctx context.Context, peerID int64, text string, keyboard bot.ActionKeyboard) error {
	encoded, err := encodeActionKeyboard(keyboard)
	if err != nil {
		return err
	}
	return client.sendWithDelay(ctx, peerID, plainText(text), "", encoded)
}

func (client *Client) AnswerAction(ctx context.Context, token, text string) error {
	decoded, err := decodeActionToken(token)
	if err != nil {
		return err
	}
	values := url.Values{
		"event_id": {decoded.EventID},
		"user_id":  {strconv.FormatInt(decoded.UserID, 10)},
		"peer_id":  {strconv.FormatInt(decoded.PeerID, 10)},
	}
	if text = strings.TrimSpace(plainText(text)); text != "" {
		eventData, err := json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: "show_snackbar", Text: truncateRunes(text, 90)})
		if err != nil {
			return fmt.Errorf("encode VK action answer: %w", err)
		}
		values.Set("event_data", string(eventData))
	}
	return client.call(ctx, "messages.sendMessageEventAnswer", values, nil)
}

func (client *Client) SendPhoto(ctx context.Context, peerID int64, path, caption string) error {
	attachment, err := client.uploadWithRetry(ctx, func() (string, error) {
		return client.uploadPhoto(ctx, peerID, path)
	})
	if err != nil {
		return err
	}
	return client.sendWithDelay(ctx, peerID, plainText(caption), attachment, nil)
}

func (client *Client) SendDocument(ctx context.Context, peerID int64, path, caption string) error {
	attachment, err := client.uploadWithRetry(ctx, func() (string, error) {
		return client.uploadDocument(ctx, peerID, path)
	})
	if err != nil {
		return err
	}
	return client.sendWithDelay(ctx, peerID, plainText(caption), attachment, nil)
}

func (client *Client) sendWithDelay(ctx context.Context, peerID int64, text, attachment string, keyboard json.RawMessage) error {
	if err := client.send(ctx, peerID, text, attachment, keyboard); err != nil {
		return err
	}
	return client.waitMessageDelay(ctx)
}

func (client *Client) waitMessageDelay(ctx context.Context) error {
	delay := client.messageDelay
	if delay <= 0 {
		delay = 250 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (client *Client) uploadWithRetry(ctx context.Context, upload func() (string, error)) (string, error) {
	delay := client.retryDelay
	if delay <= 0 {
		delay = time.Second
	}
	var lastError error
	for attempt := range 3 {
		attachment, err := upload()
		if err == nil {
			return attachment, nil
		}
		lastError = err
		if attempt == 2 {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	return "", fmt.Errorf("VK upload failed after 3 attempts: %w", lastError)
}

func (client *Client) send(ctx context.Context, peerID int64, text, attachment string, keyboard json.RawMessage) error {
	if peerID == 0 {
		return errors.New("VK peer ID is required")
	}
	if strings.TrimSpace(text) == "" && attachment == "" {
		return errors.New("VK message or attachment is required")
	}
	values := url.Values{
		"peer_id":          {strconv.FormatInt(peerID, 10)},
		"random_id":        {strconv.FormatUint(uint64(client.nextRandomID()), 10)},
		"dont_parse_links": {"1"},
		"disable_mentions": {"1"},
	}
	if text != "" {
		values.Set("message", text)
	}
	if attachment != "" {
		values.Set("attachment", attachment)
	}
	if len(keyboard) != 0 {
		values.Set("keyboard", string(keyboard))
	}
	return client.call(ctx, "messages.send", values, nil)
}

func (client *Client) nextRandomID() uint32 {
	for {
		value := client.randomID.Add(1) & 0x7fffffff
		if value != 0 {
			return value
		}
	}
}

func (client *Client) uploadPhoto(ctx context.Context, peerID int64, path string) (string, error) {
	var uploadServer struct {
		UploadURL string `json:"upload_url"`
	}
	if err := client.call(ctx, "photos.getMessagesUploadServer", url.Values{"peer_id": {strconv.FormatInt(peerID, 10)}}, &uploadServer); err != nil {
		return "", err
	}
	if uploadServer.UploadURL == "" {
		return "", errors.New("VK photo upload server returned no URL")
	}
	var uploaded struct {
		Server int64  `json:"server"`
		Photo  string `json:"photo"`
		Hash   string `json:"hash"`
	}
	if err := client.uploadFile(ctx, uploadServer.UploadURL, "photo", path, &uploaded); err != nil {
		return "", fmt.Errorf("upload VK message photo: %w", err)
	}
	if uploaded.Server == 0 || uploaded.Photo == "" || uploaded.Hash == "" {
		return "", errors.New("VK photo upload returned incomplete data")
	}
	var saved []struct {
		OwnerID   int64  `json:"owner_id"`
		ID        int64  `json:"id"`
		AccessKey string `json:"access_key"`
	}
	values := url.Values{
		"server": {strconv.FormatInt(uploaded.Server, 10)},
		"photo":  {uploaded.Photo},
		"hash":   {uploaded.Hash},
	}
	if err := client.call(ctx, "photos.saveMessagesPhoto", values, &saved); err != nil {
		return "", err
	}
	if len(saved) == 0 || saved[0].ID == 0 {
		return "", errors.New("VK saved no message photo")
	}
	return attachmentID("photo", saved[0].OwnerID, saved[0].ID, saved[0].AccessKey), nil
}

func (client *Client) uploadDocument(ctx context.Context, peerID int64, path string) (string, error) {
	var uploadServer struct {
		UploadURL string `json:"upload_url"`
	}
	values := url.Values{"peer_id": {strconv.FormatInt(peerID, 10)}, "type": {"doc"}}
	if err := client.call(ctx, "docs.getMessagesUploadServer", values, &uploadServer); err != nil {
		return "", err
	}
	if uploadServer.UploadURL == "" {
		return "", errors.New("VK document upload server returned no URL")
	}
	var uploaded struct {
		File string `json:"file"`
	}
	if err := client.uploadFile(ctx, uploadServer.UploadURL, "file", path, &uploaded); err != nil {
		return "", fmt.Errorf("upload VK message document: %w", err)
	}
	if uploaded.File == "" {
		return "", errors.New("VK document upload returned incomplete data")
	}
	var saved struct {
		Type string `json:"type"`
		Doc  struct {
			OwnerID   int64  `json:"owner_id"`
			ID        int64  `json:"id"`
			AccessKey string `json:"access_key"`
		} `json:"doc"`
	}
	values = url.Values{"file": {uploaded.File}, "title": {filepath.Base(path)}}
	if err := client.call(ctx, "docs.save", values, &saved); err != nil {
		return "", err
	}
	if saved.Doc.ID == 0 {
		return "", errors.New("VK saved no message document")
	}
	return attachmentID("doc", saved.Doc.OwnerID, saved.Doc.ID, saved.Doc.AccessKey), nil
}

func (client *Client) uploadFile(ctx context.Context, endpoint, field, path string, result any) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open VK upload file: %w", err)
	}
	defer func() { _ = file.Close() }()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(field, filepath.Base(path))
	if err != nil {
		return fmt.Errorf("create VK upload form: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("read VK upload file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish VK upload form: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return fmt.Errorf("create VK upload request: %w", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := client.apiHTTP.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// VK upload endpoints may carry a signed query string. net/http embeds
		// the URL in transport errors, so return a stable secret-free error.
		return errors.New("VK upload transport failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("VK upload returned HTTP %s", response.Status)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(result); err != nil {
		return fmt.Errorf("decode VK upload response: %w", err)
	}
	return nil
}

func encodeKeyboard(keyboard bot.Keyboard) (json.RawMessage, error) {
	if len(keyboard) == 0 {
		return nil, nil
	}
	type button struct {
		Action map[string]string `json:"action"`
		Color  string            `json:"color,omitempty"`
	}
	payload := struct {
		OneTime bool       `json:"one_time"`
		Buttons [][]button `json:"buttons"`
	}{Buttons: make([][]button, 0, len(keyboard))}
	var textButtons []button
	for _, row := range keyboard {
		for _, item := range row {
			if item.RequestLocation {
				payload.Buttons = append(payload.Buttons, []button{{Action: map[string]string{"type": "location", "payload": `{"command":"location"}`}}})
				continue
			}
			label := truncateRunes(strings.TrimSpace(item.Text), 40)
			if label == "" {
				continue
			}
			textButtons = append(textButtons, button{Action: map[string]string{"type": "text", "label": label}, Color: "primary"})
		}
	}
	// VK limits regular keyboards to ten rows. Packing text actions in pairs
	// keeps all ten saved locations plus navigation actions below that limit.
	for len(textButtons) > 0 {
		count := min(2, len(textButtons))
		payload.Buttons = append(payload.Buttons, textButtons[:count])
		textButtons = textButtons[count:]
	}
	if len(payload.Buttons) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode VK keyboard: %w", err)
	}
	return encoded, nil
}

func encodeActionKeyboard(keyboard bot.ActionKeyboard) (json.RawMessage, error) {
	if len(keyboard) == 0 {
		return nil, nil
	}
	type button struct {
		Action map[string]string `json:"action"`
		Color  string            `json:"color,omitempty"`
	}
	payload := struct {
		Inline  bool       `json:"inline"`
		Buttons [][]button `json:"buttons"`
	}{Inline: true, Buttons: make([][]button, 0, len(keyboard))}
	for _, row := range keyboard {
		encodedRow := make([]button, 0, len(row))
		for _, item := range row {
			label := truncateRunes(strings.TrimSpace(item.Text), 40)
			if label == "" {
				return nil, errors.New("VK action button text is required")
			}
			if len(item.Data) == 0 || len(item.Data) > 64 {
				return nil, errors.New("VK callback data must contain 1 to 64 bytes")
			}
			callbackPayload, err := json.Marshal(struct {
				Action string `json:"action"`
			}{Action: item.Data})
			if err != nil {
				return nil, fmt.Errorf("encode VK action payload: %w", err)
			}
			encodedRow = append(encodedRow, button{
				Action: map[string]string{
					"type":    "callback",
					"label":   label,
					"payload": string(callbackPayload),
				},
				Color: "primary",
			})
		}
		if len(encodedRow) > 0 {
			payload.Buttons = append(payload.Buttons, encodedRow)
		}
	}
	if len(payload.Buttons) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode VK action keyboard: %w", err)
	}
	return encoded, nil
}

func encodeActionToken(eventID string, userID, peerID int64) (string, error) {
	if strings.TrimSpace(eventID) == "" || userID <= 0 || peerID == 0 {
		return "", errors.New("invalid VK action token fields")
	}
	encoded, err := json.Marshal(actionToken{Version: 1, EventID: eventID, UserID: userID, PeerID: peerID})
	if err != nil {
		return "", fmt.Errorf("encode VK action token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeActionToken(value string) (actionToken, error) {
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return actionToken{}, errors.New("invalid VK action token")
	}
	var token actionToken
	if err := json.Unmarshal(encoded, &token); err != nil || token.Version != 1 || strings.TrimSpace(token.EventID) == "" || token.UserID <= 0 || token.PeerID == 0 {
		return actionToken{}, errors.New("invalid VK action token")
	}
	return token, nil
}

func attachmentID(kind string, ownerID, id int64, accessKey string) string {
	result := fmt.Sprintf("%s%d_%d", kind, ownerID, id)
	if accessKey != "" {
		result += "_" + accessKey
	}
	return result
}

func plainText(value string) string {
	value = strings.NewReplacer("<b>", "", "</b>", "", "<i>", "", "</i>", "").Replace(value)
	return html.UnescapeString(value)
}

func truncateRunes(value string, maximum int) string {
	if maximum < 1 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum-1]) + "…"
}
