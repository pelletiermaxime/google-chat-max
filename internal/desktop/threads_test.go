package desktop

import (
	"context"
	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
	"testing"
)

func TestNativeThreadViewLiveReplyAndBack(t *testing.T) {
	callbacks := make(chan func(), 8)
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	defer a.Close()
	a.backend = Demo()
	a.spaces, _ = a.backend.Spaces(context.Background())
	a.selectSpace("spaces/design")
	applyNext(t, callbacks)
	tt := ui.NewTester(a.View, 1180, 900)
	tt.Frame()
	a.messageList.ScrollTo(0, ui.Start)
	tt.Frame()
	first, _ := a.messageList.Visible()
	anchor := a.messages[first].Name
	if err := tt.Click("1 reply"); err != nil {
		t.Fatal(err)
	}
	applyNext(t, callbacks)
	tt.Frame()
	if a.threadRoot == nil || !tt.HasText("ORIGINAL MESSAGE") || !tt.HasText("REPLIES") {
		t.Fatal("thread pane did not show parent and replies")
	}
	m := liveMessage("spaces/design", "live-reply", "A new threaded reply", 10)
	m.Thread.Name = "spaces/design/threads/a"
	m.ThreadReply = true
	a.applyStreamEvent(chat.StreamEvent{Message: &m})
	a.applyStreamEvent(chat.StreamEvent{Message: &m})
	tt.Frame()
	if !tt.HasText("A new threaded reply") || len(a.threadMessages) != 3 {
		t.Fatal("thread did not receive one deduplicated reply")
	}
	if err := tt.Click("Back to conversation"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	after, _ := a.messageList.Visible()
	if a.threadName != "" || a.messages[after].Name != anchor {
		t.Fatal("back lost the conversation context")
	}
}

type threadRequest struct {
	ctx    context.Context
	result chan chat.ThreadHistory
}
type delayedThreads struct {
	Backend
	requests chan threadRequest
}

func (b delayedThreads) Thread(ctx context.Context, space, thread string) (chat.ThreadHistory, error) {
	r := threadRequest{ctx: ctx, result: make(chan chat.ThreadHistory, 1)}
	b.requests <- r
	return <-r.result, nil
}

func TestLateThreadResponseCannotReplaceNewSelection(t *testing.T) {
	callbacks := make(chan func(), 4)
	b := delayedThreads{Backend: Demo(), requests: make(chan threadRequest, 1)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	defer a.Close()
	a.backend = b
	a.selected = "spaces/design"
	a.openThread("spaces/design/threads/one")
	one := <-b.requests
	a.openThread("spaces/design/threads/two")
	two := <-b.requests
	if one.ctx.Err() == nil {
		t.Fatal("old thread request was not canceled")
	}
	root := chat.Message{Name: "two", Text: "Correct thread"}
	two.result <- chat.ThreadHistory{Root: &root}
	applyNext(t, callbacks)
	one.result <- chat.ThreadHistory{Root: &chat.Message{Name: "one"}}
	applyNext(t, callbacks)
	if a.threadRoot == nil || a.threadRoot.Name != "two" {
		t.Fatal("late thread replaced selected history")
	}
}

func TestOrdinaryMessageThreadIDsDoNotCreateButtons(t *testing.T) {
	a := New(nil, func(func()) {}, func() {}, true)
	defer a.Close()
	a.selected = "spaces/a"
	a.spaces = []chat.Space{{Name: a.selected, SpaceType: "SPACE", SpaceThreadingState: "THREADED_MESSAGES"}}
	root := liveMessage(a.selected, "root", "Ordinary message", 1)
	root.Thread.Name = "spaces/a/threads/root"
	a.messages = []chat.Message{root}
	if _, show := a.threadControl(root); show {
		t.Fatal("a thread resource ID alone created a reply button")
	}
	reply := liveMessage(a.selected, "reply", "Real reply", 2)
	reply.ThreadReply = true
	reply.Thread.Name = root.Thread.Name
	a.messages = append(a.messages, reply)
	if label, show := a.threadControl(root); !show || label != "1 reply" {
		t.Fatal("actual replies should expose a thread control")
	}
	tt := ui.NewTester(a.View, 1180, 900)
	tt.Frame()
	if r, ok := tt.Find("1 reply"); !ok || r.W > 200 {
		t.Fatal("reply control should be compact, not a full-width banner")
	}
	a.spaces[0].SpaceThreadingState = "UNTHREADED_MESSAGES"
	if _, show := a.threadControl(root); show {
		t.Fatal("unthreaded meeting chat exposed thread navigation")
	}
}
