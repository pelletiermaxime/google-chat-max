package desktop

import (
	"context"
	"testing"

	"google-chat-max/internal/chat"
)

type pendingMembers struct {
	ctx  context.Context
	done chan []chat.User
}

type delayedMembersBackend struct {
	controlledBackend
	memberships chan pendingMembers
}

func (b delayedMembersBackend) Members(ctx context.Context, _ string) ([]chat.User, error) {
	r := pendingMembers{ctx: ctx, done: make(chan []chat.User, 1)}
	b.memberships <- r
	// Deliberately return late, even after cancellation, to exercise the guard.
	return <-r.done, nil
}

func TestMessagesAppearBeforeMembersAndLateMembersCannotReplaceNames(t *testing.T) {
	callbacks := make(chan func(), 8)
	b := delayedMembersBackend{controlledBackend: controlledBackend{reads: make(chan pendingRead, 2)}, memberships: make(chan pendingMembers, 2)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, false)
	defer a.Close()
	a.backend = b
	a.selectSpace("spaces/first")
	firstRead, firstMembers := <-b.reads, <-b.memberships
	firstRead.done <- []chat.Message{{Name: "spaces/first/messages/m", Sender: chat.User{Name: "users/alex"}}}
	applyNext(t, callbacks)
	if a.reading || len(a.messages) != 1 {
		t.Fatal("messages waited for the pending membership request")
	}
	a.selectSpace("spaces/second")
	secondRead, secondMembers := <-b.reads, <-b.memberships
	if firstMembers.ctx.Err() == nil {
		t.Fatal("switching conversations did not cancel the pending membership request")
	}
	secondRead.done <- []chat.Message{{Name: "spaces/second/messages/m", Sender: chat.User{Name: "users/alex"}}}
	applyNext(t, callbacks)
	firstMembers.done <- []chat.User{{Name: "users/alex", DisplayName: "Stale name"}}
	applyNext(t, callbacks)
	if a.author(a.messages[0]).DisplayName != "" {
		t.Fatal("late membership result from the old conversation changed the current name")
	}
	secondMembers.done <- []chat.User{{Name: "users/alex", DisplayName: "Alex"}}
	applyNext(t, callbacks)
	if a.author(a.messages[0]).DisplayName != "Alex" {
		t.Fatal("background membership result did not enrich the visible sender")
	}
}
