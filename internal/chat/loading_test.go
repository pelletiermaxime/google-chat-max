package chat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHistorySmallFirstPagePreservesOlderCursor(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		switch query.Get("pageToken") {
		case "":
			if query.Get("pageSize") != "20" {
				t.Error("first page must request only 20 messages")
			}
			fmt.Fprint(w, `{"messages":[{"name":"spaces/a/messages/new","createTime":"2026-10-26T15:00:00Z"}],"nextPageToken":"older"}`)
		case "older":
			if query.Get("pageSize") != "60" {
				t.Error("older pages must retain the 60-message batch size")
			}
			fmt.Fprint(w, `{"messages":[{"name":"spaces/a/messages/old","createTime":"2026-10-26T14:00:00Z"}],"nextPageToken":"oldest"}`)
		default:
			t.Error("paging cursor changed")
		}
	}))
	defer s.Close()
	c := &Client{http: s.Client(), baseURL: s.URL}
	first, err := c.History(context.Background(), "spaces/a", "", "")
	if err != nil || first.Next != "older" || len(first.Messages) != 1 {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	older, err := c.History(context.Background(), "spaces/a", "", first.Next)
	if err != nil || older.Next != "oldest" || len(older.Messages) != 1 || !older.Messages[0].CreateTime.Before(first.Messages[0].CreateTime) {
		t.Fatalf("older page = %#v, %v", older, err)
	}
}

func TestHistoryUsesCachedIdentityWithoutMembershipRequests(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprint(cached), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/spaces/a/messages" {
					t.Error("history must not wait for an identity request")
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				fmt.Fprint(w, `{"messages":[{"name":"spaces/a/messages/m","sender":{"name":"users/alex"}}]}`)
			}))
			defer s.Close()
			c := &Client{http: s.Client(), baseURL: s.URL, identities: true}
			if cached {
				c.members = map[string]memberCache{"spaces/a": {users: []User{{Name: "users/alex", DisplayName: "Alex"}}, at: time.Now()}}
			}
			page, err := c.History(context.Background(), "spaces/a", "", "")
			if err != nil || len(page.Messages) != 1 {
				t.Fatalf("history = %#v, %v", page, err)
			}
			if got := page.Messages[0].Sender.DisplayName; (got == "Alex") != cached {
				t.Fatalf("cached=%v, sender=%q", cached, got)
			}
		})
	}
}
