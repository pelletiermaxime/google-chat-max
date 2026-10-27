package chat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestThreadFetchesParentOutsideLatestReplyPage(t *testing.T) {
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("filter") != "thread.name = spaces/a/threads/t" {
			t.Error("thread filter escaped its destination")
		}
		if r.URL.Query().Get("orderBy") == "createTime ASC" {
			if !strings.Contains(r.URL.Query().Get("fields"), "cardsV2") {
				t.Error("parent lookup excludes cards")
			}
			fmt.Fprint(w, `{"messages":[{"name":"spaces/a/messages/root","text":"Parent","cards":[{"header":{"title":"Update"}}],"thread":{"name":"spaces/a/threads/t"}}]}`)
		} else {
			fmt.Fprint(w, `{"messages":[{"name":"spaces/a/messages/reply","text":"Reply","threadReply":true,"thread":{"name":"spaces/a/threads/t"}}],"nextPageToken":"older"}`)
		}
	}))
	defer s.Close()
	c := &Client{http: s.Client(), baseURL: s.URL}
	result, err := c.Thread(context.Background(), "spaces/a", "spaces/a/threads/t")
	if err != nil || result.Root == nil || result.Root.Text != "Parent" || len(result.Page.Messages) != 1 || result.Page.Next != "older" || requests != 2 {
		t.Fatalf("thread context lost: %#v %v", result, err)
	}
	if len(result.Root.Cards) != 1 {
		t.Fatal("parent lookup lost card content")
	}
	if _, err := c.Thread(context.Background(), "spaces/a", "spaces/other/threads/t"); err == nil || requests != 2 {
		t.Fatal("cross-conversation thread accepted")
	}
}
