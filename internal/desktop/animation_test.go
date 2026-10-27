package desktop

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
	"google-chat-max/internal/media"
)

func testAnimation() *media.Animation {
	frames := make([]*image.RGBA, 2)
	for i, c := range []color.RGBA{{R: 255, A: 255}, {B: 255, A: 255}} {
		frames[i] = image.NewRGBA(image.Rect(0, 0, 20, 20))
		draw.Draw(frames[i], frames[i].Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
	}
	return &media.Animation{Frames: frames, Delays: []time.Duration{50 * time.Millisecond, 100 * time.Millisecond}, Width: 20, Height: 20}
}

func TestNativeInlineGIFUsesAnimationAndAccountsForAllFrames(t *testing.T) {
	callbacks := make(chan func(), 8)
	b := controlledImages{Backend: Demo(), calls: make(chan imageCall, 8)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	defer a.Close()
	a.backend, a.selected = b, "spaces/a"
	attachment := previewAttachment("animated", "animated.gif", "image/gif")
	a.messages = []chat.Message{{Name: "spaces/a/messages/gif", Attachment: []chat.Attachment{attachment}}}
	tt := ui.NewTester(a.View, 1180, 780)
	call := nextImageCall(t, b)
	clip := testAnimation()
	call.result <- imageResult{img: clip}
	applyNext(t, callbacks)
	tt.Frame()
	entry := a.images["animated"]
	if entry.animation != clip || entry.bytes != clip.Bytes()+20*20*4 || a.imageBytes != entry.bytes {
		t.Fatal("inline preview lost its animation or failed to account for retained frames")
	}
	tt.Advance(60 * time.Millisecond)
	if entry.play.frame != 1 {
		t.Fatal("inline GIF did not advance its visible frame")
	}
	if len(b.calls) != 0 {
		t.Fatal("animation fetched the image again")
	}
}

func TestNativeGIFPaintTimingWithoutViewRebuild(t *testing.T) {
	clip := testAnimation()
	bitmap := ui.NewBitmap(clip)
	play := animationPlayback{}
	builds := 0
	visible := true
	tt := ui.NewTester(func(c *ui.Context) {
		builds++
		if visible {
			animatedImage(c, bitmap, clip, &play, 0).Size(40, 40)
		}
	}, 100, 100)
	tt.Frame()
	firstBuilds := builds
	if got := tt.Image().RGBAAt(20, 20); got.R < 240 || got.B > 10 {
		t.Fatalf("first frame: %v", got)
	}
	tt.Advance(60 * time.Millisecond)
	if got := tt.Image().RGBAAt(20, 20); got.B < 240 || got.R > 10 {
		t.Fatalf("second frame: %v", got)
	}
	tt.Advance(100 * time.Millisecond)
	if got := tt.Image().RGBAAt(20, 20); got.R < 240 || got.B > 10 {
		t.Fatalf("looped frame: %v", got)
	}
	if builds != firstBuilds {
		t.Fatalf("animation rebuilt view: %d -> %d", firstBuilds, builds)
	}
	visible = false
	tt.Frame()
	lastFrame := play.frame
	tt.Advance(time.Second)
	if play.frame != lastFrame {
		t.Fatal("hidden image continued advancing")
	}
}

func TestNativeGIFReducedMotionFreezesFirstFrame(t *testing.T) {
	clip := testAnimation()
	bitmap := ui.NewBitmap(clip)
	play := animationPlayback{}
	tt := ui.NewTester(func(c *ui.Context) { animatedImage(c, bitmap, clip, &play, 0).Size(40, 40) }, 100, 100)
	tt.Frame()
	tt.Advance(60 * time.Millisecond)
	if play.frame != 1 {
		t.Fatal("animation did not start")
	}
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	tt.Advance(time.Second)
	if play.frame != 0 {
		t.Fatal("reduced motion did not restore first frame")
	}
	if got := tt.Image().RGBAAt(20, 20); got.R < 240 || got.B > 10 {
		t.Fatalf("reduced-motion frame: %v", got)
	}
}
