package desktop

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

const retainedHistoryLimit = 300
const messageColumnMaxWidth = 1000

type historyBackend interface {
	History(context.Context, string, string, string) (chat.HistoryPage, error)
}

func unionSize(a, b []chat.Message) int {
	seen := make(map[string]struct{}, len(a)+len(b))
	for _, m := range a {
		seen[m.Name] = struct{}{}
	}
	for _, m := range b {
		seen[m.Name] = struct{}{}
	}
	return len(seen)
}

func (a *App) appendIncoming(m chat.Message, thread bool) {
	v := a.historyView(thread)
	*v.messages, *v.newer = appendWindow(*v.messages, m, *v.expanded, *v.newer)
}

func (a *App) loadEarlier(thread bool) {
	b, ok := a.backend.(historyBackend)
	if !ok || a.closed || a.closing {
		return
	}
	v := a.historyView(thread)
	space, name, token := a.selected, "", *v.next
	generation := *v.request
	if thread {
		name = a.threadName
	}
	first, _ := v.list.Visible()
	if *v.olderLoading || token == "" || *v.reading || (len(*v.messages) >= retainedHistoryLimit && first >= retainedHistoryLimit-chat.HistoryLimit-2) {
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	*v.olderCancel = cancel
	*v.olderLoading = true
	// Preserve the paging boundary from the moment the request starts, even
	// when live arrivals would otherwise trim the recent 60-message window.
	*v.expanded = true
	*v.error = ""
	go func() {
		defer cancel()
		page, err := b.History(ctx, space, name, token)
		a.dispatch(func() {
			if a.closed || a.selected != space || *v.request != generation || (thread && a.threadName != name) {
				return
			}
			*v.olderLoading = false
			if err != nil {
				*v.error = err.Error()
				a.invalidate()
				return
			}
			if page.Next == token {
				page.Next = ""
			}
			current := *v.messages
			merged := mergeRetained(current, page.Messages, true)
			anchor := a.historyAnchor(thread)
			if anchor != "" && !slices.ContainsFunc(merged, func(m chat.Message) bool { return m.Name == anchor }) &&
				!(thread && a.threadRoot != nil && a.threadRoot.Name == anchor) {
				// The user moved while this page was in flight. Keep the current
				// window and cursor; retry when they return to its top.
				a.invalidate()
				return
			}
			*v.newer = *v.newer || unionSize(current, page.Messages) > retainedHistoryLimit
			*v.messages, *v.next = merged, page.Next
			a.invalidate()
		})
	}()
}

func (a *App) historyAnchor(thread bool) string {
	messages, state := a.messages, &a.messageList
	if thread {
		messages, state = a.threadItems(), &a.threadList
	}
	first, _ := state.Visible()
	if first >= 0 && first < len(messages) {
		return messages[first].Name
	}
	return ""
}

func (a *App) latest(thread bool) {
	a.historyView(thread).reset()
	a.refreshHistory(thread)
}

func (a *App) historyControls(c *ui.Context, thread bool) {
	v := a.historyView(thread)
	token, loading, expanded, newer := *v.next, *v.olderLoading, *v.expanded, *v.newer
	refreshing := *v.reading
	state, reached := v.list, v.reached
	count := len(*v.messages)
	if thread {
		if a.threadRoot != nil && !slices.ContainsFunc(a.threadMessages, func(m chat.Message) bool { return m.Name == a.threadRoot.Name }) {
			count++
		}
	}
	first, last := state.Visible()
	atTop := last >= first && first == 0
	// If a page still fits the viewport, fill it automatically. Wait until
	// the list has laid out the added rows before considering another page.
	if state.AtEnd() && last < count-1 {
		atTop = false
	}
	if !atTop {
		*reached = false
	} else if !*reached && !loading && !refreshing && token != "" {
		// Load once per arrival at the top. The next frame may still report
		// the old visible range while prepended rows find their keyed anchor.
		// Staying here after an error must not start a retry loop either.
		*reached = true
		a.loadEarlier(thread)
		loading = *v.olderLoading
	}
	if !loading && !expanded && !newer {
		return
	}
	ui.Row(c).Padding(8, 28).Gap(12).AlignItems(ui.Center).Children(func() {
		text := ""
		if loading {
			text = "Loading earlier…"
		}
		if newer {
			text = "Newer messages available"
		}
		ui.Text(c, text).FontSize(11).TextColor(a.theme.TextMuted).Grow(1)
		if ui.Button(c.Key(fmt.Sprintf("latest-%t", thread)), "Latest").Clicked() {
			a.latest(thread)
		}
	})
}

func (a *App) messageListView(c *ui.Context, messages []chat.Message, state *ui.ListState) {
	ui.List(c, state, len(messages)).ItemKey(func(i int) any { return messages[i].Name }).Rows(func(row ui.ListRow) {
		i := row.Index
		m := messages[i]
		rc := row.Context
		ui.Column(rc).WidthPercent(100).Padding(10, 32).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Column(rc).WidthPercent(100).MaxWidth(messageColumnMaxWidth).Gap(10).Children(func() {
				if i == 0 || m.CreateTime.Local().Format("2006-01-02") != messages[i-1].CreateTime.Local().Format("2006-01-02") {
					ui.Text(rc, m.CreateTime.Local().Format("Monday, January 2")).FontSize(11).TextColor(a.theme.TextMuted)
				}
				a.message(rc.Key(m.Name), m)
			})
		})
	}).Grow(1).MinHeight(0).WidthPercent(100)
}
