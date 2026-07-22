package bot

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestActionEnvelopeRoundTrip(t *testing.T) {
	encoded, err := EncodeAction("horizon.v1", "5993860:3031410")
	if err != nil {
		t.Fatal(err)
	}
	id, payload, err := DecodeAction(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if id != "horizon.v1" || payload != "5993860:3031410" {
		t.Fatalf("decoded action = %q %q", id, payload)
	}
}

func TestActionEnvelopeRejectsInvalidAndOversizedData(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
	}{
		{name: "empty", data: ""},
		{name: "missing payload separator", data: "v1:horizon"},
		{name: "unsupported version", data: "v2:horizon:"},
		{name: "invalid id", data: "v1:Horizon:"},
		{name: "oversized", data: strings.Repeat("x", 65)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := DecodeAction(test.data); err == nil {
				t.Fatalf("DecodeAction(%q) unexpectedly succeeded", test.data)
			}
		})
	}
	if _, err := EncodeAction("horizon", strings.Repeat("x", 64)); err == nil {
		t.Fatal("oversized encoded action unexpectedly succeeded")
	}
}

func TestActionRouterDispatchesDecodedPayload(t *testing.T) {
	data, err := EncodeAction("horizon.v1", "coordinates")
	if err != nil {
		t.Fatal(err)
	}
	var received string
	router := ActionRouter{
		"horizon.v1": func(_ context.Context, invocation ActionInvocation, payload string) error {
			if invocation.Token != "callback-token" || invocation.Chat.ID != 42 {
				t.Fatalf("unexpected invocation: %+v", invocation)
			}
			received = payload
			return nil
		},
	}
	invocation := ActionInvocation{Token: "callback-token", Data: data, Chat: Chat{ID: 42}}
	if err := router.Route(context.Background(), invocation); err != nil {
		t.Fatal(err)
	}
	if received != "coordinates" {
		t.Fatalf("received payload = %q", received)
	}

	unknown, err := EncodeAction("other", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := router.Route(context.Background(), ActionInvocation{Data: unknown}); !errors.Is(err, ErrUnregisteredAction) {
		t.Fatalf("unknown route error = %v", err)
	}
}
