package desktop

import (
	"context"
	"time"

	"google-chat-max/internal/chat"
)

// Demo is an offline fixture for trying the native interface without credentials.
func Demo() Backend { return demoBackend{} }

type demoBackend struct{}

func (demoBackend) Self() string { return "users/demo" }

func (demoBackend) Send(_ context.Context, space, thread, text, id string) (chat.Message, error) {
	m := chat.Message{Name: space + "/messages/" + id, Text: text, CreateTime: time.Now().UTC(), Sender: chat.User{Name: "users/demo", DisplayName: "You"}, ThreadReply: thread != ""}
	if thread == "" {
		thread = space + "/threads/" + id
	}
	m.Thread.Name = thread
	return m, nil
}

func (b demoBackend) Thread(ctx context.Context, space, thread string) (chat.ThreadHistory, error) {
	messages, _ := b.Messages(ctx, space)
	var result chat.ThreadHistory
	for _, m := range messages {
		if m.Thread.Name != thread {
			continue
		}
		result.Page.Messages = append(result.Page.Messages, m)
		if !m.ThreadReply {
			root := m
			result.Root = &root
		}
	}
	return result, nil
}

func (b demoBackend) SendQuoted(ctx context.Context, space, thread, text, id string, quote chat.QuotedMessageMetadata) (chat.Message, error) {
	m, err := b.Send(ctx, space, thread, text, id)
	m.QuotedMessageMetadata = &quote
	return m, err
}

func (demoBackend) Spaces(context.Context) ([]chat.Space, error) {
	return []chat.Space{
		{Name: "spaces/design", DisplayName: "Design studio", SpaceType: "SPACE"},
		{Name: "spaces/engineering", DisplayName: "Engineering", SpaceType: "SPACE"},
		{Name: "spaces/general", DisplayName: "The common room", SpaceType: "SPACE"},
		{Name: "spaces/alex", DisplayName: "Alex Morgan", SpaceType: "DIRECT_MESSAGE"},
		{Name: "spaces/sam", DisplayName: "Sam Rivera", SpaceType: "DIRECT_MESSAGE"},
	}, nil
}

func (demoBackend) Messages(_ context.Context, space string) ([]chat.Message, error) {
	if space == "spaces/general" {
		return nil, nil
	}
	texts := []struct{ author, text string }{
		{"Alex Morgan", "A quieter place to catch up.\n\nThe conversation list is on the left, and the recent history is right here."},
		{"Sam Rivera", "The extra space makes it easy to catch up. Can we keep replies in their own threads?"},
		{"Alex Morgan", "Absolutely. Hover over a message to reply in a thread, quote it, or add a reaction. Ctrl+Enter sends your draft."},
		{"Jamie Chen", "The thread view keeps the original message in sight while we talk through the details."},
		{"Sam Rivera", "Sounds good. This demo uses sample conversations, so feel free to explore."},
	}
	if space == "spaces/engineering" {
		texts = []struct{ author, text string }{{"Jamie Chen", "The experimental native client is ready to explore."}, {"Alex Morgan", "Live messages arrive through a streaming connection. Conversation history loads when you open it."}}
	}
	if space == "spaces/alex" || space == "spaces/sam" {
		texts = texts[:2]
	}
	var messages []chat.Message
	for i, item := range texts {
		m := chat.Message{Name: space + "/messages/" + string(rune('a'+i)), Text: item.text, CreateTime: time.Date(2026, 10, 26, 14, 5+i*4, 0, 0, time.Local)}
		m.Sender.DisplayName = item.author
		if i == 2 {
			m.Sender = chat.User{Name: "users/demo", DisplayName: "You"}
		}
		m.Thread.Name = space + "/threads/" + string(rune('a'+i))
		m.ThreadReply = i == 3
		if m.ThreadReply {
			m.Thread.Name = space + "/threads/a"
		}
		messages = append(messages, m)
	}
	return messages, nil
}
