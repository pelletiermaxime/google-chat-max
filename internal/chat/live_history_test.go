package chat

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"
)

func TestLiveHistoryPages(t *testing.T) {
	if os.Getenv("CHAT_MAX_LIVE_TEST") != "1" {
		t.Skip("explicit read-only live check required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c, err := Open(ctx, liveProject(t))
	if err != nil {
		t.Fatal(err)
	}
	// This check exercises paging only; identity enrichment is independently
	// tested and does not need repeating for every real history request.
	c.identities = false
	spaces, err := c.Spaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(spaces, func(a, b Space) int { return b.LastActiveTime.Compare(a.LastActiveTime) })
	attempts := 0
	for _, s := range spaces {
		if s.SpaceType != "SPACE" || s.SpaceThreadingState != "THREADED_MESSAGES" {
			continue
		}
		attempts++
		page, err := c.History(ctx, s.Name, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if page.Next == "" {
			if attempts >= 3 {
				break
			}
			continue
		}
		older, err := c.History(ctx, s.Name, "", page.Next)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Messages) == 0 || len(older.Messages) == 0 || len(page.Messages) > HistoryLimit || len(older.Messages) > HistoryLimit {
			t.Fatal("invalid bounded live history pages")
		}
		seen := map[string]bool{}
		for _, m := range page.Messages {
			seen[m.Name] = true
		}
		for _, m := range older.Messages {
			if seen[m.Name] {
				t.Fatal("live page cursor repeated messages")
			}
		}
		if older.Messages[len(older.Messages)-1].CreateTime.After(page.Messages[0].CreateTime) {
			t.Fatal("older page was not chronologically older")
		}
		t.Logf("Read-only paging verified: %d recent messages + %d older messages; no overlapping IDs", len(page.Messages), len(older.Messages))
		return
	}
	t.Skip("no paginated recent space among the bounded candidates")
}
