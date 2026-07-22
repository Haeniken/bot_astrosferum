package bot

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

type Button struct {
	Text            string
	RequestLocation bool
}

type Keyboard [][]Button

func DefaultKeyboard() Keyboard {
	return Keyboard{{{Text: "📍 Отправить геопозицию", RequestLocation: true}}, {{Text: "💾 Сохранить координаты"}, {Text: "📌 Мои точки"}}}
}
