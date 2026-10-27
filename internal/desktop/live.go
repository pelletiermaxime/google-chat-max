package desktop

import (
	"context"
	"errors"
	"slices"
	"time"

	"google-chat-max/internal/chat"
)

type liveBackend interface {
	Watch(context.Context, func(chat.StreamEvent) bool) error
}

func (a *App) Connect() {
	if a.closed || a.closing || a.streamRunning || a.demo {
		return
	}
	backend, ok := a.backend.(liveBackend)
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.streamRunning = true
	a.streamStatus, a.streamError = "Connecting", ""
	a.streamDone = make(chan struct{})
	a.streamResult = make(chan error, 1)
	done := a.streamDone
	result := a.streamResult
	go func() {
		defer cancel()
		err := backend.Watch(ctx, func(event chat.StreamEvent) bool {
			// Receive's flow-control limit bounds the number of callbacks
			// waiting here. Ack only after main-thread application succeeds.
			applied := make(chan bool, 1)
			a.dispatch(func() {
				if a.closed || ctx.Err() != nil {
					applied <- false
					return
				}
				a.applyStreamEvent(event)
				a.invalidate()
				applied <- true
			})
			select {
			case accepted := <-applied:
				return accepted
			case <-ctx.Done():
				return false
			}
		})
		// Cleanup has finished before closing done. Never wait for main
		// thread dispatch before making shutdown completion observable.
		result <- err
		close(done)
		a.dispatch(func() {
			if a.closed {
				return
			}
			a.streamRunning = false
			a.streamStatus = "Disconnected"
			if err != nil && !errors.Is(err, context.Canceled) {
				a.streamError = err.Error()
			}
			a.invalidate()
		})
	}()
}

// Shutdown keeps the native event loop alive until owned cloud resources have
// been cleaned up. It never blocks the UI thread on network work.
func (a *App) Shutdown(done func(error)) {
	if a.closed || a.closing {
		return
	}
	a.closing = true
	if a.pendingSends > 0 {
		a.streamStatus = "Finishing pending actions before closing"
		a.invalidate()
		go func() { a.sendWorkers.Wait(); a.dispatch(func() { a.finishShutdown(done) }) }()
		return
	}
	a.finishShutdown(done)
}

func (a *App) finishShutdown(done func(error)) {
	a.streamStatus = "Disconnecting"
	a.Close()
	a.invalidate()
	streamDone := a.streamDone
	result := a.streamResult
	if streamDone == nil {
		done(nil)
		return
	}
	go func() {
		<-streamDone
		err := <-result
		a.dispatch(func() { done(err) })
	}()
}

func (a *App) applyStreamEvent(event chat.StreamEvent) {
	if event.Status != "" {
		a.streamStatus = event.Status
	}
	if event.Reconcile {
		for _, entry := range a.reactions {
			entry.dirty = true
		}
		a.invalidateConversationCache()
		if a.threadName != "" {
			if a.threadReading {
				a.threadReconcilePending = true
			} else {
				a.refreshThread()
			}
		}
		// Reconnect repairs the selected bounded history. Pub/Sub retains
		// unacknowledged events for other conversations during stream gaps.
		if a.reading {
			a.reconcilePending = true
		} else {
			a.refreshMessages()
		}
	}
	if event.ReactionMessage != "" {
		a.reactionChanged(event.ReactionMessage)
	}
	if event.Message == nil {
		return
	}
	m := *event.Message
	space := chat.MessageSpace(m.Name)
	if space == "" {
		return
	}
	// Deduplicate by message name, including redeliveries with a different
	// CloudEvent ID. The ring is bounded; selected history is deduped too.
	if _, seen := a.seenMessages[m.Name]; seen {
		a.enrichDelivery(m)
		return
	}
	a.seenMessages[m.Name] = struct{}{}
	a.routeThreadEvent(m)
	a.seenOrder = append(a.seenOrder, m.Name)
	if len(a.seenOrder) > 2048 {
		delete(a.seenMessages, a.seenOrder[0])
		a.seenOrder = a.seenOrder[1:]
	}
	at := slices.IndexFunc(a.spaces, func(s chat.Space) bool { return s.Name == space })
	if at < 0 && len(a.spaces) < 1000 {
		a.spaces = append(a.spaces, chat.Space{Name: space, LastActiveTime: m.CreateTime})
	} else if at >= 0 && m.CreateTime.After(a.spaces[at].LastActiveTime) {
		a.spaces[at].LastActiveTime = m.CreateTime
	}
	if space != a.selected {
		a.cacheIncoming(space, m)
		if len(a.activity) < 1000 || a.activity[space] != 0 {
			a.activity[space] = min(a.activity[space]+1, 999)
		}
		return
	}
	if a.reading {
		// A REST snapshot may predate this event. Keep a bounded overlay
		// until it completes so its result cannot erase incoming messages.
		a.duringRead = mergeMessages(a.duringRead, []chat.Message{m})
	}
	a.appendIncoming(m, false)
	a.updated = time.Now()
}
