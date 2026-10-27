package desktop

import (
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"golang.org/x/net/html"
	"google-chat-max/internal/chat"
)

type cardLine struct {
	text                  string
	title, muted, divider bool
}

// Cards use a small HTML text format. Decode it into native selectable text;
// markup never becomes a browser document or executable card action.
func cardPlainText(raw string) string {
	z := html.NewTokenizer(strings.NewReader(raw))
	var text strings.Builder
	for {
		switch z.Next() {
		case html.ErrorToken:
			return strings.TrimSpace(text.String())
		case html.TextToken:
			text.Write(z.Text())
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "br", "p", "div", "li":
				if text.Len() > 0 && !strings.HasSuffix(text.String(), "\n") {
					text.WriteByte('\n')
				}
			}
		}
	}
}

func cardLines(card chat.Card) []cardLine {
	var lines []cardLine
	remaining := 4000
	shortened := false
	add := func(raw string, title, muted bool) {
		if raw == "" {
			return
		}
		if len(lines) >= 32 || remaining == 0 {
			shortened = true
			return
		}
		if len(raw) > 16000 {
			raw = strings.ToValidUTF8(raw[:16000], "")
			shortened = true
		}
		text := cardPlainText(raw)
		if utf8.RuneCountInString(text) > remaining {
			text = string([]rune(text)[:remaining])
			shortened = true
		}
		remaining -= utf8.RuneCountInString(text)
		if text != "" {
			lines = append(lines, cardLine{text: text, title: title, muted: muted})
		}
	}
	add(card.Header.Title, true, false)
	add(card.Header.Subtitle, false, true)
	for _, section := range card.Sections[:min(len(card.Sections), 10)] {
		start := len(lines)
		add(section.Header, false, true)
		for _, widget := range section.Widgets[:min(len(section.Widgets), 20)] {
			switch {
			case widget.TextParagraph != nil:
				add(widget.TextParagraph.Text, false, false)
			case widget.KeyValue != nil:
				add(widget.KeyValue.TopLabel, false, true)
				add(widget.KeyValue.Content, false, false)
				add(widget.KeyValue.BottomLabel, false, true)
			case widget.DecoratedText != nil:
				add(widget.DecoratedText.TopLabel, false, true)
				add(widget.DecoratedText.Text, false, false)
				add(widget.DecoratedText.BottomLabel, false, true)
			default:
				add("[Card widget not supported yet]", false, true)
			}
		}
		if start > 0 && len(lines) > start {
			lines[start].divider = true
		}
		shortened = shortened || len(section.Widgets) > 20
	}
	shortened = shortened || len(card.Sections) > 10
	if shortened {
		lines = append(lines, cardLine{text: "[Card shortened for display]", muted: true})
	}
	if len(lines) == 0 {
		lines = append(lines, cardLine{text: "[Card content not supported yet]", muted: true})
	}
	return lines
}

func (a *App) messageCards(c *ui.Context, m chat.Message) {
	cards := m.Cards[:min(len(m.Cards), 4)]
	count := len(m.Cards)
	if len(m.CardsV2) > 0 {
		count = len(m.CardsV2)
		cards = make([]chat.Card, 0, min(count, 4))
		for _, card := range m.CardsV2[:min(count, 4)] {
			cards = append(cards, card.Card)
		}
	}
	for _, card := range cards {
		ui.Column(c).WidthPercent(100).Border(1, a.theme.Border).Radius(12).Background(a.theme.Surface).Children(func() {
			for _, line := range cardLines(card) {
				if line.divider {
					ui.Column(c).Height(1).WidthPercent(100).Background(a.theme.Border)
				}
				ui.Column(c).Padding(12, 16).WidthPercent(100).Children(func() {
					text := ui.Text(c, line.text).Selectable()
					if line.title {
						text.FontSize(17).Bold()
					}
					if line.muted {
						text.FontSize(12).TextColor(a.theme.TextMuted)
					}
				})
			}
		})
	}
	if count > 4 {
		ui.Text(c, "[Additional cards omitted]").FontSize(12).TextColor(a.theme.TextMuted)
	}
}
