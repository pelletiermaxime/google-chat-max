package media

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"testing"
	"time"
)

var testPalette = color.Palette{color.Transparent, color.RGBA{R: 255, A: 255}, color.RGBA{G: 255, A: 255}, color.RGBA{B: 255, A: 255}}

func solidFrame(r image.Rectangle, index uint8) *image.Paletted {
	f := image.NewPaletted(r, testPalette)
	for i := range f.Pix {
		f.Pix[i] = index
	}
	return f
}

func encoded(t *testing.T, frames []*image.Paletted, disposal []byte, delays []int, loops int) []byte {
	t.Helper()
	var data bytes.Buffer
	g := &gif.GIF{Image: frames, Disposal: disposal, Delay: delays, LoopCount: loops, Config: image.Config{ColorModel: testPalette, Width: 4, Height: 2}}
	if err := gif.EncodeAll(&data, g); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestDecodeGIFCompositesOffsetsTransparencyAndDisposal(t *testing.T) {
	for _, disposal := range []byte{gif.DisposalBackground, gif.DisposalPrevious} {
		data := encoded(t, []*image.Paletted{
			solidFrame(image.Rect(0, 0, 4, 2), 1),
			solidFrame(image.Rect(1, 0, 3, 2), 2),
			solidFrame(image.Rect(3, 0, 4, 2), 3),
		}, []byte{gif.DisposalNone, disposal, gif.DisposalNone}, []int{5, 12, 0}, 0)
		a, err := DecodeGIF(data, 4, 2, 1024)
		if err != nil {
			t.Fatal(err)
		}
		pixel := func(frame, x int) color.RGBA { return a.Frames[frame].RGBAAt(x, 0) }
		if pixel(1, 0) != (color.RGBA{R: 255, A: 255}) || pixel(1, 1) != (color.RGBA{G: 255, A: 255}) {
			t.Fatal("offset frame lost previous canvas or green overlay")
		}
		want := color.RGBA{}
		if disposal == gif.DisposalPrevious {
			want = color.RGBA{R: 255, A: 255}
		}
		if pixel(2, 1) != want || pixel(2, 3) != (color.RGBA{B: 255, A: 255}) {
			t.Fatalf("disposal %d: got %v, want %v", disposal, pixel(2, 1), want)
		}
		if a.Delays[0] != 50*time.Millisecond || a.Delays[1] != 120*time.Millisecond || a.Delays[2] != 100*time.Millisecond {
			t.Fatalf("incorrect delays: %v", a.Delays)
		}
	}
}

func TestAnimationFrameTimingAndFiniteLoops(t *testing.T) {
	a := &Animation{Frames: make([]*image.RGBA, 2), Delays: []time.Duration{50 * time.Millisecond, 120 * time.Millisecond}}
	for _, tc := range []struct {
		loops    int
		at, next time.Duration
		frame    int
	}{
		{0, 0, 50 * time.Millisecond, 0}, {0, 60 * time.Millisecond, 110 * time.Millisecond, 1},
		{0, 180 * time.Millisecond, 40 * time.Millisecond, 0}, {-1, 170 * time.Millisecond, 0, 1},
		{1, 180 * time.Millisecond, 40 * time.Millisecond, 0}, {1, 340 * time.Millisecond, 0, 1},
	} {
		a.LoopCount = tc.loops
		frame, next := a.FrameAt(tc.at)
		if frame != tc.frame || next != tc.next {
			t.Fatalf("loops=%d at=%v: frame=%d next=%v", tc.loops, tc.at, frame, next)
		}
	}
}

func TestGIFAnimationBudgetsAndMalformedInput(t *testing.T) {
	frames := []*image.Paletted{solidFrame(image.Rect(0, 0, 4, 2), 1), solidFrame(image.Rect(0, 0, 4, 2), 2)}
	data := encoded(t, frames, nil, []int{5, 5}, 0)
	a, err := DecodeGIF(data, 4, 2, 16)
	if err != nil {
		t.Fatal(err)
	}
	if a.Bytes() > 16 || a.Width != 4 || a.Height != 2 {
		t.Fatalf("budget/dimensions lost: %d bytes, %dx%d", a.Bytes(), a.Width, a.Height)
	}
	for _, broken := range [][]byte{nil, data[:len(data)-1], append([]byte("invalid"), data...)} {
		if _, err := DecodeGIF(broken, 4, 2, 1024); err == nil {
			t.Fatal("accepted malformed GIF")
		}
	}
	many := make([]*image.Paletted, 257)
	for i := range many {
		many[i] = frames[0]
	}
	data = encoded(t, many, nil, make([]int, len(many)), 0)
	if _, err := DecodeGIF(data, 4, 2, 1024); !errors.Is(err, ErrAnimationLimit) {
		t.Fatalf("frame limit: %v", err)
	}
}
