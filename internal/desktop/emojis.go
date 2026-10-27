package desktop

import (
	"context"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
	"google-chat-max/internal/media"
)

type emojiUIState struct {
	catalog                   []chat.CustomEmoji
	loading                   bool
	loaded                    time.Time
	err                       string
	message, query, lastQuery string
	open                      bool
	grid                      ui.GridState
	category                  int
	tone                      string
	sprites                   map[string]*emojiSprite
	queue                     chan emojiSpriteJob
	clock                     uint64
}
type emojiSprite struct {
	bitmap    *ui.Bitmap
	animation *media.Animation
	play      animationPlayback
	loading   bool
	used      uint64
	url       string
}
type emojiSpriteJob struct {
	key, url string
	entry    *emojiSprite
}

var addReactionIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 13a8 8 0 1 1-9-9M8 14s1 2 4 2 4-2 4-2M8.5 9h.01M14.5 9h.01M19 2v6M16 5h6"/></svg>`))

func (a *App) closeEmojiPicker() {
	a.emojis.open = false
	a.emojis.message = ""
	a.emojis.query = ""
	a.emojis.lastQuery = ""
}

func (a *App) loadEmojis() {
	b, ok := a.backend.(reactionBackend)
	if !ok || a.closed || a.closing || a.emojis.loading || a.emojis.err != "" || (!a.emojis.loaded.IsZero() && time.Since(a.emojis.loaded) < 8*time.Minute) {
		return
	}
	a.emojis.loading = true
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
		defer cancel()
		catalog, err := b.CustomEmojis(ctx)
		a.dispatch(func() {
			if a.closed {
				return
			}
			a.emojis.loading = false
			if err != nil {
				a.emojis.err = "Could not load custom emoji"
			} else {
				a.emojis.catalog = catalog[:min(len(catalog), 1000)]
				a.emojis.loaded = time.Now()
			}
			a.invalidate()
		})
	}()
}

func (a *App) resolveEmoji(emoji chat.Emoji) chat.Emoji {
	if emoji.CustomEmoji == nil {
		return emoji
	}
	for _, custom := range a.emojis.catalog {
		if (chat.Emoji{CustomEmoji: &custom}).Key() == emoji.Key() || (emoji.CustomEmoji.Name == "" && custom.EmojiName == emoji.CustomEmoji.EmojiName) {
			copy := custom
			return chat.Emoji{CustomEmoji: &copy}
		}
	}
	return emoji
}

func (a *App) emojiPicture(c *ui.Context, emoji chat.Emoji, size float32) {
	if emoji.CustomEmoji == nil {
		ui.Text(c, emoji.Unicode).FontSize(size).SingleLine()
		return
	}
	custom := emoji.CustomEmoji
	key := emoji.Key()
	a.emojis.clock++
	entry := a.emojis.sprites[key]
	if entry != nil && entry.bitmap != nil {
		entry.used = a.emojis.clock
		animatedImage(c, entry.bitmap, entry.animation, &entry.play, 0).Size(size, size)
		return
	}
	if custom.TemporaryImageURI != "" && avatarHost(custom.TemporaryImageURI) {
		if a.emojis.queue == nil {
			a.emojis.sprites = make(map[string]*emojiSprite)
			a.emojis.queue = make(chan emojiSpriteJob, 32)
			for range 2 {
				go a.emojiSpriteWorker()
			}
		}
		if entry == nil || (!entry.loading && entry.url != custom.TemporaryImageURI) {
			if len(a.emojis.sprites) >= 64 {
				oldest := ""
				for name, sprite := range a.emojis.sprites {
					if !sprite.loading && (oldest == "" || sprite.used < a.emojis.sprites[oldest].used) {
						oldest = name
					}
				}
				if oldest != "" {
					delete(a.emojis.sprites, oldest)
				}
			}
			if len(a.emojis.sprites) < 64 {
				sprite := &emojiSprite{loading: true, used: a.emojis.clock, url: custom.TemporaryImageURI}
				select {
				case a.emojis.queue <- emojiSpriteJob{key: key, url: custom.TemporaryImageURI, entry: sprite}:
					a.emojis.sprites[key] = sprite
				default:
				}
			}
		}
	}
	ui.Text(c, "⋯").FontSize(size).Width(size).TextColor(a.theme.TextMuted)
}

