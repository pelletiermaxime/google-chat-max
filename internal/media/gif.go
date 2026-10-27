// Package media decodes bounded animated previews independently of the UI.
package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"math"
	"time"

	xdraw "golang.org/x/image/draw"
)

var ErrAnimationLimit = errors.New("GIF exceeds animation limits")

type Animation struct {
	Frames        []*image.RGBA
	Delays        []time.Duration
	LoopCount     int
	Width, Height int // Logical dimensions, preserved when frames need downscaling.
}

func (a *Animation) ColorModel() color.Model { return color.RGBAModel }
func (a *Animation) Bounds() image.Rectangle { return a.Frames[0].Bounds() }
func (a *Animation) At(x, y int) color.Color { return a.Frames[0].At(x, y) }
func (a *Animation) Bytes() int {
	total := 0
	for _, frame := range a.Frames {
		total += len(frame.Pix)
	}
	return total
}

// FrameAt returns the frame and remaining time before the next change. Zero
// remaining time means playback has finished. GIF loop counts are repeats after
// the first pass; -1 means one pass and zero means repeat indefinitely.
func (a *Animation) FrameAt(elapsed time.Duration) (int, time.Duration) {
	if len(a.Frames) < 2 {
		return 0, 0
	}
	var duration time.Duration
	for _, delay := range a.Delays {
		duration += delay
	}
	if duration <= 0 {
		return 0, 0
	}
	if elapsed < 0 {
		elapsed = 0
	}
	if a.LoopCount != 0 {
		loops := a.LoopCount + 1
		if loops < 1 {
			loops = 1
		}
		if int64(elapsed/duration) >= int64(loops) {
			return len(a.Frames) - 1, 0
		}
	}
	at := elapsed % duration
	for i, delay := range a.Delays {
		if at < delay {
			return i, delay - at
		}
		at -= delay
	}
	return 0, a.Delays[0]
}

// DecodeGIF checks every image descriptor before DecodeAll can allocate its
// paletted buffers, then composites disposal/transparency in the background.
// Only small, fully composited RGBA previews survive after decoding.
func DecodeGIF(data []byte, maxWidth, maxHeight, budget int) (*Animation, error) {
	if maxWidth < 1 || maxHeight < 1 || budget < 4 {
		return nil, ErrAnimationLimit
	}
	count, err := checkGIF(data)
	if err != nil {
		return nil, err
	}
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	w, h := g.Config.Width, g.Config.Height
	outputW, outputH := w, h
	if outputW > maxWidth {
		outputH = max(1, outputH*maxWidth/outputW)
		outputW = maxWidth
	}
	if outputH > maxHeight {
		outputW = max(1, outputW*maxHeight/outputH)
		outputH = maxHeight
	}
	if outputW*outputH*4*count > budget {
		scale := math.Sqrt(float64(budget) / float64(outputW*outputH*4*count))
		outputW = max(1, int(float64(outputW)*scale))
		outputH = max(1, int(float64(outputH)*scale))
	}
	if outputW*outputH*4*count > budget {
		return nil, ErrAnimationLimit
	}
	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	background := func(frame *image.Paletted) color.Color {
		for _, c := range frame.Palette {
			_, _, _, alpha := c.RGBA()
			if alpha == 0 {
				return color.Transparent
			}
		}
		if palette, ok := g.Config.ColorModel.(color.Palette); ok && int(g.BackgroundIndex) < len(palette) {
			return palette[g.BackgroundIndex]
		}
		return color.Transparent
	}
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: background(g.Image[0])}, image.Point{}, draw.Src)
	animation := &Animation{LoopCount: g.LoopCount, Width: w, Height: h}
	var previous *image.RGBA
	for i, frame := range g.Image {
		disposal := byte(0)
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}
		if disposal == gif.DisposalPrevious {
			if previous == nil {
				previous = image.NewRGBA(canvas.Bounds())
			}
			copy(previous.Pix, canvas.Pix)
		}
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		preview := image.NewRGBA(image.Rect(0, 0, outputW, outputH))
		xdraw.ApproxBiLinear.Scale(preview, preview.Bounds(), canvas, canvas.Bounds(), draw.Src, nil)
		animation.Frames = append(animation.Frames, preview)
		delay := g.Delay[i]
		if delay < 2 {
			delay = 10
		}
		animation.Delays = append(animation.Delays, time.Duration(delay)*10*time.Millisecond)
		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvas, frame.Bounds(), &image.Uniform{C: background(frame)}, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			copy(canvas.Pix, previous.Pix)
		}
	}
	return animation, nil
}

func checkGIF(data []byte) (int, error) {
	invalid := errors.New("invalid GIF structure")
	if len(data) < 13 || (string(data[:6]) != "GIF87a" && string(data[:6]) != "GIF89a") {
		return 0, invalid
	}
	w, h := int(binary.LittleEndian.Uint16(data[6:8])), int(binary.LittleEndian.Uint16(data[8:10]))
	if w < 1 || h < 1 || w*h > 4_000_000 {
		return 0, ErrAnimationLimit
	}
	pos := 13
	if data[10]&128 != 0 {
		pos += 3 * (1 << ((data[10] & 7) + 1))
	}
	skipBlocks := func() bool {
		for pos < len(data) {
			n := int(data[pos])
			pos++
			if n == 0 {
				return true
			}
			if n > len(data)-pos {
				return false
			}
			pos += n
		}
		return false
	}
	frames, pixels := 0, 0
	for pos < len(data) {
		kind := data[pos]
		pos++
		switch kind {
		case 0x3b:
			if frames == 0 {
				return 0, invalid
			}
			return frames, nil
		case 0x21:
			if pos >= len(data) {
				return 0, invalid
			}
			pos++
			if !skipBlocks() {
				return 0, invalid
			}
		case 0x2c:
			if len(data)-pos < 9 {
				return 0, invalid
			}
			x, y := int(binary.LittleEndian.Uint16(data[pos:])), int(binary.LittleEndian.Uint16(data[pos+2:]))
			fw, fh := int(binary.LittleEndian.Uint16(data[pos+4:])), int(binary.LittleEndian.Uint16(data[pos+6:]))
			packed := data[pos+8]
			pos += 9
			if fw < 1 || fh < 1 || x+fw > w || y+fh > h {
				return 0, invalid
			}
			frames++
			pixels += fw * fh
			if frames > 256 || pixels > 32_000_000 {
				return 0, ErrAnimationLimit
			}
			if packed&128 != 0 {
				pos += 3 * (1 << ((packed & 7) + 1))
			}
			if pos >= len(data) {
				return 0, invalid
			}
			pos++ // LZW code size.
			if !skipBlocks() {
				return 0, invalid
			}
		default:
			return 0, invalid
		}
	}
	return 0, invalid
}
