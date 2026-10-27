package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

type fakeImageClipboard struct {
	data    []byte
	formats []transfer.Format
	reads   int
}

func (c *fakeImageClipboard) Formats() []transfer.Format { return c.formats }
func (c *fakeImageClipboard) ReadFormat(transfer.Format) ([]byte, error) {
	c.reads++
	return c.data, nil
}

type uploadImageResult struct {
	attachment chat.UploadedAttachment
	err        error
}
type uploadImageCall struct {
	space string
	image chat.ImageUpload
	done  chan uploadImageResult
}
type attachedSendCall struct {
	space, thread, text, id string
	quote                   *chat.QuotedMessageMetadata
	attachments             []chat.UploadedAttachment
	done                    chan sendResult
}
type controlledAttachments struct {
	demoBackend
	uploads chan uploadImageCall
	sends   chan attachedSendCall
}

func (b controlledAttachments) UploadImage(ctx context.Context, space string, image chat.ImageUpload) (chat.UploadedAttachment, error) {
	call := uploadImageCall{space: space, image: image, done: make(chan uploadImageResult, 1)}
	select {
	case b.uploads <- call:
	case <-ctx.Done():
		return chat.UploadedAttachment{}, ctx.Err()
	}
	select {
	case result := <-call.done:
		return result.attachment, result.err
	case <-ctx.Done():
		return chat.UploadedAttachment{}, ctx.Err()
	}
}
func (b controlledAttachments) SendAttached(ctx context.Context, space, thread, text, id string, quote *chat.QuotedMessageMetadata, attachments []chat.UploadedAttachment) (chat.Message, error) {
	call := attachedSendCall{space: space, thread: thread, text: text, id: id, quote: quote, attachments: attachments, done: make(chan sendResult, 1)}
	select {
	case b.sends <- call:
	case <-ctx.Done():
		return chat.Message{}, ctx.Err()
	}
	select {
	case result := <-call.done:
		return result.m, result.err
	case <-ctx.Done():
		return chat.Message{}, ctx.Err()
	}
}
func nextUploadImage(t *testing.T, b controlledAttachments) uploadImageCall {
	t.Helper()
	select {
	case call := <-b.uploads:
		return call
	case <-time.After(time.Second):
		t.Fatal("image upload did not start")
		return uploadImageCall{}
	}
}
func nextAttachedSend(t *testing.T, b controlledAttachments) attachedSendCall {
	t.Helper()
	select {
	case call := <-b.sends:
		return call
	case <-time.After(time.Second):
		t.Fatal("attachment send did not start")
		return attachedSendCall{}
	}
}

func composerImageFixture(t *testing.T, width int, thread string) (*App, controlledAttachments, *fakeImageClipboard, chan func(), *ui.Tester, *draft) {
	t.Helper()
	callbacks := make(chan func(), 16)
	b := controlledAttachments{uploads: make(chan uploadImageCall, 2), sends: make(chan attachedSendCall, 2)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	a.backend, a.selected, a.threadName = b, "spaces/a", thread
	a.spaces = []chat.Space{{Name: a.selected, DisplayName: "Image composer", SpaceType: "SPACE"}}
	var data bytes.Buffer
	if err := png.Encode(&data, previewPattern()); err != nil {
		t.Fatal(err)
	}
	clipboard := &fakeImageClipboard{data: data.Bytes(), formats: []transfer.Format{transfer.PNG, transfer.Text}}
	a.clipboard = clipboard
	d := a.draftFor(a.selected + "|" + thread)
	tt := ui.NewTester(a.View, width, 900)
	return a, b, clipboard, callbacks, tt, d
}

func pasteImageFixture(t *testing.T, tt *ui.Tester, callbacks chan func()) {
	t.Helper()
	if err := tt.Click("Message draft"); err != nil {
		t.Fatal(err)
	}
	tt.TypeKey(ui.Ctrl, ui.KeyV, "")
	applyNext(t, callbacks)
	tt.Frame()
}

func TestNativeComposerPastesPreviewsAndRemovesImageWithoutUpload(t *testing.T) {
	for _, width := range []int{760, 1180, 1920} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a, b, clipboard, callbacks, tt, d := composerImageFixture(t, width, "")
			defer a.Close()
			d.Text = "Existing caption"
			tt.Frame()
			pasteImageFixture(t, tt, callbacks)
			if d.Image == nil || d.Text != "Existing caption" || clipboard.reads != 1 || !tt.HasText("Attached image preview") || !tt.HasText("Remove image") || len(b.uploads) != 0 || len(b.sends) != 0 {
				t.Fatal("image paste changed caption, lacked a preview, or performed network work")
			}
			rect, ok := tt.Find("Attached image preview")
			if !ok || rect.W > 241 || rect.H > 161 || rect.X < 303 || rect.X+rect.W > float32(width) {
				t.Fatalf("image preview exceeded composer bounds: %+v", rect)
			}
			remove, ok := tt.Find("Remove image")
			if !ok || remove.W > 24 || remove.H > 24 || remove.X < rect.X+rect.W-29 || remove.X+remove.W > rect.X+rect.W || remove.Y < rect.Y || remove.Y+remove.H > rect.Y+29 || tt.HasText("pasted-image.png") {
				t.Fatalf("remove control is not a compact corner button, or filename is still visible: %+v", remove)
			}
			if path := os.Getenv("CHAT_MAX_PASTE_SCREENSHOT"); path != "" && width == 1180 {
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(f, tt.Image())
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := tt.Click("Remove image"); err != nil {
				t.Fatal(err)
			}
			if d.Image != nil || d.Text != "Existing caption" || a.draftImageBytes != 0 || len(b.uploads) != 0 {
				t.Fatal("removing the image lost text, retained image memory, or uploaded data")
			}
			clipboard.formats = []transfer.Format{transfer.Text}
			tt.SetClipboard(" text paste")
			if err := tt.Click("Message draft"); err != nil {
				t.Fatal(err)
			}
			tt.TypeKey(ui.Ctrl, ui.KeyV, "")
			if !bytes.Contains([]byte(d.Text), []byte("text paste")) || clipboard.reads != 1 {
				t.Fatal("normal text paste was intercepted as an image")
			}
		})
	}
}

