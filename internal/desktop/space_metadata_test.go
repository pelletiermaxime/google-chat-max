package desktop

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

type metadataResult struct {
	space chat.Space
	err   error
}

type metadataCall struct {
	ctx  context.Context
	name string
	done chan metadataResult
}

type controlledSpaceMetadata struct {
	Backend
	calls chan metadataCall
}

func (b controlledSpaceMetadata) Space(ctx context.Context, name string) (chat.Space, error) {
	call := metadataCall{ctx: ctx, name: name, done: make(chan metadataResult, 1)}
	select {
	case b.calls <- call:
	case <-ctx.Done():
		return chat.Space{}, ctx.Err()
	}
	select {
	case result := <-call.done:
		return result.space, result.err
	case <-ctx.Done():
		return chat.Space{}, ctx.Err()
	}
}

func nextMetadata(t *testing.T, calls <-chan metadataCall) metadataCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(3 * time.Second):
		t.Fatal("conversation metadata lookup did not start")
		return metadataCall{}
	}
}

func metadataFixture() (*App, controlledSpaceMetadata, chan func()) {
	callbacks := make(chan func(), 64)
	b := controlledSpaceMetadata{Backend: Demo(), calls: make(chan metadataCall, 16)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, false)
	a.backend = b
	a.spaces, _ = b.Spaces(context.Background())
	a.selected = "spaces/engineering"
	return a, b, callbacks
}

func TestLiveConversationGetsItsNameWithoutRefresh(t *testing.T) {
	a, b, callbacks := metadataFixture()
	defer a.Close()
	m := liveMessage("spaces/new-meeting", "first", "New meeting message", 1)
	a.applyStreamEvent(chat.StreamEvent{Message: &m})
	call := nextMetadata(t, b.calls)
	tt := ui.NewTester(a.View, 1180, 780)
	if !tt.HasText("Conversation · new-meeting") || a.activity[call.name] != 1 {
		t.Fatal("message delivery waited for conversation metadata")
	}
	a.applyStreamEvent(chat.StreamEvent{Message: &m})
	newer := liveMessage(call.name, "second", "Another message while loading", 2)
	a.applyStreamEvent(chat.StreamEvent{Message: &newer})
	if len(b.calls) != 0 {
		t.Fatal("live messages duplicated an in-flight metadata lookup")
	}
	call.done <- metadataResult{space: chat.Space{Name: call.name, DisplayName: "Scrum dev - Oct 27", SpaceType: "SPACE", SpaceThreadingState: "UNTHREADED_MESSAGES", LastActiveTime: m.CreateTime}}
	applyNext(t, callbacks)
	tt.Frame()
	if !tt.HasText("Scrum dev - Oct 27") || tt.HasText("Conversation · new-meeting") || a.activity[call.name] != 2 || a.selected != "spaces/engineering" {
		t.Fatal("metadata did not replace the fallback name or preserved state incorrectly")
	}
	var meeting chat.Space
	for _, s := range a.spaces {
		if s.Name == call.name {
			meeting = s
		}
	}
	if !privateConversation(meeting) || !meeting.LastActiveTime.Equal(newer.CreateTime) {
		t.Fatal("metadata changed meeting classification or moved live activity backward")
	}
	a.applyStreamEvent(chat.StreamEvent{Message: &newer})
	if a.spaceMetadataActive != 0 || len(a.spaceMetadataPending) != 0 || len(b.calls) != 0 {
		t.Fatal("resolved conversation started another metadata lookup")
	}
}

func TestLiveMetadataFailureRetriesOnlyOnAnotherEvent(t *testing.T) {
	a, b, callbacks := metadataFixture()
	defer a.Close()
	m := liveMessage("spaces/new-meeting", "first", "Message", 1)
	a.applyStreamEvent(chat.StreamEvent{Message: &m})
	call := nextMetadata(t, b.calls)
	call.done <- metadataResult{err: errors.New("temporary failure")}
	applyNext(t, callbacks)
	if len(b.calls) != 0 || a.spaceMetadataActive != 0 {
		t.Fatal("failed metadata lookup started a retry loop")
	}
	a.applyStreamEvent(chat.StreamEvent{Message: &m})
	retry := nextMetadata(t, b.calls)
	retry.done <- metadataResult{space: chat.Space{Name: retry.name, DisplayName: "Meeting", SpaceType: "SPACE"}}
	applyNext(t, callbacks)
	if a.activity[call.name] != 1 {
		t.Fatal("retry counted a duplicate delivery as new activity")
	}
}

