package desktop

import (
	"fmt"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

func TestNativeOwnMessagesAlignRightAndHideIdentity(t *testing.T) {
	for _, width := range []int{760, 1180, 1920} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := New(nil, func(func()) {}, func() {}, true)
			defer a.Close()
			a.selfUser = "users/self"
			a.selected = "spaces/chat"
			a.spaces = []chat.Space{{Name: a.selected, DisplayName: "Design studio", SpaceType: "SPACE"}}
			when := time.Date(2026, 10, 26, 15, 0, 0, 0, time.Local)
			a.messages = []chat.Message{
				{Name: "spaces/chat/messages/a", Text: "Can you take a look at this?", Sender: chat.User{Name: "users/alex", DisplayName: "Alex Morgan"}, CreateTime: when},
				{Name: "spaces/chat/messages/b", Text: "Yes, I'll check it now.", Sender: chat.User{Name: "users/self", DisplayName: "Your Private Name"}, CreateTime: when.Add(time.Minute)},
				{Name: "spaces/chat/messages/c", Text: strings.Repeat("A longer message should wrap inside its bubble. ", 8), Sender: chat.User{Name: "users/self", DisplayName: "Your Private Name"}, CreateTime: when.Add(2 * time.Minute)},
			}
			tt := ui.NewTester(a.View, width, 1000)
			tt.Frame()
			incoming, inOK := tt.Find(a.messages[0].Text)
			own, ownOK := tt.Find(a.messages[1].Text)
			long, longOK := tt.Find(a.messages[2].Text)
			if !inOK || !ownOK || !longOK {
				t.Fatal("bubble layout lost message text")
			}
			middle := float32(303+width) / 2
			if own.X+own.W/2 <= middle || own.X+own.W <= incoming.X+incoming.W {
				t.Fatalf("own message did not align right: incoming=%+v own=%+v", incoming, own)
			}
			if long.W > 680 || long.X+long.W > float32(width-20) || long.H < 30 {
				t.Fatalf("long bubble overflowed or did not wrap: %+v", long)
			}
			columnWidth := min(float32(1000), float32(width-303-64))
			left := float32(303) + (float32(width-303)-columnWidth)/2
			wantRight := left + columnWidth - 16
			if right := own.X + own.W; right < wantRight-2 || right > wantRight+2 {
				t.Fatalf("own message is not at the right edge of the centered column: right=%v want=%v", right, wantRight)
			}
			if wantLeft := left + 34 + 12 + 16; incoming.X < wantLeft-2 || incoming.X > wantLeft+2 {
				t.Fatalf("incoming message is not at the left edge of the centered column: x=%v want=%v", incoming.X, wantLeft)
			}
			composer, ok := tt.Find("Message composer")
			if !ok || composer.X < left-2 || composer.X > left+2 || composer.W < columnWidth-2 || composer.W > columnWidth+2 {
				t.Fatalf("composer does not match the centered message column: bounds=%+v want x=%v width=%v", composer, left, columnWidth)
			}
			if tt.HasText("To: ") || tt.HasText("Enter: new line") {
				t.Fatal("composer shows a removed destination label or keyboard hint")
			}
			if !tt.HasText("Alex Morgan") || !tt.HasText(when.Add(time.Minute).Format(time.Kitchen)) {
				t.Fatal("incoming identity or outgoing timestamp disappeared")
			}
			if tt.HasText("Your Private Name") {
				t.Fatal("own sender name was rendered")
			}
			if _, ok := tt.Find("Y"); ok {
				t.Fatal("own avatar was rendered")
			}
			if path := os.Getenv("CHAT_MAX_BUBBLE_SCREENSHOT"); path != "" && (width == 1180 || width == 1920) {
				if width == 1920 {
					path = strings.TrimSuffix(path, ".png") + "-wide.png"
				}
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := png.Encode(f, tt.Image()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestOwnMessageDetectionUsesResourceIDRatherThanDisplayName(t *testing.T) {
	a := New(nil, func(func()) {}, func() {}, true)
	defer a.Close()
	a.selfUser = "users/self"
	a.selected = "spaces/chat"
	a.messages = []chat.Message{{Name: "spaces/chat/messages/m", Text: "Someone with the same display name", Sender: chat.User{Name: "users/other", DisplayName: "Your Name"}}}
	tt := ui.NewTester(a.View, 1180, 780)
	tt.Frame()
	if !tt.HasText("Your Name") {
		t.Fatal("another sender's identity was hidden")
	}
	if _, ok := tt.Find("Y"); !ok {
		t.Fatal("another sender's avatar was hidden")
	}
	// Missing identities must not classify anonymous messages as the user.
	a.selfUser = ""
	a.messages[0].Sender = chat.User{DisplayName: "Unknown identity"}
	tt.Frame()
	if !tt.HasText("Unknown identity") {
		t.Fatal("missing resource IDs were treated as an own message")
	}
}
