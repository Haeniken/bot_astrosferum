package vk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"bot_astrosferum/internal/app/bot"
)

const vkUserNamespace int64 = 1 << 62

type longPollServer struct {
	Key    string `json:"key"`
	Server string `json:"server"`
	TS     string `json:"ts"`
}

type longPollResponse struct {
	TS      string          `json:"ts"`
	Failed  int             `json:"failed"`
	Updates []longPollEvent `json:"updates"`
}

type longPollEvent struct {
	Type    string `json:"type"`
	GroupID int64  `json:"group_id"`
	Object  struct {
		Message               incomingMessage `json:"message"`
		UserID                int64           `json:"user_id"`
		PeerID                int64           `json:"peer_id"`
		EventID               string          `json:"event_id"`
		Payload               json.RawMessage `json:"payload"`
		ConversationMessageID int64           `json:"conversation_message_id"`
	} `json:"object"`
}

type incomingMessage struct {
	ID     int64  `json:"id"`
	PeerID int64  `json:"peer_id"`
	FromID int64  `json:"from_id"`
	Text   string `json:"text"`
	Geo    *struct {
		Coordinates struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"coordinates"`
	} `json:"geo"`
}

func (client *Client) EnableLongPoll(ctx context.Context) error {
	values := url.Values{
		"group_id":      {strconv.FormatInt(client.groupID, 10)},
		"enabled":       {"1"},
		"api_version":   {apiVersion},
		"message_new":   {"1"},
		"message_event": {"1"},
	}
	return client.call(ctx, "groups.setLongPollSettings", values, nil)
}

func (client *Client) getLongPollServer(ctx context.Context) (longPollServer, error) {
	var server longPollServer
	err := client.call(ctx, "groups.getLongPollServer", url.Values{"group_id": {strconv.FormatInt(client.groupID, 10)}}, &server)
	if err != nil {
		return longPollServer{}, err
	}
	parsed, err := url.Parse(server.Server)
	if err != nil || parsed.Scheme != "https" || !isVKHost(parsed.Hostname()) || server.Key == "" || server.TS == "" {
		return longPollServer{}, errors.New("VK returned an invalid Long Poll server")
	}
	return server, nil
}

func (client *Client) Run(ctx context.Context, handler *bot.Handler, workers int, requestTimeout time.Duration, logf func(string, ...any)) error {
	if handler == nil {
		return errors.New("VK handler is required")
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
	if err := client.EnableLongPoll(ctx); err != nil {
		return fmt.Errorf("enable VK Group Long Poll: %w", err)
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
					logf("VK update %d failed duration=%s: %v", update.ID, time.Since(started).Round(time.Millisecond), err)
					continue
				}
				logf("VK update %d processed duration=%s", update.ID, time.Since(started).Round(time.Millisecond))
			}
		}(queues[index])
	}
	defer func() {
		for _, queue := range queues {
			close(queue)
		}
		workerGroup.Wait()
	}()

	server, err := client.getLongPollServer(ctx)
	if err != nil {
		return err
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		response, err := client.poll(ctx, server)
		if err != nil {
			return err
		}
		switch response.Failed {
		case 0:
			if response.TS == "" {
				return errors.New("VK Long Poll returned no ts")
			}
			server.TS = response.TS
		case 1:
			if response.TS == "" {
				return errors.New("VK Long Poll failed=1 returned no ts")
			}
			server.TS = response.TS
			continue
		case 2, 3:
			server, err = client.getLongPollServer(ctx)
			if err != nil {
				return err
			}
			continue
		default:
			return fmt.Errorf("VK Long Poll returned failed=%d", response.Failed)
		}
		for _, event := range response.Updates {
			update, ok := client.convertEvent(event)
			if !ok {
				continue
			}
			shard := vkUpdateShard(update, workers)
			select {
			case queues[shard] <- update:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func (client *Client) poll(ctx context.Context, server longPollServer) (longPollResponse, error) {
	endpoint, err := url.Parse(server.Server)
	if err != nil {
		return longPollResponse{}, err
	}
	query := endpoint.Query()
	query.Set("act", "a_check")
	query.Set("key", server.Key)
	query.Set("ts", server.TS)
	query.Set("wait", "25")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return longPollResponse{}, fmt.Errorf("create VK Long Poll request: %w", err)
	}
	response, err := client.longPollHTTP.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return longPollResponse{}, ctx.Err()
		}
		// net/http transport errors include the complete request URL. The VK
		// Long Poll URL carries the secret key in its query, so never wrap it.
		return longPollResponse{}, errors.New("poll VK events transport failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return longPollResponse{}, fmt.Errorf("VK Long Poll returned HTTP %s", response.Status)
	}
	var result longPollResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&result); err != nil {
		return longPollResponse{}, fmt.Errorf("decode VK Long Poll response: %w", err)
	}
	return result, nil
}

func (client *Client) convertEvent(event longPollEvent) (bot.Update, bool) {
	if event.GroupID != client.groupID {
		return bot.Update{}, false
	}
	if event.Type == "message_event" {
		return client.convertMessageEvent(event)
	}
	message := event.Object.Message
	if event.Type != "message_new" || message.PeerID == 0 || message.FromID <= 0 {
		return bot.Update{}, false
	}
	userID, err := UserKey(message.FromID)
	if err != nil {
		return bot.Update{}, false
	}
	text := strings.TrimSpace(message.Text)
	if strings.EqualFold(text, "начать") || strings.EqualFold(text, "start") {
		text = "/start"
	}
	converted := &bot.Message{
		ID:   message.ID,
		Chat: bot.Chat{ID: message.PeerID},
		Text: text,
		From: &bot.User{ID: userID, LanguageCode: "ru"},
	}
	if message.Geo != nil {
		converted.Location = &bot.Location{
			Latitude:  message.Geo.Coordinates.Latitude,
			Longitude: message.Geo.Coordinates.Longitude,
		}
	}
	return bot.Update{ID: message.ID, Message: converted}, true
}

func (client *Client) convertMessageEvent(event longPollEvent) (bot.Update, bool) {
	object := event.Object
	if object.PeerID == 0 || object.UserID <= 0 || strings.TrimSpace(object.EventID) == "" {
		return bot.Update{}, false
	}
	data, ok := decodeMessageEventPayload(object.Payload)
	if !ok {
		return bot.Update{}, false
	}
	userID, err := UserKey(object.UserID)
	if err != nil {
		return bot.Update{}, false
	}
	token, err := encodeActionToken(object.EventID, object.UserID, object.PeerID)
	if err != nil {
		return bot.Update{}, false
	}
	return bot.Update{
		ID: object.ConversationMessageID,
		Action: &bot.ActionInvocation{
			Token: token,
			Data:  data,
			Chat:  bot.Chat{ID: object.PeerID},
			From:  &bot.User{ID: userID, LanguageCode: "ru"},
		},
	}, true
}

func decodeMessageEventPayload(encoded json.RawMessage) (string, bool) {
	if len(encoded) == 0 || len(encoded) > 1024 {
		return "", false
	}
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(encoded, &payload); err == nil && payload.Action != "" {
		return payload.Action, true
	}
	var nested string
	if err := json.Unmarshal(encoded, &nested); err != nil || nested == "" {
		return "", false
	}
	if err := json.Unmarshal([]byte(nested), &payload); err != nil || payload.Action == "" {
		return "", false
	}
	return payload.Action, true
}

func vkUpdateShard(update bot.Update, workers int) int {
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

func namespaceVKUserID(externalID int64) (int64, bool) {
	if externalID <= 0 || externalID >= vkUserNamespace {
		return 0, false
	}
	return vkUserNamespace | externalID, true
}

// UserKey maps a public VK user ID into the shared PostgreSQL numeric keyspace
// without colliding with Telegram IDs.
func UserKey(externalID int64) (int64, error) {
	value, ok := namespaceVKUserID(externalID)
	if !ok {
		return 0, errors.New("VK user ID must be positive")
	}
	return value, nil
}
