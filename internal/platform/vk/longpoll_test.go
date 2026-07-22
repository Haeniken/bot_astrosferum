package vk

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVKTransportHostAllowlist(t *testing.T) {
	for _, host := range []string{"vk.ru", "api.vk.ru", "lp.vk.ru", "vk.com", "pu.vk.com", "API.VK.RU."} {
		if !isVKHost(host) {
			t.Errorf("expected %q to be allowed", host)
		}
	}
	for _, host := range []string{"", "evilvk.ru", "vk.ru.example.org", "userapi.com", "127.0.0.1"} {
		if isVKHost(host) {
			t.Errorf("expected %q to be rejected", host)
		}
	}
}

func TestGetLongPollServerRejectsNonVKDomain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		writeAPIResponse(response, `{"key":"key","server":"https://example.org/poll","ts":"1"}`)
	}))
	defer server.Close()
	client := testClient(server.URL)
	if _, err := client.getLongPollServer(context.Background()); err == nil {
		t.Fatal("non-VK Long Poll endpoint was accepted")
	}
}

func TestConvertMessageNewWithGeo(t *testing.T) {
	client := testClient("https://example.invalid")
	var event longPollEvent
	event.Type = "message_new"
	event.GroupID = client.groupID
	event.Object.Message.ID = 17
	event.Object.Message.PeerID = 123
	event.Object.Message.FromID = 456
	event.Object.Message.Text = "Начать"
	event.Object.Message.Geo = &struct {
		Coordinates struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"coordinates"`
	}{}
	event.Object.Message.Geo.Coordinates.Latitude = 59.9386
	event.Object.Message.Geo.Coordinates.Longitude = 30.3141
	update, ok := client.convertEvent(event)
	if !ok || update.Message == nil {
		t.Fatal("message_new was not converted")
	}
	if update.Message.Text != "/start" || update.Message.Chat.ID != 123 || update.Message.From.ID != vkUserNamespace|456 {
		t.Fatalf("unexpected update: %+v", update)
	}
	if update.Message.Location == nil || update.Message.Location.Latitude != 59.9386 || update.Message.Location.Longitude != 30.3141 {
		t.Fatalf("unexpected location: %+v", update.Message.Location)
	}
}

func TestConvertEventRejectsOtherGroupsAndOutgoingAuthors(t *testing.T) {
	client := testClient("https://example.invalid")
	var event longPollEvent
	event.Type = "message_new"
	event.GroupID = client.groupID + 1
	event.Object.Message.PeerID = 1
	event.Object.Message.FromID = 2
	if _, ok := client.convertEvent(event); ok {
		t.Fatal("event for another group was accepted")
	}
	event.GroupID = client.groupID
	event.Object.Message.FromID = -client.groupID
	if _, ok := client.convertEvent(event); ok {
		t.Fatal("outgoing community message was accepted")
	}
}

func TestPollUsesBotsLongPollParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if query.Get("act") != "a_check" || query.Get("key") != "secret-key" || query.Get("ts") != "17" || query.Get("wait") != "25" {
			t.Fatalf("unexpected Long Poll query: %v", query)
		}
		_, _ = io.WriteString(response, `{"ts":"18","updates":[]}`)
	}))
	defer server.Close()
	client := testClient("https://example.invalid")
	response, err := client.poll(context.Background(), longPollServer{Server: server.URL, Key: "secret-key", TS: "17"})
	if err != nil {
		t.Fatal(err)
	}
	if response.TS != "18" || response.Failed != 0 {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestNamespaceVKUserIDIsStableAndDisjoint(t *testing.T) {
	first, ok := namespaceVKUserID(42)
	if !ok || first != vkUserNamespace|42 || first <= 0 {
		t.Fatalf("unexpected namespaced ID: %d %v", first, ok)
	}
	if _, ok := namespaceVKUserID(0); ok {
		t.Fatal("zero VK user ID was accepted")
	}
}
