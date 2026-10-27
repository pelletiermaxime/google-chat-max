package desktop

import (
	"context"
	"image"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
	"google-chat-max/internal/media"
)

const imageCacheLimit = 32
const imageCacheBytes = 24 << 20

type imageBackend interface {
	AttachmentImage(context.Context, chat.Attachment) (image.Image, error)
}

type imageEntry struct {
	bitmap    *ui.Bitmap
	animation *media.Animation
	play      animationPlayback
	loading   bool
	err       string
	bytes     int
	used      uint64
}

type imageJob struct {
	ctx        context.Context
	backend    imageBackend
	attachment chat.Attachment
	entry      *imageEntry
}

func (a *App) resetImageLoads() {
	if a.imageCancel != nil {
		a.imageCancel()
	}
	a.imageCtx, a.imageCancel = context.WithCancel(a.ctx)
	for key, entry := range a.images {
		if entry.loading {
			delete(a.images, key)
		}
	}
}

// Loading entries are bounded by the two workers and the 16-job queue. Completed
// entries use LRU eviction, with both a count and decoded bitmap byte budget.
func (a *App) evictImage(except *imageEntry) bool {
	oldest := ""
	for key, entry := range a.images {
		if entry == except || entry.loading {
			continue
		}
		if oldest == "" || entry.used < a.images[oldest].used {
			oldest = key
		}
	}
	if oldest == "" {
		return false
	}
	a.imageBytes -= a.images[oldest].bytes
	delete(a.images, oldest)
	return true
}

func (a *App) imageWorker() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case job := <-a.imageQueue:
			var img image.Image
			var err error
			if job.ctx.Err() == nil {
				ctx, cancel := context.WithTimeout(job.ctx, 20*time.Second)
				img, err = job.backend.AttachmentImage(ctx, job.attachment)
				cancel()
			}
			a.dispatch(func() {
				if a.closed {
					return
				}
				defer a.invalidate()
				entry := a.images[job.attachment.AttachmentDataRef.ResourceName]
				if entry != job.entry || job.ctx.Err() != nil {
					return
				}
				entry.loading = false
				if err != nil || img == nil {
					entry.err = "Image preview unavailable"
					return
				}
				size := img.Bounds().Dx() * img.Bounds().Dy() * 4
				clip, _ := img.(*media.Animation)
				if clip != nil {
					size += clip.Bytes()
				}
				for a.imageBytes+size > imageCacheBytes {
					if !a.evictImage(entry) {
						entry.err = "Image preview unavailable"
						return
					}
				}
				entry.bitmap, entry.bytes = ui.NewBitmap(img), size
				entry.animation = clip
				a.imageBytes += size
			})
		}
	}
}

func (a *App) queueImage(attachment chat.Attachment) *imageEntry {
	key := attachment.AttachmentDataRef.ResourceName
	a.imageClock++
	if entry := a.images[key]; entry != nil {
		entry.used = a.imageClock
		return entry
	}
	b, ok := a.backend.(imageBackend)
	if !ok || a.closed || a.closing {
		return nil
	}
	if a.imageQueue == nil {
		a.images = make(map[string]*imageEntry)
		a.imageQueue = make(chan imageJob, 16)
		for range 2 {
			go a.imageWorker()
		}
	}
	if a.imageCtx == nil {
		a.resetImageLoads()
	}
	for len(a.images) >= imageCacheLimit {
		if !a.evictImage(nil) {
			return nil
		}
	}
	entry := &imageEntry{loading: true, used: a.imageClock}
	select {
	case a.imageQueue <- imageJob{ctx: a.imageCtx, backend: b, attachment: attachment, entry: entry}:
		a.images[key] = entry
		return entry
	default:
		return nil // A completing job invalidates the view and retries visible rows.
	}
}

func (a *App) attachment(c *ui.Context, attachment chat.Attachment) {
	label := attachment.ContentName
	if label == "" {
		label = "Attachment"
	}
	if !attachment.IsImage() {
		ui.Text(c, "Attachment · "+label).FontSize(12).TextColor(a.theme.TextMuted)
		return
	}
	entry := a.queueImage(attachment)
	ui.Column(c).Gap(6).AlignItems(ui.Start).MaxWidthPercent(100).Children(func() {
		switch {
		case entry != nil && entry.bitmap != nil:
			w, h := entry.bitmap.Size()
			if entry.animation != nil {
				w, h = entry.animation.Width, entry.animation.Height
			}
			width := min(float32(w), 480)
			if height := width * float32(h) / float32(w); height > 360 {
				width = 360 * float32(w) / float32(h)
			}
			animatedImage(c, entry.bitmap, entry.animation, &entry.play, 8).Width(width).MaxWidthPercent(100).AspectRatio(float32(w) / float32(h)).Shrink(0).Label("Image preview · " + label)
		case entry != nil && entry.err != "":
			ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
				ui.Text(c, entry.err).FontSize(12).TextColor(a.theme.TextMuted)
				if ui.Button(c, "Retry image").Clicked() {
					delete(a.images, attachment.AttachmentDataRef.ResourceName)
					a.queueImage(attachment)
					a.invalidate()
				}
			})
		default:
			text := "Loading image…"
			if _, ok := a.backend.(imageBackend); !ok {
				text = "Image preview unavailable"
			}
			ui.Column(c).Width(240).MaxWidthPercent(100).Height(100).Radius(8).Background(a.theme.Surface).Center().Children(func() {
				ui.Text(c, text).FontSize(12).TextColor(a.theme.TextMuted)
			})
		}
		ui.Text(c, label).FontSize(12).TextColor(a.theme.TextMuted).Selectable().SingleLine()
	})
}
