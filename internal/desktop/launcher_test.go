package desktop

import (
	"context"
	"errors"
	"fmt"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
	"google-chat-max/internal/settings"
)

func readyAuth(context.Context) chat.AuthStatus {
	return chat.AuthStatus{GWSInstalled: true, ClientConfigured: true, LoginFound: true, SuggestedProject: "project-one"}
}

func TestConfiguredStartupAutomaticallyReceivesWithoutOptIn(t *testing.T) {
	callbacks := make(chan func(), 16)
	b := &fakeLiveBackend{Backend: Demo(), events: make(chan deliveredEvent), stopped: make(chan struct{}), started: make(chan struct{}, 2)}
	l := NewLauncher(LauncherOptions{
		Load: func() (settings.Config, error) { return settings.Config{ProjectID: "project-one"}, nil },
		Open: func(context.Context, string) (Backend, error) { return b, nil },
	}, func(fn func()) { callbacks <- fn }, func() {})
	l.Start()
	applyNext(t, callbacks)
	applyNext(t, callbacks)
	select {
	case <-b.started:
	case <-time.After(time.Second):
		t.Fatal("normal startup did not start incoming messages automatically")
	}
	l.app.RefreshSpaces()
	applyNext(t, callbacks)
	if len(b.started) != 0 {
		t.Fatal("refresh created a second receiver")
	}
	closed := false
	l.Shutdown(func(error) { closed = true })
	<-b.stopped
	deadline := time.After(time.Second)
	for !closed {
		select {
		case fn := <-callbacks:
			fn()
		case <-deadline:
			t.Fatal("automatic connection did not shut down cleanly")
		}
	}
}

func TestSetupScreenChecksToolsAndSavesBeforeOpeningChat(t *testing.T) {
	callbacks := make(chan func(), 16)
	checked := make(chan chan chat.AuthStatus, 1)
	saved, opened := make(chan settings.Config, 1), make(chan string, 1)
	l := NewLauncher(LauncherOptions{
		Load: func() (settings.Config, error) { return settings.Config{}, nil },
		Save: func(config settings.Config) error { saved <- config; return nil },
		CheckAuth: func(ctx context.Context) chat.AuthStatus {
			result := make(chan chat.AuthStatus, 1)
			checked <- result
			select {
			case status := <-result:
				return status
			case <-ctx.Done():
				return chat.AuthStatus{}
			}
		},
		Open: func(_ context.Context, project string) (Backend, error) { opened <- project; return Demo(), nil },
	}, func(fn func()) { callbacks <- fn }, func() {})
	defer l.Shutdown(func(error) {})
	l.Start()
	applyNext(t, callbacks)
	check := <-checked
	tt := ui.NewTester(l.View, 1180, 780)
	tt.Frame()
	if l.app != nil || len(opened) != 0 || !tt.HasText("Set up Google Chat Max") || !tt.HasText("Checking authentication tools") {
		t.Fatal("unconfigured startup opened Chat or hid setup while tool checks were pending")
	}
	check <- readyAuth(context.Background())
	applyNext(t, callbacks)
	tt.Frame()
	if l.projectInput != "project-one" || !tt.HasText("Saved Google login found") {
		t.Fatal("setup did not show the login check and suggested project")
	}
	if path := os.Getenv("CHAT_MAX_SETUP_SCREENSHOT"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tt.Image()); err != nil {
			f.Close()
			t.Fatal(err)
		}
		f.Close()
	}
	if err := tt.Click("Save and open Chat"); err != nil {
		t.Fatal(err)
	}
	config := <-saved
	if config.ProjectID != "project-one" || len(opened) != 0 {
		t.Fatal("Chat opened before project configuration was saved")
	}
	applyNext(t, callbacks)
	if project := <-opened; project != config.ProjectID {
		t.Fatal("backend did not receive the saved project")
	}
	applyNext(t, callbacks)
	tt.Frame()
	if l.setupVisible || l.app == nil || !tt.HasText("Conversations") || !tt.HasText("Settings") {
		t.Fatal("saving setup did not open the native chat view")
	}
}

func TestConfiguredStartupSkipsSlowAuthCheckAndDemoSkipsSetup(t *testing.T) {
	for _, demo := range []bool{false, true} {
		t.Run(fmt.Sprint(demo), func(t *testing.T) {
			callbacks := make(chan func(), 8)
			opened := make(chan string, 1)
			l := NewLauncher(LauncherOptions{
				Demo: demo,
				Load: func() (settings.Config, error) {
					if demo {
						t.Error("demo read host settings")
					}
					return settings.Config{ProjectID: "project-one"}, nil
				},
				CheckAuth: func(context.Context) chat.AuthStatus {
					t.Error("configured startup repeated the setup auth check")
					return chat.AuthStatus{}
				},
				Open: func(_ context.Context, project string) (Backend, error) { opened <- project; return Demo(), nil },
			}, func(fn func()) { callbacks <- fn }, func() {})
			defer l.Shutdown(func(error) {})
			l.Start()
			if !demo {
				applyNext(t, callbacks)
				if project := <-opened; project != "project-one" {
					t.Fatal("configured project was lost")
				}
			}
			applyNext(t, callbacks)
			if l.app == nil || l.setupVisible {
				t.Fatal("configured/demo startup entered setup")
			}
		})
	}
}

