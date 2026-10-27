package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMarkReadAdvancesOnlyToViewedMessageAndDoesNotRewind(t *testing.T) {
	through := time.Date(2026, 10, 27, 15, 0, 0, 123456000, time.UTC)
	current := through.Add(-time.Minute)
	gets, patches := 0, 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/me/spaces/a/spaceReadState" {
			t.Error("read state targeted a different user or conversation")
		}
		switch r.Method {
		case http.MethodGet:
			gets++
		case http.MethodPatch:
			patches++
			if r.URL.Query().Get("updateMask") != "lastReadTime" || r.Header.Get("Content-Type") != "application/json" {
				t.Error("read state updated unexpected fields")
			}
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["lastReadTime"] != through.Add(time.Nanosecond).Format(time.RFC3339Nano) {
				t.Fatal("read update included unseen messages or lost timestamp precision")
			}
			current = through // Google coerces to the latest message's create time.
		default:
			t.Errorf("unexpected read-state method %s", r.Method)
		}
		fmt.Fprintf(w, `{"name":"users/123/spaces/a/spaceReadState","lastReadTime":%q}`, current.Format(time.RFC3339Nano))
	}))
	defer s.Close()
	c := &Client{http: s.Client(), baseURL: s.URL}
	confirmed, err := c.MarkRead(context.Background(), "spaces/a", through)
	if err != nil || !confirmed.Equal(through) || gets != 1 || patches != 1 {
		t.Fatalf("read update failed: gets=%d patches=%d err=%v", gets, patches, err)
	}
	current = through.Add(time.Minute) // Another client has read newer messages.
	confirmed, err = c.MarkRead(context.Background(), "spaces/a", through)
	if err != nil || !confirmed.Equal(current) || gets != 2 || patches != 1 {
		t.Fatal("older native history rewound Google's newer read position")
	}
	if _, err := c.MarkRead(context.Background(), "spaces/a/messages/m", through); err == nil || gets != 2 {
		t.Fatal("invalid conversation reached the network")
	}
	if _, err := c.MarkRead(context.Background(), "spaces/a", time.Time{}); err == nil || gets != 2 {
		t.Fatal("empty read position reached the network")
	}
}

func TestMarkReadPermissionAndMalformedResponsesDoNotWrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"permission", 403, "private server response"},
		{"rate limit", 429, "private server response"},
		{"different conversation", 200, `{"name":"users/123/spaces/other/spaceReadState"}`},
		{"missing name", 200, `{}`},
		{"malformed", 200, "private server response"},
		{"oversized", 200, strings.Repeat("x", maxResponseBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("failed read-state lookup attempted a write")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer s.Close()
			c := &Client{http: s.Client(), baseURL: s.URL}
			_, err := c.MarkRead(context.Background(), "spaces/a", time.Now())
			if err == nil || strings.Contains(err.Error(), "private server") || (tc.status == 403 && !errors.Is(err, ErrReadStatePermission)) {
				t.Fatalf("read-state error missing or leaked server data: %v", err)
			}
		})
	}
}

func TestMarkReadCancellationBeforePatchAndUnconfirmedUpdate(t *testing.T) {
	for _, cancelBeforePatch := range []bool{true, false} {
		t.Run(fmt.Sprint(cancelBeforePatch), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			patches := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && cancelBeforePatch {
					cancel()
				}
				if r.Method == http.MethodPatch {
					patches++
				}
				fmt.Fprint(w, `{"name":"users/me/spaces/a/spaceReadState"}`)
			}))
			defer s.Close()
			c := &Client{http: s.Client(), baseURL: s.URL}
			if _, err := c.MarkRead(ctx, "spaces/a", time.Now()); err == nil {
				t.Fatal("canceled or unconfirmed update reported success")
			}
			if cancelBeforePatch && patches != 0 {
				t.Fatal("canceled read-state update sent a PATCH")
			}
		})
	}
}
