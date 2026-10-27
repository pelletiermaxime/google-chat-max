package desktop

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"google-chat-max/internal/chat"

	"github.com/egoist/mygo/ui"
)

func (a *App) View(c *ui.Context) {
	c.SetTheme(a.theme)
	ui.Row(c).Fill().Background(a.theme.Background).Children(func() {
		a.sidebar(c)
		ui.Column(c).Width(1).HeightPercent(100).Background(a.theme.Border)
		ui.Column(c).Grow(1).MinWidth(0).HeightPercent(100).Children(func() {
			if a.threadName != "" {
				a.threadView(c)
			} else {
				a.history(c)
			}
		})
	})
}

func (a *App) sidebar(c *ui.Context) {
	t := a.theme
	sidebarTheme := *t
	sidebarTheme.Surface = ui.Hex("#171717")
	sidebarTheme.SurfaceHover = ui.Hex("#242424")
	sidebarTheme.SurfacePressed = ui.Hex("#303030")
	sidebarTheme.Accent = sidebarTheme.SurfacePressed
	sidebarTheme.AccentText = t.Text
	c.SetTheme(&sidebarTheme)
	defer c.SetTheme(t)
	ui.Column(c).Width(302).HeightPercent(100).Background(sidebarTheme.Surface).Children(func() {
		ui.Column(c).Padding(22, 20, 16, 20).Gap(6).Children(func() {
			ui.Text(c, "GOOGLE CHAT MAX").FontSize(11).Bold().TextColor(t.Accent)
			ui.Text(c, "Conversations").FontSize(23).Bold()
			ui.Text(c, "A little more room to focus.").FontSize(12).TextColor(t.TextMuted)
		})
		ui.Column(c).Padding(0, 16, 12, 16).Gap(10).Children(func() {
			ui.TextInput(c.Key("search"), &a.query).Placeholder("Find a conversation…").Label("Find a conversation")
			if a.loading {
				ui.Text(c, "Loading conversations…").FontSize(12).TextColor(t.TextMuted)
			}
		})
		if a.spacesError != "" {
			ui.Column(c).Padding(0, 16, 12, 16).Children(func() {
				ui.Text(c, a.spacesError).TextColor(t.Danger).FontSize(12)
			})
		}
		selected := a.selected
		visible := 0
		ui.Sidebar(c.Key("conversations"), &selected, func() {
			for _, group := range a.sidebarGroups() {
				visible += len(group.items)
				if len(group.items) != 0 {
					ui.SidebarSection(c, group.label, nil, func() {
						for _, s := range group.items {
							item := ui.SidebarItem(c, s.Name, nil, s.Title())
							if s.AvatarURL != "" {
								item.Children(func() { a.avatar(c, s.AvatarURL, s.Title(), 22) })
							}
							if count := a.activity[s.Name]; count > 0 {
								item.Children(func() { ui.Badge(c, fmt.Sprintf("%d new", count)) })
							}
						}
						if group.label == "Private messages" && group.total > recentPrivateLimit && strings.TrimSpace(a.query) == "" {
							label := fmt.Sprintf("Show more (%d older)", group.total-recentPrivateLimit)
							if a.showAllPrivate {
								label = "Show only latest 10"
							}
							if ui.Button(c.Key("more-private"), label).Clicked() {
								a.showAllPrivate = !a.showAllPrivate
							}
						}
					})
				}
			}
			if visible == 0 && !a.loading && a.spacesError == "" {
				text := "No conversations yet."
				if strings.TrimSpace(a.query) != "" {
					text = "No matching conversations."
				}
				ui.Text(c, text).Padding(8).TextColor(t.TextMuted)
			}
		}).WidthPercent(100).Grow(1).MinHeight(0)
		if selected != a.selected {
			a.selectSpace(selected)
		}
		ui.Column(c).Padding(16, 20).Gap(9).Children(func() {
			if a.demo {
				ui.Text(c, "DEMO DATA · OFFLINE").FontSize(10).Bold().TextColor(t.Accent)
			}
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				if a.openSettings != nil {
					if ui.Button(c.Key("settings"), "Settings").Disabled(a.closing || a.closed).Clicked() {
						a.openSettings()
					}
				}
				if ui.Button(c.Key("refresh-spaces"), "Refresh").Disabled(a.loading).Clicked() {
					a.RefreshSpaces()
				}
			})
			if !a.demo {
				if a.streamError != "" {
					ui.Text(c, a.streamError).FontSize(11).TextColor(t.Danger)
					if ui.Button(c.Key("retry-incoming"), "Retry incoming messages").Disabled(a.streamRunning || a.backend == nil || a.closing || a.closed).Clicked() {
						a.Connect()
					}
				} else if a.streamStatus == "Reconnecting" {
					ui.Text(c, "Reconnecting…").FontSize(11).TextColor(t.TextMuted)
				}
			} else {
				ui.Text(c, "Offline sample conversations.").FontSize(11).TextColor(t.TextMuted)
			}
		})
	})
}

