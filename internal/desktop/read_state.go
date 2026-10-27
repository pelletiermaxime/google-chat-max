package desktop

import (
	"context"
	"errors"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

const readSyncDelay = 500 * time.Millisecond
const readSyncCacheLimit = 1000

type readStateBackend interface {
	MarkRead(context.Context, string, time.Time) (time.Time, error)
}

type readSyncState struct {
	space        string
	through, due time.Time
	confirmed    map[string]time.Time
	busy         bool
	cancel       context.CancelFunc
	err          string
}

func (a *App) setFocused(focused bool) {
	if a.focused == focused {
		return
	}
	a.focused = focused
	if !focused {
		a.cancelReadSync()
	}
	a.invalidate()
}

func (a *App) cancelReadSync() {
	a.readSync.space, a.readSync.through, a.readSync.due = "", time.Time{}, time.Time{}
	if a.readSync.cancel != nil {
		a.readSync.cancel()
		a.readSync.cancel = nil
	}
}

func (a *App) syncReadState(c *ui.Context) {
	b, ok := a.backend.(readStateBackend)
	_, last := a.messageList.Visible()
	if !ok || a.demo || !a.focused || a.closed || a.closing || a.selected == "" || a.threadName != "" || !a.historyLoaded || a.reading || a.olderLoading || a.messageError != "" || a.newerAvailable || len(a.messages) == 0 || !a.messageList.AtEnd() || last != len(a.messages)-1 {
		a.cancelReadSync()
		return
	}
	var through time.Time
	for _, m := range a.messages {
		if !m.ThreadReply && chat.MessageSpace(m.Name) == a.selected && m.CreateTime.After(through) {
			through = m.CreateTime
		}
	}
	s := &a.readSync
	if through.IsZero() || !through.After(s.confirmed[a.selected]) || s.err != "" {
		return
	}
	if s.space != a.selected || !s.through.Equal(through) {
		s.space, s.through, s.due = a.selected, through, c.Now().Add(readSyncDelay)
	}
	if s.busy {
		return
	}
	if wait := s.due.Sub(c.Now()); wait > 0 {
		c.After(wait)
		return
	}
	space := s.space
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	s.cancel, s.busy = cancel, true
	go func() {
		defer cancel()
		confirmed, err := b.MarkRead(ctx, space, through)
		canceled := errors.Is(ctx.Err(), context.Canceled)
		a.dispatch(func() {
			if a.closed {
				return
			}
			s.busy, s.cancel = false, nil
			if err != nil {
				if !canceled {
					s.err = err.Error()
				}
			} else {
				if s.confirmed == nil {
					s.confirmed = make(map[string]time.Time)
				}
				if _, exists := s.confirmed[space]; !exists && len(s.confirmed) >= readSyncCacheLimit {
					for name := range s.confirmed {
						delete(s.confirmed, name)
						break
					}
				}
				if confirmed.After(s.confirmed[space]) {
					s.confirmed[space] = confirmed
				}
			}
			a.invalidate()
		})
	}()
}

func (a *App) readSyncNotice(c *ui.Context) {
	if a.readSync.err == "" {
		return
	}
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, a.readSync.err).FontSize(12).TextColor(a.theme.Warning)
		if ui.Button(c.Key("retry-read-sync"), "Retry read sync").Disabled(a.closed || a.closing || a.readSync.busy).Clicked() {
			a.readSync.err = ""
			a.cancelReadSync()
		}
	})
}
