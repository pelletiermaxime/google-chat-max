package desktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

type reactionBackend interface {
	Reactions(context.Context, string) (chat.Reactions, error)
	SetReaction(context.Context, string, chat.Emoji, bool) (chat.Reactions, error)
	CustomEmojis(context.Context) ([]chat.CustomEmoji, error)
}
type reactionIntent struct {
	emoji chat.Emoji
	want  bool
}
type reactionEntry struct {
	state                        chat.Reactions
	loaded, loading, busy, dirty bool
	err                          string
	intent                       *reactionIntent
	used                         uint64
}
type reactionReadJob struct {
	ctx     context.Context
	backend reactionBackend
	message string
	entry   *reactionEntry
}

func (a *App) reactionFor(message string) *reactionEntry {
	if chat.MessageSpace(message) == "" {
		return nil
	}
	if a.reactions == nil {
		a.reactions = make(map[string]*reactionEntry)
	}
	a.reactionClock++
	if e := a.reactions[message]; e != nil {
		e.used = a.reactionClock
		return e
	}
	if len(a.reactions) >= 64 {
		oldest := ""
		for name, e := range a.reactions {
			if e.loading || e.busy || e.intent != nil || name == a.emojis.message {
				continue
			}
			if oldest == "" || e.used < a.reactions[oldest].used {
				oldest = name
			}
		}
		if oldest == "" {
			return nil
		}
		delete(a.reactions, oldest)
	}
	e := &reactionEntry{used: a.reactionClock}
	a.reactions[message] = e
	return e
}

func (a *App) resetReactionReads() {
	if a.reactionReadCancel != nil {
		a.reactionReadCancel()
	}
	a.reactionReadCtx, a.reactionReadCancel = context.WithCancel(a.ctx)
	for name, e := range a.reactions {
		if e.loading {
			delete(a.reactions, name)
		}
	}
	a.closeEmojiPicker()
}

func (a *App) readReactions(message string, e *reactionEntry) {
	b, ok := a.backend.(reactionBackend)
	if !ok || e == nil || a.closed || a.closing || e.loading || e.busy || e.err != "" || (e.loaded && !e.dirty) {
		return
	}
	if a.reactionReads == nil {
		a.reactionReads = make(chan reactionReadJob, 16)
		for range 2 {
			go a.reactionReader()
		}
	}
	if a.reactionReadCtx == nil {
		a.resetReactionReads()
	}
	select {
	case a.reactionReads <- reactionReadJob{ctx: a.reactionReadCtx, backend: b, message: message, entry: e}:
		e.loading = true
		e.dirty = false
	default:
	}
}

func (a *App) reactionReader() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case job := <-a.reactionReads:
			ctx, cancel := context.WithTimeout(job.ctx, 15*time.Second)
			var state chat.Reactions
			var err error
			if ctx.Err() == nil {
				state, err = job.backend.Reactions(ctx, job.message)
			}
			cancel()
			a.dispatch(func() {
				if a.closed {
					return
				}
				defer a.invalidate()
				if job.ctx.Err() != nil || a.reactions[job.message] != job.entry {
					return
				}
				job.entry.loading = false
				if err != nil {
					job.entry.err = "Could not load reactions"
					return
				}
				job.entry.state, job.entry.loaded = state, true
			})
		}
	}
}

func (a *App) changeReaction(message string, emoji chat.Emoji) {
	e := a.reactionFor(message)
	if e == nil || !e.loaded || e.busy || e.loading || e.intent != nil || a.pendingSends >= 8 {
		return
	}
	e.intent = &reactionIntent{emoji: emoji, want: !e.state.Has(emoji)}
	a.submitReaction(message, e)
}

func (a *App) submitReaction(message string, e *reactionEntry) {
	b, ok := a.backend.(reactionBackend)
	if !ok || a.closed || a.closing || e == nil || e.busy || e.intent == nil || a.pendingSends >= 8 {
		return
	}
	intent := *e.intent
	e.busy = true
	e.err = ""
	a.pendingSends++
	a.sendWorkers.Add(1)
	go func() {
		defer a.sendWorkers.Done()
		ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
		defer cancel()
		state, err := b.SetReaction(ctx, message, intent.emoji, intent.want)
		a.dispatch(func() {
			if a.closed {
				return
			}
			a.pendingSends--
			e.busy = false
			if err != nil {
				e.err = "Could not confirm reaction. Retry to check its state."
			} else {
				e.state, e.loaded, e.intent, e.err = state, true, nil, ""
				a.invalidateConversationCache()
			}
			a.invalidate()
		})
	}()
}

