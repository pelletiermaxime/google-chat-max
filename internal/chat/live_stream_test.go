package chat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// This opt-in creates isolated cloud resources and publishes a synthetic event
// ONLY to this app's private Pub/Sub topic. It never sends a Google Chat message.
func TestLiveStreamTransportRenewalAndCleanup(t *testing.T) {
	if os.Getenv("CHAT_MAX_LIVE_STREAM_TEST") != "1" {
		t.Skip("set CHAT_MAX_LIVE_STREAM_TEST=1 to verify real cloud lifecycle and transport")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root, stop := context.WithTimeout(context.Background(), 2*time.Minute)
	defer stop()
	c, err := Open(root, liveProject(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(root)
	defer cancel()
	ready := make(chan struct{}, 1)
	received := make(chan string, 8)
	finished := make(chan error, 1)
	go func() {
		finished <- c.Watch(ctx, func(e StreamEvent) bool {
			if e.Status == "Live" {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
			if e.Message != nil {
				select {
				case received <- e.Message.Name:
				default:
				}
			}
			return true
		})
	}()
	// Always join Watch, including failed assertions, so test failure still
	// finishes cloud cleanup before the temporary journal is removed.
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case err := <-finished:
				if err != nil {
					t.Errorf("watch cleanup: %v", err)
				}
			case <-time.After(95 * time.Second):
				t.Error("watch did not finish cleanup")
			}
		}
		// Keep a failed cleanup journal recoverable after TempDir cleanup.
		if journal, err := streamJournalPath(); err == nil {
			if r, err := loadResources(journal); err == nil && r != nil {
				path := os.TempDir() + "/google-chat-max-cleanup-" + r.ID + ".json"
				if err := saveResources(path, r); err == nil {
					t.Logf("Cleanup journal preserved at %s", path)
				}
			}
		}
	}()
	select {
	case <-ready:
	case err := <-finished:
		joined = true
		t.Fatalf("live setup failed: %v", err)
	case <-time.After(time.Minute):
		t.Fatal("live setup timed out")
	}
	journal, _ := streamJournalPath()
	r, err := loadResources(journal)
	if err != nil || r == nil {
		t.Fatalf("missing resource journal: %v", err)
	}
	a := &streamAPI{client: c, pubsubURL: "https://pubsub.googleapis.com/v1", workspaceURL: "https://workspaceevents.googleapis.com/v1"}
	if _, err := a.renew(root, r, journal); err != nil {
		t.Fatalf("live renewal failed: %v", err)
	}
	t.Log("Live Workspace subscription creation and renewal succeeded")
	m := Message{Name: "spaces/chat-max-validation/messages/" + r.ID, CreateTime: time.Now().UTC(), Text: "Private Pub/Sub transport fixture"}
	data, _ := json.Marshal(map[string]any{"message": m})
	body := map[string]any{"messages": []any{map[string]any{
		"data":       base64.StdEncoding.EncodeToString(data),
		"attributes": map[string]string{"ce-type": messageCreated, "ce-id": "transport-validation-" + r.ID},
	}}}
	if err := a.request(root, "POST", a.pubsubURL+"/"+r.topic()+":publish", body, nil); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	found := false
	for !found {
		select {
		case name := <-received:
			found = name == m.Name
		case err := <-finished:
			joined = true
			t.Fatalf("receiver stopped early: %v", err)
		case <-timer.C:
			t.Fatal("private synthetic event did not arrive through StreamingPull")
		}
	}
	t.Log("Private synthetic new-message event received through real gRPC StreamingPull; no Chat message was sent")
	cancel()
	select {
	case err := <-finished:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(95 * time.Second):
		t.Fatal("cleanup timed out")
	}
	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer verifyCancel()
	for _, name := range []string{r.queue(), r.topic()} {
		if err := a.request(verifyCtx, "GET", a.pubsubURL+"/"+name, nil, nil); !isCloudStatus(err, 404) {
			t.Fatalf("resource still exists after cleanup: %v", err)
		}
	}
	if subs, err := a.subscriptions(verifyCtx, r); err != nil || len(subs) != 0 {
		t.Fatalf("Workspace cleanup not confirmed: owned=%d err=%v", len(subs), err)
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatal("successful cleanup left the resource journal behind")
	}
	t.Log("Cleanup independently confirmed: private topic and queue return 404, no owned Workspace subscriptions remain")
}
