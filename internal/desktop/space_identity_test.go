package desktop

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

type profileCall struct {
	ctx  context.Context
	done chan string
}

type spaceIdentityCall struct {
	ctx   context.Context
	space chat.Space
	done  chan chat.Space
}

type controlledSpaceIdentities struct {
	Backend
	spaces   []chat.Space
	profiles chan profileCall
	calls    chan spaceIdentityCall
}

func (b controlledSpaceIdentities) Spaces(context.Context) ([]chat.Space, error) {
	return slices.Clone(b.spaces), nil
}

func (b controlledSpaceIdentities) ResolveIdentity(ctx context.Context) (string, error) {
	call := profileCall{ctx: ctx, done: make(chan string, 1)}
	b.profiles <- call
	select {
	case self := <-call.done:
		return self, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (b controlledSpaceIdentities) ResolveSpaceIdentity(ctx context.Context, space chat.Space) (chat.Space, error) {
	call := spaceIdentityCall{ctx: ctx, space: space, done: make(chan chat.Space, 1)}
	b.calls <- call
	select {
	case resolved := <-call.done:
		return resolved, nil
	case <-ctx.Done():
		return space, ctx.Err()
	}
}

func spaceIdentityFixture(count int) controlledSpaceIdentities {
	b := controlledSpaceIdentities{Backend: Demo(), profiles: make(chan profileCall, 2), calls: make(chan spaceIdentityCall, 40)}
	for i := range count {
		b.spaces = append(b.spaces, chat.Space{Name: fmt.Sprintf("spaces/dm-%02d", i), SpaceType: "DIRECT_MESSAGE", LastActiveTime: time.Unix(int64(i), 0)})
	}
	b.spaces = append(b.spaces, chat.Space{Name: "spaces/engineering", DisplayName: "Engineering", SpaceType: "SPACE"})
	return b
}

func nextSpaceIdentity(t *testing.T, calls <-chan spaceIdentityCall) spaceIdentityCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(3 * time.Second):
		t.Fatal("background identity worker did not start")
		return spaceIdentityCall{}
	}
}

func TestStartupShowsListBeforeIdentityAndPublishesProgressively(t *testing.T) {
	callbacks := make(chan func(), 64)
	b := spaceIdentityFixture(8)
	a := New(func(context.Context) (Backend, error) { return b, nil }, func(fn func()) { callbacks <- fn }, func() {}, false)
	defer a.Close()
	a.RefreshSpaces()
	applyNext(t, callbacks)
	profile := <-b.profiles
	if a.loading || len(a.spaces) != 9 {
		t.Fatal("conversation list waited for the blocked profile lookup")
	}
	tt := ui.NewTester(a.View, 1180, 780)
	tt.Frame()
	if !tt.HasText("Engineering") || !tt.HasText("Direct message · dm-07") {
		t.Fatal("initial sidebar is unavailable during identity resolution")
	}
	if err := tt.Click("Engineering"); err != nil {
		t.Fatal(err)
	}
	applyNext(t, callbacks)
	if len(a.messages) == 0 || a.reading {
		t.Fatal("conversation could not load while profile lookup was pending")
	}
	profile.done <- "users/self"
	applyNext(t, callbacks)
	if a.selfUser != "users/self" {
		t.Fatal("background profile did not publish the authenticated identity")
	}
	var pending []spaceIdentityCall
	for range spaceIdentityWorkers {
		call := nextSpaceIdentity(t, b.calls)
		if call.space.LastActiveTime.Before(b.spaces[4].LastActiveTime) {
			t.Fatal("background loading did not prioritize the four most recent chats")
		}
		pending = append(pending, call)
	}
	if len(b.calls) != 0 {
		t.Fatal("more than four concurrent membership lookups started")
	}
	call := pending[0]
	m := liveMessage(call.space.Name, "new", "Live activity", 1)
	m.CreateTime = time.Now()
	a.applyStreamEvent(chat.StreamEvent{Message: &m})
	resolved := call.space
	resolved.DisplayName = "Resolved Alex"
	resolved.Participants = []chat.User{{Name: "users/alex", DisplayName: "Alex"}}
	call.done <- resolved
	applyNext(t, callbacks)
	tt.Frame()
	if !tt.HasText("Resolved Alex") || a.selected != "spaces/engineering" || a.activity[call.space.Name] != 1 {
		t.Fatal("progressive identity update lost selection or unread activity")
	}
	for _, space := range a.spaces {
		if space.Name == call.space.Name && !space.LastActiveTime.Equal(m.CreateTime) {
			t.Fatal("late membership response moved live activity backward")
		}
	}
	a.Close()
	for _, call := range pending[1:] {
		if call.ctx.Err() == nil {
			t.Fatal("closing did not cancel background membership requests")
		}
	}
}

func TestBackgroundIdentitiesStayWithinConversationBudget(t *testing.T) {
	callbacks := make(chan func(), 64)
	b := spaceIdentityFixture(40)
	a := New(func(context.Context) (Backend, error) { return b, nil }, func(fn func()) { callbacks <- fn }, func() {}, false)
	defer a.Close()
	a.RefreshSpaces()
	applyNext(t, callbacks)
	profile := <-b.profiles
	profile.done <- "users/self"
	applyNext(t, callbacks)
	for range spaceIdentityLimit {
		call := nextSpaceIdentity(t, b.calls)
		if call.space.LastActiveTime.Before(b.spaces[8].LastActiveTime) {
			t.Fatal("identity work exceeded the 32 most recent private chats")
		}
		resolved := call.space
		resolved.DisplayName = "Resolved " + resolved.Name
		call.done <- resolved
		applyNext(t, callbacks)
	}
	if len(b.calls) != 0 {
		t.Fatal("identity work exceeded its conversation budget")
	}
	resolved := 0
	for _, space := range a.spaces {
		if space.SpaceType == "DIRECT_MESSAGE" && space.DisplayName != "" {
			resolved++
		}
	}
	if resolved != spaceIdentityLimit {
		t.Fatalf("resolved %d identities instead of %d", resolved, spaceIdentityLimit)
	}
}

func TestRefreshRejectsQueuedOldIdentityUpdate(t *testing.T) {
	callbacks := make(chan func(), 16)
	b := spaceIdentityFixture(1)
	a := New(func(context.Context) (Backend, error) { return b, nil }, func(fn func()) { callbacks <- fn }, func() {}, false)
	defer a.Close()
	a.RefreshSpaces()
	applyNext(t, callbacks)
	profile := <-b.profiles
	profile.done <- "users/self"
	applyNext(t, callbacks)
	call := nextSpaceIdentity(t, b.calls)
	resolved := call.space
	resolved.DisplayName = "Stale name"
	call.done <- resolved
	var late func()
	select {
	case late = <-callbacks:
	case <-time.After(3 * time.Second):
		t.Fatal("old identity result was not published")
	}
	a.RefreshSpaces()
	late()
	applyNext(t, callbacks)
	for _, space := range a.spaces {
		if space.DisplayName == "Stale name" {
			t.Fatal("refresh accepted a queued identity update from the prior generation")
		}
	}
}
