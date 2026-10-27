package desktop

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

const draftImageBudget = 24 << 20

type imageClipboard interface {
	Formats() []transfer.Format
	ReadFormat(transfer.Format) ([]byte, error)
}

type attachmentSendBackend interface {
	UploadImage(context.Context, string, chat.ImageUpload) (chat.UploadedAttachment, error)
	SendAttached(context.Context, string, string, string, string, *chat.QuotedMessageMetadata, []chat.UploadedAttachment) (chat.Message, error)
}

type draftImage struct {
	upload      chat.ImageUpload
	preview     *ui.Bitmap
	fingerprint string
	bytes       int
	uploaded    *chat.UploadedAttachment
}

// OnPaste runs on the main thread. Copy the native clipboard representation
// there; decode and resize its pixels in a single bounded background job.
func (a *App) pasteComposerImage(d *draft) bool {
	if _, ok := a.backend.(attachmentSendBackend); !ok {
		return false
	}
	source := a.clipboard
	if source == nil {
		source = mygo.Clipboard
	}
	format, ok := transfer.PreferredFormat(source.Formats(), transfer.PNG, "image/jpeg", "image/gif", "image/webp")
	if !ok {
		return false
	} // Let normal text paste preserve selection/undo behavior.
	if a.closed || a.closing || d.Sending || d.Uncertain || d.Pasting {
		return true
	}
	if d.Image != nil {
		d.Error = "Remove the attached image before pasting another."
		return true
	}
	if a.pasteBusy {
		d.Error = "Wait for the current image paste to finish, then paste again."
		return true
	}
	a.pasteBusy, d.Pasting, d.Error = true, true, ""
	data, err := source.ReadFormat(format)
	if err != nil || len(data) == 0 || len(data) > chat.MaxImageUploadBytes {
		a.pasteBusy, d.Pasting = false, false
		d.Error = "Could not paste the image. Copy a PNG, JPEG, GIF, or WebP image up to 10 MiB and try again."
		return true
	}
	reserved := len(data) + 240*160*4
	if a.draftImageBytes+reserved > draftImageBudget {
		a.pasteBusy, d.Pasting = false, false
		d.Error = "Image draft limit reached. Send or remove an attached image first."
		return true
	}
	a.draftImageBytes += reserved
	go func() {
		upload, preview, err := chat.PrepareImageUpload(data)
		fingerprint := fmt.Sprintf("%x", sha256.Sum256(data))
		a.dispatch(func() {
			if a.closed {
				return
			}
			a.pasteBusy, d.Pasting = false, false
			a.draftImageBytes -= reserved
			if err != nil {
				d.Error = err.Error()
			} else {
				size := len(upload.Data) + preview.Bounds().Dx()*preview.Bounds().Dy()*4
				d.Image = &draftImage{upload: upload, preview: ui.NewBitmap(preview), fingerprint: fingerprint, bytes: size}
				a.draftImageBytes += size
			}
			a.invalidate()
		})
	}()
	return true
}

func (a *App) removeDraftImage(d *draft) {
	if d.Image == nil || d.Sending || d.Uncertain || a.closing {
		return
	}
	a.draftImageBytes -= d.Image.bytes
	d.Image, d.Error = nil, ""
	a.invalidate()
}

func (a *App) draftImageView(c *ui.Context, d *draft) {
	if d.Pasting {
		ui.Text(c, "Preparing image…").FontSize(12).TextColor(a.theme.TextMuted)
	}
	if d.Image == nil {
		return
	}
	img := d.Image
	w, h := img.preview.Size()
	ui.Row(c.Key("draft-image")).WidthPercent(100).Padding(8).Children(func() {
		ui.Box(c).Width(float32(w)).MaxWidthPercent(100).AspectRatio(float32(w) / float32(h)).Children(func() {
			ui.Image(c, img.preview).Fill().Radius(8).Label("Attached image preview")
			remove := ui.ButtonBase(c.Key("remove-image")).Attach(ui.AnchorTopRight, ui.AnchorTopRight).Top(4).Right(4).
				Size(24, 24).Radius(12).Center().Background(ui.RGBA(24, 24, 24, 0.9)).Border(1, a.theme.Border).
				Label("Remove image").Tooltip("Remove image").Disabled(d.Sending || d.Uncertain || a.closing)
			if remove.Hovered() || remove.FocusVisible() {
				remove.Background(a.theme.SurfacePressed)
			}
			remove.Children(func() { ui.Icon(c, cancelQuoteIcon).Size(14, 14).TextColor(a.theme.Text) })
			if remove.Clicked() {
				a.removeDraftImage(d)
			}
		})
	})
}
