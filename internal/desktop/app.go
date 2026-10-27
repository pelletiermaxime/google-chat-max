package desktop

import (
	"context"
	"strings"
	"sync"
	"time"

	"google-chat-max/internal/chat"

	"github.com/egoist/mygo/ui"
)

type Backend interface {
	Spaces(context.Context) ([]chat.Space, error)
	Messages(context.Context, string) ([]chat.Message, error)
}

type OpenBackend func(context.Context) (Backend, error)

// App's state is owned by the UI thread; workers publish through dispatch.
type App struct {
	ctx          context.Context
	cancel       context.CancelFunc
	open         OpenBackend
	backend      Backend
	dispatch     func(func())
	invalidate   func()
	theme        *ui.Theme
	demo         bool
	closed       bool
	openSettings func()

	spaces                               []chat.Space
	selected                             string
	query                                string
	showAllPrivate                       bool
	loading                              bool
	spacesError                          string
	messages                             []chat.Message
	messageError                         string
	reading                              bool
	updated                              time.Time
	historyFetched                       time.Time
	historyLoaded                        bool
	historyCache                         map[string]cachedConversation
	cacheClock                           uint64
	request                              uint64
	readCancel                           context.CancelFunc
	authorsCancel                        context.CancelFunc
	spaceIdentityCancel                  context.CancelFunc
	spaceIdentityGeneration              uint64
	duringRead                           []chat.Message
	reconcilePending                     bool
	activity                             map[string]int
	seenMessages                         map[string]struct{}
	seenOrder                            []string
	streamRunning                        bool
	streamStatus, streamError            string
	streamDone                           chan struct{}
	streamResult                         chan error
	users                                map[string]chat.User
	selfUser                             string
	avatars                              map[string]avatarEntry
	avatarQueue                          chan string
	images                               map[string]*imageEntry
	imageQueue                           chan imageJob
	imageCtx                             context.Context
	imageCancel                          context.CancelFunc
	imageClock                           uint64
	imageBytes                           int
	threadName                           string
	threadRoot                           *chat.Message
	threadMessages, threadOverlay        []chat.Message
	threadReading                        bool
	threadError                          string
	threadRequest                        uint64
	threadCancel                         context.CancelFunc
	threadReconcilePending               bool
	threadActivity                       map[string]int
	drafts                               map[string]*draft
	focusDraft                           string
	emojis                               emojiUIState
	reactions                            map[string]*reactionEntry
	reactionClock                        uint64
	reactionReads                        chan reactionReadJob
	reactionReadCtx                      context.Context
	reactionReadCancel                   context.CancelFunc
	pendingSends                         int
	sendWorkers                          sync.WaitGroup
	closing                              bool
	messageList, threadList              ui.ListState
	historyNext, threadNext              string
	olderLoading, threadOlderLoading     bool
	historyTopReached, threadTopReached  bool
	olderCancel, threadOlderCancel       context.CancelFunc
	historyExpanded, threadExpanded      bool
	newerAvailable, threadNewerAvailable bool
}

func New(open OpenBackend, dispatch func(func()), invalidate func(), demo bool) *App {
	ctx, cancel := context.WithCancel(context.Background())
	return &App{ctx: ctx, cancel: cancel, open: open, dispatch: dispatch, invalidate: invalidate, theme: appTheme(), demo: demo,
		activity: make(map[string]int), seenMessages: make(map[string]struct{}), users: make(map[string]chat.User), threadActivity: make(map[string]int), drafts: make(map[string]*draft), messageList: ui.ListState{FollowEnd: true}, threadList: ui.ListState{FollowEnd: true}, streamStatus: "Disconnected"}
}

