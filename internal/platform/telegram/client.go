package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"bot_astrosferum/internal/app/bot"
)

type Client struct {
	httpClient *http.Client
	endpoint   string
}

var (
	_ bot.Messenger             = (*Client)(nil)
	_ bot.KeyboardMessenger     = (*Client)(nil)
	_ bot.HTMLKeyboardMessenger = (*Client)(nil)
	_ bot.ActionMessenger       = (*Client)(nil)
)

type incomingUpdate struct {
	ID            int64          `json:"update_id"`
	Message       *bot.Message   `json:"message"`
	CallbackQuery *callbackQuery `json:"callback_query"`
}

type callbackQuery struct {
	ID      string       `json:"id"`
	From    *bot.User    `json:"from"`
	Message *bot.Message `json:"message"`
	Data    string       `json:"data"`
}

func NewClient(token string) (*Client, error) {
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, "\r\n/ ") {
		return nil, errors.New("invalid Telegram token")
	}
	return &Client{
		httpClient: &http.Client{Timeout: 40 * time.Second},
		endpoint:   "https://api.telegram.org/bot" + token + "/",
	}, nil
}

func (client *Client) Run(ctx context.Context, handler *bot.Handler, workers int, requestTimeout time.Duration, logf func(string, ...any)) error {
	if handler == nil {
		return errors.New("telegram handler is required")
	}
	if workers < 1 {
		workers = 1
	}
	if requestTimeout <= 0 {
		requestTimeout = 15 * time.Minute
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	queues := make([]chan bot.Update, workers)
	var workerGroup sync.WaitGroup
	for index := range queues {
		queues[index] = make(chan bot.Update, 8)
		workerGroup.Add(1)
		go func(queue <-chan bot.Update) {
			defer workerGroup.Done()
			for update := range queue {
				started := time.Now()
				requestContext, cancel := context.WithTimeout(ctx, requestTimeout)
				err := handler.Handle(requestContext, update)
				cancel()
				if err != nil {
					logf("telegram update %d failed duration=%s: %v", update.ID, time.Since(started).Round(time.Millisecond), err)
					continue
				}
				logf("telegram update %d processed duration=%s", update.ID, time.Since(started).Round(time.Millisecond))
			}
		}(queues[index])
	}
	defer func() {
		for _, queue := range queues {
			close(queue)
		}
		workerGroup.Wait()
	}()
	var offset int64
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		updates, err := client.getUpdates(ctx, offset)
		if err != nil {
			logf("telegram polling failed: %v", err)
			if !wait(ctx, 2*time.Second) {
				return nil
			}
			continue
		}
		for _, update := range updates {
			shard := updateShard(update, workers)
			select {
			case queues[shard] <- update:
			case <-ctx.Done():
				return nil
			}
			if update.ID >= offset {
				offset = update.ID + 1
			}
		}
	}
}

func updateShard(update bot.Update, workers int) int {
	if workers <= 1 {
		return 0
	}
	var chatID int64
	switch {
	case update.Message != nil:
		chatID = update.Message.Chat.ID
	case update.Action != nil:
		chatID = update.Action.Chat.ID
	default:
		return 0
	}
	return int(uint64(chatID) % uint64(workers))
}

func (client *Client) SendMessage(ctx context.Context, chatID int64, text string, locationButton bool) error {
	if locationButton {
		return client.SendMessageWithKeyboard(ctx, chatID, text, bot.DefaultKeyboard())
	}
	return client.SendMessageWithKeyboard(ctx, chatID, text, nil)
}

func (client *Client) SendMessageWithKeyboard(ctx context.Context, chatID int64, text string, keyboard bot.Keyboard) error {
	return client.sendMessageWithKeyboard(ctx, chatID, text, keyboard, "")
}

func (client *Client) SendHTMLMessageWithKeyboard(ctx context.Context, chatID int64, text string, keyboard bot.Keyboard) error {
	return client.sendMessageWithKeyboard(ctx, chatID, text, keyboard, "HTML")
}

func (client *Client) SendMessageWithActions(ctx context.Context, chatID int64, text string, keyboard bot.ActionKeyboard) error {
	markup, err := encodeActionKeyboard(keyboard)
	if err != nil {
		return err
	}
	payload := struct {
		ChatID      int64  `json:"chat_id"`
		Text        string `json:"text"`
		ReplyMarkup any    `json:"reply_markup,omitempty"`
	}{ChatID: chatID, Text: text, ReplyMarkup: markup}
	return client.call(ctx, "sendMessage", payload, nil)
}

func (client *Client) AnswerAction(ctx context.Context, token, text string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("telegram callback token is required")
	}
	payload := struct {
		CallbackQueryID string `json:"callback_query_id"`
		Text            string `json:"text,omitempty"`
	}{CallbackQueryID: token, Text: text}
	return client.call(ctx, "answerCallbackQuery", payload, nil)
}

func (client *Client) sendMessageWithKeyboard(ctx context.Context, chatID int64, text string, keyboard bot.Keyboard, parseMode string) error {
	payload := struct {
		ChatID      int64  `json:"chat_id"`
		Text        string `json:"text"`
		ParseMode   string `json:"parse_mode,omitempty"`
		ReplyMarkup any    `json:"reply_markup,omitempty"`
	}{ChatID: chatID, Text: text, ParseMode: parseMode}
	if len(keyboard) > 0 {
		rows := make([][]map[string]any, 0, len(keyboard))
		for _, row := range keyboard {
			out := make([]map[string]any, 0, len(row))
			for _, b := range row {
				item := map[string]any{"text": b.Text}
				if b.RequestLocation {
					item["request_location"] = true
				}
				out = append(out, item)
			}
			rows = append(rows, out)
		}
		payload.ReplyMarkup = map[string]any{"resize_keyboard": true, "keyboard": rows}
	}
	return client.call(ctx, "sendMessage", payload, nil)
}

