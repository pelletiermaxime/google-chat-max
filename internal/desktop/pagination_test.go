package desktop

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

type pageCall struct {
	ctx                  context.Context
	space, thread, token string
	result               chan chat.HistoryPage
	failure              chan error
}
type controlledPages struct {
	Backend
	calls chan pageCall
}

func (b controlledPages) History(ctx context.Context, space, thread, token string) (chat.HistoryPage, error) {
	call := pageCall{ctx: ctx, space: space, thread: thread, token: token, result: make(chan chat.HistoryPage, 1), failure: make(chan error, 1)}
	b.calls <- call
	select {
	case page := <-call.result:
		return page, nil
	case err := <-call.failure:
		return chat.HistoryPage{}, err
	case <-ctx.Done():
		return chat.HistoryPage{}, ctx.Err()
	}
}

func historyFixture(start, count int) []chat.Message {
	var out []chat.Message
	for i := start; i < start+count; i++ {
		m := chat.Message{Name: fmt.Sprintf("spaces/a/messages/%04d", i), Text: fmt.Sprintf("Message %04d", i), CreateTime: time.Date(2026, 10, 26, 0, i, 0, 0, time.UTC), Sender: chat.User{DisplayName: "Alex"}}
		m.Thread.Name = "spaces/a/threads/t"
		m.ThreadReply = i > 0
		out = append(out, m)
	}
	return out
}

