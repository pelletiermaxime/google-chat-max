package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDecodeIncomingMessageAndLifecycle(t *testing.T) {
	attrs := map[string]string{"ce-type": messageCreated, "ce-id": "event-1"}
	event, lifecycle, err := decodeStreamEvent(attrs, []byte(`{"message":{"name":"spaces/a/messages/b","text":"Hello","createTime":"2026-10-26T15:00:00Z","thread":{"name":"spaces/a/threads/t"},"threadReply":true}}`))
	if err != nil || lifecycle != "" || event.ID != "event-1" || event.Message == nil || event.Message.Text != "Hello" || !event.Message.ThreadReply || MessageSpace(event.Message.Name) != "spaces/a" {
		t.Fatalf("unexpected message event: %#v, %q, %v", event, lifecycle, err)
	}
	imageEvent, _, err := decodeStreamEvent(attrs, []byte(`{"message":{"name":"spaces/a/messages/image","createTime":"2026-10-26T15:00:00Z","attachment":[{"contentName":"image.png","contentType":"image/png","attachmentDataRef":{"resourceName":"opaque"}}]}}`))
	if err != nil || imageEvent.Message == nil || len(imageEvent.Message.Attachment) != 1 || !imageEvent.Message.Attachment[0].IsImage() {
		t.Fatal("live event lost image attachment metadata")
	}
	if _, _, err := decodeStreamEvent(attrs, []byte(`{"message":{"name":"spaces/a/messages/b"}}`)); err == nil {
		t.Fatal("incomplete message should request reconciliation")
	}
	if _, _, err := decodeStreamEvent(attrs, []byte(strings.Repeat("x", 1<<20+1))); err == nil {
		t.Fatal("oversized event accepted")
	}
	event, lifecycle, err = decodeStreamEvent(map[string]string{"ce-type": "future.event"}, nil)
	if err != nil || event.Message != nil || lifecycle != "" {
		t.Fatal("unknown event must be ignored")
	}
	_, lifecycle, _ = decodeStreamEvent(map[string]string{"ce-type": "google.workspace.events.subscription.v1.suspended"}, nil)
	if !strings.Contains(lifecycle, "suspended") {
		t.Fatal("suspension must leave a visible connection error")
	}
}

type cloudFixture struct {
	t           *testing.T
	r           *liveResources
	resources   map[string]map[string]string
	ownedSub    bool
	foreignSub  bool
	deletions   []string
	createError bool
	renewals    int
}

