package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
	"google-chat-max/internal/emojidata"
)

var emojiCategories = []struct{ name, icon string }{
	{"Smileys & Emotion", "😀"}, {"People & Body", "👋"},
	{"Animals & Nature", "🐻"}, {"Food & Drink", "🍔"},
	{"Travel & Places", "🚗"}, {"Activities", "⚽"},
	{"Objects", "💡"}, {"Symbols", "🔣"}, {"Flags", "🏁"},
	{"Custom", "✦"},
}

var emojiTones = []string{"Default", "Light", "Medium-light", "Medium", "Medium-dark", "Dark", "All tones"}

type emojiChoice struct {
	emoji chat.Emoji
	name  string
}

func (a *App) emojiChoices() []emojiChoice {
	query := strings.ToLower(strings.TrimSpace(a.emojis.query))
	category := emojiCategories[a.emojis.category].name
	var choices []emojiChoice
	if category == "Custom" || query != "" {
		for _, custom := range a.emojis.catalog {
			if query == "" || strings.Contains(strings.ToLower(custom.EmojiName), strings.Trim(query, ":")) {
				copy := custom
				choices = append(choices, emojiChoice{emoji: chat.Emoji{CustomEmoji: &copy}, name: custom.EmojiName})
			}
		}
	}
	for _, standard := range emojidata.Catalog {
		if (query != "" || standard.Group == category) && standard.Matches(query) && (query == standard.Unicode || standard.MatchesTone(a.emojis.tone)) {
			choices = append(choices, emojiChoice{emoji: chat.Emoji{Unicode: standard.Unicode}, name: standard.Name})
		}
	}
	return choices
}

func (a *App) emojiPickerContents(c *ui.Context, message string, reaction *reactionEntry) {
	ui.TextInput(c.Key("emoji-search"), &a.emojis.query).Label("Search emoji").Placeholder("Search all emoji…").AutoFocus()
	tabs := ui.TabsBase(c.Key("emoji-categories"), &a.emojis.category, len(emojiCategories))
	tabs.List.Label("Emoji categories").Gap(1).Children(func() {
		for i, category := range emojiCategories {
			tab := tabs.Tab(i).Label(category.name).Tooltip(category.name).Size(30, 30).Center().Radius(6)
			if i == a.emojis.category {
				tab.Background(a.theme.SurfacePressed)
			} else if tab.Hovered() {
				tab.Background(a.theme.SurfaceHover)
			}
			tab.Children(func() { ui.Text(c, category.icon).FontSize(18).SingleLine() })
		}
	})
	if tabs.List.Changed() {
		a.emojis.query = ""
		a.emojis.grid = ui.GridState{}
	}
	if a.emojis.tone == "" {
		a.emojis.tone = "Default"
	}
	ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
		label := emojiCategories[a.emojis.category].name
		if strings.TrimSpace(a.emojis.query) != "" {
			label = "Search results"
		}
		ui.Text(c, label).FontSize(12).TextColor(a.theme.TextMuted).Grow(1)
		if ui.Select(c.Key("emoji-tone"), &a.emojis.tone, emojiTones).Label("Skin tone").Tooltip("Skin tone").Width(122).Changed() {
			a.emojis.grid = ui.GridState{}
		}
	})
	if tabs.List.Changed() || a.emojis.query != a.emojis.lastQuery {
		a.emojis.grid = ui.GridState{}
		a.emojis.lastQuery = a.emojis.query
	}
	if a.emojis.loading {
		ui.Text(c, "Loading custom emoji…").FontSize(11).TextColor(a.theme.TextMuted)
	}
	if a.emojis.err != "" {
		ui.Text(c, a.emojis.err).FontSize(11).TextColor(a.theme.Warning)
		if ui.Button(c, "Retry custom emoji").Clicked() {
			a.emojis.err = ""
			a.loadEmojis()
		}
	}
	choices := a.emojiChoices()
	if len(choices) == 0 {
		label := "No matching emoji"
		if a.emojis.query == "" && emojiCategories[a.emojis.category].name == "Custom" && !a.emojis.loading && a.emojis.err == "" {
			label = "No custom emoji available"
		}
		ui.Box(c).Height(240).Center().Children(func() { ui.Text(c, label).TextColor(a.theme.TextMuted) })
		return
	}
	originalTheme := c.Theme()
	gridTheme := *originalTheme
	gridTheme.Spacing = 1 // GridView takes its cell spacing from the theme.
	c.SetTheme(&gridTheme)
	// Leave room for the overlay scrollbar beside the nine columns.
	ui.GridView(c.Key("emoji-grid"), &a.emojis.grid, len(choices), 32, 32, func(i int) {
		choice := choices[i]
		a.emojiButton(c.Key(choice.emoji.Key()), message, choice.emoji, reaction, 28, choice.name)
	}).Label("Emoji choices").WidthPercent(100).Height(240).PaddingEnd(12).Gap(0)
	c.SetTheme(originalTheme)
}