func TestLiveMetadataBoundsAndCloseCancellation(t *testing.T) {
	a, b, _ := metadataFixture()
	for i := range 1100 {
		m := liveMessage(fmt.Sprintf("spaces/live-%04d", i), "first", "Burst", 1)
		a.applyStreamEvent(chat.StreamEvent{Message: &m})
	}
	var calls []metadataCall
	for range spaceIdentityWorkers {
		calls = append(calls, nextMetadata(t, b.calls))
	}
	if len(b.calls) != 0 || a.spaceMetadataActive != spaceIdentityWorkers || len(a.spaceMetadataPending) > 1000 || len(a.spaces) != 1000 {
		t.Fatal("live conversation discovery exceeded its concurrency or retained-state limits")
	}
	a.Close()
	for _, call := range calls {
		if call.ctx.Err() == nil {
			t.Fatal("closing did not cancel the metadata lookup")
		}
	}
}

type metadataDuringRefresh struct {
	controlledSpaceMetadata
	lists chan chan []chat.Space
}

func (b metadataDuringRefresh) Spaces(context.Context) ([]chat.Space, error) {
	result := make(chan []chat.Space, 1)
	b.lists <- result
	return <-result, nil
}

func TestLiveDiscoverySurvivesConcurrentSpaceRefresh(t *testing.T) {
	a, metadata, callbacks := metadataFixture()
	defer a.Close()
	b := metadataDuringRefresh{controlledSpaceMetadata: metadata, lists: make(chan chan []chat.Space, 1)}
	a.backend = b
	a.RefreshSpaces()
	list := <-b.lists
	m := liveMessage("spaces/new-meeting", "first", "Message during refresh", 1)
	a.applyStreamEvent(chat.StreamEvent{Message: &m})
	call := nextMetadata(t, metadata.calls)
	a.selected = call.name
	list <- []chat.Space{{Name: "spaces/engineering", DisplayName: "Engineering", SpaceType: "SPACE"}}
	applyNext(t, callbacks)
	call.done <- metadataResult{space: chat.Space{Name: call.name, DisplayName: "Scrum dev - Oct 27", SpaceType: "SPACE", SpaceThreadingState: "UNTHREADED_MESSAGES"}}
	applyNext(t, callbacks)
	if a.selected != call.name || a.selectedSpace().DisplayName != "Scrum dev - Oct 27" || a.activity[call.name] != 1 {
		t.Fatal("an older space snapshot removed or reset a live-discovered conversation")
	}
}

func TestRefreshRejectsStaleLiveMetadata(t *testing.T) {
	a, metadata, callbacks := metadataFixture()
	defer a.Close()
	b := metadataDuringRefresh{controlledSpaceMetadata: metadata, lists: make(chan chan []chat.Space, 1)}
	a.backend = b
	m := liveMessage("spaces/new-meeting", "first", "Message", 1)
	a.applyStreamEvent(chat.StreamEvent{Message: &m})
	call := nextMetadata(t, metadata.calls)
	a.RefreshSpaces()
	list := <-b.lists
	list <- []chat.Space{{Name: call.name, DisplayName: "Current name", SpaceType: "SPACE"}}
	applyNext(t, callbacks)
	call.done <- metadataResult{space: chat.Space{Name: call.name, DisplayName: "Stale name", SpaceType: "SPACE"}}
	applyNext(t, callbacks)
	if a.spaces[0].DisplayName != "Current name" {
		t.Fatal("an older live metadata lookup replaced refreshed metadata")
	}
}

type metadataWithIdentities struct {
	controlledSpaceMetadata
	identities controlledSpaceIdentities
}

func (b metadataWithIdentities) ResolveIdentity(ctx context.Context) (string, error) {
	return b.identities.ResolveIdentity(ctx)
}

func (b metadataWithIdentities) ResolveSpaceIdentity(ctx context.Context, space chat.Space) (chat.Space, error) {
	return b.identities.ResolveSpaceIdentity(ctx, space)
}

func TestLivePrivateConversationResolvesMemberNames(t *testing.T) {
	a, metadata, callbacks := metadataFixture()
	defer a.Close()
	b := metadataWithIdentities{controlledSpaceMetadata: metadata, identities: spaceIdentityFixture(0)}
	a.backend = b
	m := liveMessage("spaces/new-group", "first", "New group message", 1)
	a.applyStreamEvent(chat.StreamEvent{Message: &m})
	call := nextMetadata(t, metadata.calls)
	call.done <- metadataResult{space: chat.Space{Name: call.name, SpaceType: "GROUP_CHAT"}}
	profile := <-b.identities.profiles
	profile.done <- "users/self"
	identity := nextSpaceIdentity(t, b.identities.calls)
	resolved := identity.space
	resolved.DisplayName = "Alex, Sam"
	resolved.Participants = []chat.User{{Name: "users/alex", DisplayName: "Alex"}}
	identity.done <- resolved
	applyNext(t, callbacks)
	if a.selfUser != "users/self" || a.users["users/alex"].DisplayName != "Alex" {
		t.Fatal("new private conversation lost profile or member identities")
	}
	tt := ui.NewTester(a.View, 1180, 780)
	if !tt.HasText("Alex, Sam") || tt.HasText("Conversation · new-group") {
		t.Fatal("live private conversation did not acquire its member names")
	}
}
