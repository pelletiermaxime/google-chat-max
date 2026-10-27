package desktop

import (
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

const conversationCacheLimit = 12
const conversationCacheFreshness = 30 * time.Second

// Only inactive conversations live here. Selection transfers the saved view
// back to App, so its ListState is never shared by two active lists.
type cachedConversation struct {
	messages                          []chat.Message
	next                              string
	list                              ui.ListState
	expanded, newerAvailable, reached bool
	updated, fetched                  time.Time
	used                              uint64
}

func (a *App) cacheConversation() {
	if a.selected == "" || !a.historyLoaded {
		return
	}
	if a.historyCache == nil {
		a.historyCache = make(map[string]cachedConversation)
	}
	fetched := a.historyFetched
	if a.reading {
		// A canceled explicit refresh still needs revalidation on return.
		fetched = time.Time{}
	}
	a.cacheClock++
	a.historyCache[a.selected] = cachedConversation{
		messages: a.messages, next: a.historyNext, list: a.messageList,
		expanded: a.historyExpanded, newerAvailable: a.newerAvailable,
		reached: a.historyTopReached && !a.olderLoading,
		updated: a.updated, fetched: fetched, used: a.cacheClock,
	}
}

func (a *App) trimConversationCache() {
	if len(a.historyCache) > conversationCacheLimit {
		oldest := ""
		for name, entry := range a.historyCache {
			if oldest == "" || entry.used < a.historyCache[oldest].used {
				oldest = name
			}
		}
		delete(a.historyCache, oldest)
	}
}

// Restore even a stale entry before fetching. The return value reports whether
// it is fresh enough to skip the network request, not whether it was found.
func (a *App) restoreConversation(name string) bool {
	entry, ok := a.historyCache[name]
	if !ok {
		return false
	}
	delete(a.historyCache, name)
	a.messages, a.historyNext, a.messageList = entry.messages, entry.next, entry.list
	a.historyExpanded, a.newerAvailable, a.historyTopReached = entry.expanded, entry.newerAvailable, entry.reached
	a.updated, a.historyFetched, a.historyLoaded = entry.updated, entry.fetched, true
	return !entry.fetched.IsZero() && time.Since(entry.fetched) < conversationCacheFreshness
}

func (a *App) invalidateConversationCache() {
	a.historyFetched = time.Time{}
	for name, entry := range a.historyCache {
		entry.fetched = time.Time{}
		a.historyCache[name] = entry
	}
}

func (a *App) cacheIncoming(space string, m chat.Message) {
	entry, ok := a.historyCache[space]
	if !ok {
		return
	}
	if !entry.expanded {
		if unionSize(entry.messages, []chat.Message{m}) > chat.HistoryLimit {
			// Trimming the boundary invalidates the older cursor. Revalidate
			// on selection before allowing the view to page past that gap.
			entry.fetched, entry.next = time.Time{}, ""
		}
	}
	entry.messages, entry.newerAvailable = appendWindow(entry.messages, m, entry.expanded, entry.newerAvailable)
	entry.updated = time.Now()
	a.historyCache[space] = entry
}