func (a *App) emojiSpriteWorker() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case job := <-a.emojis.queue:
			img := loadRemoteImage(a.ctx, job.url, true)
			a.dispatch(func() {
				if a.closed || a.emojis.sprites[job.key] != job.entry {
					return
				}
				job.entry.loading = false
				if img != nil {
					job.entry.bitmap = ui.NewBitmap(img)
					if clip, ok := img.(*media.Animation); ok {
						job.entry.animation = clip
					}
				}
				a.invalidate()
			})
		}
	}
}

func (a *App) quickEmojiButton(c *ui.Context, message string, emoji chat.Emoji, e *reactionEntry, size float32) {
	a.emojiButton(c, message, emoji, e, size, "")
}

func (a *App) emojiButton(c *ui.Context, message string, emoji chat.Emoji, e *reactionEntry, size float32, name string) {
	emoji = a.resolveEmoji(emoji)
	label := "React " + emoji.Label()
	if e != nil && e.state.Has(emoji) {
		label = "Remove " + emoji.Label()
	}
	available := emoji.Unicode != "" || (emoji.CustomEmoji != nil && emoji.CustomEmoji.Name != "")
	tooltip := label
	if name != "" {
		tooltip += " · " + name
	}
	button := ui.ButtonBase(c).Label(label).Tooltip(tooltip).Size(size, size).Center().Radius(8).Disabled(!available || e == nil || !e.loaded || e.loading || e.busy || e.intent != nil || a.closing || a.pendingSends >= 8)
	if button.Hovered() || button.FocusVisible() {
		button.Background(a.theme.SurfaceHover)
	}
	button.Children(func() { a.emojiPicture(c, emoji, 22) })
	if button.Clicked() {
		a.changeReaction(message, emoji)
		a.closeEmojiPicker()
	}
}

func (a *App) emojiActions(c *ui.Context, m chat.Message) {
	if _, ok := a.backend.(reactionBackend); !ok {
		return
	}
	a.loadEmojis()
	e := a.reactionFor(m.Name)
	a.readReactions(m.Name, e)
	quick := []chat.Emoji{{Unicode: "👍"}, {CustomEmoji: &chat.CustomEmoji{EmojiName: ":yep:"}}, {CustomEmoji: &chat.CustomEmoji{EmojiName: ":peepohey:"}}}
	for i, emoji := range quick {
		a.quickEmojiButton(c.Key("quick-"+strconv.Itoa(i)), m.Name, emoji, e, 32)
	}
	anchor := ui.ButtonBase(c.Key("emoji-picker-button")).Label("Add reaction").Tooltip("Add reaction").Size(32, 32).Center().Radius(8).Disabled(a.closing)
	if anchor.Hovered() {
		anchor.Background(a.theme.SurfaceHover)
	}
	anchor.Children(func() { ui.Icon(c, addReactionIcon).Size(20, 20) })
	if anchor.Clicked() {
		a.emojis.open = !(a.emojis.open && a.emojis.message == m.Name)
		a.emojis.message = m.Name
		a.emojis.query = ""
		a.emojis.grid = ui.GridState{}
	}
	if a.emojis.message != m.Name {
		return
	}
	panel := ui.PopoverBase(c.Key("emoji-picker"), anchor, &a.emojis.open, func(panel ui.Element) {
		panel.Width(348).Padding(14).Gap(10).Radius(14).Border(1, a.theme.Border).Background(a.theme.Surface).Children(func() {
			a.emojiPickerContents(c, m.Name, e)
		})
	})
	if panel.Dismissed() || !a.emojis.open {
		a.closeEmojiPicker()
	}
}
