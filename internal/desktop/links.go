package desktop

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

var messageURLPattern = regexp.MustCompile(`(?i)https?://|www\.`)

type messageLink struct {
	start, end int
	url        string
}

func messageLinks(text string) []messageLink {
	var links []messageLink
	consumed := 0
	for _, match := range messageURLPattern.FindAllStringIndex(text, -1) {
		start := match[0]
		if start < consumed {
			continue
		}
		if start > 0 {
			previous, _ := utf8.DecodeLastRuneInString(text[:start])
			if unicode.IsLetter(previous) || unicode.IsDigit(previous) || strings.ContainsRune("_@.", previous) {
				continue
			}
		}
		candidate := text[start:]
		if stop := strings.IndexFunc(candidate, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune("<>\"'`|“”‘’", r)
		}); stop >= 0 {
			candidate = candidate[:stop]
		}
		consumed = start + len(candidate)
		candidate = trimMessageURL(candidate)
		href := candidate
		if strings.HasPrefix(strings.ToLower(href), "www.") {
			href = "https://" + href
		}
		u, err := url.Parse(href)
		if err != nil || u.Hostname() == "" || strings.ContainsAny(u.Hostname(), "(){}|") {
			continue
		}
		links = append(links, messageLink{start: start, end: start + len(candidate), url: href})
	}
	return links
}

// Keep balanced URL parentheses and brackets, but leave sentence punctuation
// and enclosing delimiters in the surrounding message text.
func trimMessageURL(text string) string {
	excess := map[rune]int{
		')': strings.Count(text, ")") - strings.Count(text, "("),
		']': strings.Count(text, "]") - strings.Count(text, "["),
		'}': strings.Count(text, "}") - strings.Count(text, "{"),
	}
	for len(text) > 0 {
		r, size := utf8.DecodeLastRuneInString(text)
		switch {
		case strings.ContainsRune(".,!?;:…。，！？；：", r):
		case excess[r] > 0:
			excess[r]--
		default:
			return text
		}
		text = text[:len(text)-size]
	}
	return text
}

func messageText(c *ui.Context, text string) ui.Element {
	links := messageLinks(text)
	if len(links) == 0 {
		return ui.Text(c, text).Selectable()
	}
	return ui.RichText(c).Selectable().Children(func() {
		start := 0
		for _, link := range links {
			if start < link.start {
				ui.Text(c, text[start:link.start])
			}
			ui.Link(c, text[link.start:link.end], link.url).Tooltip(link.url)
			start = link.end
		}
		if start < len(text) {
			ui.Text(c, text[start:])
		}
	})
}
