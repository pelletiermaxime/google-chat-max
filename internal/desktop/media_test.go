package desktop

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

type imageResult struct {
	img image.Image
	err error
}
type imageCall struct {
	ctx        context.Context
	attachment chat.Attachment
	result     chan imageResult
}
type controlledImages struct {
	Backend
	calls chan imageCall
}

func (b controlledImages) AttachmentImage(ctx context.Context, attachment chat.Attachment) (image.Image, error) {
	call := imageCall{ctx: ctx, attachment: attachment, result: make(chan imageResult, 1)}
	select {
	case b.calls <- call:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case result := <-call.result:
		return result.img, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func nextImageCall(t *testing.T, b controlledImages) imageCall {
	t.Helper()
	select {
	case call := <-b.calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("image worker did not start")
		return imageCall{}
	}
}

func previewAttachment(ref, filename, kind string) chat.Attachment {
	a := chat.Attachment{Name: "spaces/a/messages/image/attachments/" + ref, ContentName: filename, ContentType: kind, Source: "UPLOADED_CONTENT"}
	a.AttachmentDataRef.ResourceName = ref
	return a
}

func previewPattern() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 320, 160))
	for y := range 160 {
		for x := range 320 {
			pixel := color.RGBA{R: 30, G: uint8(70 + y/2), B: 210, A: 255}
			if x > 30 && x < 290 && y > 35 && y < 65 {
				pixel = color.RGBA{R: 240, G: 190, B: 55, A: 255}
			}
			if x > 30 && x < 190 && y > 95 && y < 120 {
				pixel = color.RGBA{R: 245, G: 245, B: 245, A: 255}
			}
			img.SetRGBA(x, y, pixel)
		}
	}
	return img
}