func (client *Client) SendPhoto(ctx context.Context, chatID int64, path, caption string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open rendered chart: %w", err)
	}
	defer func() { _ = file.Close() }()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return fmt.Errorf("encode Telegram photo chat: %w", err)
	}
	if caption != "" {
		if err := writer.WriteField("caption", caption); err != nil {
			return fmt.Errorf("encode Telegram photo caption: %w", err)
		}
		if err := writer.WriteField("parse_mode", "HTML"); err != nil {
			return fmt.Errorf("encode Telegram photo parse mode: %w", err)
		}
	}
	part, err := writer.CreateFormFile("photo", filepath.Base(path))
	if err != nil {
		return fmt.Errorf("encode Telegram photo: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("read rendered chart: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish Telegram photo request: %w", err)
	}
	return client.callBody(ctx, "sendPhoto", writer.FormDataContentType(), &body, nil)
}

func (client *Client) SendDocument(ctx context.Context, chatID int64, path, caption string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open rendered chart: %w", err)
	}
	defer func() { _ = file.Close() }()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return fmt.Errorf("encode Telegram document chat: %w", err)
	}
	if caption != "" {
		if err := writer.WriteField("caption", caption); err != nil {
			return fmt.Errorf("encode Telegram document caption: %w", err)
		}
		if err := writer.WriteField("parse_mode", "HTML"); err != nil {
			return fmt.Errorf("encode Telegram document parse mode: %w", err)
		}
	}
	if err := writer.WriteField("disable_content_type_detection", "true"); err != nil {
		return fmt.Errorf("encode Telegram document options: %w", err)
	}
	part, err := writer.CreateFormFile("document", filepath.Base(path))
	if err != nil {
		return fmt.Errorf("encode Telegram document: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("read rendered chart: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish Telegram document request: %w", err)
	}
	return client.callBody(ctx, "sendDocument", writer.FormDataContentType(), &body, nil)
}

func (client *Client) getUpdates(ctx context.Context, offset int64) ([]bot.Update, error) {
	payload := struct {
		Offset         int64    `json:"offset"`
		Timeout        int      `json:"timeout"`
		AllowedUpdates []string `json:"allowed_updates"`
	}{Offset: offset, Timeout: 25, AllowedUpdates: []string{"message", "callback_query"}}
	var incoming []incomingUpdate
	if err := client.call(ctx, "getUpdates", payload, &incoming); err != nil {
		return nil, err
	}
	updates := make([]bot.Update, 0, len(incoming))
	for _, update := range incoming {
		updates = append(updates, convertUpdate(update))
	}
	return updates, nil
}

func convertUpdate(update incomingUpdate) bot.Update {
	converted := bot.Update{ID: update.ID, Message: update.Message}
	callback := update.CallbackQuery
	if callback == nil || callback.Message == nil || callback.Message.Chat.ID == 0 || callback.ID == "" || callback.Data == "" {
		return converted
	}
	converted.Action = &bot.ActionInvocation{
		Token: callback.ID,
		Data:  callback.Data,
		Chat:  callback.Message.Chat,
		From:  callback.From,
	}
	return converted
}

func encodeActionKeyboard(keyboard bot.ActionKeyboard) (any, error) {
	if len(keyboard) == 0 {
		return nil, nil
	}
	rows := make([][]map[string]string, 0, len(keyboard))
	for _, row := range keyboard {
		encodedRow := make([]map[string]string, 0, len(row))
		for _, button := range row {
			text := strings.TrimSpace(button.Text)
			if text == "" {
				return nil, errors.New("telegram action button text is required")
			}
			if len(button.Data) == 0 || len(button.Data) > 64 {
				return nil, errors.New("telegram callback data must contain 1 to 64 bytes")
			}
			encodedRow = append(encodedRow, map[string]string{"text": text, "callback_data": button.Data})
		}
		if len(encodedRow) > 0 {
			rows = append(rows, encodedRow)
		}
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return map[string]any{"inline_keyboard": rows}, nil
}

func (client *Client) call(ctx context.Context, method string, payload, result any) error {
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Telegram %s request: %w", method, err)
	}
	return client.callBody(ctx, method, "application/json", bytes.NewReader(requestBody), result)
}

func (client *Client) callBody(ctx context.Context, method, contentType string, body io.Reader, result any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint+method, body)
	if err != nil {
		return fmt.Errorf("create Telegram %s request", method)
	}
	request.Header.Set("Content-Type", contentType)
	response, err := client.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// net/http errors may include the secret-bearing URL, so do not wrap them.
		return fmt.Errorf("telegram %s transport failed", method)
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return fmt.Errorf("read Telegram %s response", method)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram %s returned HTTP %d", method, response.StatusCode)
	}
	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return fmt.Errorf("decode Telegram %s response", method)
	}
	if !envelope.OK {
		return fmt.Errorf("telegram %s rejected request: %s", method, envelope.Description)
	}
	if result != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, result); err != nil {
			return fmt.Errorf("decode Telegram %s result", method)
		}
	}
	return nil
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
