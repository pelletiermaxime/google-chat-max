package desktop

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

var sendMessageIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M12 19V5M5 12l7-7 7 7"/></svg>`))
var retryMessageIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 7v5h-5M20 12a8 8 0 1 0-2 5M20 12l-3-5"/></svg>`))

type sendBackend interface {
	Send(context.Context, string, string, string, string) (chat.Message, error)
}
type quoteSendBackend interface {
	SendQuoted(context.Context, string, string, string, string, chat.QuotedMessageMetadata) (chat.Message, error)
}
type draft struct {
	Text, requestID, snapshot, Error string
	Quote                            *chat.QuotedMessageMetadata
	Image                            *draftImage
	Pasting                          bool
	snapshotQuote                    string
	snapshotImage                    string
	Sending, Uncertain               bool
}

func (a *App) draftFor(key string) *draft {
	if d := a.drafts[key]; d != nil {
		return d
	}
	if len(a.drafts) >= 128 {
		for k, d := range a.drafts {
			if !d.Sending && !d.Uncertain && !d.Pasting && d.Text == "" && d.Quote == nil && d.Image == nil {
				delete(a.drafts, k)
				break
			}
		}
	}
	if len(a.drafts) >= 128 {
		return nil
	}
	d := &draft{}
	a.drafts[key] = d
	return d
}

func (a *App) submitDraft(space, thread string) {
	if a.closed || a.closing || space == "" {
		return
	}
	b, ok := a.backend.(sendBackend)
	if !ok {
		return
	}
	d := a.draftFor(space + "|" + thread)
	if d == nil || d.Sending || d.Pasting || (strings.TrimSpace(d.Text) == "" && d.Image == nil) {
		return
	}
	image := d.Image
	imageKey := ""
	ib, canAttach := a.backend.(attachmentSendBackend)
	var uploaded *chat.UploadedAttachment
	if image != nil {
		if !canAttach {
			d.Error = "Image attachments are unavailable"
			return
		}
		imageKey = image.fingerprint
		if image.uploaded != nil {
			copy := *image.uploaded
			uploaded = &copy
		}
	}
	quoteKey := ""
	var quote *chat.QuotedMessageMetadata
	qb, canQuote := a.backend.(quoteSendBackend)
	if d.Quote != nil {
		if !canQuote && image == nil {
			d.Error = "Quoted replies are unavailable"
			return
		}
		copy := *d.Quote
		quote = &copy
		quoteKey = copy.Name + "|" + copy.LastUpdateTime.Format(time.RFC3339Nano)
	}
	if d.Uncertain && (d.snapshot != d.Text || d.snapshotQuote != quoteKey || d.snapshotImage != imageKey) {
		return
	}
	if d.requestID == "" || d.snapshot != d.Text || d.snapshotQuote != quoteKey || d.snapshotImage != imageKey {
		var err error
		d.requestID, err = chat.NewRequestID()
		if err != nil {
			d.Error = "Could not prepare the send request"
			return
		}
		d.snapshot = d.Text
		d.snapshotQuote = quoteKey
		d.snapshotImage = imageKey
	}
	text, id := d.snapshot, d.requestID
	d.Sending = true
	d.Error = ""
	a.pendingSends++
	a.sendWorkers.Add(1)
	go func() {
		defer a.sendWorkers.Done()
		timeout := 30 * time.Second
		if image != nil {
			timeout = 60 * time.Second
		}
		ctx, cancel := context.WithTimeout(a.ctx, timeout)
		defer cancel()
		var m chat.Message
		var err error
		if image != nil {
			if uploaded == nil {
				var ref chat.UploadedAttachment
				ref, err = ib.UploadImage(ctx, space, image.upload)
				if err == nil {
					uploaded = &ref
				}
			}
			if err == nil {
				m, err = ib.SendAttached(ctx, space, thread, text, id, quote, []chat.UploadedAttachment{*uploaded})
			}
		} else if quote != nil {
			m, err = qb.SendQuoted(ctx, space, thread, text, id, *quote)
		} else {
			m, err = b.Send(ctx, space, thread, text, id)
		}
		a.dispatch(func() {
			if a.closed {
				return
			}
			a.pendingSends--
			d.Sending = false
			if image != nil {
				image.uploaded = uploaded
			}
			if err != nil {
				d.Error = err.Error()
				var e *chat.SendError
				d.Uncertain = errors.As(err, &e) && e.Uncertain
			} else {
				d.Text = ""
				d.requestID = ""
				d.snapshot = ""
				d.snapshotQuote = ""
				d.snapshotImage = ""
				d.Quote = nil
				if d.Image != nil {
					a.draftImageBytes -= d.Image.bytes
					d.Image = nil
				}
				d.Uncertain = false
				a.applyStreamEvent(chat.StreamEvent{Message: &m})
				if a.selected == space {
					if thread != "" && thread == a.threadName && a.threadNewerAvailable {
						a.latest(true)
					} else if thread == "" && a.newerAvailable {
						a.latest(false)
					}
				}
			}
			a.invalidate()
		})
	}()
}

