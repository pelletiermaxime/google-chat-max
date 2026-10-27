package desktop

import (
	"bytes"
	"context"
	"image"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"golang.org/x/image/draw"
	"google-chat-max/internal/chat"
	"google-chat-max/internal/media"
)

type avatarEntry struct{ bitmap *ui.Bitmap }

func avatarHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	for _, domain := range []string{"google.com", "googleusercontent.com", "gstatic.com", "googleapis.com"} {
		if h == domain || strings.HasSuffix(h, "."+domain) {
			return true
		}
	}
	return false
}

func loadAvatar(ctx context.Context, raw string) image.Image {
	return loadRemoteImage(ctx, raw, false)
}

func loadRemoteImage(ctx context.Context, raw string, animate bool) image.Image {
	if !avatarHost(raw) {
		return nil
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || !avatarHost(req.URL.String()) {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil
	}
	res, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 1024 || config.Height > 1024 {
		return nil
	}
	if animate && format == "gif" {
		if clip, err := media.DecodeGIF(data, 64, 64, 512<<10); err == nil {
			return clip
		}
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, 64, 64))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}

func (a *App) avatar(c *ui.Context, raw, label string, size float32) {
	if a.avatars == nil {
		a.avatars = make(map[string]avatarEntry)
		a.avatarQueue = make(chan string, 32)
		for range 2 {
			go func() {
				for {
					select {
					case <-a.ctx.Done():
						return
					case address := <-a.avatarQueue:
						img := loadAvatar(a.ctx, address)
						a.dispatch(func() {
							if a.closed {
								return
							}
							entry, ok := a.avatars[address]
							if !ok {
								return
							}
							if img != nil {
								entry.bitmap = ui.NewBitmap(img)
							}
							a.avatars[address] = entry
							a.invalidate()
						})
					}
				}
			}()
		}
	}
	if entry, ok := a.avatars[raw]; ok && entry.bitmap != nil {
		ui.Image(c, entry.bitmap).Width(size).Height(size).Radius(size / 2).Shrink(0)
		return
	}
	if raw != "" && avatarHost(raw) {
		if _, exists := a.avatars[raw]; !exists && len(a.avatars) < 64 {
			select {
			case a.avatarQueue <- raw:
				a.avatars[raw] = avatarEntry{}
			default:
			}
		}
	}
	initial, _ := utf8.DecodeRuneInString(label)
	if initial == utf8.RuneError || initial == 0 {
		initial = '?'
	}
	ui.Column(c).Width(size).Height(size).Radius(size / 3).Background(a.theme.SurfacePressed).Center().Shrink(0).Children(func() { ui.Text(c, strings.ToUpper(string(initial))).Bold().TextColor(a.theme.Accent) })
}

func (a *App) rememberUsers(space chat.Space) {
	for _, u := range space.Participants {
		if len(a.users) < 1024 || a.users[u.Name].Name != "" {
			a.users[u.Name] = u
		}
	}
}

func (a *App) author(m chat.Message) chat.User {
	return m.Sender.WithFallback(a.users[m.Sender.Name])
}

// Resolve names and avatars independently so a slow membership response does
// not hold up the conversation. Switching conversations cancels this work.
func (a *App) refreshAuthors(space string, request uint64) {
	if a.authorsCancel != nil {
		a.authorsCancel()
	}
	b, ok := a.backend.(interface {
		Members(context.Context, string) ([]chat.User, error)
	})
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	a.authorsCancel = cancel
	go func() {
		defer cancel()
		users, err := b.Members(ctx, space)
		if err != nil {
			return
		}
		a.dispatch(func() {
			if a.closed || a.selected != space || a.request != request {
				return
			}
			a.rememberUsers(chat.Space{Participants: users})
			a.invalidate()
		})
	}()
}
