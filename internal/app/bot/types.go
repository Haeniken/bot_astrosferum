package bot

type Update struct {
	ID      int64
	Message *Message
	Action  *ActionInvocation
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

type Button struct {
	Text            string
	RequestLocation bool
}

type Keyboard [][]Button

type ActionID string

// ActionInvocation is a platform-neutral callback from an inline action
// button. Token is opaque platform state used only to acknowledge the action;
// Data is the versioned application payload.
type ActionInvocation struct {
	Token string
	Data  string
	Chat  Chat
	From  *User
}

type ActionButton struct {
	Text string
	Data string
}

type ActionKeyboard [][]ActionButton

func DefaultKeyboard() Keyboard {
	return Keyboard{{{Text: "📍 Отправить геопозицию", RequestLocation: true}}, {{Text: "💾 Сохранить координаты"}, {Text: "📌 Мои точки"}}}
}