func TestComposerImageOnlyAndQuotedThreadRetryReuseUpload(t *testing.T) {
	for _, thread := range []string{"", "spaces/a/threads/t"} {
		t.Run(thread, func(t *testing.T) {
			a, b, _, callbacks, tt, d := composerImageFixture(t, 1180, thread)
			defer a.Close()
			if thread != "" {
				d.Text = "Caption"
				d.Quote = &chat.QuotedMessageMetadata{Name: "spaces/a/messages/source", LastUpdateTime: time.Now()}
			}
			pasteImageFixture(t, tt, callbacks)
			image := d.Image
			// Enter also sends an image-only draft; pasting itself never sends.
			tt.TypeKey(0, ui.KeyEnter, "")
			upload := nextUploadImage(t, b)
			if upload.space != "spaces/a" || !bytes.Equal(upload.image.Data, image.upload.Data) || len(b.sends) != 0 {
				t.Fatal("upload changed image data/destination or sent before uploading")
			}
			a.submitDraft(a.selected, thread)
			if len(b.uploads) != 0 {
				t.Fatal("pending image send started a second upload")
			}
			ref := chat.UploadedAttachment{Space: upload.space, Filename: upload.image.Filename, ContentType: upload.image.ContentType, DataRef: chat.AttachmentDataRef{AttachmentUploadToken: "test-upload"}}
			upload.done <- uploadImageResult{attachment: ref}
			first := nextAttachedSend(t, b)
			if first.thread != thread || first.text != d.Text || len(first.attachments) != 1 || (thread != "" && first.quote == nil) {
				t.Fatal("image send lost caption, quote, or thread")
			}
			first.done <- sendResult{err: &chat.SendError{Uncertain: true, Detail: "Uncertain delivery"}}
			applyNext(t, callbacks)
			tt.Frame()
			a.removeDraftImage(d)
			if !d.Uncertain || d.Image != image || image.uploaded == nil {
				t.Fatal("uncertain send lost its image or reusable upload")
			}
			if err := tt.Click("Retry safely"); err != nil {
				t.Fatal(err)
			}
			retry := nextAttachedSend(t, b)
			if len(b.uploads) != 0 || retry.id != first.id || retry.text != first.text || retry.thread != first.thread || retry.attachments[0].DataRef != ref.DataRef {
				t.Fatal("retry reuploaded the image or changed its idempotent payload")
			}
			m := liveMessage("spaces/a", "sent-image", retry.text, 1)
			m.Thread.Name, m.ThreadReply = thread, thread != ""
			m.Attachment = []chat.Attachment{{ContentName: ref.Filename, ContentType: ref.ContentType}}
			retry.done <- sendResult{m: m}
			applyNext(t, callbacks)
			tt.Frame()
			if d.Image != nil || d.Text != "" || d.Quote != nil || a.draftImageBytes != 0 {
				t.Fatal("successful image send did not clear the draft and release its bytes")
			}
		})
	}
}

func TestImageUploadFailureAndNavigationPreserveDraft(t *testing.T) {
	a, b, _, callbacks, tt, d := composerImageFixture(t, 1180, "")
	defer a.Close()
	pasteImageFixture(t, tt, callbacks)
	a.submitDraft(a.selected, "")
	call := nextUploadImage(t, b)
	a.selectSpace("spaces/engineering")
	applyNext(t, callbacks)
	other := a.draftFor(a.selected + "|")
	other.Text = "Different conversation"
	call.done <- uploadImageResult{err: errors.New("Upload failed")}
	applyNext(t, callbacks)
	if d.Image == nil || d.Uncertain || d.Sending || d.Error != "Upload failed" || other.Image != nil || other.Text != "Different conversation" || len(b.sends) != 0 {
		t.Fatal("failed upload lost its image, posted anyway, or crossed drafts")
	}
	a.removeDraftImage(d)
	if a.draftImageBytes != 0 {
		t.Fatal("discarding a failed image upload retained its image budget")
	}
}

func TestComposerImagePreparationAndMemoryBounds(t *testing.T) {
	a, b, clipboard, callbacks, tt, d := composerImageFixture(t, 1180, "")
	defer a.Close()
	clipboard.data = []byte("invalid image")
	pasteImageFixture(t, tt, callbacks)
	if d.Image != nil || d.Error == "" || a.draftImageBytes != 0 {
		t.Fatal("invalid image was attached or leaked a reservation")
	}
	clipboard.data = make([]byte, chat.MaxImageUploadBytes+1)
	if !a.pasteComposerImage(d) || d.Pasting || d.Image != nil || a.draftImageBytes != 0 {
		t.Fatal("oversized image started preparation")
	}
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, previewPattern()); err != nil {
		t.Fatal(err)
	}
	clipboard.data = pngData.Bytes()
	a.draftImageBytes = draftImageBudget
	a.pasteComposerImage(d)
	if d.Pasting || d.Image != nil || len(b.uploads) != 0 {
		t.Fatal("draft image memory budget was exceeded")
	}
	a.draftImageBytes = 0
}
