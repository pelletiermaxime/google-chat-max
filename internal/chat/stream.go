package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"google-chat-max/internal/settings"

	"cloud.google.com/go/pubsub/v2"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const messageCreated = "google.workspace.chat.message.v1.created"
const reactionCreated = "google.workspace.chat.reaction.v1.created"
const reactionDeleted = "google.workspace.chat.reaction.v1.deleted"

// StreamEvent is delivered synchronously with backpressure. Reconcile means
// refresh the selected history after a new connection or a transport gap.
type StreamEvent struct {
	ID              string
	Message         *Message
	Status          string
	Reconcile       bool
	ReactionMessage string
}

// Watch owns one isolated cloud session until cancellation. No Chat messages
// are sent. Returning from emit acknowledges the event only when it returns true.
func (c *Client) Watch(ctx context.Context, emit func(StreamEvent) bool) (err error) {
	if settings.ValidateProjectID(c.project) != nil {
		return errors.New("configure a valid Google Cloud project before connecting")
	}
	api := &streamAPI{client: c, pubsubURL: "https://pubsub.googleapis.com/v1", workspaceURL: "https://workspaceevents.googleapis.com/v1"}
	journal, err := streamJournalPath()
	if err != nil {
		return err
	}
	if previous, err := loadResources(journal); err != nil {
		return err
	} else if previous != nil {
		if err := api.cleanup(ctx, previous); err != nil {
			return fmt.Errorf("previous live-session cleanup needs a retry: %w", err)
		}
		if err := removeJournal(journal); err != nil {
			return err
		}
	}
	r, err := newResources(c.project)
	if err != nil {
		return err
	}
	if err := saveResources(journal, r); err != nil {
		return err
	}
	defer func() {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			err = nil
		}
		// This runs before Watch returns, so the window can wait for cleanup.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if cleanupErr := api.cleanup(cleanupCtx, r); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("session cleanup incomplete; retry incoming messages to try again: %w", cleanupErr))
		} else {
			err = errors.Join(err, removeJournal(journal))
		}
	}()
	setupCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if !emit(StreamEvent{Status: "Connecting"}) {
		return nil
	}
	if err := api.create(setupCtx, r, journal); err != nil {
		return err
	}
	client, err := pubsub.NewClient(setupCtx, r.Project, option.WithTokenSource(c.tokens))
	if err != nil {
		return errors.New("could not initialize the incoming-message transport")
	}
	defer client.Close()
	subscriber := client.Subscriber(r.queue())
	subscriber.ReceiveSettings.NumGoroutines = 1
	subscriber.ReceiveSettings.MaxOutstandingMessages = 8
	subscriber.ReceiveSettings.MaxOutstandingBytes = 1 << 20
	subscriber.ReceiveSettings.MaxExtension = time.Minute

	streamCtx, stop := context.WithCancel(ctx)
	defer stop()
	lifecycleErrors := make(chan string, 1)
	err = runStream(streamCtx, r.Expires, func(ctx context.Context) error {
		return subscriber.Receive(ctx, func(deliveryCtx context.Context, m *pubsub.Message) {
			event, lifecycle, decodeErr := decodeStreamEvent(m.Attributes, m.Data)
			if lifecycle != "" {
				// A suspended/deleted Workspace subscription needs explicit
				// reconnect; don't silently claim it is still delivering.
				emit(StreamEvent{Status: lifecycle})
				select {
				case lifecycleErrors <- lifecycle:
				default:
				}
				stop()
				m.Ack()
				return
			}
			if decodeErr != nil {
				// A malformed event must not poison the receiving queue.
				emit(StreamEvent{Status: "Live · refreshing history", Reconcile: true})
				m.Ack()
				return
			}
			if event.Message == nil && event.ReactionMessage == "" { // Unknown event type: ignore for forward compatibility.
				m.Ack()
				return
			}
			if deliveryCtx.Err() == nil && emit(event) {
				m.Ack()
			} else {
				m.Nack()
			}
		})
	}, func(ctx context.Context) (time.Time, error) {
		return api.renew(ctx, r, journal)
	}, emit)
	select {
	case reason := <-lifecycleErrors:
		return errors.New(reason)
	default:
		return err
	}
}

