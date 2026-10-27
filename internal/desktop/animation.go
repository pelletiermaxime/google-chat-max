package desktop

import (
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/media"
)

type animationPlayback struct {
	started time.Time
	frame   int
}

// Paint timers redraw only visible media and don't rebuild the message list.
// Decoding, compositing and scaling have already completed in a worker.
func animatedImage(c *ui.Context, bitmap *ui.Bitmap, clip *media.Animation, play *animationPlayback, radius float32) ui.Element {
	reduced := c.Preferences().ReduceMotion
	return ui.Box(c).Draw(func(p *ui.Painter, r ui.Rect) {
		if clip != nil && len(clip.Frames) > 1 {
			frame := 0
			if !reduced {
				if play.started.IsZero() {
					play.started = p.Now()
				}
				var next time.Duration
				frame, next = clip.FrameAt(p.Now().Sub(play.started))
				if next > 0 {
					p.After(next)
				}
			}
			if frame != play.frame {
				bitmap.Update(clip.Frames[frame])
				play.frame = frame
			}
		}
		p.Clip(r, radius, func() { p.Image(bitmap, r, ui.Contain) })
	})
}
