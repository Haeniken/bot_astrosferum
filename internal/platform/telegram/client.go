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
)

type Update struct {
	ID      int64    `json:"update_id"`
	Message *Message `json:"message"`
}

type Message struct {
	ID       int64     `json:"message_id"`
	Chat     Chat      `json:"chat"`
	Text     string    `json:"text"`
	Location *Location `json:"location"`
	From     *User     `json:"from"`
}

type User struct {
	ID           int64  `json:"id"`
	LanguageCode string `json:"language_code"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type Client struct {
	httpClient *http.Client
	endpoint   string
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

func (client *Client) Run(ctx context.Context, handler *Handler, workers int, requestTimeout time.Duration, logf func(string, ...any)) error {
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
	queues := make([]chan Update, workers)
	var workerGroup sync.WaitGroup
	for index := range queues {
		queues[index] = make(chan Update, 8)
		workerGroup.Add(1)
		go func(queue <-chan Update) {
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

func updateShard(update Update, workers int) int {
	if workers <= 1 || update.Message == nil {
		return 0
	}
	return int(uint64(update.Message.Chat.ID) % uint64(workers))
}

func (client *Client) SendMessage(ctx context.Context, chatID int64, text string, locationButton bool) error {
	if locationButton {
		return client.SendMessageWithKeyboard(ctx, chatID, text, DefaultKeyboard())
	}
	return client.SendMessageWithKeyboard(ctx, chatID, text, nil)
}

type Button struct {
	Text            string
	RequestLocation bool
}
type Keyboard [][]Button

func DefaultKeyboard() Keyboard {
	return Keyboard{{{Text: "📍 Отправить геопозицию", RequestLocation: true}}, {{Text: "💾 Сохранить координаты"}, {Text: "📌 Мои точки"}}}
}

func (client *Client) SendMessageWithKeyboard(ctx context.Context, chatID int64, text string, keyboard Keyboard) error {
	return client.sendMessageWithKeyboard(ctx, chatID, text, keyboard, "")
}

func (client *Client) SendHTMLMessageWithKeyboard(ctx context.Context, chatID int64, text string, keyboard Keyboard) error {
	return client.sendMessageWithKeyboard(ctx, chatID, text, keyboard, "HTML")
}

func (client *Client) sendMessageWithKeyboard(ctx context.Context, chatID int64, text string, keyboard Keyboard, parseMode string) error {
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

func (client *Client) getUpdates(ctx context.Context, offset int64) ([]Update, error) {
	payload := struct {
		Offset         int64    `json:"offset"`
		Timeout        int      `json:"timeout"`
		AllowedUpdates []string `json:"allowed_updates"`
	}{Offset: offset, Timeout: 25, AllowedUpdates: []string{"message"}}
	var updates []Update
	if err := client.call(ctx, "getUpdates", payload, &updates); err != nil {
		return nil, err
	}
	return updates, nil
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