func (f *cloudFixture) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	path := strings.TrimPrefix(req.URL.Path, "/pubsub/")
	if req.URL.Path == "/workspace/subscriptions" {
		switch req.Method {
		case "POST":
			if f.createError {
				w.WriteHeader(http.StatusConflict)
				return
			}
			var body struct {
				Target     string   `json:"targetResource"`
				TTL        string   `json:"ttl"`
				EventTypes []string `json:"eventTypes"`
				Payload    struct {
					IncludeResource bool   `json:"includeResource"`
					FieldMask       string `json:"fieldMask"`
				} `json:"payloadOptions"`
			}
			_ = json.NewDecoder(req.Body).Decode(&body)
			if body.Target != allSpacesTarget || body.TTL != "900s" || len(body.EventTypes) != 3 || body.EventTypes[0] != messageCreated || body.EventTypes[1] != reactionCreated || body.EventTypes[2] != reactionDeleted || !body.Payload.IncludeResource {
				f.t.Error("subscription must request bounded all-space message-created events with resource data")
			}
			if body.Payload.FieldMask != "" {
				f.t.Error("mixed message/reaction events must include their full resource payloads")
			}
			f.ownedSub = true
			f.operation(w)
		case "GET":
			if !strings.Contains(req.URL.Query().Get("filter"), messageCreated) {
				f.t.Error("missing subscription filter")
			}
			var subs []map[string]any
			if f.ownedSub {
				subs = append(subs, f.subscription("subscriptions/ours", f.r.topic()))
			}
			if f.foreignSub {
				subs = append(subs, f.subscription("subscriptions/existing-listener", "projects/other-project/topics/existing-listener"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"subscriptions": subs})
		default:
			f.t.Errorf("unexpected Workspace method: %s", req.Method)
		}
		return
	}
	if req.URL.Path == "/workspace/subscriptions/ours" {
		switch req.Method {
		case "DELETE":
			f.deletions = append(f.deletions, "workspace")
			f.ownedSub = false
			fmt.Fprint(w, `{"done":true}`)
		case "PATCH":
			if req.URL.Query().Get("updateMask") != "ttl" {
				f.t.Error("renewal must update only the lease")
			}
			f.renewals++
			f.operation(w)
		default:
			f.t.Errorf("unexpected owned-subscription method: %s", req.Method)
		}
		return
	}
	if strings.HasPrefix(req.URL.Path, "/workspace/") {
		f.t.Errorf("touched a foreign Workspace resource: %s", req.URL.Path)
		w.WriteHeader(500)
		return
	}
	if strings.HasSuffix(path, ":setIamPolicy") {
		if req.Method != "POST" || strings.TrimSuffix(path, ":setIamPolicy") != f.r.topic() {
			f.t.Error("IAM changes escaped the private topic")
		}
		fmt.Fprint(w, `{}`)
		return
	}
	switch req.Method {
	case "PUT":
		var body struct {
			Labels    map[string]string `json:"labels"`
			Topic     string            `json:"topic"`
			Retention string            `json:"messageRetentionDuration"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		if path == f.r.queue() && (body.Topic != f.r.topic() || body.Retention != "600s") {
			f.t.Error("receiving queue must use the private topic and bounded retention")
		}
		f.resources[path] = body.Labels
		fmt.Fprint(w, `{}`)
	case "GET":
		labels, ok := f.resources[path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"labels": labels})
	case "DELETE":
		f.deletions = append(f.deletions, path)
		delete(f.resources, path)
		fmt.Fprint(w, `{}`)
	default:
		f.t.Errorf("unexpected cloud request: %s %s", req.Method, req.URL.Path)
	}
}

func (f *cloudFixture) subscription(name, topic string) map[string]any {
	return map[string]any{"name": name, "expireTime": time.Now().Add(15 * time.Minute).UTC().Format(time.RFC3339Nano), "state": "ACTIVE", "notificationEndpoint": map[string]string{"pubsubTopic": topic}}
}
func (f *cloudFixture) operation(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(map[string]any{"done": true, "response": f.subscription("subscriptions/ours", f.r.topic())})
}

func newCloudFixture(t *testing.T) (*streamAPI, *cloudFixture, string) {
	t.Helper()
	r, err := newResources("test-project")
	if err != nil {
		t.Fatal(err)
	}
	f := &cloudFixture{t: t, r: r, resources: make(map[string]map[string]string), foreignSub: true}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	a := &streamAPI{client: &Client{http: server.Client()}, pubsubURL: server.URL + "/pubsub", workspaceURL: server.URL + "/workspace"}
	return a, f, filepath.Join(t.TempDir(), "live-resources.json")
}

func TestOwnedResourceLifecycleAndRenewal(t *testing.T) {
	a, f, journal := newCloudFixture(t)
	ctx := context.Background()
	if err := saveResources(journal, f.r); err != nil {
		t.Fatal(err)
	}
	if err := a.create(ctx, f.r, journal); err != nil {
		t.Fatal(err)
	}
	if _, err := a.renew(ctx, f.r, journal); err != nil {
		t.Fatal(err)
	}
	if f.renewals != 1 {
		t.Fatal("lease was not renewed")
	}
	saved, err := loadResources(journal)
	if err != nil || saved.ID != f.r.ID || saved.Expires.IsZero() {
		t.Fatalf("journal did not preserve recoverable ownership: %v", err)
	}
	if err := a.cleanup(ctx, saved); err != nil {
		t.Fatal(err)
	}
	if len(f.deletions) != 3 || f.deletions[0] != "workspace" || f.deletions[1] != f.r.queue() || f.deletions[2] != f.r.topic() || !f.foreignSub || len(f.resources) != 0 {
		t.Fatalf("incorrect cleanup order or ownership: %v", f.deletions)
	}
	if err := a.cleanup(ctx, saved); err != nil {
		t.Fatalf("recovery cleanup must be idempotent: %v", err)
	}
}

func TestFailedSetupCleansOnlyItsPartialResources(t *testing.T) {
	a, f, journal := newCloudFixture(t)
	f.createError = true
	if err := a.create(context.Background(), f.r, journal); !isCloudStatus(err, 409) {
		t.Fatalf("expected subscription conflict, got %v", err)
	}
	if err := a.cleanup(context.Background(), f.r); err != nil {
		t.Fatal(err)
	}
	if len(f.resources) != 0 || !f.foreignSub || len(f.deletions) != 2 {
		t.Fatal("failed setup touched an existing listener or leaked private resources")
	}
}

func TestOwnershipMismatchPreventsEveryDeletion(t *testing.T) {
	a, f, journal := newCloudFixture(t)
	if err := a.create(context.Background(), f.r, journal); err != nil {
		t.Fatal(err)
	}
	f.resources[f.r.topic()] = map[string]string{"app": "another-app"}
	if err := a.cleanup(context.Background(), f.r); err == nil || len(f.deletions) != 0 {
		t.Fatal("cleanup must refuse to touch resources when ownership mismatches")
	}
}

func TestStreamReconnectAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts, reconciles := 0, 0
	err := runStream(ctx, time.Now().Add(15*time.Minute), func(ctx context.Context) error {
		attempts++
		if attempts == 1 {
			return errors.New("transport disconnected")
		}
		cancel()
		return ctx.Err()
	}, func(context.Context) (time.Time, error) { t.Error("unexpected early renewal"); return time.Time{}, nil }, func(e StreamEvent) bool {
		if e.Reconcile {
			reconciles++
		}
		return true
	})
	if err != nil || attempts != 2 || reconciles != 2 {
		t.Fatalf("reconnect failed: attempts=%d reconciles=%d err=%v", attempts, reconciles, err)
	}
}

func TestRenewalFailureStopsReceiver(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := runStream(ctx, time.Now(), func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, func(context.Context) (time.Time, error) { return time.Time{}, errors.New("renewal failed") }, func(StreamEvent) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "renewed") {
		t.Fatalf("renewal failure must stop delivery and surface an error: %v", err)
	}
}