func (a *App) composer(c *ui.Context, thread string) {
	space := a.selected
	if space == "" {
		return
	}
	if a.edit != nil && chat.MessageSpace(a.edit.original.Name) == space && a.edit.viewThread == thread {
		a.editComposer(c)
		return
	}
	d := a.draftFor(space + "|" + thread)
	ui.Column(c.Key("composer-"+space+"|"+thread)).WidthPercent(100).Padding(12, 32).AlignItems(ui.Center).Background(a.theme.Background).Children(func() {
		ui.Column(c).WidthPercent(100).MaxWidth(messageColumnMaxWidth).Gap(7).Children(func() {
			if d == nil {
				ui.Text(c, "Draft limit reached. Finish an existing draft first.").TextColor(a.theme.Danger)
				return
			}
			ui.Column(c).WidthPercent(100).Label("Message composer").Padding(10, 12).Gap(4).Radius(20).Background(a.theme.Surface).Children(func() {
				a.draftImageView(c, d)
				if d.Quote != nil {
					var remove func()
					if !d.Sending && !d.Uncertain && !a.closing {
						remove = func() { d.Quote = nil }
					}
					a.quotePreview(c, d.Quote, remove)
				}
				input := ui.TextArea(c.Key("draft-"+space+"|"+thread), &d.Text).Label("Message draft").Placeholder("Message…").Height(64).MinHeight(64).
					Background(ui.Transparent).Border(0, ui.Transparent).FocusRing(false).Disabled(d.Sending || d.Uncertain || a.closing).
					OnPaste(func(string) bool { return a.pasteComposerImage(d) }).
					OnTextKey(func(k ui.TextKey) bool {
						if k.Key != ui.KeyEnter || k.Mods != 0 {
							return false
						}
						// Read the editor's current text even if the last keystroke
						// has not reached the frame's bound draft value yet.
						d.Text = k.Text()
						a.submitDraft(space, thread)
						return true
					})
				if a.focusDraft == space+"|"+thread && !d.Sending && !d.Uncertain && !a.closing {
					input.Focus()
					a.focusDraft = ""
				}
				if len(d.Text) > 28000 {
					cut := 28000
					for cut > 0 && !utf8.RuneStart(d.Text[cut]) {
						cut--
					}
					d.Text = d.Text[:cut]
					d.Error = "Draft limited to 28000 bytes to fit Google's message limit."
				}
				_, canSend := a.backend.(sendBackend)
				ui.Row(c).WidthPercent(100).Justify(ui.End).Children(func() {
					label, icon := "Send", sendMessageIcon
					if thread != "" {
						label = "Send reply"
					}
					if d.Uncertain {
						label, icon = "Retry safely", retryMessageIcon
					}
					if d.Sending {
						label = "Sending…"
					}
					send := ui.PrimaryButton(c.Key("send-"+space+"|"+thread), "").Label(label).Tooltip(label).
						Size(34, 34).Padding(0).Radius(17).Disabled(!canSend || d.Sending || d.Pasting || a.closing || (strings.TrimSpace(d.Text) == "" && d.Image == nil))
					send.Children(func() { ui.Icon(c, icon).FontSize(20) })
					if send.Clicked() {
						a.submitDraft(space, thread)
					}
				})
			})
			if d.Error != "" {
				ui.Text(c, d.Error).FontSize(12).TextColor(a.theme.Danger)
			}
			if d.Uncertain {
				ui.Text(c, "This message may already have arrived. Retry safely confirms the same request.").FontSize(11).TextColor(a.theme.Warning)
				if ui.Button(c, "Edit as a new message").Clicked() {
					d.Uncertain = false
					d.requestID = ""
					d.Error = "Previous delivery is still uncertain; sending again will create a new request."
				}
			}
		})
	})
}
