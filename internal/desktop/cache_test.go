package desktop

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

func TestNativeSidebarRestoresCachedHistoryAndScrollWithoutRead(t *testing.T) {
	callbacks := make(chan func(), 8)
	b := controlledBackend{reads: make(chan pendingRead, 4)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	defer a.Close()
	a.backend = b
	a.spaces = []chat.Space{{Name: "spaces/a", DisplayName: "Cached chat", SpaceType: "SPACE"}, {Name: "spaces/b", DisplayName: "Other chat", SpaceType: "SPACE"}}
	a.selected = "spaces/a"
	a.messages = historyFixture(0, 120)
	a.historyNext, a.historyExpanded = "older-a", true
	a.historyFetched, a.historyLoaded = time.Now(), true
	tt := ui.NewTester(a.View, 1180, 800)
	tt.Frame()
	a.messageList.ScrollTo(50, ui.Start)
	tt.Frame()
	first, _ := a.messageList.Visible()
	anchor := a.messages[first].Name
	if err := tt.Click("Other chat"); err != nil {
		t.Fatal(err)
	}
	other := <-b.reads
	if err := tt.Click("Cached chat"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.selected != "spaces/a" || a.reading || len(b.reads) != 0 || len(a.messages) != 120 || a.historyNext != "older-a" || !a.historyExpanded {
		t.Fatal("returning through the sidebar did not restore cached history immediately without a request")
	}
	after, _ := a.messageList.Visible()
	if a.messages[after].Name != anchor {
		t.Fatalf("cached scroll anchor changed: %s -> %s", anchor, a.messages[after].Name)
	}
	other.done <- []chat.Message{{Name: "spaces/b/messages/late"}}
	applyNext(t, callbacks)
	if a.messages[after].Name != anchor {
		t.Fatal("late response replaced restored cache")
	}
}

func TestStaleCacheDisplaysImmediatelyAndRefreshPreservesLiveArrival(t *testing.T) {
	callbacks := make(chan func(), 8)
	b := controlledPages{Backend: Demo(), calls: make(chan pageCall, 4)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, false)
	defer a.Close()
	a.backend = b
	a.historyCache = map[string]cachedConversation{"spaces/a": {
		messages: historyFixture(60, 20), next: "cached-older", fetched: time.Now().Add(-time.Minute),
		list: ui.ListState{FollowEnd: true},
	}}
	a.selectSpace("spaces/a")
	if len(a.messages) != 20 || !a.reading || a.historyNext != "cached-older" {
		t.Fatal("stale cache must remain visible while the refresh is pending")
	}
	refresh := <-b.calls
	if refresh.token != "" {
		t.Fatal("cache revalidation must fetch the latest page")
	}
	incoming := historyFixture(80, 1)[0]
	a.applyStreamEvent(chat.StreamEvent{Message: &incoming})
	refresh.result <- chat.HistoryPage{Messages: historyFixture(60, 20), Next: "fresh-older"}
	applyNext(t, callbacks)
	if a.reading || len(a.messages) != 21 || a.messages[20].Name != incoming.Name || a.historyNext != "fresh-older" {
		t.Fatal("revalidation lost an incoming message or kept the old cursor")
	}
}

func TestCancelledRefreshKeepsCachedViewButRequiresRevalidation(t *testing.T) {
	callbacks := make(chan func(), 8)
	b := controlledBackend{reads: make(chan pendingRead, 4)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, false)
	defer a.Close()
	a.backend = b
	a.selected = "spaces/a"
	a.messages = historyFixture(60, 20)
	a.historyFetched, a.historyLoaded = time.Now(), true
	a.refreshMessages()
	canceled := <-b.reads
	a.selectSpace("spaces/b")
	other := <-b.reads
	a.selectSpace("spaces/a")
	if len(a.messages) != 20 || !a.reading || canceled.ctx.Err() == nil {
		t.Fatal("canceling refresh must keep the view and require revalidation on return")
	}
	refresh := <-b.reads
	canceled.done <- nil
	other.done <- nil
	applyNext(t, callbacks)
	applyNext(t, callbacks)
	refresh.done <- historyFixture(61, 20)
	applyNext(t, callbacks)
	if a.reading || len(a.messages) != 20 || a.messages[0].Name != historyFixture(61, 1)[0].Name {
		t.Fatal("return did not finish the canceled refresh with a current result")
	}
}

func TestCachedConversationReceivesLiveActivityAndReconnectInvalidation(t *testing.T) {
	callbacks := make(chan func(), 8)
	b := controlledBackend{reads: make(chan pendingRead, 4)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, false)
	defer a.Close()
	a.backend = b
	a.selected = "spaces/a"
	a.messages = historyFixture(60, 20)
	a.historyFetched, a.historyLoaded = time.Now(), true
	a.selectSpace("spaces/b")
	other := <-b.reads
	other.done <- nil
	applyNext(t, callbacks)
	incoming := historyFixture(80, 1)[0]
	a.applyStreamEvent(chat.StreamEvent{Message: &incoming})
	if a.activity["spaces/a"] != 1 {
		t.Fatal("cached live activity must retain its unread badge")
	}
	a.selectSpace("spaces/a")
	if a.reading || len(b.reads) != 0 || len(a.messages) != 21 || a.messages[20].Name != incoming.Name || a.activity["spaces/a"] != 0 {
		t.Fatal("returning did not show live-updated cache and clear its badge")
	}
	// Reconnect must invalidate inactive cache entries as well as refreshing
	// the selected conversation, even if they are within the freshness TTL.
	a.applyStreamEvent(chat.StreamEvent{Reconcile: true})
	selectedRefresh := <-b.reads
	selectedRefresh.done <- a.messages
	applyNext(t, callbacks)
	a.selectSpace("spaces/b")
	if !a.reading {
		t.Fatal("reconnect did not invalidate an inactive cached conversation")
	}
	background := <-b.reads
	background.done <- nil
	applyNext(t, callbacks)
	// Successfully loaded empty conversations are cached, too.
	a.selectSpace("spaces/a")
	a.selectSpace("spaces/b")
	if a.reading || len(b.reads) != 0 || len(a.messages) != 0 {
		t.Fatal("returning to an empty cached conversation performed another read")
	}
}

func TestConversationCacheEvictsLeastRecentlyVisitedAndKeepsBounds(t *testing.T) {
	a := New(nil, func(func()) {}, func() {}, true)
	defer a.Close()
	for i := 0; i <= conversationCacheLimit; i++ {
		a.selectSpace(fmt.Sprintf("spaces/%d", i))
		a.messages = historyFixture(0, retainedHistoryLimit)
		a.historyFetched, a.historyLoaded = time.Now(), true
		if len(a.historyCache) > conversationCacheLimit {
			t.Fatal("conversation cache exceeded its bound")
		}
	}
	// Restoring the oldest cached view must not evict it while saving the
	// view being left; eviction happens after that transfer.
	a.selectSpace("spaces/0")
	if len(a.messages) != retainedHistoryLimit || len(a.historyCache) != conversationCacheLimit {
		t.Fatal("saving the current view evicted the requested cached view")
	}
	a.selectSpace("spaces/new")
	if len(a.historyCache) != conversationCacheLimit {
		t.Fatal("cache did not remain bounded")
	}
	if _, ok := a.historyCache["spaces/1"]; ok {
		t.Fatal("least recently visited conversation was not evicted")
	}
	if _, ok := a.historyCache["spaces/0"]; !ok {
		t.Fatal("recently visited conversation was evicted")
	}
}

func TestCachedLiveOverflowInvalidatesCursorAndKeepsExpandedWindowBounded(t *testing.T) {
	a := New(nil, func(func()) {}, func() {}, true)
	defer a.Close()
	a.historyCache = map[string]cachedConversation{"spaces/a": {
		messages: historyFixture(60, 60), next: "old-boundary", fetched: time.Now(),
	}}
	incoming := historyFixture(120, 1)[0]
	a.cacheIncoming("spaces/a", incoming)
	entry := a.historyCache["spaces/a"]
	if len(entry.messages) != 60 || !entry.fetched.IsZero() || entry.next != "" {
		t.Fatal("live overflow must invalidate the cursor before it can skip a dropped boundary")
	}
	entry.messages = historyFixture(0, retainedHistoryLimit)
	entry.expanded, entry.newerAvailable = true, false
	a.historyCache["spaces/a"] = entry
	delayed := historyFixture(299, 1)[0]
	delayed.Name = "spaces/a/messages/delayed"
	delayed.CreateTime = delayed.CreateTime.Add(-30 * time.Second)
	a.cacheIncoming("spaces/a", delayed)
	entry = a.historyCache["spaces/a"]
	if len(entry.messages) != retainedHistoryLimit || !entry.newerAvailable {
		t.Fatal("cached expanded history silently discarded newer history or exceeded its bound")
	}
}