// runStream keeps one receiver running, retries terminal receive failures with
// backoff, and renews the Workspace lease before expiry. The Pub/Sub client also
// reconnects transient stream failures internally without dropping its backlog.
func runStream(ctx context.Context, expires time.Time, receive func(context.Context) error, renew func(context.Context) (time.Time, error), emit func(StreamEvent) bool) (result error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	renewed := make(chan error, 1)
	go func() {
		for {
			wait := time.Until(expires.Add(-5 * time.Minute))
			timer := time.NewTimer(max(wait, time.Second))
			select {
			case <-ctx.Done():
				timer.Stop()
				renewed <- nil
				return
			case <-timer.C:
			}
			renewCtx, stop := context.WithTimeout(ctx, time.Minute)
			next, err := renew(renewCtx)
			stop()
			if err != nil {
				if ctx.Err() != nil {
					renewed <- nil
					return
				}
				renewed <- errors.New("live subscription could not be renewed; reconnect to resume")
				cancel()
				return
			}
			expires = next
		}
	}()
	defer func() {
		cancel()
		if err := <-renewed; result == nil {
			result = err
		}
	}()

	backoff := time.Second
	for ctx.Err() == nil {
		if !emit(StreamEvent{Status: "Live", Reconcile: true}) {
			return nil
		}
		started := time.Now()
		receiveErr := receive(ctx)
		if ctx.Err() != nil {
			break
		}
		switch status.Code(receiveErr) {
		case codes.PermissionDenied, codes.Unauthenticated:
			return errors.New("incoming-message access denied; check the gws login and reconnect")
		case codes.NotFound, codes.InvalidArgument:
			return errors.New("incoming-message subscription is unavailable; reconnect to recreate it")
		}
		if !emit(StreamEvent{Status: "Reconnecting"}) {
			return nil
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
		backoff = min(backoff*2, 30*time.Second)
	}
	return nil
}

func decodeStreamEvent(attributes map[string]string, data []byte) (StreamEvent, string, error) {
	kind, id := attributes["ce-type"], attributes["ce-id"]
	if len(data) > 1<<20 {
		return StreamEvent{}, "", errors.New("event exceeded the payload limit")
	}
	var payload struct {
		Message  Message  `json:"message"`
		Reaction Reaction `json:"reaction"`
	}
	switch kind {
	case "google.workspace.events.subscription.v1.suspended":
		return StreamEvent{}, "Subscription suspended · reconnect required", nil
	case "google.workspace.events.subscription.v1.expired":
		return StreamEvent{}, "Subscription expired · reconnect required", nil
	case messageCreated:
		if json.Unmarshal(data, &payload) != nil || MessageSpace(payload.Message.Name) == "" || payload.Message.CreateTime.IsZero() {
			return StreamEvent{}, "", errors.New("invalid new-message event")
		}
		return StreamEvent{ID: id, Message: &payload.Message}, "", nil
	case reactionCreated, reactionDeleted:
		if json.Unmarshal(data, &payload) != nil || ReactionMessage(payload.Reaction.Name) == "" {
			return StreamEvent{}, "", errors.New("invalid reaction event")
		}
		return StreamEvent{ID: id, ReactionMessage: ReactionMessage(payload.Reaction.Name)}, "", nil
	default:
		return StreamEvent{}, "", nil
	}
}

// MessageSpace validates a Chat message resource and returns its conversation.
func MessageSpace(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] != "spaces" || parts[1] == "" || parts[2] != "messages" || parts[3] == "" || strings.ContainsAny(name, "?#%") {
		return ""
	}
	return parts[0] + "/" + parts[1]
}