func TestNativeInlineImagesLoadOffThreadAndReuseCache(t *testing.T) {
	callbacks := make(chan func(), 8)
	b := controlledImages{Backend: Demo(), calls: make(chan imageCall, 8)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	defer a.Close()
	a.backend = b
	a.selected = "spaces/a"
	a.spaces = []chat.Space{{Name: a.selected, DisplayName: "Image previews", SpaceType: "SPACE"}}
	attachments := []chat.Attachment{previewAttachment("png", "image.png", "image/png"), previewAttachment("jpeg", "photo.jpg", "image/jpeg")}
	a.messages = []chat.Message{
		{Name: "spaces/a/messages/one", Sender: chat.User{DisplayName: "Alex"}, Attachment: attachments[:1]},
		{Name: "spaces/a/messages/two", Sender: chat.User{DisplayName: "Alex"}, Attachment: attachments[1:]},
	}
	tt := ui.NewTester(a.View, 1180, 900)
	tt.Frame()
	if !tt.HasText("Loading image…") || tt.HasText("[Non-text message]") {
		t.Fatal("image-only messages do not show native loading placeholders")
	}
	one, two := nextImageCall(t, b), nextImageCall(t, b)
	tt.Frame()
	tt.Frame()
	if len(b.calls) != 0 {
		t.Fatal("repainting duplicated image downloads")
	}
	one.result <- imageResult{img: previewPattern()}
	two.result <- imageResult{img: previewPattern()}
	applyNext(t, callbacks)
	applyNext(t, callbacks)
	tt.Frame()
	for _, attachment := range attachments {
		entry := a.images[attachment.AttachmentDataRef.ResourceName]
		if entry == nil || entry.bitmap == nil || !tt.HasText(attachment.ContentName) {
			t.Fatal("inline image or its filename was lost")
		}
	}
	if tt.HasText("Loading image…") || tt.HasText("[Non-text message]") {
		t.Fatal("loaded images retain obsolete placeholders")
	}
	colored := 0
	img := tt.Image()
	for y := range img.Bounds().Dy() {
		for x := range img.Bounds().Dx() {
			r, g, b, _ := img.At(x, y).RGBA()
			if b > r+10000 && b > g+10000 {
				colored++
			}
		}
	}
	if colored < 20000 {
		t.Fatal("native screenshot did not actually paint the image pixels")
	}
	tt.Frame()
	if len(b.calls) != 0 {
		t.Fatal("loaded previews were downloaded again")
	}
	if path := os.Getenv("CHAT_MAX_IMAGE_SCREENSHOT"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tt.Image()); err != nil {
			f.Close()
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestImageFailureRequiresExplicitRetryAndKeepsOtherAttachments(t *testing.T) {
	callbacks := make(chan func(), 8)
	b := controlledImages{Backend: Demo(), calls: make(chan imageCall, 8)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	defer a.Close()
	a.backend = b
	a.selected = "spaces/a"
	attachment := previewAttachment("image", "photo.jpg", "image/jpeg")
	a.messages = []chat.Message{{Name: "spaces/a/messages/one", Attachment: []chat.Attachment{attachment, {ContentName: "notes.pdf", ContentType: "application/pdf"}}}}
	tt := ui.NewTester(a.View, 1180, 780)
	call := nextImageCall(t, b)
	call.result <- imageResult{err: errors.New("synthetic network failure")}
	applyNext(t, callbacks)
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Retry image") || !tt.HasText("photo.jpg") || !tt.HasText("Attachment · notes.pdf") || len(b.calls) != 0 {
		t.Fatal("failed preview lost its fallback or retried automatically")
	}
	if err := tt.Click("Retry image"); err != nil {
		t.Fatal(err)
	}
	retry := nextImageCall(t, b)
	retry.result <- imageResult{img: previewPattern()}
	applyNext(t, callbacks)
	tt.Frame()
	if tt.HasText("Retry image") || a.images[attachment.AttachmentDataRef.ResourceName].bitmap == nil {
		t.Fatal("explicit retry did not restore the preview")
	}
}

func TestImageLoadingAboveViewportPreservesNativeScrollAnchor(t *testing.T) {
	callbacks := make(chan func(), 8)
	b := controlledImages{Backend: Demo(), calls: make(chan imageCall, 8)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	defer a.Close()
	a.backend, a.selected = b, "spaces/a"
	a.messages = historyFixture(0, 60)
	a.messages[29].Attachment = []chat.Attachment{previewAttachment("above", "portrait.png", "image/png")}
	tt := ui.NewTester(a.View, 1180, 780)
	if len(b.calls) != 0 {
		t.Fatal("offscreen images downloaded before their rows were built")
	}
	a.messageList.ScrollTo(30, ui.Start)
	tt.Frame()
	call := nextImageCall(t, b)
	before, ok := tt.Find("Message 0030")
	if !ok {
		t.Fatal("anchor message is not visible")
	}
	call.result <- imageResult{img: image.NewRGBA(image.Rect(0, 0, 500, 720))}
	applyNext(t, callbacks)
	tt.Frame()
	after, ok := tt.Find("Message 0030")
	if !ok || after.Y < before.Y-1 || after.Y > before.Y+1 {
		t.Fatalf("image loading moved the visible message from %v to %v", before, after)
	}
}

func TestImageCacheBoundsAndSwitchCancellation(t *testing.T) {
	callbacks := make(chan func(), 8)
	b := controlledImages{Backend: Demo(), calls: make(chan imageCall, 8)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	defer a.Close()
	a.backend = b
	tiny := image.NewRGBA(image.Rect(0, 0, 8, 8))
	large := image.NewRGBA(image.Rect(0, 0, 960, 720))
	for i := range imageCacheLimit + 12 {
		attachment := previewAttachment(fmt.Sprint(i), "sample.png", "image/png")
		a.queueImage(attachment)
		call := nextImageCall(t, b)
		img := tiny
		if i >= imageCacheLimit {
			img = large
		}
		call.result <- imageResult{img: img}
		applyNext(t, callbacks)
		if len(a.images) > imageCacheLimit || a.imageBytes > imageCacheBytes {
			t.Fatal("image cache exceeded its entry or bitmap budget")
		}
	}
	if a.images["0"] != nil || a.images[fmt.Sprint(imageCacheLimit+11)] == nil {
		t.Fatal("image cache did not retain recent previews and evict older ones")
	}
	attachment := previewAttachment("cancel", "sample.png", "image/png")
	a.queueImage(attachment)
	old := nextImageCall(t, b)
	a.selectSpace("")
	if old.ctx.Err() == nil {
		t.Fatal("switching conversations did not cancel the image download")
	}
	current := a.queueImage(attachment)
	newCall := nextImageCall(t, b)
	applyNext(t, callbacks) // The canceled worker's callback must not replace the new entry.
	if a.images["cancel"] != current || !current.loading {
		t.Fatal("a stale image result changed the replacement request")
	}
	newCall.result <- imageResult{img: tiny}
	applyNext(t, callbacks)
	if a.images["cancel"].bitmap == nil {
		t.Fatal("replacement image download was lost after switching")
	}
}
