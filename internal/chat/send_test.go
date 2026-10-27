package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendRetryKeepsIdempotentDestinationAndDoesNotFallBackToNewThread(t *testing.T) {
	requests, created := 0, 0
	seen := map[string]bool{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		id := r.URL.Query().Get("requestId")
		if r.Method != "POST" || r.URL.Path != "/spaces/a/messages" || r.URL.Query().Get("messageReplyOption") != "REPLY_MESSAGE_OR_FAIL" {
			t.Error("unsafe send destination or fallback")
		}
		var body struct {
			Text   string `json:"text"`
			Thread struct {
				Name string `json:"name"`
			} `json:"thread"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Text != "Reply" || body.Thread.Name != "spaces/a/threads/t" {
			t.Error("retry changed its message")
		}
		if !seen[id] {
			seen[id] = true
			created++
		}
		if requests == 1 {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{"name":"spaces/a/messages/sent","text":"Reply","createTime":"2026-10-27T02:00:00Z","threadReply":true,"thread":{"name":"spaces/a/threads/t"}}`)
	}))
	defer s.Close()
	c := &Client{http: s.Client(), baseURL: s.URL}
	id, _ := NewRequestID()
	_, err := c.Send(context.Background(), "spaces/a", "spaces/a/threads/t", "Reply", id)
	var e *SendError
	if !errors.As(err, &e) || !e.Uncertain {
		t.Fatal("ambiguous delivery must not look like a definite failure")
	}
	m, err := c.Send(context.Background(), "spaces/a", "spaces/a/threads/t", "Reply", id)
	if err != nil || m.Name != "spaces/a/messages/sent" || created != 1 || requests != 2 {
		t.Fatalf("retry created duplicates: %d %d %v", created, requests, err)
	}
	if _, err := c.Send(context.Background(), "spaces/a", "spaces/b/threads/t", "Reply", id); err == nil || requests != 2 {
		t.Fatal("cross-conversation reply was sent")
	}
}

func TestAcceptedSendHydratesPartialIdempotentResponse(t *testing.T) {
	posts, gets := 0, 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts++
			fmt.Fprint(w, `{"name":"spaces/a/messages/sent","text":"Hello"}`)
			return
		}
		gets++
		if r.URL.Path != "/spaces/a/messages/sent" {
			t.Error("wrong metadata hydration target")
		}
		fmt.Fprint(w, `{"name":"spaces/a/messages/sent","text":"Hello","createTime":"2026-10-27T02:00:00Z","thread":{"name":"spaces/a/threads/t"}}`)
	}))
	defer s.Close()
	c := &Client{http: s.Client(), baseURL: s.URL}
	id, _ := NewRequestID()
	m, err := c.Send(context.Background(), "spaces/a", "", "Hello", id)
	if err != nil || m.Thread.Name == "" || m.CreateTime.IsZero() || posts != 1 || gets != 1 {
		t.Fatalf("partial response hydration failed: %d %d %v", posts, gets, err)
	}
}
