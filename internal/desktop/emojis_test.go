package desktop

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

type reactionResult struct {
	state chat.Reactions
	err   error
}
type reactionCall struct {
	message string
	emoji   chat.Emoji
	want    bool
	result  chan reactionResult
}
type fakeReactions struct {
	demoBackend
	mu    sync.Mutex
	state chat.Reactions
	calls chan reactionCall
}

func testCustomEmojis() []chat.CustomEmoji {
	return []chat.CustomEmoji{
		{Name: "customEmojis/yep", UID: "yep", EmojiName: ":yep:"},
		{Name: "customEmojis/hey", UID: "hey", EmojiName: ":peepohey:"},
		{Name: "customEmojis/parrot", UID: "parrot", EmojiName: ":partyparrot:"},
	}
}
func (b *fakeReactions) CustomEmojis(context.Context) ([]chat.CustomEmoji, error) {
	return testCustomEmojis(), nil
}
func (b *fakeReactions) Reactions(context.Context, string) (chat.Reactions, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state, nil
}
func (b *fakeReactions) SetReaction(ctx context.Context, message string, emoji chat.Emoji, want bool) (chat.Reactions, error) {
	call := reactionCall{message: message, emoji: emoji, want: want, result: make(chan reactionResult, 1)}
	select {
	case b.calls <- call:
	case <-ctx.Done():
		return chat.Reactions{}, ctx.Err()
	}
	select {
	case result := <-call.result:
		if result.err == nil {
			b.mu.Lock()
			b.state = result.state
			b.mu.Unlock()
		}
		return result.state, result.err
	case <-ctx.Done():
		return chat.Reactions{}, ctx.Err()
	}
}
func waitEmojiCallbacks(t *testing.T, callbacks <-chan func(), ready func() bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for !ready() {
		select {
		case fn := <-callbacks:
			fn()
		case <-deadline:
			t.Fatal("emoji UI callbacks did not settle")
			return
		}
	}
}
func nextReactionCall(t *testing.T, b *fakeReactions) reactionCall {
	t.Helper()
	select {
	case call := <-b.calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("reaction action did not reach the worker")
		return reactionCall{}
	}
}
func reactionState(emoji chat.Emoji) chat.Reactions {
	return chat.Reactions{Summaries: []chat.ReactionSummary{{Emoji: emoji, Count: 1}}, Mine: []chat.Reaction{{Name: "spaces/design/messages/a/reactions/own", Emoji: emoji, User: chat.User{Name: "users/demo"}}}}
}

func TestNativeReactionHoverShowsParticipantsWithoutMutation(t *testing.T) {
	a, _, m := actionFixture(t)
	defer a.Close()
	a.selfUser = "users/demo"
	emoji := chat.Emoji{Unicode: "👍"}
	state := reactionState(emoji)
	state.Summaries[0].Count = 3
	state.All = append([]chat.Reaction{
		{Emoji: emoji, User: chat.User{Name: "users/alex"}},
		{Emoji: emoji, User: chat.User{Name: "users/sam", DisplayName: "Sam Rivera"}},
		{Emoji: chat.Emoji{Unicode: "❤️"}, User: chat.User{Name: "users/jamie", DisplayName: "Jamie Chen"}},
	}, state.Mine...)
	a.users["users/alex"] = chat.User{Name: "users/alex", DisplayName: "Alex Morgan"}
	b := &fakeReactions{state: state, calls: make(chan reactionCall, 8)}
	a.backend = b
	a.reactions = map[string]*reactionEntry{m.Name: {loaded: true, state: state}}
	tt := ui.NewTester(a.View, 1180, 900)
	rect, ok := tt.Find("Remove 👍 · 3")
	if !ok {
		t.Fatal("reaction badge is missing")
	}
	tt.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tt.Advance(time.Second)
	if !tt.HasText("👍 · 3\nAlex Morgan\nSam Rivera\nYou") {
		t.Fatalf("hover tooltip did not show only this emoji's participants: %q", tt.Texts())
	}
	if len(b.calls) != 0 {
		t.Fatal("hovering over participants changed a reaction")
	}
}