func (a *App) history(c *ui.Context) {
	t := a.theme
	if a.selected == "" {
		ui.Column(c).Fill().Center().Gap(14).Padding(36).Children(func() {
			ui.Text(c, "Your conversations, with room to breathe.").FontSize(25).Bold()
			ui.Text(c, "Choose a conversation to read its recent messages.").TextColor(t.TextMuted)
		})
		return
	}
	s := a.selectedSpace()
	ui.Column(c).Padding(26, 32, 20, 32).Gap(7).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(16).Children(func() {
			ui.Text(c, s.Title()).FontSize(24).Bold().SingleLine().Grow(1)
			if ui.Button(c.Key("refresh-history"), "Refresh messages").Disabled(a.reading).Clicked() {
				a.refreshMessages()
			}
		})
		ui.Text(c, s.Name).FontSize(11).TextColor(t.TextMuted).Selectable()
	})
	ui.Column(c).Height(1).WidthPercent(100).Background(t.Border)
	a.historyControls(c, false)
	if a.messageError != "" {
		ui.Column(c).Padding(20, 32).Gap(10).Children(func() {
			ui.Text(c, a.messageError).TextColor(t.Danger)
			if ui.Button(c.Key("retry-history"), "Try again").Clicked() {
				a.refreshMessages()
			}
		})
	}
	if len(a.messages) == 0 {
		ui.Column(c).Grow(1).WidthPercent(100).Center().Gap(10).Children(func() {
			text := "No messages in this conversation yet."
			if a.reading {
				text = "Loading messages…"
			} else if a.messageError != "" {
				text = "Messages could not be loaded."
			}
			ui.Text(c, text).TextColor(t.TextMuted)
		})
	} else {
		a.messageListView(c.Key("history-"+a.selected), a.messages, &a.messageList)
	}
	a.composer(c, "")
}

func (a *App) message(c *ui.Context, m chat.Message) {
	t := a.theme
	own := a.selfUser != "" && m.Sender.Name == a.selfUser
	user := a.author(m)
	if user.DisplayName != "" {
		m.Sender = user
	}
	width, _ := c.Size()
	columnWidth := min(float32(messageColumnMaxWidth), width-303-64)
	bubbleWidth := min(float32(680), max(float32(160), columnWidth*0.8))
	row := ui.Row(c).WidthPercent(100).Gap(12).AlignItems(ui.Start).Shrink(0)
	fill := ui.Hex("#242424")
	if own {
		row.Justify(ui.End)
		fill = ui.Hex("#303030")
	}
	row.Children(func() {
		if !own {
			a.avatar(c, user.AvatarURL, m.Author(), 34)
		}
		label := "Message from " + m.Author()
		if own {
			label = "Outgoing message"
		}
		bubble := ui.Column(c.Key("bubble-"+m.Name)).MaxWidth(bubbleWidth).MinWidth(0).Shrink(1).
			Padding(12, 16).Gap(8).Radius(16).Background(fill).Focusable().Label(label)
		bubble.ContextMenu(func(menu *ui.Menu) { a.messageMenu(menu, m, false) })
		bubble.Children(func() {
			if !own {
				ui.Text(c, m.Author()).FontSize(12).Bold().SingleLine().TextColor(t.TextMuted)
			}
			a.messageContent(c, m)
			ui.Row(c).Gap(10).Justify(ui.End).AlignItems(ui.Center).Children(func() {
				if m.ThreadReply {
					ui.Text(c, "Thread reply").FontSize(10).TextColor(t.TextMuted)
				}
				ui.Text(c, m.CreateTime.Local().Format(time.Kitchen)).FontSize(10).TextColor(t.TextMuted)
			})
			if row.Hovered() || row.FocusWithin() || (a.emojis.open && a.emojis.message == m.Name) {
				a.messageActions(c, m)
			}
		})
	})
}

func (a *App) messageContent(c *ui.Context, m chat.Message) {
	if m.QuotedMessageMetadata != nil {
		a.quotePreview(c, m.QuotedMessageMetadata, nil)
	}
	text := m.Text
	if text == "" && len(m.Cards) == 0 && len(m.CardsV2) == 0 {
		text = m.FallbackText
		if text == "" && len(m.Attachment) == 0 {
			text = "[Non-text message]"
		}
	}
	// Bound the layout cost of unusually large individual messages.
	if len(text) > 16000 {
		text = string([]rune(text)[:min(4000, utf8.RuneCountInString(text))]) + "\n[Message shortened for display]"
	}
	if text != "" {
		body := ui.Text(c, text).Selectable()
		body.ContextMenu(func(menu *ui.Menu) { a.messageMenu(menu, m, true) })
	}
	a.messageCards(c, m)
	for i, attachment := range m.Attachment {
		a.attachment(c.Key(fmt.Sprintf("attachment-%d", i)), attachment)
	}
	a.messageReactions(c, m)
	if label, show := a.threadControl(m); show {
		if n := a.threadActivity[m.Thread.Name]; n > 0 {
			label += fmt.Sprintf(" · %d new", n)
		}
		ui.Row(c).Children(func() {
			if ui.Button(c.Key("open-thread-"+m.Name), label).Clicked() {
				a.openThread(m.Thread.Name)
			}
		})
	}
}
