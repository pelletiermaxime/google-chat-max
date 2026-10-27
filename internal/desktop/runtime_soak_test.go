package desktop

import (
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

// Explicit offline runtime check: native frames, repeated view/cache churn,
// duplicate deliveries, and bounded live/history state. No Google calls.
func TestBoundedHistoryRuntimeSoak(t *testing.T) {
	if os.Getenv("CHAT_MAX_SOAK_TEST") != "1" {
		t.Skip("set CHAT_MAX_SOAK_TEST=1 for the bounded native runtime soak")
	}
	a := New(nil, func(func()) {}, func() {}, true)
	defer a.Close()
	for i := range 20 {
		a.spaces = append(a.spaces, chat.Space{Name: fmt.Sprintf("spaces/soak-%02d", i), DisplayName: fmt.Sprintf("Room %02d", i), SpaceType: "SPACE"})
	}
	tt := ui.NewTester(a.View, 1180, 780)
	var warm, final runtime.MemStats
	started := time.Now()
	for cycle := range 240 {
		space := a.spaces[cycle%len(a.spaces)].Name
		a.selectSpace(space)
		if !a.historyLoaded {
			for i := range retainedHistoryLimit {
				m := chat.Message{Name: fmt.Sprintf("%s/messages/%04d", space, i), Text: "Synthetic history message for the native runtime check.", CreateTime: time.Unix(int64(i), 0), Sender: chat.User{DisplayName: "Fixture sender"}}
				a.messages = append(a.messages, m)
			}
			a.historyFetched, a.historyLoaded, a.historyExpanded = time.Now(), true, cycle%2 == 0
		}
		for i := range 10 {
			m := chat.Message{Name: fmt.Sprintf("%s/messages/live-%04d-%02d", space, cycle, i), Text: "Synthetic live arrival.", CreateTime: time.Unix(int64(10000+cycle*10+i), 0)}
			a.applyStreamEvent(chat.StreamEvent{Message: &m})
			m.Sender.DisplayName = "Resolved sender"
			a.applyStreamEvent(chat.StreamEvent{Message: &m})
		}
		other := a.spaces[(cycle+1)%len(a.spaces)].Name
		m := chat.Message{Name: fmt.Sprintf("%s/messages/inactive-%04d", other, cycle), Text: "Synthetic inactive arrival.", CreateTime: time.Unix(int64(20000+cycle), 0)}
		a.applyStreamEvent(chat.StreamEvent{Message: &m})
		tt.Frame()
		if len(a.historyCache) > conversationCacheLimit || len(a.messages) > retainedHistoryLimit || len(a.seenMessages) > 2048 || len(a.activity) > 1000 {
			t.Fatal("runtime state exceeded its bounds")
		}
		for _, entry := range a.historyCache {
			if len(entry.messages) > retainedHistoryLimit {
				t.Fatal("an inactive history window exceeded its bound")
			}
		}
		if cycle == 39 {
			runtime.GC()
			runtime.ReadMemStats(&warm)
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&final)
	t.Logf("240 native view/cache cycles, 2,400 live arrivals plus duplicates: %.2fs; retained heap after warmup %.2f MiB, final %.2f MiB; inactive views=%d, dedup=%d", time.Since(started).Seconds(), float64(warm.HeapAlloc)/(1<<20), float64(final.HeapAlloc)/(1<<20), len(a.historyCache), len(a.seenMessages))
	runtime.KeepAlive(tt)
	runtime.KeepAlive(a)
}
