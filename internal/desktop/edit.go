package desktop

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

type editBackend interface {
	Edit(context.Context, string, string) (chat.Message, error)
}

// Keep one edit separate from conversation and thread drafts. Navigation can
// hide it without losing the text or changing a pending request's destination.
type messageEdit struct {
	original                chat.Message
	viewThread, text, error string
	saving, focus           bool
}

func (a *App) canEditMessage(m chat.Message) bool {
	_, ok := a.backend.(editBackend)
	return ok && !a.closed && !a.closing && a.edit == nil && a.selfUser != "" &&
		m.Sender.Name == a.selfUser && m.Text != "" && chat.MessageSpace(m.Name) == a.selected
}

func (a *App) editMessage(m chat.Message) {
	if !a.canEditMessage(m) {
		return
	}
	a.closeEmojiPicker()
	a.edit = &messageEdit{original: m, viewThread: a.threadName, text: m.Text, focus: true}
	a.invalidate()
}

func (a *App) cancelEdit() {
	if a.edit != nil && !a.edit.saving {
		a.edit = nil
		a.invalidate()
	}
}

func (a *App) saveEdit() {
	e := a.edit
	b, ok := a.backend.(editBackend)
	if !ok || a.closed || a.closing || e == nil || e.saving || strings.TrimSpace(e.text) == "" || e.text == e.original.Text {
		return
	}
	name, text := e.original.Name, e.text
	e.saving, e.error = true, ""
	a.pendingSends++
	a.sendWorkers.Add(1)
	go func() {
		defer a.sendWorkers.Done()
		ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
		defer cancel()
		m, err := b.Edit(ctx, name, text)
		a.dispatch(func() {
			if a.closed {
				return
			}
			a.pendingSends--
			e.saving = false
			if err != nil {
				e.error = err.Error()
			} else {
				m = mergeMessage(e.original, m)
				a.enrichDelivery(m)
				// A history read may have started before this edit completed.
				// Overlay the confirmed version until its snapshot is applied.
				if chat.MessageSpace(name) == a.selected {
					if a.reading {
						a.duringRead = mergeMessages(a.duringRead, []chat.Message{m})
					}
					if a.threadReading && m.Thread.Name == a.threadName {
						a.threadOverlay = mergeMessages(a.threadOverlay, []chat.Message{m})
					}
				}
				a.edit = nil
			}
			a.invalidate()
		})
	}()
}

func (a *App) editComposer(c *ui.Context) {
	e := a.edit
	ui.Column(c.Key("edit-"+e.original.Name)).WidthPercent(100).Padding(12, 32).AlignItems(ui.Center).Background(a.theme.Background).Children(func() {
		ui.Column(c).WidthPercent(100).MaxWidth(messageColumnMaxWidth).Gap(8).Padding(12).Radius(16).Background(a.theme.Surface).Children(func() {
			ui.Text(c, "Editing message").FontSize(12).Bold().TextColor(a.theme.TextMuted)
			input := ui.TextArea(c.Key("edit-text"), &e.text).Label("Edit message text").Height(96).MinHeight(64).Disabled(e.saving || a.closing)
			if e.focus && !e.saving && !a.closing {
				input.Focus()
				e.focus = false
			}
			ui.Row(c).WidthPercent(100).Justify(ui.End).Gap(8).Children(func() {
				if ui.Button(c.Key("cancel-edit"), "Cancel").Disabled(e.saving || a.closing).Clicked() || input.Shortcut(0, ui.KeyEscape) {
					a.cancelEdit()
				}
				label := "Save changes"
				if e.saving {
					label = "Saving…"
				}
				if ui.PrimaryButton(c.Key("save-edit"), label).Disabled(e.saving || a.closing || strings.TrimSpace(e.text) == "" || e.text == e.original.Text).Clicked() || input.Shortcut(ui.Ctrl, ui.KeyEnter) {
					a.saveEdit()
				}
			})
			if e.error != "" {
				ui.Text(c, e.error).FontSize(12).TextColor(a.theme.Danger)
			}
		})
	})
}
