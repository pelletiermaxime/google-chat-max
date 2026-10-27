package desktop

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

// Both views share one lifecycle. Bind their existing fields here so unrelated
// message rendering and actions do not depend on the request implementation.
type historyView struct {
	messages, overlay                *[]chat.Message
	next, error                      *string
	list                             *ui.ListState
	reading, olderLoading, expanded  *bool
	newer, reached, reconcilePending *bool
	request                          *uint64
	cancel, olderCancel              *context.CancelFunc
}

func (a *App) historyView(thread bool) historyView {
	if thread {
		return historyView{
			messages: &a.threadMessages, overlay: &a.threadOverlay, next: &a.threadNext, error: &a.threadError,
			list: &a.threadList, reading: &a.threadReading, olderLoading: &a.threadOlderLoading,
			expanded: &a.threadExpanded, newer: &a.threadNewerAvailable, reached: &a.threadTopReached,
			reconcilePending: &a.threadReconcilePending, request: &a.threadRequest,
			cancel: &a.threadCancel, olderCancel: &a.threadOlderCancel,
		}
	}
	return historyView{
		messages: &a.messages, overlay: &a.duringRead, next: &a.historyNext, error: &a.messageError,
		list: &a.messageList, reading: &a.reading, olderLoading: &a.olderLoading,
		expanded: &a.historyExpanded, newer: &a.newerAvailable, reached: &a.historyTopReached,
		reconcilePending: &a.reconcilePending, request: &a.request, cancel: &a.readCancel, olderCancel: &a.olderCancel,
	}
}

func (v historyView) cancelReads() {
	if *v.cancel != nil {
		(*v.cancel)()
		*v.cancel = nil
	}
	if *v.olderCancel != nil {
		(*v.olderCancel)()
		*v.olderCancel = nil
	}
	*v.olderLoading = false
}

func (v historyView) reset() {
	v.cancelReads()
	*v.request++
	*v.messages, *v.overlay = nil, nil
	*v.next, *v.error = "", ""
	*v.reading, *v.expanded, *v.newer, *v.reached, *v.reconcilePending = false, false, false, false, false
	*v.list = ui.ListState{FollowEnd: true}
}

func (v historyView) acceptRecent(page chat.HistoryPage) {
	incoming := mergeMessages(page.Messages, *v.overlay)
	if *v.expanded {
		if !*v.newer {
			*v.newer = unionSize(*v.messages, incoming) > retainedHistoryLimit
			*v.messages = mergeRetained(*v.messages, incoming, true)
		}
	} else {
		*v.messages = incoming
		*v.next = page.Next
	}
	*v.overlay = nil
}

func (a *App) refreshHistory(thread bool) {
	if a.backend == nil || a.selected == "" || a.closed || (thread && a.threadName == "") {
		return
	}
	v := a.historyView(thread)
	b, hasThread := a.backend.(threadBackend)
	if thread && !hasThread {
		*v.error = "Thread history is unavailable"
		return
	}
	v.cancelReads()
	*v.request++
	id, space, name, backend := *v.request, a.selected, "", a.backend
	if thread {
		name = a.threadName
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	*v.cancel, *v.reading, *v.error = cancel, true, ""
	if !thread {
		a.refreshAuthors(space, id)
	}
	go func() {
		defer cancel()
		var result chat.ThreadHistory
		var err error
		if thread {
			result, err = b.Thread(ctx, space, name)
		} else if b, ok := backend.(historyBackend); ok {
			result.Page, err = b.History(ctx, space, "", "")
		} else {
			result.Page.Messages, err = backend.Messages(ctx, space)
		}
		a.dispatch(func() {
			if a.closed || a.selected != space || *v.request != id || (thread && a.threadName != name) {
				return
			}
			*v.reading = false
			if err != nil {
				*v.error = err.Error()
			} else {
				if thread {
					a.threadRoot = result.Root
					if a.threadRoot != nil {
						for _, m := range *v.overlay {
							if m.Name == a.threadRoot.Name {
								merged := mergeMessage(*a.threadRoot, m)
								a.threadRoot = &merged
							}
						}
					}
				} else {
					a.historyFetched, a.historyLoaded = time.Now(), true
					a.updated = time.Now()
				}
				v.acceptRecent(result.Page)
				for _, m := range result.Page.Messages {
					if m.Sender.Name != "" && m.Sender.DisplayName != "" {
						a.rememberUsers(chat.Space{Participants: []chat.User{m.Sender}})
					}
				}
			}
			// Keep live arrivals after a failed snapshot, too: a retry must
			// not erase messages received since the previous successful read.
			if *v.reconcilePending {
				*v.reconcilePending = false
				a.refreshHistory(thread)
			}
			a.invalidate()
		})
	}()
}
