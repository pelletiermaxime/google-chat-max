package desktop

import (
	"errors"
	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
	"slices"
	"testing"
	"time"
)

func TestPagingBoundarySurvivesLiveArrival(t *testing.T) {
	callbacks := make(chan func(), 4)
	b := controlledPages{Backend: Demo(), calls: make(chan pageCall, 1)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	defer a.Close()
	a.backend = b
	a.selected = "spaces/a"
	a.messages = historyFixture(60, 60)
	a.historyNext = "older"
	a.loadEarlier(false)
	call := <-b.calls
	incoming := historyFixture(120, 1)[0]
	a.applyStreamEvent(chat.StreamEvent{Message: &incoming})
	call.result <- chat.HistoryPage{Messages: historyFixture(0, 60)}
	applyNext(t, callbacks)
	missing := historyFixture(60, 1)[0].Name
	if !slices.ContainsFunc(a.messages, func(m chat.Message) bool { return m.Name == missing }) {
		t.Fatalf("boundary message %s disappeared; retained=%d, expected contiguous 0..120", missing, len(a.messages))
	}
}

func TestOutOfOrderArrivalMarksDroppedNewest(t *testing.T) {
	a := New(nil, func(func()) {}, func() {}, true)
	defer a.Close()
	a.selected = "spaces/a"
	a.messages = historyFixture(0, 300)
	a.historyExpanded = true
	newest := a.messages[299].Name
	late := chat.Message{Name: "spaces/a/messages/delayed", CreateTime: a.messages[299].CreateTime.Add(-30 * time.Second), Text: "Delayed event"}
	a.applyStreamEvent(chat.StreamEvent{Message: &late})
	if !a.newerAvailable && !slices.ContainsFunc(a.messages, func(m chat.Message) bool { return m.Name == newest }) {
		t.Fatal("newest retained message was evicted without Newer messages available")
	}
}

func TestDuplicateDeliveryEnrichesPartialSend(t *testing.T) {
	a := New(nil, func(func()) {}, func() {}, true)
	defer a.Close()
	a.selected = "spaces/a"
	partial := chat.Message{Name: "spaces/a/messages/sent", Text: "Hello", CreateTime: time.Now()}
	a.applyStreamEvent(chat.StreamEvent{Message: &partial})
	full := partial
	full.Thread.Name = "spaces/a/threads/t"
	full.Sender.DisplayName = "Alex"
	full.LastUpdateTime = partial.CreateTime.Add(time.Second)
	a.applyStreamEvent(chat.StreamEvent{Message: &full})
	if a.messages[0].Thread.Name == "" || a.messages[0].Sender.DisplayName != "Alex" || len(a.messages) != 1 {
		t.Fatal("full stream delivery did not repair partial send metadata")
	}
	partial.Text = "Old partial response"
	a.applyStreamEvent(chat.StreamEvent{Message: &partial})
	if a.messages[0].Thread.Name == "" || a.messages[0].Text != "Hello" || len(a.seenOrder) != 1 || len(a.activity) != 0 {
		t.Fatal("a repeated partial response erased metadata, changed content, or counted delivery twice")
	}
}

func TestPagingRechecksAnchorAfterFetch(t *testing.T) {
	callbacks := make(chan func(), 4)
	b := controlledPages{Backend: Demo(), calls: make(chan pageCall, 1)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	defer a.Close()
	a.backend = b
	a.selected = "spaces/a"
	a.spaces = []chat.Space{{Name: a.selected, SpaceType: "SPACE"}}
	a.messages = historyFixture(60, 300)
	a.historyExpanded = true
	a.historyNext = "older"
	tt := ui.NewTester(a.View, 1180, 900)
	tt.Frame()
	a.messageList.ScrollTo(20, ui.Start)
	tt.Frame()
	a.loadEarlier(false)
	call := <-b.calls
	a.messageList.ScrollTo(299, ui.Start)
	tt.Frame()
	first, _ := a.messageList.Visible()
	anchor := a.messages[first].Name
	call.result <- chat.HistoryPage{Messages: historyFixture(0, 60)}
	applyNext(t, callbacks)
	if !slices.ContainsFunc(a.messages, func(m chat.Message) bool { return m.Name == anchor }) {
		t.Fatalf("visible anchor %s was evicted after scrolling during fetch", anchor)
	}
	if a.historyNext != "older" || a.olderLoading {
		t.Fatal("postponing a page advanced its cursor or left the request loading")
	}
}

func TestThreadPagingBoundarySurvivesLiveArrival(t *testing.T) {
	callbacks := make(chan func(), 4)
	b := controlledPages{Backend: Demo(), calls: make(chan pageCall, 1)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	defer a.Close()
	a.backend, a.selected, a.threadName = b, "spaces/a", "spaces/a/threads/t"
	a.threadMessages, a.threadNext = historyFixture(60, 60), "older"
	a.loadEarlier(true)
	call := <-b.calls
	incoming := historyFixture(120, 1)[0]
	a.applyStreamEvent(chat.StreamEvent{Message: &incoming})
	call.result <- chat.HistoryPage{Messages: historyFixture(0, 60)}
	applyNext(t, callbacks)
	if len(a.threadMessages) != 121 || a.threadMessages[60].Name != historyFixture(60, 1)[0].Name {
		t.Fatal("thread paging lost the boundary message during live delivery")
	}
}

func TestThreadPagingRechecksAnchorWithPinnedParent(t *testing.T) {
	callbacks := make(chan func(), 4)
	b := controlledPages{Backend: Demo(), calls: make(chan pageCall, 1)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	defer a.Close()
	a.backend, a.selected, a.threadName = b, "spaces/a", "spaces/a/threads/t"
	a.spaces = []chat.Space{{Name: a.selected, SpaceType: "SPACE"}}
	a.threadMessages, a.threadNext, a.threadExpanded = historyFixture(60, 300), "older", true
	root := historyFixture(0, 1)[0]
	a.threadRoot = &root
	tt := ui.NewTester(a.View, 1180, 900)
	tt.Frame()
	a.threadList.ScrollTo(20, ui.Start)
	tt.Frame()
	a.loadEarlier(true)
	call := <-b.calls
	a.threadList.ScrollTo(299, ui.Start)
	tt.Frame()
	anchor := a.historyAnchor(true)
	call.result <- chat.HistoryPage{Messages: historyFixture(0, 60)}
	applyNext(t, callbacks)
	if !slices.ContainsFunc(a.threadItems(), func(m chat.Message) bool { return m.Name == anchor }) || a.threadNext != "older" || a.threadOlderLoading {
		t.Fatal("thread paging evicted the current anchor or advanced a postponed page")
	}
}

func TestFailedSnapshotRetryKeepsLiveOverlay(t *testing.T) {
	callbacks := make(chan func(), 4)
	b := controlledPages{Backend: Demo(), calls: make(chan pageCall, 1)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, false)
	defer a.Close()
	a.backend = b
	a.selectSpace("spaces/a")
	first := <-b.calls
	incoming := historyFixture(1, 1)[0]
	a.applyStreamEvent(chat.StreamEvent{Message: &incoming})
	first.failure <- errors.New("temporary snapshot failure")
	applyNext(t, callbacks)
	a.refreshMessages()
	retry := <-b.calls
	retry.result <- chat.HistoryPage{Messages: historyFixture(0, 1)}
	applyNext(t, callbacks)
	if len(a.messages) != 2 || a.messages[1].Name != incoming.Name || len(a.duringRead) != 0 {
		t.Fatal("retry after a failed snapshot erased an in-flight live message")
	}
}

func TestThreadReconnectDuringSnapshotQueuesReconciliation(t *testing.T) {
	callbacks := make(chan func(), 4)
	b := delayedThreads{Backend: Demo(), requests: make(chan threadRequest, 2)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, false)
	defer a.Close()
	a.backend, a.selected, a.threadName = b, "spaces/a", "spaces/a/threads/t"
	// Keep the conversation snapshot pending so the reconnect exercises the
	// open thread without starting a second independent backend request.
	a.reading = true
	a.refreshThread()
	first := <-b.requests
	a.applyStreamEvent(chat.StreamEvent{Reconcile: true})
	incoming := historyFixture(1, 1)[0]
	a.applyStreamEvent(chat.StreamEvent{Message: &incoming})
	first.result <- chat.ThreadHistory{Page: chat.HistoryPage{Messages: historyFixture(0, 1)}}
	applyNext(t, callbacks)
	if !a.threadReading || a.threadReconcilePending || len(a.threadMessages) != 2 {
		t.Fatal("thread reconnect did not preserve live data and queue a fresh snapshot")
	}
	second := <-b.requests
	second.result <- chat.ThreadHistory{Page: chat.HistoryPage{Messages: historyFixture(0, 2)}}
	applyNext(t, callbacks)
	if a.threadReading || len(a.threadMessages) != 2 {
		t.Fatal("thread did not settle after reconnect reconciliation")
	}
}

func TestEqualTimeArrivalMarksEvictedNewerHistory(t *testing.T) {
	for _, thread := range []bool{false, true} {
		a := New(nil, func(func()) {}, func() {}, true)
		v := a.historyView(thread)
		*v.messages, *v.expanded = historyFixture(0, retainedHistoryLimit), true
		m := historyFixture(retainedHistoryLimit-1, 1)[0]
		m.Name = "spaces/a/messages/same-time-new"
		a.appendIncoming(m, thread)
		if !*v.newer || len(*v.messages) != retainedHistoryLimit {
			t.Fatal("equal-time delivery silently disappeared at the retention limit")
		}
		a.Close()
	}
}
