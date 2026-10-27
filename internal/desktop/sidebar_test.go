package desktop

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"testing"
	"time"

	"google-chat-max/internal/chat"
	"google-chat-max/internal/settings"

	"github.com/egoist/mygo/ui"
)

func sidebarFixture() []chat.Space {
	var spaces []chat.Space
	for i := range 14 {
		spaces = append(spaces, chat.Space{
			Name: fmt.Sprintf("spaces/private-%02d", i), DisplayName: fmt.Sprintf("Private %02d", i),
			SpaceType: "DIRECT_MESSAGE", LastActiveTime: time.Date(2026, 10, 26, 12, i, 0, 0, time.UTC),
		})
	}
	spaces = append(spaces,
		chat.Space{Name: "spaces/meeting", DisplayName: "A very old meeting", SpaceType: "SPACE", SpaceThreadingState: "UNTHREADED_MESSAGES", LastActiveTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		chat.Space{Name: "spaces/group", DisplayName: "Recent group conversation", SpaceType: "GROUP_CHAT", LastActiveTime: time.Date(2026, 10, 26, 13, 0, 0, 0, time.UTC)},
		chat.Space{Name: "spaces/team", DisplayName: "Team space", SpaceType: "SPACE", SpaceThreadingState: "THREADED_MESSAGES"},
		chat.Space{Name: "spaces/topics", DisplayName: "Legacy topic space", SpaceType: "SPACE", SpaceThreadingState: "GROUPED_MESSAGES"},
		chat.Space{Name: "spaces/bot", DisplayName: "App conversation", SpaceType: "DIRECT_MESSAGE", SingleUserBotDM: true},
	)
	return spaces
}

func TestSidebarGroupsMeetingChatsAndLimitsPrivateRecency(t *testing.T) {
	a := New(nil, func(func()) {}, func() {}, false)
	defer a.Close()
	a.spaces = sidebarFixture()
	groups := a.sidebarGroups()
	if len(groups[0].items) != 10 || groups[0].total != 16 || groups[0].items[0].Name != "spaces/group" || groups[0].items[9].Name != "spaces/private-05" {
		t.Fatalf("private section did not select the 10 most recent: %#v", groups[0])
	}
	if len(groups[1].items) != 2 || len(groups[2].items) != 1 || groups[2].items[0].Name != "spaces/bot" {
		t.Fatal("meeting chats, regular spaces, and app DMs were not separated")
	}
	a.query = "very old meeting"
	groups = a.sidebarGroups()
	if len(groups[0].items) != 1 || groups[0].items[0].Name != "spaces/meeting" || len(groups[1].items) != 0 {
		t.Fatal("search must find old meeting chats in Private messages")
	}
}

func TestNativeShowMoreAndSearchReachOlderPrivateChats(t *testing.T) {
	a := New(nil, func(func()) {}, func() {}, false)
	defer a.Close()
	a.spaces = sidebarFixture()
	tt := ui.NewTester(a.View, 1180, 1100)
	tt.Frame()
	if path := os.Getenv("CHAT_MAX_SIDEBAR_SCREENSHOT"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if tt.HasText("A very old meeting") || !tt.HasText("Team space") || !tt.HasText("Private messages") {
		t.Fatal("initial sidebar is not compact and correctly grouped")
	}
	if err := tt.Click("Show more (6 older)"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("A very old meeting") {
		t.Fatal("Show more did not reveal older private chats")
	}
	if err := tt.Click("Show only latest 10"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("A very old meeting") {
		t.Fatal("collapse did not restore the private limit")
	}
	if err := tt.Click("Find a conversation"); err != nil {
		t.Fatal(err)
	}
	tt.Type("very old meeting")
	if !tt.HasText("A very old meeting") || tt.HasText("Team space") {
		t.Fatal("search did not bypass the recent limit")
	}
}

type delayedSpaces struct{ response chan chan []chat.Space }

func (b delayedSpaces) Spaces(context.Context) ([]chat.Space, error) {
	result := make(chan []chat.Space, 1)
	b.response <- result
	return <-result, nil
}
func (b delayedSpaces) Messages(context.Context, string) ([]chat.Message, error) { return nil, nil }

func TestListRefreshCannotRegressLiveRecency(t *testing.T) {
	callbacks := make(chan func(), 2)
	b := delayedSpaces{response: make(chan chan []chat.Space, 1)}
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, false)
	defer a.Close()
	a.spaces = sidebarFixture()
	a.backend = b
	a.RefreshSpaces()
	response := <-b.response
	m := liveMessage("spaces/meeting", "new", "Activity during list refresh", 0)
	a.applyStreamEvent(chat.StreamEvent{Message: &m})
	response <- sidebarFixture()
	applyNext(t, callbacks)
	if a.sidebarGroups()[0].items[0].Name != "spaces/meeting" || a.activity["spaces/meeting"] != 1 {
		t.Fatal("an older list snapshot erased live recency or activity")
	}
}

func TestLiveSidebarMetadata(t *testing.T) {
	if os.Getenv("CHAT_MAX_LIVE_TEST") != "1" {
		t.Skip("explicit live metadata opt-in required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	config, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	project := os.Getenv("CHAT_MAX_TEST_PROJECT")
	if project == "" {
		project = config.ProjectID
	}
	if project == "" {
		t.Skip("configure a project in the app or set CHAT_MAX_TEST_PROJECT")
	}
	c, err := chat.Open(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	spaces, err := c.Spaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a := New(nil, func(func()) {}, func() {}, false)
	defer a.Close()
	a.spaces = spaces
	groups := a.sidebarGroups()
	withActivity := 0
	for _, s := range spaces {
		if !s.LastActiveTime.IsZero() {
			withActivity++
		}
	}
	if len(spaces) == 0 || withActivity == 0 || len(groups[0].items) > recentPrivateLimit {
		t.Fatal("missing recency metadata or private limit")
	}
	t.Logf("Live metadata: %d conversations; private %d/%d shown; %d regular spaces; %d apps; %d activity timestamps", len(spaces), len(groups[0].items), groups[0].total, groups[1].total, groups[2].total, withActivity)
}

func TestIncomingPrivateActivityMovesIntoRecentTen(t *testing.T) {
	a := New(nil, func(func()) {}, func() {}, false)
	defer a.Close()
	a.spaces = sidebarFixture()
	m := liveMessage("spaces/meeting", "new", "Incoming meeting reply", 0)
	a.applyStreamEvent(chat.StreamEvent{Message: &m})
	groups := a.sidebarGroups()
	if len(groups[0].items) != 10 || groups[0].items[0].Name != "spaces/meeting" || a.activity["spaces/meeting"] != 1 {
		t.Fatal("incoming activity did not promote an old meeting into the recent private section")
	}
	// Redeliveries and older out-of-order events must not move it backward.
	older := m
	older.Name += "-older"
	older.CreateTime = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	a.applyStreamEvent(chat.StreamEvent{Message: &older})
	if a.sidebarGroups()[0].items[0].LastActiveTime != m.CreateTime {
		t.Fatal("out-of-order delivery regressed the conversation's activity time")
	}
}
