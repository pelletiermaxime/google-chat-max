package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestQuotedSendUsesVersionAndRetainsTheSamePayloadOnRetry(t *testing.T) {
	for _, thread := range []string{"", "spaces/a/threads/t"} {
		t.Run(thread, func(t *testing.T) {
			requests := 0
			var previous string
			version := time.Date(2026, 10, 27, 12, 34, 56, 123456000, time.UTC)
			quote := QuotedMessageMetadata{Name: "spaces/a/messages/original", LastUpdateTime: version, QuotedMessageSnapshot: &QuotedMessageSnapshot{Sender: "Alex", Text: "Preview only"}}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != "POST" || r.URL.Path != "/spaces/a/messages" {
					t.Error("quoted send used the wrong endpoint")
				}
				if r.URL.Query().Get("requestId") != "12345678-1234-1234-1234-123456789abc" {
					t.Error("quoted retry changed its request ID")
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if string(body["text"]) != `"My reply"` {
					t.Error("quote altered the user's reply text")
				}
				var metadata map[string]string
				if err := json.Unmarshal(body["quotedMessageMetadata"], &metadata); err != nil {
					t.Error(err)
				}
				if metadata["name"] != quote.Name || metadata["lastUpdateTime"] != version.Format(time.RFC3339Nano) || len(metadata) != 2 {
					t.Error("quote reference/version missing, or output-only snapshot was sent")
				}
				if thread != "" {
					if r.URL.Query().Get("messageReplyOption") != "REPLY_MESSAGE_OR_FAIL" || !strings.Contains(string(body["thread"]), thread) {
						t.Error("quoted thread reply changed destination")
					}
				} else if len(body["thread"]) != 0 || r.URL.Query().Get("messageReplyOption") != "" {
					t.Error("root quote unexpectedly started a thread reply")
				}
				encoded, _ := json.Marshal(body)
				if requests == 1 {
					previous = string(encoded)
					w.WriteHeader(503)
					return
				}
				if string(encoded) != previous {
					t.Error("quoted retry changed its payload")
				}
				fmt.Fprint(w, `{"name":"spaces/a/messages/sent","text":"My reply","createTime":"2026-10-27T13:00:00Z","thread":{"name":"spaces/a/threads/t"}}`)
			}))
			defer s.Close()
			c := &Client{http: s.Client(), baseURL: s.URL}
			id := "12345678-1234-1234-1234-123456789abc"
			if _, err := c.SendQuoted(context.Background(), "spaces/a", thread, "My reply", id, quote); err == nil {
				t.Fatal("ambiguous quoted send was treated as success")
			}
			m, err := c.SendQuoted(context.Background(), "spaces/a", thread, "My reply", id, quote)
			if err != nil || requests != 2 || m.QuotedMessageMetadata == nil || m.QuotedMessageMetadata.Name != quote.Name {
				t.Fatalf("quoted retry lost metadata: %v", err)
			}
		})
	}
}

func TestQuoteValidationAndIncomingMetadata(t *testing.T) {
	c := &Client{}
	for _, quote := range []QuotedMessageMetadata{{Name: "spaces/other/messages/m", LastUpdateTime: time.Now()}, {Name: "spaces/a/messages/m"}} {
		if _, err := c.SendQuoted(context.Background(), "spaces/a", "", "Reply", "12345678-1234-1234-1234-123456789abc", quote); err == nil {
			t.Fatal("invalid quote reached the network")
		}
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, field := range []string{"lastUpdateTime", "quotedMessageMetadata", "quotedMessageSnapshot"} {
			if !strings.Contains(r.URL.Query().Get("fields"), field) {
				t.Errorf("history excludes %s", field)
			}
		}
		fmt.Fprint(w, `{"messages":[{"name":"spaces/a/messages/reply","text":"Agreed","createTime":"2026-10-27T13:00:00Z","lastUpdateTime":"2026-10-27T13:01:00Z","quotedMessageMetadata":{"name":"spaces/a/messages/source","lastUpdateTime":"2026-10-27T12:00:00Z","quotedMessageSnapshot":{"sender":"Alex","text":"Original message"}}}]}`)
	}))
	defer s.Close()
	c = &Client{http: s.Client(), baseURL: s.URL}
	page, err := c.History(context.Background(), "spaces/a", "", "")
	if err != nil || len(page.Messages) != 1 {
		t.Fatalf("history failed: %v", err)
	}
	m := page.Messages[0]
	if m.LastUpdateTime.IsZero() || m.QuotedMessageMetadata == nil || m.QuotedMessageMetadata.QuotedMessageSnapshot.Text != "Original message" {
		t.Fatal("received quote content or message version was dropped")
	}
	event, _, err := decodeStreamEvent(map[string]string{"ce-type": messageCreated}, []byte(`{"message":{"name":"spaces/a/messages/reply","createTime":"2026-10-27T13:00:00Z","quotedMessageMetadata":{"name":"spaces/a/messages/source","lastUpdateTime":"2026-10-27T12:00:00Z","quotedMessageSnapshot":{"sender":"Alex","text":"Live quote"}}}}`))
	if err != nil || event.Message == nil || event.Message.QuotedMessageMetadata == nil || event.Message.QuotedMessageMetadata.QuotedMessageSnapshot.Text != "Live quote" {
		t.Fatal("live quote content was dropped")
	}
}