func TestPaginationPreservesNativeAnchorAndConcurrentLiveMessages(t *testing.T) {
	callbacks := make(chan func(), 8)
	b := controlledPages{Backend: Demo(), calls: make(chan pageCall, 2)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	defer a.Close()
	a.backend = b
	a.selected = "spaces/a"
	a.spaces = []chat.Space{{Name: a.selected, DisplayName: "History", SpaceType: "SPACE"}}
	a.messages = historyFixture(60, 60)
	a.historyNext = "older-1"
	tt := ui.NewTester(a.View, 1180, 800)
	tt.Frame()
	if a.olderLoading || len(b.calls) != 0 || tt.HasText("Load earlier") {
		t.Fatal("opening history must stay at the latest messages without a paging button")
	}
	tt.Scroll(700, 350, 0, -1e6)
	tt.Frame()
	first, _ := a.messageList.Visible()
	anchor := a.messages[first].Name
	if !a.olderLoading {
		t.Fatal("scrolling to the top did not load earlier history")
	}
	call := <-b.calls
	if call.token != "older-1" || call.thread != "" {
		t.Fatal("wrong pagination cursor")
	}
	tt.Frame()
	tt.Frame()
	if len(b.calls) != 0 {
		t.Fatal("frames at the top started duplicate page requests")
	}
	incoming := historyFixture(120, 1)[0]
	a.applyStreamEvent(chat.StreamEvent{Message: &incoming})
	call.result <- chat.HistoryPage{Messages: historyFixture(0, 60), Next: "older-2"}
	applyNext(t, callbacks)
	tt.Frame()
	if len(a.messages) != 121 || a.historyNext != "older-2" || !a.historyExpanded {
		t.Fatal("pagination lost live messages or duplicated overlap")
	}
	after, _ := a.messageList.Visible()
	if a.messages[after].Name != anchor {
		t.Fatalf("prepending jumped from %s to %s", anchor, a.messages[after].Name)
	}
	if len(b.calls) != 0 || a.olderLoading {
		t.Fatal("prepending triggered another page before scrolling up again")
	}
	if len(tt.Texts()) > 160 {
		t.Fatal("history rendered every retained row instead of virtualizing")
	}
	a.messageList.ScrollTo(0, ui.Start)
	tt.Frame()
	if !a.olderLoading {
		t.Fatal("returning to the top did not request the next page")
	}
	next := <-b.calls
	if next.token != "older-2" {
		t.Fatal("automatic paging did not advance its cursor")
	}
	next.result <- chat.HistoryPage{}
	applyNext(t, callbacks)
	tt.Frame()
	a.messageList.ScrollTo(10, ui.Start)
	tt.Frame()
	a.messageList.ScrollTo(0, ui.Start)
	tt.Frame()
	if a.olderLoading || len(b.calls) != 0 {
		t.Fatal("exhausted history started another request")
	}
	if err := tt.Click("Latest"); err != nil {
		t.Fatal(err)
	}
	refresh := <-b.calls
	if refresh.token != "" {
		t.Fatal("Latest reused an older-page cursor")
	}
	refresh.result <- chat.HistoryPage{Messages: historyFixture(120, 60), Next: "recent-older"}
	applyNext(t, callbacks)
	tt.Frame()
	if a.historyExpanded || len(a.messages) != 60 || a.messages[0].Name != historyFixture(120, 1)[0].Name {
		t.Fatal("Latest did not restore the current live range")
	}
	// A stale older-page result cannot replace another conversation.
	a.loadEarlier(false)
	stale := <-b.calls
	a.selectSpace("spaces/other")
	latest := <-b.calls
	if stale.ctx.Err() == nil {
		t.Fatal("switch did not cancel older page")
	}
	stale.result <- chat.HistoryPage{Messages: historyFixture(-60, 60)}
	applyNext(t, callbacks)
	latest.result <- chat.HistoryPage{}
	applyNext(t, callbacks)
	if len(a.messages) != 0 || a.selected != "spaces/other" {
		t.Fatal("late older page leaked into another conversation")
	}
}

func TestThreadPaginationKeepsParentAndDoesNotUseConversationCursor(t *testing.T) {
	callbacks := make(chan func(), 4)
	b := controlledPages{Backend: Demo(), calls: make(chan pageCall, 2)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	defer a.Close()
	a.backend = b
	a.selected = "spaces/a"
	a.threadName = "spaces/a/threads/t"
	a.threadNext = "thread-older"
	a.historyNext = "conversation-older"
	a.threadMessages = historyFixture(60, 60)
	root := historyFixture(0, 1)[0]
	a.threadRoot = &root
	tt := ui.NewTester(a.View, 1180, 800)
	tt.Frame()
	if a.threadOlderLoading || len(b.calls) != 0 {
		t.Fatal("opening a thread must not page backward immediately")
	}
	tt.Scroll(700, 350, 0, -1e6)
	tt.Frame()
	if !a.threadOlderLoading || a.olderLoading {
		t.Fatal("scrolling to the top did not load only the thread history")
	}
	call := <-b.calls
	if call.thread != a.threadName || call.token != "thread-older" {
		t.Fatal("thread pagination used the conversation's cursor")
	}
	call.result <- chat.HistoryPage{Messages: historyFixture(0, 60)}
	applyNext(t, callbacks)
	tt.Frame()
	if len(a.threadMessages) != 120 || a.threadRoot.Name != root.Name || a.historyNext != "conversation-older" {
		t.Fatal("thread pagination lost parent or changed main history")
	}
}

func TestAutomaticPagingFillsShortHistory(t *testing.T) {
	callbacks := make(chan func(), 4)
	b := controlledPages{Backend: Demo(), calls: make(chan pageCall, 2)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	defer a.Close()
	a.backend = b
	a.selected = "spaces/a"
	a.messages = historyFixture(60, 1)
	a.historyNext = "short-older"
	tt := ui.NewTester(a.View, 1180, 800)
	tt.Frame()
	if !a.olderLoading {
		t.Fatal("short history must load earlier without requiring an unavailable scrollbar")
	}
	call := <-b.calls
	call.result <- chat.HistoryPage{Messages: historyFixture(59, 1), Next: "more-older"}
	applyNext(t, callbacks)
	tt.Frame()
	if !a.olderLoading {
		t.Fatal("short pages stopped before filling the viewport")
	}
	next := <-b.calls
	if next.token != "more-older" {
		t.Fatal("short history did not advance its cursor")
	}
	next.result <- chat.HistoryPage{Messages: historyFixture(0, 59), Next: "even-older"}
	applyNext(t, callbacks)
	tt.Frame()
	if a.olderLoading || len(b.calls) != 0 || !a.messageList.AtEnd() {
		t.Fatal("filling the viewport must stop paging and keep the latest messages visible")
	}
}

func TestAutomaticPagingPausesAfterFailureAndSlidesRetainedWindow(t *testing.T) {
	callbacks := make(chan func(), 4)
	b := controlledPages{Backend: Demo(), calls: make(chan pageCall, 2)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	defer a.Close()
	a.backend = b
	a.selected = "spaces/a"
	a.messages = historyFixture(0, retainedHistoryLimit)
	a.historyNext = "older"
	tt := ui.NewTester(a.View, 1180, 800)
	tt.Frame()
	tt.Scroll(700, 350, 0, -1e6)
	tt.Frame()
	if !a.olderLoading {
		t.Fatal("scrolling to the top did not page a full retained window")
	}
	call := <-b.calls
	call.failure <- errors.New("temporary paging failure")
	applyNext(t, callbacks)
	tt.Frame()
	tt.Frame()
	if a.messageError == "" || a.olderLoading || len(b.calls) != 0 {
		t.Fatal("a failed page must show an error without automatic retry loops")
	}
	tt.Scroll(700, 350, 0, 600)
	tt.Scroll(700, 350, 0, -1e6)
	tt.Frame()
	if !a.olderLoading || a.messageError != "" {
		t.Fatal("returning to the top did not retry and clear the paging error")
	}
	retry := <-b.calls
	if retry.token != "older" {
		t.Fatal("failure advanced the paging cursor")
	}
	retry.result <- chat.HistoryPage{Messages: historyFixture(-60, 60), Next: "older-2"}
	applyNext(t, callbacks)
	tt.Frame()
	first, _ := a.messageList.Visible()
	if len(a.messages) != retainedHistoryLimit || !a.newerAvailable || a.messages[first].Name != historyFixture(0, 1)[0].Name {
		t.Fatal("automatic paging must preserve the anchor while sliding a bounded window")
	}
}

func TestRetainedHistoryWindowIsBoundedAndLatestIsReachable(t *testing.T) {
	a := New(nil, func(func()) {}, func() {}, true)
	defer a.Close()
	a.selected = "spaces/a"
	a.messages = historyFixture(0, 300)
	a.historyExpanded = true
	newest := historyFixture(300, 1)[0]
	a.applyStreamEvent(chat.StreamEvent{Message: &newest})
	if len(a.messages) != 300 || !a.newerAvailable {
		t.Fatal("incoming message overflowed a browsed history window")
	}
	older := mergeRetained(a.messages, historyFixture(-60, 60), true)
	if len(older) != retainedHistoryLimit || older[0].Name != historyFixture(-60, 1)[0].Name {
		t.Fatal("older history window did not slide within its memory budget")
	}
	// Keep a bounded contiguous older window while indicating newer activity.
	for i := 301; i < 700; i++ {
		m := historyFixture(i, 1)[0]
		a.applyStreamEvent(chat.StreamEvent{Message: &m})
	}
	if len(a.messages) != 300 || !a.newerAvailable {
		t.Fatal("new activity grew a detached history window")
	}
}
