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
		Message incomingMessage `json:"message"`
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
		"group_id":    {strconv.FormatInt(client.groupID, 10)},
		"enabled":     {"1"},
		"api_version": {apiVersion},
		"message_new": {"1"},
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
			shard := int(uint64(update.Message.Chat.ID) % uint64(workers))
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
		return longPollResponse{}, fmt.Errorf("poll VK events: %w", err)
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
	message := event.Object.Message
	if event.Type != "message_new" || event.GroupID != client.groupID || message.PeerID == 0 || message.FromID <= 0 {
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
