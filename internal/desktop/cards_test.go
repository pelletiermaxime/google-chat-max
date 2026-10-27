package desktop

import (
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

func TestNativeAppCardIncludesTheUpdateAndKeepsSender(t *testing.T) {
	card := `{"header":{"title":"🔔🔔🔔🔔🔔"},"sections":[{"widgets":[{"textParagraph":{"text":"Alex has just moved a task to <b>Code Review</b>."}}]}]}`
	for _, modern := range []bool{false, true} {
		t.Run(fmt.Sprintf("cardsV2=%t", modern), func(t *testing.T) {
			a := New(nil, func(func()) {}, func() {}, true)
			defer a.Close()
			a.selected = "spaces/growth"
			a.spaces = []chat.Space{{Name: a.selected, DisplayName: "Growth updates", SpaceType: "SPACE"}}
			m := chat.Message{Name: a.selected + "/messages/update", Text: "Zap sent by @Alex", CreateTime: time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC), Sender: chat.User{Name: "users/app", DisplayName: "Zapier", Type: "BOT"}}
			payload := `{"cards":[` + card + `]}`
			if modern {
				payload = `{"cardsV2":[{"card":` + card + `}],"cards":[{"header":{"title":"Duplicate legacy card"}}]}`
			}
			if err := json.Unmarshal([]byte(payload), &m); err != nil {
				t.Fatal(err)
			}
			a.users[m.Sender.Name] = chat.User{Name: m.Sender.Name, DisplayName: "Stale membership identity"}
			a.messages = []chat.Message{m}
			tt := ui.NewTester(a.View, 1180, 780)
			tt.Frame()
			for _, text := range []string{"Zapier", "Zap sent by @Alex", "🔔🔔🔔🔔🔔", "Alex has just moved a task to Code Review."} {
				if !tt.HasText(text) {
					t.Errorf("native card is missing %q", text)
				}
			}
			for _, text := range []string{"Duplicate legacy card", "[Non-text message]", "Stale membership identity", "User app"} {
				if tt.HasText(text) {
					t.Errorf("native card unexpectedly shows %q", text)
				}
			}
			if path := os.Getenv("CHAT_MAX_CARD_SCREENSHOT"); path != "" && !modern {
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := png.Encode(f, tt.Image()); err != nil {
					f.Close()
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestCardOnlyMessageAndFallbackText(t *testing.T) {
	a := New(nil, func(func()) {}, func() {}, true)
	defer a.Close()
	a.selected = "spaces/a"
	var m chat.Message
	if err := json.Unmarshal([]byte(`{"name":"spaces/a/messages/card","fallbackText":"Do not duplicate card contents","cardsV2":[{"card":{"header":{"title":"Deployment"},"sections":[{"widgets":[{"decoratedText":{"topLabel":"Status","text":"Ready &amp; waiting","bottomLabel":"Today"}},{"keyValue":{"topLabel":"Owner","content":"Alex"}}]}]}}]}`), &m); err != nil {
		t.Fatal(err)
	}
	a.messages = []chat.Message{m}
	tt := ui.NewTester(a.View, 1180, 780)
	tt.Frame()
	for _, text := range []string{"Deployment", "Status", "Ready & waiting", "Today", "Owner", "Alex"} {
		if !tt.HasText(text) {
			t.Errorf("card-only message lost %q", text)
		}
	}
	if tt.HasText("[Non-text message]") || tt.HasText(m.FallbackText) {
		t.Fatal("card-only message shows redundant fallback text")
	}
	a.messages = []chat.Message{{Name: "spaces/a/messages/fallback", FallbackText: "App update summary"}}
	tt.Frame()
	if !tt.HasText("App update summary") {
		t.Fatal("plain fallback text was lost")
	}
}

func TestCardTextFormattingAndDisplayBounds(t *testing.T) {
	if got := cardPlainText(`<b>Growth &amp; progress</b><br>Review <a href="https://example.com">the task</a><p>Ready</p>`); got != "Growth & progress\nReview the task\nReady" {
		t.Fatalf("card formatting leaked or lost text: %q", got)
	}
	var card chat.Card
	card.Header.Title = strings.Repeat("🔔", 10000)
	lines := cardLines(card)
	if len(lines) != 2 || utf8.RuneCountInString(lines[0].text) != 4000 || !utf8.ValidString(lines[0].text) || lines[1].text != "[Card shortened for display]" {
		t.Fatal("large card did not respect its display budget")
	}
	card = chat.Card{Sections: []chat.CardSection{{Widgets: make([]chat.CardWidget, 1000)}}}
	if lines := cardLines(card); len(lines) > 21 || lines[len(lines)-1].text != "[Card shortened for display]" {
		t.Fatal("card widget count is unbounded")
	}
}