func appTheme() *ui.Theme {
	t := ui.DarkTheme()
	t.Background = ui.Hex("#181818")
	t.Surface = ui.Hex("#2b2b2b")
	t.SurfaceHover = ui.Hex("#353535")
	t.SurfacePressed = ui.Hex("#404040")
	t.Border = ui.Hex("#333333")
	t.Accent = ui.Hex("#ededed")
	t.AccentHover = ui.Hex("#d9d9d9")
	t.AccentPressed = ui.Hex("#bfbfbf")
	t.AccentText = ui.Hex("#171717")
	t.Text = ui.Hex("#ededed")
	t.TextMuted = ui.Hex("#a3a3a3")
	t.Selection = ui.RGBA(255, 255, 255, 0.22)
	t.Focus = ui.RGBA(255, 255, 255, 0.45)
	t.Scrollbar = ui.RGBA(255, 255, 255, 0.28)
	t.FontSize = 14
	t.Radius = 8
	return t
}

func (a *App) Close() {
	a.closed = true
	a.cancel()
}

func (a *App) RefreshSpaces() {
	if a.loading || a.closed {
		return
	}
	if a.spaceIdentityCancel != nil {
		a.spaceIdentityCancel()
	}
	a.spaceIdentityGeneration++
	a.loading, a.spacesError = true, ""
	backend := a.backend
	go func() {
		var err error
		if backend == nil {
			backend, err = a.open(a.ctx)
		}
		var spaces []chat.Space
		var self string
		if err == nil {
			ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
			defer cancel()
			spaces, err = backend.Spaces(ctx)
			if b, ok := backend.(interface{ Self() string }); ok {
				self = b.Self()
			}
		}
		a.dispatch(func() {
			if a.closed {
				return
			}
			a.loading = false
			if err != nil {
				a.spacesError = err.Error()
			} else {
				// A list response may predate events received while it was
				// in flight. Never move a newly active conversation backward.
				previous := make(map[string]chat.Space, len(a.spaces))
				for _, existing := range a.spaces {
					previous[existing.Name] = existing
				}
				for i := range spaces {
					old := previous[spaces[i].Name]
					if old.LastActiveTime.After(spaces[i].LastActiveTime) {
						spaces[i].LastActiveTime = old.LastActiveTime
					}
					if spaces[i].DisplayName == "" {
						spaces[i].DisplayName = old.DisplayName
					}
					if spaces[i].AvatarURL == "" {
						spaces[i].AvatarURL = old.AvatarURL
					}
					if len(spaces[i].Participants) == 0 {
						spaces[i].Participants = old.Participants
					}
				}
				a.backend, a.spaces = backend, spaces
				a.selfUser = self
				for _, s := range spaces {
					a.rememberUsers(s)
				}
				a.Connect()
				found := false
				for _, s := range spaces {
					found = found || s.Name == a.selected
				}
				if !found {
					a.selectSpace("")
				}
				a.resolveSpaceIdentities()
			}
			a.invalidate()
		})
	}()
}

func (a *App) selectSpace(name string) {
	a.cacheConversation()
	a.resetImageLoads()
	a.resetReactionReads()
	defer a.trimConversationCache()
	a.closeThread()
	if a.authorsCancel != nil {
		a.authorsCancel()
	}
	a.historyView(false).reset()
	a.selected = name
	delete(a.activity, name)
	a.reading, a.updated = false, time.Time{}
	a.historyFetched, a.historyLoaded = time.Time{}, false
	if name != "" {
		if a.restoreConversation(name) {
			a.refreshAuthors(name, a.request)
			a.invalidate()
			return
		}
		a.refreshMessages()
	}
}

func (a *App) refreshMessages() {
	for message, e := range a.reactions {
		if chat.MessageSpace(message) == a.selected {
			e.dirty = true
			if e.intent == nil {
				e.err = ""
			}
		}
	}
	a.refreshHistory(false)
}

func (a *App) selectedSpace() chat.Space {
	for _, s := range a.spaces {
		if s.Name == a.selected {
			return s
		}
	}
	return chat.Space{}
}

func (a *App) matches(s chat.Space) bool {
	q := strings.ToLower(strings.TrimSpace(a.query))
	text := s.Title() + " " + s.Name
	for _, u := range s.Participants {
		text += " " + u.DisplayName + " " + u.Email
	}
	return q == "" || strings.Contains(strings.ToLower(text), q)
}
