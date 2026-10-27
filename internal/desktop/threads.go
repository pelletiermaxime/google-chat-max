package desktop

import (
	"context"
	"fmt"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

type threadBackend interface {
	Thread(context.Context, string, string) (chat.ThreadHistory, error)
}

func (a *App) threadControl(m chat.Message) (string, bool) {
	s := a.selectedSpace()
	if a.threadName != "" || s.SpaceType != "SPACE" || s.SpaceThreadingState == "UNTHREADED_MESSAGES" || !chat.ValidThread(a.selected, m.Thread.Name) {
		return "", false
	}
	if m.ThreadReply {
		return "View thread", true
	}
	count := 0
	for _, reply := range a.messages {
		if reply.ThreadReply && reply.Thread.Name == m.Thread.Name {
			count++
		}
	}
	if count == 0 && a.threadActivity[m.Thread.Name] == 0 {
		return "", false
	}
	if count == 1 {
		return "1 reply", true
	}
	if count > 1 {
		return fmt.Sprintf("%d replies", count), true
	}
	return "New replies", true
}

func (a *App) closeThread() {
	a.closeEmojiPicker()
	a.historyView(true).reset()
	a.threadName = ""
	a.threadRoot = nil
}

func (a *App) openThread(name string) {
	if !chat.ValidThread(a.selected, name) {
		return
	}
	a.closeThread()
	a.threadName = name
	delete(a.threadActivity, name)
	a.refreshThread()
}

func (a *App) refreshThread() {
	a.refreshHistory(true)
}

func (a *App) routeThreadEvent(m chat.Message) {
	name := m.Thread.Name
	if name == "" {
		return
	}
	if name == a.threadName {
		if a.threadReading {
			a.threadOverlay = mergeMessages(a.threadOverlay, []chat.Message{m})
		}
		a.appendIncoming(m, true)
		if !m.ThreadReply {
			root := m
			a.threadRoot = &root
		}
	} else if m.ThreadReply && (len(a.threadActivity) < 256 || a.threadActivity[name] > 0) {
		a.threadActivity[name] = min(999, a.threadActivity[name]+1)
	}
}

func (a *App) threadView(c *ui.Context) {
	t := a.theme
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Padding(20, 28).Gap(16).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, "Back to conversation").Clicked() {
				a.closeThread()
			}
			ui.Text(c, "Thread").FontSize(24).Bold().Grow(1)
			if ui.Button(c, "Refresh thread").Disabled(a.threadReading).Clicked() {
				a.refreshThread()
			}
		})
		if a.threadName == "" {
			return
		}
		if a.threadError != "" {
			ui.Text(c, a.threadError).TextColor(t.Danger).Padding(16)
		}
		a.historyControls(c, true)
		ui.Text(c, "ORIGINAL MESSAGE AND REPLIES").Padding(8, 32).FontSize(11).TextColor(t.Accent)
		if a.threadRoot == nil {
			ui.Text(c, "Original message unavailable").Padding(8, 32).TextColor(t.TextMuted)
		}
		if a.threadReading {
			ui.Text(c, "Loading thread…").Padding(8, 32).TextColor(t.TextMuted)
		}
		a.messageListView(c.Key("thread-history-"+a.threadName), a.threadItems(), &a.threadList)
		a.composer(c, a.threadName)
	})
}

func (a *App) threadItems() []chat.Message {
	items := make([]chat.Message, 0, len(a.threadMessages)+1)
	if a.threadRoot != nil {
		items = append(items, *a.threadRoot)
	}
	for _, m := range a.threadMessages {
		if a.threadRoot == nil || m.Name != a.threadRoot.Name {
			items = append(items, m)
		}
	}
	return items
}
