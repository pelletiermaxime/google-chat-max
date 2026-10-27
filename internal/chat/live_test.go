package chat

import (
	"context"
	"os"
	"testing"
	"time"

	"google-chat-max/internal/settings"
)

func liveProject(t *testing.T) string {
	t.Helper()
	project := os.Getenv("CHAT_MAX_TEST_PROJECT")
	if project == "" {
		config, err := settings.Load()
		if err != nil {
			t.Fatal(err)
		}
		project = config.ProjectID
	}
	if project == "" {
		t.Skip("configure a project in the app or set CHAT_MAX_TEST_PROJECT")
	}
	if err := settings.ValidateProjectID(project); err != nil {
		t.Fatal(err)
	}
	return project
}

// Explicit opt-in: reads the host login and live Chat data, but logs counts only.
func TestLiveReadOnly(t *testing.T) {
	if os.Getenv("CHAT_MAX_LIVE_TEST") != "1" {
		t.Skip("set CHAT_MAX_LIVE_TEST=1 to verify live read-only access")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c, err := Open(ctx, liveProject(t))
	if err != nil {
		t.Fatal(err)
	}
	spaces, err := c.Spaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(spaces) == 0 {
		t.Fatal("no accessible conversations")
	}
	selected := spaces[0]
	for _, s := range spaces {
		if s.SpaceType == "SPACE" && s.DisplayName != "" {
			selected = s
			break
		}
	}
	messages, err := c.Messages(ctx, selected.Name)
	if err != nil {
		t.Fatal(err)
	}
	withText, withThread := 0, 0
	for _, m := range messages {
		if m.Text != "" {
			withText++
		}
		if m.Thread.Name != "" {
			withThread++
		}
	}
	t.Logf("Live read-only proof: %d conversations; selected history returned %d messages (%d with text, %d with thread IDs)", len(spaces), len(messages), withText, withThread)
}
