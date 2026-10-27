package desktop

import (
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

var threadReplyIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15a4 4 0 0 1-4 4H7l-4 3V7a4 4 0 0 1 4-4h10a4 4 0 0 1 4 4z"/><path d="M8 8h8M8 12h5"/></svg>`))
var quoteReplyIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m9 17-5-5 5-5M20 18v-2a4 4 0 0 0-4-4H4"/></svg>`))
var cancelQuoteIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="m6 6 12 12M6 18 18 6"/></svg>`))
var editMessageIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m16 3 5 5-12 12-6 1 1-6zM14 5l5 5"/></svg>`))

func (a *App) canReplyInThread(m chat.Message) bool {
	_, ok := a.backend.(threadBackend)
	s := a.selectedSpace()
	return ok && !a.closed && !a.closing && chat.MessageSpace(m.Name) == a.selected && s.SpaceType == "SPACE" && s.SpaceThreadingState != "UNTHREADED_MESSAGES" && chat.ValidThread(a.selected, m.Thread.Name)
}

func (a *App) replyInThread(m chat.Message) {
	if !a.canReplyInThread(m) {
		return
	}
	if a.threadName != m.Thread.Name {
		a.openThread(m.Thread.Name)
		if !m.ThreadReply {
			copy := m
			a.threadRoot = &copy
		}
	}
	a.focusDraft = a.selected + "|" + m.Thread.Name
	a.invalidate()
}

func (a *App) quoteTarget(m chat.Message) (string, bool) {
	if chat.MessageSpace(m.Name) != a.selected || (m.CreateTime.IsZero() && m.LastUpdateTime.IsZero()) {
		return "", false
	}
	if a.threadName != "" {
		return a.threadName, m.Thread.Name == a.threadName
	}
	if m.ThreadReply {
		return m.Thread.Name, a.canReplyInThread(m)
	}
	return "", true
}

func (a *App) canQuoteMessage(m chat.Message) bool {
	if a.closed || a.closing {
		return false
	}
	if _, ok := a.backend.(quoteSendBackend); !ok {
		return false
	}
	if _, ok := a.backend.(sendBackend); !ok {
		return false
	}
	thread, ok := a.quoteTarget(m)
	if !ok {
		return false
	}
	d := a.drafts[a.selected+"|"+thread]
	return (d == nil && len(a.drafts) < 128) || (d != nil && !d.Sending && !d.Uncertain)
}

func (a *App) quoteMessage(m chat.Message) {
	if !a.canQuoteMessage(m) {
		return
	}
	thread, _ := a.quoteTarget(m)
	d := a.draftFor(a.selected + "|" + thread)
	if d == nil {
		return
	}
	version := m.LastUpdateTime
	if version.IsZero() {
		version = m.CreateTime
	}
	m.Sender = a.author(m)
	d.Quote = &chat.QuotedMessageMetadata{Name: m.Name, LastUpdateTime: version, QuotedMessageSnapshot: &chat.QuotedMessageSnapshot{Sender: m.Author(), Text: quoteSummary(m)}}
	d.Error = ""
	if thread != "" && thread != a.threadName {
		a.replyInThread(m)
	}
	a.focusDraft = a.selected + "|" + thread
	a.invalidate()
}

func quoteSummary(m chat.Message) string {
	text := m.Text
	if text == "" {
		text = m.FallbackText
	}
	if text == "" && len(m.CardsV2) > 0 {
		for _, line := range cardLines(m.CardsV2[0].Card) {
			text += line.text + "\n"
		}
	}
	if text == "" && len(m.Cards) > 0 {
		for _, line := range cardLines(m.Cards[0]) {
			text += line.text + "\n"
		}
	}
	if text == "" && len(m.Attachment) > 0 {
		text = "Attachment · " + m.Attachment[0].ContentName
	}
	if text == "" && len(m.AttachedGIFs) > 0 {
		text = "GIF"
	}
	if text == "" {
		text = "Message"
	}
	if utf8.RuneCountInString(text) > 600 {
		text = string([]rune(text)[:600]) + "…"
	}
	return strings.TrimSpace(text)
}

