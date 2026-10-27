package desktop

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"testing"
	"time"

	"google-chat-max/internal/chat"

	"github.com/egoist/mygo/ui"
)

type deliveredEvent struct {
	event chat.StreamEvent
	acked chan bool
}

type fakeLiveBackend struct {
	Backend
	events      chan deliveredEvent
	stopped     chan struct{}
	cleanupGate chan struct{}
	started     chan struct{}
}

func (b *fakeLiveBackend) Watch(ctx context.Context, emit func(chat.StreamEvent) bool) error {
	if b.started != nil {
		b.started <- struct{}{}
	}
	for {
		select {
		case <-ctx.Done():
			close(b.stopped)
			if b.cleanupGate != nil {
				<-b.cleanupGate
			}
			return nil
		case delivery := <-b.events:
			delivery.acked <- emit(delivery.event)
		}
	}
}

func liveMessage(space, id, text string, offset int) chat.Message {
	m := chat.Message{Name: space + "/messages/" + id, Text: text, CreateTime: time.Date(2026, 10, 27, 1, 0, offset, 0, time.UTC)}
	m.Sender.DisplayName = "Incoming sender"
	m.Thread.Name = space + "/threads/live"
	return m
}

func deliver(t *testing.T, b *fakeLiveBackend, callbacks <-chan func(), event chat.StreamEvent) {
	t.Helper()
	acked := make(chan bool, 1)
	b.events <- deliveredEvent{event: event, acked: acked}
	applyNext(t, callbacks)
	select {
	case accepted := <-acked:
		if !accepted {
			t.Fatal("main-thread delivery was not accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("delivery was not acknowledged")
	}
}

func TestLiveNativeUpdatesActivityDeduplicationWithoutConnectionControls(t *testing.T) {
	callbacks := make(chan func(), 16)
	b := &fakeLiveBackend{Backend: Demo(), events: make(chan deliveredEvent, 1), stopped: make(chan struct{})}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, false)
	defer a.Close()
	a.backend = b
	a.spaces, _ = b.Spaces(context.Background())
	tt := ui.NewTester(a.View, 1180, 780)
	tt.Frame()
	if err := tt.Click("Design studio"); err != nil {
		t.Fatal(err)
	}
	applyNext(t, callbacks)
	tt.Frame()
	a.Connect()
	deliver(t, b, callbacks, chat.StreamEvent{Status: "Live"})
	tt.Frame()
	for _, label := range []string{"Live", "Connect", "Disconnect", "Live while connected · Activity is local", "Connect for incoming messages"} {
		if tt.HasText(label) {
			t.Fatalf("normal chat view exposes connection controls or labels: %s", label)
		}
	}
	deliver(t, b, callbacks, chat.StreamEvent{Status: "Reconnecting"})
	tt.Frame()
	if !tt.HasText("Reconnecting…") {
		t.Fatal("temporary outage is hidden")
	}
	deliver(t, b, callbacks, chat.StreamEvent{Status: "Live"})
	tt.Frame()
	if tt.HasText("Reconnecting…") || tt.HasText("Retry incoming messages") {
		t.Fatal("healthy connection retains outage UI")
	}
	m := liveMessage("spaces/design", "new", "Incoming message", 1)
	deliver(t, b, callbacks, chat.StreamEvent{ID: "event1", Message: &m})
	deliver(t, b, callbacks, chat.StreamEvent{ID: "redelivery-with-different-id", Message: &m})
	tt.Frame()
	if !tt.HasText("Incoming message") || len(a.messages) != 6 || a.activity["spaces/design"] != 0 {
		t.Fatal("open conversation did not update exactly once")
	}
	other := liveMessage("spaces/engineering", "other", "Activity elsewhere", 2)
	deliver(t, b, callbacks, chat.StreamEvent{Message: &other})
	deliver(t, b, callbacks, chat.StreamEvent{Message: &other})
	tt.Frame()
	if a.activity["spaces/engineering"] != 1 || !tt.HasText("1 new") {
		t.Fatal("inactive conversation must show one deduplicated activity badge")
	}
	if path := os.Getenv("CHAT_MAX_LIVE_SCREENSHOT"); path != "" {
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
	if err := tt.Click("Engineering"); err != nil {
		t.Fatal(err)
	}
	applyNext(t, callbacks)
	tt.Frame()
	if a.activity["spaces/engineering"] != 0 || tt.HasText("1 new") {
		t.Fatal("opening conversation must clear local activity")
	}
}

func TestIncomingMessageRetryAppearsOnlyAfterFailure(t *testing.T) {
	callbacks := make(chan func(), 8)
	b := &fakeLiveBackend{Backend: Demo(), events: make(chan deliveredEvent), stopped: make(chan struct{}), started: make(chan struct{}, 1)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, false)
	defer a.Close()
	a.backend = b
	a.streamError = "Incoming messages need attention"
	tt := ui.NewTester(a.View, 1180, 780)
	if !tt.HasText(a.streamError) || !tt.HasText("Retry incoming messages") {
		t.Fatal("connection failure has no recovery action")
	}
	if err := tt.Click("Retry incoming messages"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.started:
	case <-time.After(time.Second):
		t.Fatal("retry did not start incoming messages")
	}
	tt.Frame()
	if a.streamError != "" || tt.HasText("Retry incoming messages") || tt.HasText("Disconnect") {
		t.Fatal("recovery left connection controls visible")
	}
}

func TestHistorySnapshotCannotEraseConcurrentIncomingMessage(t *testing.T) {
	callbacks := make(chan func(), 4)
	b := controlledBackend{reads: make(chan pendingRead, 1)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, false)
	defer a.Close()
	a.backend = b
	a.selectSpace("spaces/a")
	read := <-b.reads
	old := liveMessage("spaces/a", "old", "Older history", 1)
	incoming := liveMessage("spaces/a", "new", "Arrived during fetch", 2)
	a.applyStreamEvent(chat.StreamEvent{Message: &incoming})
	a.applyStreamEvent(chat.StreamEvent{Message: &incoming})
	// Starting another refresh cancels the first request but must preserve
	// the live overlay until an accepted REST snapshot has been merged.
	a.refreshMessages()
	replacement := <-b.reads
	read.done <- []chat.Message{old}
	applyNext(t, callbacks)
	replacement.done <- []chat.Message{old}
	applyNext(t, callbacks)
	if len(a.messages) != 2 || a.messages[1].Name != incoming.Name {
		t.Fatal("REST snapshot erased the incoming event")
	}
	// Arrival ordering and memory stay bounded even under a large burst.
	for i := 0; i < 2200; i++ {
		m := liveMessage("spaces/a", fmt.Sprint(i), "Burst", i+3)
		a.applyStreamEvent(chat.StreamEvent{Message: &m})
	}
	if len(a.messages) != chat.HistoryLimit || len(a.seenMessages) != 2048 || len(a.duringRead) != 0 {
		t.Fatalf("live state exceeded its limits: history=%d seen=%d overlay=%d", len(a.messages), len(a.seenMessages), len(a.duringRead))
	}
}

func TestWindowShutdownWaitsForCloudCleanup(t *testing.T) {
	callbacks := make(chan func(), 4)
	b := &fakeLiveBackend{Backend: Demo(), events: make(chan deliveredEvent), stopped: make(chan struct{}), cleanupGate: make(chan struct{})}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, false)
	a.backend = b
	a.Connect()
	closed := false
	a.Shutdown(func(error) { closed = true })
	<-b.stopped
	if closed {
		t.Fatal("window closed before cloud cleanup")
	}
	close(b.cleanupGate)
	// Both the worker completion and the close callback return to main.
	applyNext(t, callbacks)
	applyNext(t, callbacks)
	if !closed {
		t.Fatal("window did not close after cloud cleanup")
	}
}
