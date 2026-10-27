package desktop

import (
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNativeFullEmojiPickerCategoriesSearchTonesAndSelection(t *testing.T) {
	a, callbacks, m := actionFixture(t)
	defer a.Close()
	b := &fakeReactions{calls: make(chan reactionCall, 8)}
	a.backend = b
	tt := ui.NewTester(a.View, 1180, 900)
	hoverMessage(t, tt, m.Text)
	waitEmojiCallbacks(t, callbacks, func() bool { return a.reactions[m.Name] != nil && a.reactions[m.Name].loaded && !a.emojis.loading })
	tt.Frame()
	if err := tt.Click("Add reaction"); err != nil {
		t.Fatal(err)
	}
	for _, category := range emojiCategories {
		if _, ok := tt.Find(category.name); !ok {
			t.Fatalf("missing category %s", category.name)
		}
	}
	if err := tt.Click("Animals & Nature"); err != nil {
		t.Fatal(err)
	}
	if got := emojiCategories[a.emojis.category].name; got != "Animals & Nature" {
		t.Fatalf("selected %s", got)
	}
	if err := tt.Click("Search emoji"); err != nil {
		t.Fatal(err)
	}
	tt.Type("woman technologist")
	if _, ok := tt.Find("React 👩‍💻"); !ok {
		t.Fatal("search did not cross category boundaries")
	}
	if err := tt.Click("People & Body"); err != nil {
		t.Fatal(err)
	}
	if a.emojis.query != "" {
		t.Fatal("category switch did not clear search")
	}
	if err := tt.Click("Skin tone"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Medium"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Search emoji"); err != nil {
		t.Fatal(err)
	}
	tt.Type("thumbs up")
	choices := a.emojiChoices()
	if len(choices) != 1 || choices[0].emoji.Unicode != "👍🏽" {
		t.Fatalf("tone selection returned %+v", choices)
	}
	if _, ok := tt.Find("React 👍🏽"); !ok {
		t.Fatal("selected tone was not rendered")
	}
	if err := tt.Click("Custom"); err != nil {
		t.Fatal(err)
	}
	if len(a.emojiChoices()) != len(testCustomEmojis()) {
		t.Fatal("custom category mixed in standard emoji")
	}
	if err := tt.Click("Flags"); err != nil {
		t.Fatal(err)
	}
	index := -1
	for i, choice := range a.emojiChoices() {
		if choice.emoji.Unicode == "🇨🇦" {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("complete flag catalog is missing Canada")
	}
	a.emojis.grid.ScrollTo(index, ui.Center)
	tt.Frame()
	flag, ok := tt.Find("React 🇨🇦")
	if !ok {
		t.Fatal("virtual grid did not scroll to Canada")
	}
	grid, ok := tt.Find("Emoji choices")
	if !ok {
		t.Fatal("grid missing")
	}
	for _, choice := range a.emojiChoices() {
		if r, ok := tt.Find("React " + choice.emoji.Unicode); ok && r.X+r.W > grid.X+grid.W-12 {
			t.Fatalf("emoji overlaps scrollbar: %+v", r)
		}
	}
	tt.Move(grid.X+grid.W-5, flag.Y)
	tt.Frame()
	if path := os.Getenv("CHAT_MAX_PICKER_SCREENSHOT"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tt.Image()); err != nil {
			f.Close()
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := tt.Click("React 🇨🇦"); err != nil {
		t.Fatal(err)
	}
	call := nextReactionCall(t, b)
	if !call.want || call.emoji.Unicode != "🇨🇦" || a.emojis.open {
		t.Fatal("picker selection used wrong emoji or did not dismiss")
	}
	call.result <- reactionResult{state: reactionState(call.emoji)}
	applyNext(t, callbacks)
}

func TestFullEmojiPickerCatalogIsAvailableAcrossTabs(t *testing.T) {
	a := New(nil, func(func()) {}, func() {}, true)
	defer a.Close()
	a.emojis.tone = "All tones"
	count := 0
	for i, category := range emojiCategories {
		a.emojis.category = i
		choices := a.emojiChoices()
		if category.name != "Custom" && len(choices) == 0 {
			t.Errorf("empty category %s", category.name)
		}
		count += len(choices)
	}
	if count != 3963 {
		t.Fatalf("tabs expose %d emoji, want 3963", count)
	}
}