func (a *App) quotePreview(c *ui.Context, quote *chat.QuotedMessageMetadata, remove func()) {
	author, text := "Quoted message", "Original message"
	if snapshot := quote.QuotedMessageSnapshot; snapshot != nil {
		if snapshot.Sender != "" {
			author = snapshot.Sender
		}
		if snapshot.Text != "" {
			text = snapshot.Text
		}
	} else {
		messages := a.messages
		if a.threadName != "" {
			messages = a.threadMessages
		}
		for _, m := range messages {
			if m.Name == quote.Name {
				m.Sender = a.author(m)
				author, text = m.Author(), quoteSummary(m)
				break
			}
		}
		if a.threadRoot != nil && a.threadRoot.Name == quote.Name {
			m := *a.threadRoot
			m.Sender = a.author(m)
			author, text = m.Author(), quoteSummary(m)
		}
	}
	if utf8.RuneCountInString(author) > 100 {
		author = string([]rune(author)[:100]) + "…"
	}
	if utf8.RuneCountInString(text) > 600 {
		text = string([]rune(text)[:600]) + "…"
	}
	ui.Row(c).WidthPercent(100).MinWidth(0).Gap(10).Padding(10, 12).BorderWidth(0, 0, 0, 3).BorderColor(a.theme.TextMuted).Radius(6).Background(a.theme.SurfacePressed).AlignItems(ui.Start).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, author).FontSize(11).Bold().SingleLine()
			ui.Text(c, text).FontSize(12).TextColor(a.theme.TextMuted).MaxLines(3).Selectable()
		})
		if remove != nil {
			button := ui.ButtonBase(c).Label("Cancel quote").Tooltip("Cancel quote").Size(24, 24).Radius(12).Center()
			if button.Hovered() {
				button.Background(a.theme.SurfaceHover)
			}
			button.Children(func() { ui.Icon(c, cancelQuoteIcon).Size(14, 14) })
			if button.Clicked() {
				remove()
			}
		}
	})
}

func (a *App) messageActions(c *ui.Context, m chat.Message) {
	at, self := ui.AnchorTopLeft, ui.AnchorBottomLeft
	if a.selfUser != "" && m.Sender.Name == a.selfUser {
		at, self = ui.AnchorTopRight, ui.AnchorBottomRight
	}
	ui.Row(c.Key("message-actions")).Attach(at, self).Top(8).Padding(3).Gap(2).Radius(12).Border(1, a.theme.Border).Background(a.theme.Surface).FocusGroup(ui.Horizontal).Children(func() {
		a.emojiActions(c, m)
		if a.canReplyInThread(m) {
			if actionIcon(c.Key("reply-thread"), "Reply in thread", threadReplyIcon, a.theme, false) {
				a.replyInThread(m)
			}
		}
		if actionIcon(c.Key("quote-reply"), "Quote reply", quoteReplyIcon, a.theme, !a.canQuoteMessage(m)) {
			a.quoteMessage(m)
		}
		if a.canEditMessage(m) && actionIcon(c.Key("edit-message"), "Edit message", editMessageIcon, a.theme, false) {
			a.editMessage(m)
		}
	})
}

func actionIcon(c *ui.Context, label string, icon *ui.SVG, theme *ui.Theme, disabled bool) bool {
	button := ui.ButtonBase(c).Label(label).Tooltip(label).Size(32, 32).Radius(8).Center().Disabled(disabled)
	if button.Hovered() || button.FocusVisible() {
		button.Background(theme.SurfaceHover)
	}
	button.Children(func() { ui.Icon(c, icon).Size(19, 19) })
	return button.Clicked()
}

func (a *App) messageMenu(menu *ui.Menu, m chat.Message, edit bool) {
	if a.canReplyInThread(m) && menu.Item("Reply in thread").Chosen() {
		a.replyInThread(m)
	}
	if menu.Item("Quote reply").Disabled(!a.canQuoteMessage(m)).Chosen() {
		a.quoteMessage(m)
	}
	if a.canEditMessage(m) && menu.Item("Edit message").Chosen() {
		a.editMessage(m)
	}
	if edit {
		menu.Separator()
		menu.EditItems()
	}
}