func TestNativeQuickCustomReactionsAndSearchablePicker(t *testing.T) {
	a, callbacks, m := actionFixture(t)
	defer a.Close()
	b := &fakeReactions{calls: make(chan reactionCall, 8)}
	a.backend = b
	green := image.NewRGBA(image.Rect(0, 0, 64, 64))
	draw.Draw(green, green.Bounds(), &image.Uniform{C: color.RGBA{R: 35, G: 180, B: 65, A: 255}}, image.Point{}, draw.Src)
	a.emojis.sprites = make(map[string]*emojiSprite)
	for _, custom := range testCustomEmojis() {
		a.emojis.sprites[(chat.Emoji{CustomEmoji: &custom}).Key()] = &emojiSprite{bitmap: ui.NewBitmap(green)}
	}
	tt := ui.NewTester(a.View, 1180, 900)
	hoverMessage(t, tt, m.Text)
	waitEmojiCallbacks(t, callbacks, func() bool { e := a.reactions[m.Name]; return e != nil && e.loaded && !a.emojis.loading })
	tt.Frame()
	for _, label := range []string{"React 👍", "React :yep:", "React :peepohey:", "Add reaction"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("quick reaction is missing: %s", label)
		}
	}
	if len(b.calls) != 0 {
		t.Fatal("hovering or rendering added a reaction")
	}
	if err := tt.Click("React :yep:"); err != nil {
		t.Fatal(err)
	}
	call := nextReactionCall(t, b)
	if !call.want || call.emoji.CustomEmoji == nil || call.emoji.CustomEmoji.Name != "customEmojis/yep" {
		t.Fatal("quick custom emoji used the wrong target")
	}
	tt.Frame()
	if len(b.calls) != 0 {
		t.Fatal("pending reaction submitted twice")
	}
	call.result <- reactionResult{state: reactionState(call.emoji)}
	applyNext(t, callbacks)
	tt.Frame()
	if _, ok := tt.Find("Remove :yep: · 1"); !ok {
		t.Fatal("confirmed custom reaction count/ownership not shown")
	}
	hoverMessage(t, tt, m.Text)
	if err := tt.Click("Remove :yep:"); err != nil {
		t.Fatal(err)
	}
	remove := nextReactionCall(t, b)
	if remove.want {
		t.Fatal("second click did not request removal")
	}
	remove.result <- reactionResult{state: chat.Reactions{}}
	applyNext(t, callbacks)
	tt.Frame()
	hoverMessage(t, tt, m.Text)
	if err := tt.Click("Add reaction"); err != nil {
		t.Fatal(err)
	}
	if !a.emojis.open || !tt.HasText("Search emoji") {
		t.Fatal("native emoji picker did not open")
	}
	if err := tt.Click("Search emoji"); err != nil {
		t.Fatal(err)
	}
	tt.Type("party")
	if _, ok := tt.Find("React :partyparrot:"); !ok {
		t.Fatal("search did not find a non-favorite custom emoji")
	}
	if path := os.Getenv("CHAT_MAX_EMOJI_SCREENSHOT"); path != "" {
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
	if err := tt.Click("React :partyparrot:"); err != nil {
		t.Fatal(err)
	}
	parrot := nextReactionCall(t, b)
	if !parrot.want || a.emojis.open {
		t.Fatal("picker selection did not add the reaction and close")
	}
	parrot.result <- reactionResult{state: reactionState(parrot.emoji)}
	applyNext(t, callbacks)
	tt.Frame()
	hoverMessage(t, tt, m.Text)
	if err := tt.Click("Add reaction"); err != nil {
		t.Fatal(err)
	}
	tt.TypeKey(0, ui.KeyEscape, "")
	if a.emojis.open {
		t.Fatal("Escape did not dismiss the picker")
	}
}

func TestReactionRetryAndLiveRefreshDoNotToggleOrCountAsMessages(t *testing.T) {
	a, callbacks, m := actionFixture(t)
	defer a.Close()
	b := &fakeReactions{calls: make(chan reactionCall, 8)}
	a.backend = b
	tt := ui.NewTester(a.View, 1180, 900)
	hoverMessage(t, tt, m.Text)
	waitEmojiCallbacks(t, callbacks, func() bool { return a.reactions[m.Name] != nil && a.reactions[m.Name].loaded && !a.emojis.loading })
	tt.Frame()
	if err := tt.Click("React 👍"); err != nil {
		t.Fatal(err)
	}
	first := nextReactionCall(t, b)
	first.result <- reactionResult{err: errors.New("uncertain")}
	applyNext(t, callbacks)
	tt.Frame()
	tt.Frame()
	if len(b.calls) != 0 || !tt.HasText("Retry reaction") {
		t.Fatal("failed reaction retried automatically or lost retry control")
	}
	if err := tt.Click("Retry reaction"); err != nil {
		t.Fatal(err)
	}
	retry := nextReactionCall(t, b)
	if retry.message != first.message || retry.emoji.Key() != first.emoji.Key() || retry.want != first.want {
		t.Fatal("reaction retry changed its desired state")
	}
	retry.result <- reactionResult{state: reactionState(retry.emoji)}
	applyNext(t, callbacks)
	tt.Frame()
	b.mu.Lock()
	b.state = chat.Reactions{Summaries: []chat.ReactionSummary{{Emoji: chat.Emoji{Unicode: "😂"}, Count: 3}}}
	b.mu.Unlock()
	a.applyStreamEvent(chat.StreamEvent{ReactionMessage: m.Name})
	tt.Frame()
	waitEmojiCallbacks(t, callbacks, func() bool { e := a.reactions[m.Name]; return !e.loading && !e.dirty })
	tt.Frame()
	if !tt.HasText("React 😂 · 3") || len(a.messages) != 1 || a.activity[a.selected] != 0 {
		t.Fatal("live reaction refresh changed message activity or failed to update counts")
	}
}
