package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	actionEnvelopeVersion = "v1"
	maxActionDataBytes    = 64
)

var (
	ErrInvalidActionData  = errors.New("invalid action data")
	ErrUnsupportedAction  = errors.New("unsupported action envelope version")
	ErrUnregisteredAction = errors.New("unregistered action")
)

// ActionMessenger is an optional platform capability. Regular reply
// keyboards continue to use KeyboardMessenger.
type ActionMessenger interface {
	SendMessageWithActions(context.Context, int64, string, ActionKeyboard) error
	AnswerAction(context.Context, string, string) error
}

// ActionHandler receives the decoded payload of an action. Transport-specific
// callback tokens stay in invocation and must only be passed to AnswerAction.
type ActionHandler func(context.Context, ActionInvocation, string) error

// ActionRouter is deliberately a map rather than a framework: each currently
// supported action has one explicit application handler.
type ActionRouter map[ActionID]ActionHandler

// EncodeAction creates a payload which is accepted by both Telegram and VK.
// The shared 64-byte ceiling is Telegram's callback_data limit.
func EncodeAction(id ActionID, payload string) (string, error) {
	if !validActionID(id) || !utf8.ValidString(payload) {
		return "", ErrInvalidActionData
	}
	encoded := actionEnvelopeVersion + ":" + string(id) + ":" + payload
	if len(encoded) > maxActionDataBytes {
		return "", fmt.Errorf("%w: payload exceeds %d bytes", ErrInvalidActionData, maxActionDataBytes)
	}
	return encoded, nil
}

// DecodeAction validates the envelope and returns its application action ID
// and opaque payload. Only versions understood by this binary are accepted.
func DecodeAction(data string) (ActionID, string, error) {
	if data == "" || len(data) > maxActionDataBytes || !utf8.ValidString(data) {
		return "", "", ErrInvalidActionData
	}
	parts := strings.SplitN(data, ":", 3)
	if len(parts) != 3 {
		return "", "", ErrInvalidActionData
	}
	if parts[0] != actionEnvelopeVersion {
		return "", "", fmt.Errorf("%w: %s", ErrUnsupportedAction, parts[0])
	}
	id := ActionID(parts[1])
	if !validActionID(id) {
		return "", "", ErrInvalidActionData
	}
	return id, parts[2], nil
}

// Route decodes an invocation and invokes the explicitly registered handler.
func (router ActionRouter) Route(ctx context.Context, invocation ActionInvocation) error {
	id, payload, err := DecodeAction(invocation.Data)
	if err != nil {
		return err
	}
	handler, ok := router[id]
	if !ok || handler == nil {
		return fmt.Errorf("%w: %s", ErrUnregisteredAction, id)
	}
	return handler(ctx, invocation, payload)
}

func validActionID(id ActionID) bool {
	value := string(id)
	if value == "" || len(value) > 32 {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' || character == '_' || character == '.' && index > 0 {
			continue
		}
		return false
	}
	return true
}
