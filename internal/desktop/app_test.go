package desktop

import (
	"context"
	"errors"
	"image/png"
	"os"
	"testing"
	"time"

	"google-chat-max/internal/chat"

	"github.com/egoist/mygo/ui"
)

type pendingRead struct {
	ctx   context.Context
	space string
	done  chan []chat.Message
}

type controlledBackend struct{ reads chan pendingRead }

func (b controlledBackend) Spaces(context.Context) ([]chat.Space, error) {
	return Demo().Spaces(context.Background())
}
func (b controlledBackend) Messages(ctx context.Context, space string) ([]chat.Message, error) {
	r := pendingRead{ctx: ctx, space: space, done: make(chan []chat.Message, 1)}
	b.reads <- r
	// Deliberately ignore cancellation to exercise late-result handling.
	return <-r.done, nil
}

func applyNext(t *testing.T, callbacks <-chan func()) {
	t.Helper()
	select {
	case fn := <-callbacks:
		fn()
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not publish its result")
	}
}

func TestSwitchConversationRejectsLateResultAndCloseCancels(t *testing.T) {
	callbacks := make(chan func(), 4)
	b := controlledBackend{reads: make(chan pendingRead, 2)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, false)
	a.backend = b
	a.selectSpace("spaces/first")
	first := <-b.reads
	a.selectSpace("spaces/second")
	second := <-b.reads
	if first.ctx.Err() == nil {
		t.Fatal("switching must cancel the previous network request")
	}
	second.done <- []chat.Message{{Name: "second"}}
	applyNext(t, callbacks)
	first.done <- []chat.Message{{Name: "first"}}
	applyNext(t, callbacks)
	if len(a.messages) != 1 || a.messages[0].Name != "second" || a.selected != "spaces/second" {
		t.Fatal("late result replaced the selected conversation's history")
	}
	a.refreshMessages()
	last := <-b.reads
	a.Close()
	if last.ctx.Err() == nil {
		t.Fatal("closing must cancel pending network work")
	}
	last.done <- []chat.Message{{Name: "after-close"}}
	applyNext(t, callbacks)
	if a.messages[0].Name != "second" {
		t.Fatal("closed app accepted a worker result")
	}
}

func TestNativeConversationSelectionSearchRefreshAndEmptyHistory(t *testing.T) {
	callbacks := make(chan func(), 4)
	a := New(func(context.Context) (Backend, error) { return Demo(), nil }, func(fn func()) { callbacks <- fn }, func() {}, true)
	defer a.Close()
	a.RefreshSpaces()
	applyNext(t, callbacks)
	height := 780
	if os.Getenv("CHAT_MAX_SCREENSHOT") != "" {
		height = 940 // Include the complete demo conversation in the README capture.
	}
	tt := ui.NewTester(a.View, 1180, height)
	tt.Frame()
	if err := tt.Click("Design studio"); err != nil {
		t.Fatal(err)
	}
	applyNext(t, callbacks)
	tt.Frame()
	if a.selected != "spaces/design" || !tt.HasText("Thread reply") || len(a.messages) != 5 {
		t.Fatalf("native selection failed: selected=%s, messages=%d", a.selected, len(a.messages))
	}
	if path := os.Getenv("CHAT_MAX_SCREENSHOT"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := tt.Click("Refresh messages"); err != nil {
		t.Fatal(err)
	}
	applyNext(t, callbacks)
	tt.Frame()
	if err := tt.Click("The common room"); err != nil {
		t.Fatal(err)
	}
	applyNext(t, callbacks)
	tt.Frame()
	if !tt.HasText("No messages in this conversation yet.") || len(a.messages) != 0 {
		t.Fatal("empty conversation retained another conversation's messages")
	}
	if err := tt.Click("Find a conversation"); err != nil {
		t.Fatal(err)
	}
	tt.Type("no such conversation")
	if !tt.HasText("No matching conversations.") {
		t.Fatal("search did not filter the conversation list")
	}
}

func TestLoginFailureCanBeRetried(t *testing.T) {
	callbacks := make(chan func(), 2)
	attempts := 0
	a := New(func(context.Context) (Backend, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("Login unavailable")
		}
		return Demo(), nil
	}, func(fn func()) { callbacks <- fn }, func() {}, false)
	defer a.Close()
	a.RefreshSpaces()
	applyNext(t, callbacks)
	if a.spacesError != "Login unavailable" || a.loading {
		t.Fatal("login failure did not settle into an error state")
	}
	a.RefreshSpaces()
	applyNext(t, callbacks)
	if a.spacesError != "" || len(a.spaces) != 5 {
		t.Fatal("retry did not recover from login failure")
	}
}
