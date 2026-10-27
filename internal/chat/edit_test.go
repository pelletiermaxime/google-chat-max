package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEditPatchesOnlyTextAndNeverCreatesMessages(t *testing.T) {
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPatch || r.URL.Path != "/spaces/a/messages/m" || r.URL.Query().Get("updateMask") != "text" || r.URL.Query().Get("allowMissing") != "false" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("edit changed its target or updated more than text")
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 || body["text"] != "Updated\nmessage 👋" {
			t.Error("unexpected edit payload")
		}
		fmt.Fprint(w, `{"name":"spaces/a/messages/m","text":"Updated\nmessage 👋","lastUpdateTime":"2026-10-27T16:00:00Z"}`)
	}))
	defer s.Close()
	c := &Client{http: s.Client(), baseURL: s.URL}
	for range 2 {
		m, err := c.Edit(context.Background(), "spaces/a/messages/m", "Updated\nmessage 👋")
		if err != nil || m.Text != "Updated\nmessage 👋" || m.LastUpdateTime.IsZero() {
			t.Fatalf("edit failed: %v", err)
		}
	}
	for _, input := range []struct{ name, text string }{
		{"spaces/a/messages/m?other=1", "text"},
		{"spaces/a/messages/m", " \n\t"},
		{"spaces/a/messages/m", strings.Repeat("a", 32000)},
		{"spaces/a/messages/m", strings.Repeat("\n", 16000)},
	} {
		if _, err := c.Edit(context.Background(), input.name, input.text); err == nil {
			t.Fatal("invalid or oversized edit accepted")
		}
	}
	if requests != 2 {
		t.Fatal("invalid edits reached the server")
	}
}

func TestEditRejectsUnconfirmedResponsesAndRedactsServerErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{403, "private response body"},
		{404, "private response body"},
		{429, "private response body"},
		{503, "private response body"},
		{200, `{"name":"spaces/a/messages/other","lastUpdateTime":"2026-10-27T16:00:00Z"}`},
		{200, `{"name":"spaces/a/messages/m"}`},
		{200, `{"name":"spaces/a/messages/m","lastUpdateTime":"2026-10-27T16:00:00Z"}`},
		{200, "private response body"},
	} {
		t.Run(fmt.Sprint(tc.status, tc.body), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer s.Close()
			c := &Client{http: s.Client(), baseURL: s.URL}
			_, err := c.Edit(context.Background(), "spaces/a/messages/m", "Updated")
			if err == nil || strings.Contains(err.Error(), "private response body") {
				t.Fatalf("unconfirmed edit accepted or response leaked: %v", err)
			}
		})
	}
}
