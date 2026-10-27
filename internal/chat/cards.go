package chat

// Legacy cards and cardsV2 share these text-bearing fields. Interactive widgets
// stay outside the native reader's supported card content.
type Card struct {
	Header struct {
		Title    string `json:"title"`
		Subtitle string `json:"subtitle"`
	} `json:"header"`
	Sections []CardSection `json:"sections"`
}

type CardWithID struct {
	Card Card `json:"card"`
}

type CardSection struct {
	Header  string       `json:"header"`
	Widgets []CardWidget `json:"widgets"`
}

type CardWidget struct {
	TextParagraph *struct {
		Text string `json:"text"`
	} `json:"textParagraph"`
	KeyValue *struct {
		TopLabel    string `json:"topLabel"`
		Content     string `json:"content"`
		BottomLabel string `json:"bottomLabel"`
	} `json:"keyValue"`
	DecoratedText *struct {
		TopLabel    string `json:"topLabel"`
		Text        string `json:"text"`
		BottomLabel string `json:"bottomLabel"`
	} `json:"decoratedText"`
}
