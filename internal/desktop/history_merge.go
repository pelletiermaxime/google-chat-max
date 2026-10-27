package desktop

import (
	"slices"
	"strings"

	"google-chat-max/internal/chat"
)

// Created-message responses can arrive with different amounts of metadata.
// Enrich repeats without letting a partial or older response erase known data.
func mergeMessage(existing, incoming chat.Message) chat.Message {
	if !existing.LastUpdateTime.IsZero() && incoming.LastUpdateTime.Before(existing.LastUpdateTime) {
		existing, incoming = incoming, existing
	}
	if incoming.CreateTime.IsZero() {
		incoming.CreateTime = existing.CreateTime
	}
	if incoming.LastUpdateTime.IsZero() {
		incoming.LastUpdateTime = existing.LastUpdateTime
	}
	if incoming.Text == "" {
		incoming.Text = existing.Text
	}
	if incoming.FallbackText == "" {
		incoming.FallbackText = existing.FallbackText
	}
	incoming.Sender = incoming.Sender.WithFallback(existing.Sender)
	if incoming.Thread.Name == "" {
		incoming.Thread = existing.Thread
	}
	incoming.ThreadReply = incoming.ThreadReply || existing.ThreadReply
	if len(incoming.Cards) == 0 {
		incoming.Cards = existing.Cards
	}
	if len(incoming.CardsV2) == 0 {
		incoming.CardsV2 = existing.CardsV2
	}
	if len(incoming.Attachment) == 0 {
		incoming.Attachment = existing.Attachment
	}
	if incoming.QuotedMessageMetadata == nil {
		incoming.QuotedMessageMetadata = existing.QuotedMessageMetadata
	}
	return incoming
}

func mergeWindow(history, incoming []chat.Message, limit int, oldest bool) []chat.Message {
	byName := make(map[string]int, len(history)+len(incoming))
	merged := make([]chat.Message, 0, len(history)+len(incoming))
	for _, list := range [][]chat.Message{history, incoming} {
		for _, m := range list {
			if i, exists := byName[m.Name]; exists {
				merged[i] = mergeMessage(merged[i], m)
			} else {
				byName[m.Name] = len(merged)
				merged = append(merged, m)
			}
		}
	}
	slices.SortFunc(merged, func(a, b chat.Message) int {
		if order := a.CreateTime.Compare(b.CreateTime); order != 0 {
			return order
		}
		return strings.Compare(a.Name, b.Name)
	})
	if len(merged) > limit {
		if oldest {
			clear(merged[limit:])
			merged = merged[:limit:limit]
		} else {
			start := len(merged) - limit
			clear(merged[:start])
			merged = merged[start:]
		}
	}
	return merged
}

func appendWindow(history []chat.Message, m chat.Message, expanded, newer bool) ([]chat.Message, bool) {
	if !expanded {
		return mergeMessages(history, []chat.Message{m}), newer
	}
	if newer || (len(history) >= retainedHistoryLimit && m.CreateTime.Before(history[0].CreateTime)) {
		return history, newer
	}
	return mergeRetained(history, []chat.Message{m}, true), unionSize(history, []chat.Message{m}) > retainedHistoryLimit
}

func mergeMessages(history, incoming []chat.Message) []chat.Message {
	return mergeWindow(history, incoming, chat.HistoryLimit, false)
}

func mergeRetained(history, incoming []chat.Message, oldest bool) []chat.Message {
	return mergeWindow(history, incoming, retainedHistoryLimit, oldest)
}

func enrichMessage(messages []chat.Message, incoming chat.Message) {
	for i := range messages {
		if messages[i].Name == incoming.Name {
			messages[i] = mergeMessage(messages[i], incoming)
		}
	}
}

func (a *App) enrichDelivery(m chat.Message) {
	enrichMessage(a.messages, m)
	enrichMessage(a.duringRead, m)
	enrichMessage(a.threadMessages, m)
	enrichMessage(a.threadOverlay, m)
	if a.threadRoot != nil && a.threadRoot.Name == m.Name {
		merged := mergeMessage(*a.threadRoot, m)
		a.threadRoot = &merged
	}
	if entry, ok := a.historyCache[chat.MessageSpace(m.Name)]; ok {
		enrichMessage(entry.messages, m)
	}
}
