package desktop

import (
	"fmt"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

func TestMessageLinks(t *testing.T) {
	for _, test := range []struct {
		text string
		urls []string
	}{
		{text: "No links here. https:// www. javascript:alert(1) file:///tmp/file"},
		{text: "Read https://example.com/docs?q=hello&lang=fr#section.", urls: []string{"https://example.com/docs?q=hello&lang=fr#section"}},
		{text: "Try www.example.com and HTTP://localhost:8080/test!", urls: []string{"https://www.example.com", "HTTP://localhost:8080/test"}},
		{text: "(https://example.com/wiki/Go_(language)). [https://example.com/guide]", urls: []string{"https://example.com/wiki/Go_(language)", "https://example.com/guide"}},
		{text: "Bonjour “https://example.com/fr”\nhttps://example.com/a\u00a0https://example.com/b", urls: []string{"https://example.com/fr", "https://example.com/a", "https://example.com/b"}},
		{text: "https://example.com/redirect?next=https://example.org www.example.net", urls: []string{"https://example.com/redirect?next=https://example.org", "https://www.example.net"}},
		{text: "foohttps://example.com user@www.example.com https://example.com/%zz"},
	} {
		t.Run(test.text, func(t *testing.T) {
			var urls []string
			end := 0
			for _, link := range messageLinks(test.text) {
				if link.start < end || link.end <= link.start || link.end > len(test.text) {
					t.Fatalf("invalid or overlapping link range: %+v", link)
				}
				end = link.end
				urls = append(urls, link.url)
			}
			if !slices.Equal(urls, test.urls) {
				t.Fatalf("got %q, want %q", urls, test.urls)
			}
		})
	}
}

func TestNativeMessageLinksOpenOnlyOnClick(t *testing.T) {
	for _, width := range []int{760, 1180} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a, _, m := actionFixture(t)
			defer a.Close()
			m.Text = "Read https://example.com/docs.\nThen visit www.example.org!"
			a.messages = []chat.Message{m}
			tt := ui.NewTester(a.View, width, 900)
			if !tt.HasText(m.Text) {
				t.Fatal("link detection changed the message's text or line breaks")
			}
			if len(tt.OpenedURLs()) != 0 {
				t.Fatal("rendering a message opened a link")
			}
			for _, label := range []string{"https://example.com/docs", "www.example.org"} {
				if err := tt.Click(label); err != nil {
					t.Fatal(err)
				}
			}
			if want := []string{"https://example.com/docs", "https://www.example.org"}; !slices.Equal(tt.OpenedURLs(), want) {
				t.Fatalf("opened %q, want %q", tt.OpenedURLs(), want)
			}
		})
	}
}