func TestSetupMissingToolsAndInvalidProjectPreventSave(t *testing.T) {
	callbacks := make(chan func(), 8)
	saves := 0
	l := NewLauncher(LauncherOptions{
		Load:      func() (settings.Config, error) { return settings.Config{}, nil },
		Save:      func(settings.Config) error { saves++; return nil },
		CheckAuth: func(context.Context) chat.AuthStatus { return chat.AuthStatus{} },
	}, func(fn func()) { callbacks <- fn }, func() {})
	defer l.Shutdown(func(error) {})
	l.Start()
	applyNext(t, callbacks)
	applyNext(t, callbacks)
	l.projectInput = "project-one"
	tt := ui.NewTester(l.View, 760, 780)
	tt.Frame()
	if !tt.HasText("gws) was not found") {
		t.Fatal("missing authentication tool was not explained")
	}
	if err := tt.Click("Save and open Chat"); err != nil {
		t.Fatal(err)
	}
	if saves != 0 || l.app != nil {
		t.Fatal("setup continued without the required tool/login")
	}
	l.auth = readyAuth(context.Background())
	l.projectInput = "Invalid Project"
	tt.Frame()
	if err := tt.Click("Save and open Chat"); err != nil {
		t.Fatal(err)
	}
	if saves != 0 || l.saving {
		t.Fatal("invalid project was accepted")
	}
}

func TestProjectSwitchWaitsForCleanupAndCloseCannotReopenApp(t *testing.T) {
	for _, closeDuringSwitch := range []bool{false, true} {
		t.Run(fmt.Sprint(closeDuringSwitch), func(t *testing.T) {
			callbacks := make(chan func(), 32)
			opened := make(chan string, 2)
			b := &fakeLiveBackend{Backend: Demo(), events: make(chan deliveredEvent), stopped: make(chan struct{}), cleanupGate: make(chan struct{})}
			l := NewLauncher(LauncherOptions{
				Load: func() (settings.Config, error) { return settings.Config{ProjectID: "project-one"}, nil },
				Save: func(settings.Config) error { return nil }, CheckAuth: readyAuth,
				Open: func(_ context.Context, project string) (Backend, error) {
					opened <- project
					if project == "project-one" {
						return b, nil
					}
					return Demo(), nil
				},
			}, func(fn func()) { callbacks <- fn }, func() {})
			l.Start()
			applyNext(t, callbacks)
			<-opened
			applyNext(t, callbacks)
			old := l.app
			l.showSettings()
			applyNext(t, callbacks)
			l.projectInput = "project-two"
			l.saveProject()
			applyNext(t, callbacks)
			<-b.stopped
			if !l.switching || len(opened) != 0 {
				t.Fatal("new project opened before old live cleanup completed")
			}
			closed := false
			if closeDuringSwitch {
				l.Shutdown(func(error) { closed = true })
			}
			close(b.cleanupGate)
			deadline := time.After(3 * time.Second)
			for l.switching {
				select {
				case fn := <-callbacks:
					fn()
				case <-deadline:
					t.Fatal("project-switch cleanup did not finish")
				}
			}
			if closeDuringSwitch {
				if !closed || !l.closed || len(opened) != 0 {
					t.Fatal("closing during project switch reopened Chat or lost shutdown completion")
				}
			} else {
				if project := <-opened; project != "project-two" {
					t.Fatal("switched backend has the wrong project")
				}
				applyNext(t, callbacks)
				if l.app == old || !old.closed || l.config.ProjectID != "project-two" || l.setupVisible {
					t.Fatal("project switch did not replace the closed session")
				}
				l.Shutdown(func(error) {})
			}
		})
	}
}

func TestSetupAuthInstructionsAreSpecificToMissingClient(t *testing.T) {
	l := NewLauncher(LauncherOptions{}, func(func()) {}, func() {})
	defer l.Shutdown(func(error) {})
	l.loading, l.setupVisible = false, true
	l.auth = chat.AuthStatus{GWSInstalled: true}
	tt := ui.NewTester(l.View, 1180, 780)
	tt.Frame()
	texts := strings.Join(tt.Texts(), "\n")
	if !strings.Contains(texts, "gws auth setup") || !strings.Contains(texts, "gws auth login") || !strings.Contains(texts, "gcloud") {
		t.Fatal("first-time authentication instructions are incomplete")
	}
	tt.SetSize(760, 520)
	tt.Frame()
	tt.Scroll(600, 300, 0, 1000)
	tt.Frame()
	button, ok := tt.Find("Save and open Chat")
	if !ok || button.Y < 0 || button.Y+button.H > 520 {
		t.Fatal("setup controls cannot be reached in the minimum window size")
	}
}

func TestSavingSameProjectRetriesFailedAuthentication(t *testing.T) {
	callbacks := make(chan func(), 16)
	opens := 0
	l := NewLauncher(LauncherOptions{
		Load: func() (settings.Config, error) { return settings.Config{ProjectID: "project-one"}, nil },
		Save: func(settings.Config) error { return nil }, CheckAuth: readyAuth,
		Open: func(context.Context, string) (Backend, error) {
			opens++
			if opens == 1 {
				return nil, errors.New("saved login needs updating")
			}
			return Demo(), nil
		},
	}, func(fn func()) { callbacks <- fn }, func() {})
	defer l.Shutdown(func(error) {})
	l.Start()
	applyNext(t, callbacks)
	applyNext(t, callbacks)
	old := l.app
	if old.spacesError == "" {
		t.Fatal("fixture did not fail authentication")
	}
	l.showSettings()
	applyNext(t, callbacks)
	l.saveProject()
	applyNext(t, callbacks)
	applyNext(t, callbacks)
	if l.app == old || !old.closed || l.app.backend == nil || l.app.spacesError != "" {
		t.Fatal("saving setup after fixing authentication did not open a fresh client")
	}
}
