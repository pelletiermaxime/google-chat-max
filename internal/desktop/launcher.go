package desktop

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
	"google-chat-max/internal/settings"
)

type LauncherOptions struct {
	Demo      bool
	Load      func() (settings.Config, error)
	Save      func(settings.Config) error
	CheckAuth func(context.Context) chat.AuthStatus
	Open      func(context.Context, string) (Backend, error)
}

// Launcher owns first-run setup and project switches. Each App keeps an
// immutable backend/project until its normal shutdown and cleanup completes.
type Launcher struct {
	options                                            LauncherOptions
	ctx                                                context.Context
	cancel                                             context.CancelFunc
	dispatch                                           func(func())
	invalidate                                         func()
	theme                                              *ui.Theme
	app                                                *App
	config                                             settings.Config
	projectInput, error                                string
	loading, setupVisible, checking, saving, switching bool
	closing, closed                                    bool
	focused                                            bool
	checkGeneration                                    uint64
	checkCancel                                        context.CancelFunc
	auth                                               chat.AuthStatus
	shutdownDone                                       func(error)
}

func NewLauncher(options LauncherOptions, dispatch func(func()), invalidate func()) *Launcher {
	if options.Load == nil {
		options.Load = settings.Load
	}
	if options.Save == nil {
		options.Save = settings.Save
	}
	if options.CheckAuth == nil {
		options.CheckAuth = chat.CheckAuth
	}
	if options.Open == nil {
		options.Open = func(ctx context.Context, project string) (Backend, error) { return chat.Open(ctx, project) }
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Launcher{options: options, ctx: ctx, cancel: cancel, dispatch: dispatch, invalidate: invalidate, theme: appTheme(), loading: true}
}

func (l *Launcher) Start() {
	if l.options.Demo {
		l.loading = false
		l.startApp(settings.Config{})
		return
	}
	go func() {
		config, err := l.options.Load()
		l.dispatch(func() {
			if l.closing || l.closed {
				return
			}
			l.loading, l.config, l.projectInput = false, config, config.ProjectID
			if err != nil {
				l.error = err.Error()
			}
			if err != nil || config.ProjectID == "" {
				l.setupVisible = true
				l.checkAuth()
			} else {
				l.startApp(config)
			}
			l.invalidate()
		})
	}()
}

func (l *Launcher) startApp(config settings.Config) {
	if l.checkCancel != nil {
		l.checkCancel()
	}
	l.checkGeneration++
	l.config, l.projectInput, l.setupVisible = config, config.ProjectID, false
	open := func(ctx context.Context) (Backend, error) { return l.options.Open(ctx, config.ProjectID) }
	if l.options.Demo {
		open = func(context.Context) (Backend, error) { return Demo(), nil }
	}
	l.app = New(open, l.dispatch, l.invalidate, l.options.Demo)
	l.app.setFocused(l.focused)
	if !l.options.Demo {
		l.app.openSettings = l.showSettings
	}
	l.app.RefreshSpaces()
	l.invalidate()
}

func (l *Launcher) showSettings() {
	if l.app != nil {
		l.app.setFocused(false)
	}
	l.projectInput, l.error, l.setupVisible = l.config.ProjectID, "", true
	l.checkAuth()
	l.invalidate()
}

func (l *Launcher) checkAuth() {
	if l.checkCancel != nil {
		l.checkCancel()
	}
	l.checkGeneration++
	id := l.checkGeneration
	ctx, cancel := context.WithTimeout(l.ctx, 10*time.Second)
	l.checkCancel, l.checking = cancel, true
	go func() {
		defer cancel()
		status := l.options.CheckAuth(ctx)
		l.dispatch(func() {
			if l.closing || l.closed || l.checkGeneration != id {
				return
			}
			l.auth, l.checking = status, false
			if strings.TrimSpace(l.projectInput) == "" && settings.ValidateProjectID(status.SuggestedProject) == nil {
				l.projectInput = status.SuggestedProject
			}
			l.invalidate()
		})
	}()
}

func (l *Launcher) saveProject() {
	if l.saving || l.closing || l.checking || !l.auth.GWSInstalled || !l.auth.LoginFound {
		return
	}
	config := settings.Config{ProjectID: strings.TrimSpace(l.projectInput)}
	if err := settings.ValidateProjectID(config.ProjectID); err != nil {
		l.error = err.Error()
		return
	}
	l.saving, l.error = true, ""
	go func() {
		err := l.options.Save(config)
		l.dispatch(func() {
			if l.closing || l.closed {
				return
			}
			if err != nil {
				l.saving, l.error = false, err.Error()
				l.invalidate()
				return
			}
			if l.app != nil && config.ProjectID == l.config.ProjectID && l.app.backend != nil && l.app.spacesError == "" && l.app.streamError == "" {
				l.saving, l.setupVisible = false, false
				l.invalidate()
				return
			}
			if l.app == nil {
				l.saving = false
				l.startApp(config)
				return
			}
			l.switching = true
			l.app.Shutdown(func(cleanupErr error) {
				l.switching, l.saving = false, false
				if l.closing {
					l.finishShutdown(cleanupErr)
					return
				}
				l.startApp(config)
				if cleanupErr != nil {
					l.app.streamError = "Previous session cleanup needs a retry. Retry incoming messages to try again."
				}
			})
			l.invalidate()
		})
	}()
}

func (l *Launcher) Shutdown(done func(error)) {
	if l.closing || l.closed {
		return
	}
	l.closing, l.shutdownDone = true, done
	if l.checkCancel != nil {
		l.checkCancel()
	}
	if l.switching {
		return
	}
	if l.app != nil {
		l.app.Shutdown(l.finishShutdown)
		return
	}
	l.finishShutdown(nil)
}

func (l *Launcher) finishShutdown(err error) {
	l.closed = true
	l.cancel()
	if l.shutdownDone != nil {
		l.shutdownDone(err)
	}
}

// SetFocused is called by native window focus/blur events on the main thread.
func (l *Launcher) SetFocused(focused bool) {
	l.focused = focused
	if l.app != nil {
		l.app.setFocused(focused && !l.setupVisible)
	}
}

func (l *Launcher) View(c *ui.Context) {
	if !l.loading && !l.setupVisible && l.app != nil {
		l.app.setFocused(l.focused)
		l.app.View(c)
		return
	}
	if l.app != nil {
		l.app.setFocused(false)
	}
	c.SetTheme(l.theme)
	_, height := c.Size()
	ui.Scroll(c).Fill().Background(l.theme.Background).Children(func() {
		ui.Column(c).WidthPercent(100).MinHeight(height).Padding(32).Center().Children(func() {
			ui.Column(c).WidthPercent(100).MaxWidth(600).Gap(18).Children(func() {
				if l.loading {
					ui.Text(c, "Loading settings…").TextColor(l.theme.TextMuted)
					return
				}
				ui.Text(c, "GOOGLE CHAT MAX").FontSize(11).Bold().TextColor(l.theme.TextMuted)
				title := "Set up Google Chat Max"
				if l.app != nil {
					title = "Settings"
				}
				ui.Text(c, title).FontSize(28).Bold()
				ui.Text(c, "Choose the Google Cloud project used for incoming messages.").TextColor(l.theme.TextMuted)
				ui.Column(c).Gap(8).Children(func() {
					ui.Text(c, "Google Cloud project ID").Bold()
					ui.TextInput(c.Key("project-id"), &l.projectInput).Label("Google Cloud project ID").Placeholder("your-project-id").Disabled(l.saving)
					if l.projectInput != "" {
						if err := settings.ValidateProjectID(strings.TrimSpace(l.projectInput)); err != nil {
							ui.Text(c, err.Error()).FontSize(12).TextColor(l.theme.TextMuted)
						}
					}
					ui.Text(c, "Use a project with Pub/Sub and Google Workspace Events enabled.").FontSize(12).TextColor(l.theme.TextMuted)
				})
				l.authChecks(c)
				if l.error != "" {
					ui.Text(c, l.error).TextColor(l.theme.Danger)
				}
				ui.Row(c).Gap(12).Justify(ui.End).Children(func() {
					if l.app != nil {
						if ui.Button(c, "Cancel").Disabled(l.saving).Clicked() {
							l.setupVisible = false
						}
					}
					label := "Save and open Chat"
					if l.switching {
						label = "Finishing current connection…"
					} else if l.saving {
						label = "Saving…"
					}
					disabled := l.saving || l.checking || !l.auth.GWSInstalled || !l.auth.LoginFound || settings.ValidateProjectID(strings.TrimSpace(l.projectInput)) != nil
					if ui.PrimaryButton(c, label).Disabled(disabled).Clicked() {
						l.saveProject()
					}
				})
				ui.Text(c, "Only the project ID is saved here. Credentials stay with gws.").FontSize(12).TextColor(l.theme.TextMuted)
			})
		})
	})
}

func (l *Launcher) authChecks(c *ui.Context) {
	ui.Column(c).WidthPercent(100).Padding(16).Gap(10).Radius(12).Background(l.theme.Surface).Children(func() {
		if l.checking {
			ui.Text(c, "Checking authentication tools…").TextColor(l.theme.TextMuted)
			return
		}
		if !l.auth.GWSInstalled {
			ui.Text(c, "Google Workspace CLI (gws) was not found.").Bold()
			ui.Text(c, "Install gws and make it available on PATH, then check again.").TextColor(l.theme.TextMuted)
		} else {
			ui.Text(c, "Google Workspace CLI (gws) is installed.")
			if l.auth.LoginFound {
				ui.Text(c, "Saved Google login found.").TextColor(l.theme.Success)
			} else {
				ui.Text(c, "Sign in with gws before opening Chat.")
				if !l.auth.ClientConfigured {
					ui.Text(c, "First-time OAuth client setup:").FontSize(12).TextColor(l.theme.TextMuted)
					ui.Text(c, "gws auth setup").Selectable()
					if !l.auth.GCloudInstalled {
						ui.Text(c, "gws auth setup also requires the Google Cloud CLI (gcloud).").FontSize(12).TextColor(l.theme.TextMuted)
					}
				}
				ui.Text(c, "gws auth login").Selectable()
				ui.Text(c, "The login needs access to Google Chat and this project's event delivery services.").FontSize(12).TextColor(l.theme.TextMuted)
			}
		}
		if l.auth.Error != "" {
			ui.Text(c, l.auth.Error).FontSize(12).TextColor(l.theme.Danger)
		}
		if ui.Button(c, "Check again").Disabled(l.saving).Clicked() {
			l.checkAuth()
		}
	})
}