func (a *App) reactionChanged(message string) {
	a.invalidateConversationCache()
	if chat.MessageSpace(message) != a.selected {
		return
	}
	e := a.reactionFor(message)
	if e != nil {
		e.dirty = true
		if e.intent == nil {
			e.err = ""
		}
	}
}

func (a *App) reactionTooltip(emoji chat.Emoji, count int, e *reactionEntry) string {
	title := fmt.Sprintf("%s · %d", emoji.Label(), count)
	if !e.loaded {
		if e.err != "" {
			return title + "\nCould not load who reacted"
		}
		return title + "\nLoading who reacted…"
	}
	seen := make(map[string]bool)
	var names []string
	for _, reactions := range [][]chat.Reaction{e.state.All, e.state.Mine} {
		for _, reaction := range reactions {
			if reaction.Emoji.Key() != emoji.Key() || reaction.User.Name == "" || seen[reaction.User.Name] {
				continue
			}
			seen[reaction.User.Name] = true
			if len(names) >= 20 {
				continue
			}
			user := reaction.User.WithFallback(a.users[reaction.User.Name])
			name := (chat.Message{Sender: user}).Author()
			if user.Name == a.selfUser {
				name = "You"
			}
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return title + "\nNames unavailable"
	}
	if remaining := max(count, len(seen)) - len(names); remaining > 0 {
		names = append(names, fmt.Sprintf("and %d more", remaining))
	}
	return title + "\n" + strings.Join(names, "\n")
}

func (a *App) messageReactions(c *ui.Context, m chat.Message) {
	if _, ok := a.backend.(reactionBackend); !ok {
		return
	}
	e := a.reactions[m.Name]
	if e == nil && len(m.EmojiReactionSummaries) > 0 {
		e = a.reactionFor(m.Name)
	}
	if e == nil {
		return
	}
	a.readReactions(m.Name, e)
	summaries := m.EmojiReactionSummaries
	if e.loaded {
		summaries = e.state.Summaries
	}
	if len(summaries) > 0 {
		ui.Row(c).Wrap().Gap(5).Children(func() {
			for _, summary := range summaries[:min(len(summaries), 40)] {
				if summary.Count <= 0 {
					continue
				}
				emoji := a.resolveEmoji(summary.Emoji)
				owned := e.state.Has(emoji)
				label := fmt.Sprintf("React %s · %d", emoji.Label(), summary.Count)
				if owned {
					label = fmt.Sprintf("Remove %s · %d", emoji.Label(), summary.Count)
				}
				button := ui.ButtonBase(c.Key(emoji.Key())).Label(label).Tooltip(a.reactionTooltip(emoji, summary.Count, e)).Padding(4, 8).Radius(12).Border(1, a.theme.Border).Disabled(!e.loaded || e.busy || e.loading || e.intent != nil || a.closing)
				if owned {
					button.Background(a.theme.SurfacePressed).BorderColor(a.theme.TextMuted)
				} else {
					button.Background(a.theme.Background)
				}
				button.Children(func() { a.emojiPicture(c, emoji, 18); ui.Text(c, fmt.Sprint(summary.Count)).FontSize(11) })
				if button.Clicked() {
					a.changeReaction(m.Name, emoji)
				}
			}
		})
	}
	if e.err != "" {
		ui.Column(c).Gap(5).Children(func() {
			ui.Text(c, e.err).FontSize(11).TextColor(a.theme.Warning)
			if ui.Button(c, "Retry reaction").Disabled(e.busy || a.closing).Clicked() {
				e.err = ""
				if e.intent != nil {
					a.submitReaction(m.Name, e)
				} else {
					e.loaded = false
					a.readReactions(m.Name, e)
				}
			}
		})
	}
}
