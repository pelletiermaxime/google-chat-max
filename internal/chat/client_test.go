package chat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadOnlyPaginationAndHistory(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected mutation: %s", r.Method)
		}
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/spaces":
			if r.URL.Query().Get("pageToken") == "" {
				fmt.Fprint(w, `{"spaces":[{"name":"spaces/z","displayName":"Zebra"}],"nextPageToken":"page-2"}`)
			} else {
				if r.URL.Query().Get("pageToken") != "page-2" {
					t.Error("pagination token was not preserved")
				}
				fmt.Fprint(w, `{"spaces":[{"name":"spaces/a","displayName":"Alpha","spaceType":"SPACE","spaceThreadingState":"UNTHREADED_MESSAGES","lastActiveTime":"2026-10-26T14:00:00Z"}]}`)
			}
		case "/spaces/a/messages":
			if r.URL.Query().Get("pageSize") != "20" || r.URL.Query().Get("orderBy") != "createTime DESC" {
				t.Error("history must request a bounded newest-first window")
			}
			if !strings.Contains(r.URL.Query().Get("fields"), "attachmentDataRef(resourceName)") {
				t.Error("history excludes authenticated image references")
			}
			fmt.Fprint(w, `{"messages":[{"name":"new","createTime":"2026-10-26T15:00:00Z","text":"Newest","sender":{"displayName":"Alex"},"threadReply":true,"thread":{"name":"threads/t"},"attachment":[{"contentName":"sample.png","contentType":"image/png","attachmentDataRef":{"resourceName":"opaque-reference"}}]},{"name":"old","createTime":"2026-10-26T14:00:00Z","text":"Oldest"}]}`)
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c := &Client{http: server.Client(), baseURL: server.URL}
	spaces, err := c.Spaces(context.Background())
	if err != nil || len(spaces) != 2 || spaces[0].Title() != "Alpha" {
		t.Fatalf("paginated spaces = %v, %v", spaces, err)
	}
	if spaces[0].SpaceThreadingState != "UNTHREADED_MESSAGES" || spaces[0].LastActiveTime.IsZero() {
		t.Fatal("sidebar grouping and activity metadata were not decoded")
	}
	messages, err := c.Messages(context.Background(), spaces[0].Name)
	if err != nil || len(messages) != 2 || messages[0].Name != "old" || messages[1].Author() != "Alex" || !messages[1].ThreadReply || messages[1].Thread.Name != "threads/t" {
		t.Fatalf("history = %v, %v", messages, err)
	}
	if len(paths) != 3 {
		t.Fatalf("expected two list requests and one history request, got %v", paths)
	}
	if len(messages[1].Attachment) != 1 || !messages[1].Attachment[0].IsImage() || messages[1].Attachment[0].AttachmentDataRef.ResourceName != "opaque-reference" {
		t.Fatal("image metadata was lost during history decoding")
	}
}

func TestResponseAndPaginationBounds(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"oversized", strings.Repeat("x", maxResponseBytes+1), "4 MiB"},
		{"malformed", `{"messages":`, "invalid response"},
		{"looping pagination", `{"spaces":[],"nextPageToken":"same"}`, "10 page"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			defer server.Close()
			c := &Client{http: server.Client(), baseURL: server.URL}
			_, err := c.Spaces(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
}

func TestErrorBodyAndInvalidResourceAreNotExposed(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "sensitive server response")
	}))
	defer server.Close()
	c := &Client{http: server.Client(), baseURL: server.URL}
	_, err := c.Messages(context.Background(), "spaces/a/messages/b")
	if err == nil || requests != 0 {
		t.Fatal("invalid resource must be rejected before making a request")
	}
	_, err = c.Spaces(context.Background())
	if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("unexpected access error: %v", err)
	}
}

func TestHistoryCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	c := &Client{http: server.Client(), baseURL: server.URL}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.Messages(ctx, "spaces/a"); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled history request succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("history request did not cancel")
	}
}
