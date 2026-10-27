package desktop

import (
	"context"
	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
	"testing"
	"time"
)

type sendCall struct {
	space, thread, text, id string
	result                  chan sendResult
}
type sendResult struct {
	m   chat.Message
	err error
}
type controlledSend struct {
	Backend
	calls chan sendCall
}

func (b controlledSend) Send(_ context.Context, space, thread, text, id string) (chat.Message, error) {
	c := sendCall{space: space, thread: thread, text: text, id: id, result: make(chan sendResult, 1)}
	b.calls <- c
	r := <-c.result
	return r.m, r.err
}

func TestComposerExplicitSendIsolationAndSafeRetry(t *testing.T) {
	callbacks := make(chan func(), 8)
	b := controlledSend{Backend: Demo(), calls: make(chan sendCall, 2)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, false)
	defer a.Close()
	a.backend = b
	a.spaces, _ = b.Spaces(context.Background())
	a.selected = "spaces/design"
	d := a.draftFor("spaces/design|")
	d.Text = "Hello\nworld"
	tt := ui.NewTester(a.View, 1180, 900)
	tt.Frame()
	select {
	case <-b.calls:
		t.Fatal("rendering sent a message")
	default:
	}
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	first := <-b.calls
	if first.space != "spaces/design" || first.thread != "" || first.text != "Hello\nworld" {
		t.Fatal("wrong destination")
	}
	a.submitDraft("spaces/design", "")
	select {
	case <-b.calls:
		t.Fatal("double send accepted")
	default:
	}
	a.selected = "spaces/engineering"
	other := a.draftFor("spaces/engineering|")
	other.Text = "Other draft"
	first.result <- sendResult{err: &chat.SendError{Uncertain: true, Detail: "Uncertain"}}
	applyNext(t, callbacks)
	if d.Text != "Hello\nworld" || !d.Uncertain || other.Text != "Other draft" {
		t.Fatal("failed send lost or crossed drafts")
	}
	a.submitDraft("spaces/design", "")
	retry := <-b.calls
	if retry.id != first.id || retry.text != first.text {
		t.Fatal("retry changed its idempotency key")
	}
	m := liveMessage("spaces/design", "sent", "Hello\nworld", 1)
	retry.result <- sendResult{m: m}
	applyNext(t, callbacks)
	a.applyStreamEvent(chat.StreamEvent{Message: &m})
	if d.Text != "" || other.Text != "Other draft" || a.activity["spaces/design"] != 1 {
		t.Fatal("send result/event duplication or draft isolation failed")
	}
}

func TestComposerKeyboardAndCloseWaitForPendingSend(t *testing.T) {
	callbacks := make(chan func(), 8)
	b := controlledSend{Backend: Demo(), calls: make(chan sendCall, 2)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, false)
	a.backend = b
	a.spaces, _ = b.Spaces(context.Background())
	a.selected = "spaces/design"
	tt := ui.NewTester(a.View, 1180, 900)
	tt.Frame()
	if err := tt.Click("Message draft"); err != nil {
		t.Fatal(err)
	}
	tt.Type("one")
	tt.TypeKey(ui.Shift, ui.KeyEnter, "")
	tt.Type("two")
	select {
	case <-b.calls:
		t.Fatal("Shift+Enter sent a message")
	default:
	}
	tt.TypeKey(0, ui.KeyEnter, "")
	var call sendCall
	select {
	case call = <-b.calls:
	case <-time.After(time.Second):
		t.Fatal("Enter did not submit")
	}
	if call.text != "one\ntwo" {
		t.Fatalf("multiline draft corrupted: %q", call.text)
	}
	closed := false
	a.Shutdown(func(error) { closed = true })
	if closed || a.ctx.Err() != nil {
		t.Fatal("close canceled a user-initiated send before it settled")
	}
	call.result <- sendResult{m: liveMessage("spaces/design", "sent", "one\ntwo", 1)}
	applyNext(t, callbacks)
	applyNext(t, callbacks)
	if !closed {
		t.Fatal("window did not close after send completed")
	}
}
