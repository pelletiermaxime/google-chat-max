package desktop

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

type markReadResult struct {
	through time.Time
	err     error
}
type markReadCall struct {
	ctx     context.Context
	space   string
	through time.Time
	done    chan markReadResult
}
type controlledReadState struct {
	Backend
	calls chan markReadCall
}

func (b controlledReadState) MarkRead(ctx context.Context, space string, through time.Time) (time.Time, error) {
	call := markReadCall{ctx: ctx, space: space, through: through, done: make(chan markReadResult, 1)}
	select {
	case b.calls <- call:
	case <-ctx.Done():
		return time.Time{}, ctx.Err()
	}
	select {
	case result := <-call.done:
		return result.through, result.err
	case <-ctx.Done():
		return time.Time{}, ctx.Err()
	}
}

func nextMarkRead(t *testing.T, b controlledReadState) markReadCall {
	t.Helper()
	select {
	case call := <-b.calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("read-state update did not start")
		return markReadCall{}
	}
}

func readStateFixture() (*App, controlledReadState, chan func(), *ui.Tester) {
	callbacks := make(chan func(), 16)
	b := controlledReadState{Backend: Demo(), calls: make(chan markReadCall, 4)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, false)
	a.backend, a.selected, a.historyLoaded = b, "spaces/a", true
	a.spaces = []chat.Space{{Name: a.selected, DisplayName: "Conversation", SpaceType: "DIRECT_MESSAGE"}}
	a.messages = historyFixture(0, 3)
	for i := range a.messages {
		a.messages[i].ThreadReply = false
	}
	tt := ui.NewTester(a.View, 1180, 780)
	return a, b, callbacks, tt
}

func TestReadSyncNeedsFocusLatestMessagesAndDwell(t *testing.T) {
	a, b, callbacks, tt := readStateFixture()
	defer a.Close()
	tt.Advance(time.Second)
	if len(b.calls) != 0 {
		t.Fatal("background window marked messages read")
	}
	a.setFocused(true)
	tt.Frame()
	tt.Advance(readSyncDelay / 2)
	if len(b.calls) != 0 {
		t.Fatal("read state was updated before the viewing delay")
	}
	tt.Advance(readSyncDelay)
	call := nextMarkRead(t, b)
	if call.space != a.selected || !call.through.Equal(a.messages[2].CreateTime) {
		t.Fatal("read sync included a different conversation or unseen timestamp")
	}
	call.done <- markReadResult{through: call.through}
	applyNext(t, callbacks)
	tt.Frame()
	tt.Advance(time.Second)
	if len(b.calls) != 0 {
		t.Fatal("unchanged rendered messages repeated the read-state update")
	}
	incoming := historyFixture(3, 1)[0]
	incoming.ThreadReply = false
	a.applyStreamEvent(chat.StreamEvent{Message: &incoming})
	tt.Frame()
	tt.Advance(readSyncDelay + time.Millisecond)
	newRead := nextMarkRead(t, b)
	if !newRead.through.Equal(incoming.CreateTime) {
		t.Fatal("visible incoming message did not advance the read position")
	}
	newRead.done <- markReadResult{through: newRead.through}
	applyNext(t, callbacks)
}

func TestReadSyncSkipsOldHistoryThreadsAndIncompleteHistory(t *testing.T) {
	for _, mode := range []string{"scrolled up", "newer omitted", "thread", "loading", "failed", "demo"} {
		t.Run(mode, func(t *testing.T) {
			a, b, _, tt := readStateFixture()
			defer a.Close()
			switch mode {
			case "scrolled up":
				a.messages = historyFixture(0, 60)
				a.messageList.ScrollTo(0, ui.Start)
			case "newer omitted":
				a.newerAvailable = true
			case "thread":
				a.threadName = "spaces/a/threads/t"
				a.threadMessages = a.messages
			case "loading":
				a.reading = true
			case "failed":
				a.messageError = "History could not load"
			case "demo":
				a.demo = true
			}
			a.setFocused(true)
			tt.Frame()
			tt.Advance(time.Second)
			if len(b.calls) != 0 || a.readSync.busy {
				t.Fatal("ineligible conversation view marked messages read")
			}
		})
	}
}

func TestReadSyncCancelsOnBlurAndConversationSwitch(t *testing.T) {
	a, b, callbacks, tt := readStateFixture()
	defer a.Close()
	a.setFocused(true)
	tt.Frame()
	a.setFocused(false)
	tt.Advance(time.Second)
	if len(b.calls) != 0 {
		t.Fatal("blurred window completed a pending viewing delay")
	}
	a.setFocused(true)
	tt.Frame()
	tt.Advance(readSyncDelay + time.Millisecond)
	call := nextMarkRead(t, b)
	a.selectSpace("")
	if call.ctx.Err() == nil {
		t.Fatal("conversation switch did not cancel an in-flight read update")
	}
	applyNext(t, callbacks)
	if a.readSync.err != "" || a.readSync.busy {
		t.Fatal("intentional cancellation left a read-sync error or stuck request")
	}
}

func TestReadSyncPermissionFailureIsVisibleAndDoesNotLoop(t *testing.T) {
	a, b, callbacks, tt := readStateFixture()
	defer a.Close()
	a.setFocused(true)
	tt.Frame()
	tt.Advance(readSyncDelay + time.Millisecond)
	call := nextMarkRead(t, b)
	call.done <- markReadResult{err: chat.ErrReadStatePermission}
	applyNext(t, callbacks)
	tt.Frame()
	tt.Advance(10 * time.Second)
	if !tt.HasText("Retry read sync") || len(b.calls) != 0 || a.readSync.confirmed[a.selected] != (time.Time{}) {
		t.Fatal("permission failure was hidden, treated as success, or retried automatically")
	}
	if err := tt.Click("Retry read sync"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(readSyncDelay + time.Millisecond)
	retry := nextMarkRead(t, b)
	retry.done <- markReadResult{through: retry.through}
	applyNext(t, callbacks)
	tt.Frame()
	if tt.HasText("Retry read sync") {
		t.Fatal("successful retry kept an obsolete read-sync warning")
	}
}

func TestLauncherFocusAndSettingsGateReadSync(t *testing.T) {
	a, _, _, _ := readStateFixture()
	defer a.Close()
	l := NewLauncher(LauncherOptions{}, func(func()) {}, func() {})
	l.app, l.loading = a, false
	l.SetFocused(true)
	if !a.focused {
		t.Fatal("native focus was not forwarded to the app")
	}
	l.setupVisible = true
	l.SetFocused(true)
	if a.focused {
		t.Fatal("settings screen left the hidden conversation eligible for read sync")
	}
	l.setupVisible = false
	l.SetFocused(true)
	l.SetFocused(false)
	if a.focused {
		t.Fatal("native blur was not forwarded to the app")
	}
}
