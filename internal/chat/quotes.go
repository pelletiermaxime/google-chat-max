package chat

import "time"

type QuotedMessageMetadata struct {
	Name                  string                 `json:"name"`
	LastUpdateTime        time.Time              `json:"lastUpdateTime"`
	QuotedMessageSnapshot *QuotedMessageSnapshot `json:"quotedMessageSnapshot,omitempty"`
}

type QuotedMessageSnapshot struct {
	Sender string `json:"sender"`
	Text   string `json:"text"`
}
