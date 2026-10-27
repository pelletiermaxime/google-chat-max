package chat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHistoryKeepsAppSenderAndCardContent(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/spaces/a/messages":
			fields := r.URL.Query().Get("fields")
			for _, field := range []string{"cards", "cardsV2", "fallbackText", "sender"} {
				if !strings.Contains(fields, field) {
					t.Errorf("history excludes %s", field)
				}
			}
			fmt.Fprint(w, `{"messages":[{"name":"spaces/a/messages/legacy","sender":{"name":"users/app","displayName":"Zapier","avatarUrl":"https://lh3.googleusercontent.com/zapier","type":"BOT"},"text":"Zap sent by @Alex","cards":[{"header":{"title":"Growth"},"sections":[{"widgets":[{"textParagraph":{"text":"Task moved to review"}}]}]}]},{"name":"spaces/a/messages/v2","cardsV2":[{"cardId":"update","card":{"header":{"title":"Card v2"},"sections":[{"widgets":[{"decoratedText":{"topLabel":"Status","text":"Ready"}}]}]}}],"fallbackText":"Update summary"}]}`)
		case "/spaces/a/members":
			// This is what the live membership endpoint returns for Zapier.
			fmt.Fprint(w, `{"memberships":[{"member":{"name":"users/app","type":"BOT"}}]}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer s.Close()
	c := &Client{http: s.Client(), baseURL: s.URL, identities: true}
	page, err := c.History(context.Background(), "spaces/a", "", "")
	if err != nil || len(page.Messages) != 2 {
		t.Fatalf("history failed: %v", err)
	}
	legacy, modern := page.Messages[1], page.Messages[0]
	if legacy.Author() != "Zapier" || legacy.Sender.AvatarURL == "" || legacy.Sender.Type != "BOT" {
		t.Fatal("incomplete membership erased rich app sender metadata")
	}
	if len(legacy.Cards) != 1 || legacy.Cards[0].Sections[0].Widgets[0].TextParagraph.Text != "Task moved to review" {
		t.Fatal("legacy card content was not retained")
	}
	if modern.FallbackText != "Update summary" || len(modern.CardsV2) != 1 || modern.CardsV2[0].Card.Sections[0].Widgets[0].DecoratedText.Text != "Ready" {
		t.Fatal("cardsV2 or fallback content was not retained")
	}
}

func TestStreamEventKeepsCardsAndAppIdentity(t *testing.T) {
	event, _, err := decodeStreamEvent(map[string]string{"ce-type": messageCreated}, []byte(`{"message":{"name":"spaces/a/messages/b","createTime":"2026-10-26T15:00:00Z","sender":{"name":"users/app","displayName":"Zapier","type":"BOT"},"cards":[{"header":{"title":"Growth"}}],"cardsV2":[{"card":{"header":{"title":"Update"}}}],"fallbackText":"Summary"}}`))
	if err != nil || event.Message == nil {
		t.Fatalf("decode failed: %v", err)
	}
	m := event.Message
	if m.Author() != "Zapier" || len(m.Cards) != 1 || len(m.CardsV2) != 1 || m.FallbackText != "Summary" {
		t.Fatal("live delivery discarded card or sender fields")
	}
}
