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

func TestReactionDesiredStateRetryAndOwnership(t *testing.T) {
	for _, emoji := range []Emoji{{Unicode: "👍"}, {CustomEmoji: &CustomEmoji{Name: "customEmojis/yep", UID: "yep", EmojiName: ":yep:", TemporaryImageURI: "https://lh3.googleusercontent.com/image"}}} {
		t.Run(emoji.Key(), func(t *testing.T) {
			own, posts, deletes := false, 0, 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/reactions"):
					if filter := r.URL.Query().Get("filter"); filter != "" && filter != `user.name = "users/self"` {
						t.Error("reaction lookup must use the resolved caller ID")
					}
					reactions := []Reaction{}
					if own {
						reactions = append(reactions, Reaction{Name: "spaces/a/messages/m/reactions/own", Emoji: emoji, User: User{Name: "users/self"}})
					}
					json.NewEncoder(w).Encode(map[string]any{"reactions": reactions})
				case r.Method == http.MethodGet && r.URL.Path == "/spaces/a/messages/m":
					count := 2
					if own {
						count++
					}
					json.NewEncoder(w).Encode(Message{Name: "spaces/a/messages/m", EmojiReactionSummaries: []ReactionSummary{{Emoji: emoji, Count: count}}})
				case r.Method == http.MethodPost:
					posts++
					var body map[string]json.RawMessage
					json.NewDecoder(r.Body).Decode(&body)
					var sent map[string]json.RawMessage
					json.Unmarshal(body["emoji"], &sent)
					if emoji.CustomEmoji != nil {
						var custom map[string]string
						json.Unmarshal(sent["customEmoji"], &custom)
						if custom["name"] != emoji.CustomEmoji.Name || len(custom) != 1 {
							t.Error("custom reaction must send only its resource name")
						}
					} else if string(sent["unicode"]) != `"👍"` {
						t.Error("unicode reaction changed")
					}
					own = true
					w.WriteHeader(503) // The server applied it before the response was lost.
				case r.Method == http.MethodDelete:
					if r.URL.Path != "/spaces/a/messages/m/reactions/own" {
						t.Error("another user's reaction was targeted")
					}
					deletes++
					own = false
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected reaction request %s %s", r.Method, r.URL.Path)
				}
			}))
			defer s.Close()
			c := &Client{http: s.Client(), baseURL: s.URL, self: "users/self"}
			if _, err := c.SetReaction(context.Background(), "spaces/a/messages/m", emoji, true); err == nil {
				t.Fatal("uncertain mutation was treated as confirmed")
			}
			state, err := c.SetReaction(context.Background(), "spaces/a/messages/m", emoji, true)
			if err != nil || !state.Has(emoji) || posts != 1 || state.Summaries[0].Count != 3 {
				t.Fatalf("retry reversed or duplicated an applied reaction: %v", err)
			}
			state, err = c.SetReaction(context.Background(), "spaces/a/messages/m", emoji, false)
			if err != nil || state.Has(emoji) || deletes != 1 || state.Summaries[0].Count != 2 {
				t.Fatalf("removal changed other users' counts: %v", err)
			}
			if _, err = c.SetReaction(context.Background(), "spaces/a/messages/m", emoji, false); err != nil || deletes != 1 {
				t.Fatal("removal retry repeated the mutation")
			}
		})
	}
}

func TestReactionParticipantsPaginationAndOwnership(t *testing.T) {
	emoji := Emoji{Unicode: "👍"}
	other := Reaction{Name: "spaces/a/messages/m/reactions/other", Emoji: emoji, User: User{Name: "users/other", DisplayName: "Alex Morgan"}}
	own := Reaction{Name: "spaces/a/messages/m/reactions/own", Emoji: emoji, User: User{Name: "users/self"}}
	pages := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("loading participants must not mutate reactions: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/spaces/a/messages/m" {
			json.NewEncoder(w).Encode(Message{EmojiReactionSummaries: []ReactionSummary{{Emoji: emoji, Count: 2}}})
			return
		}
		pages++
		if r.URL.Query().Get("filter") != "" {
			t.Error("participant lookup must include other users")
		}
		if !strings.Contains(r.URL.Query().Get("fields"), "displayName") {
			t.Error("participant lookup must request display names")
		}
		if r.URL.Query().Get("pageToken") == "" {
			json.NewEncoder(w).Encode(map[string]any{"reactions": []Reaction{other}, "nextPageToken": "next"})
		} else {
			json.NewEncoder(w).Encode(map[string]any{"reactions": []Reaction{own}})
		}
	})
	c := &Client{http: &http.Client{Transport: reactionTestTransport{handler}}, baseURL: "https://chat.test", self: "users/self"}
	state, err := c.Reactions(context.Background(), "spaces/a/messages/m")
	if err != nil {
		t.Fatal(err)
	}
	if pages != 2 || len(state.All) != 2 || state.All[0].User.DisplayName != "Alex Morgan" {
		t.Fatalf("reaction participants or names were lost: %+v", state)
	}
	if len(state.Mine) != 1 || state.Mine[0].Name != own.Name || !state.Has(emoji) {
		t.Fatalf("another user's reaction was treated as owned: %+v", state.Mine)
	}
}

type reactionTestTransport struct{ handler http.Handler }

func (transport reactionTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	transport.handler.ServeHTTP(w, r)
	return w.Result(), nil
}

func TestReactionRejectsUnverifiedOwnerAndInvalidTargets(t *testing.T) {
	mutations := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations++
			return
		}
		fmt.Fprint(w, `{"reactions":[{"name":"spaces/a/messages/m/reactions/other","emoji":{"unicode":"👍"},"user":{"name":"users/other"}}]}`)
	}))
	defer s.Close()
	c := &Client{http: s.Client(), baseURL: s.URL, self: "users/self"}
	if _, err := c.SetReaction(context.Background(), "spaces/a/messages/m", Emoji{Unicode: "👍"}, false); err == nil || mutations != 0 {
		t.Fatal("unverified reaction ownership reached deletion")
	}
	c = &Client{}
	if _, err := c.SetReaction(context.Background(), "spaces/a/messages/m", Emoji{CustomEmoji: &CustomEmoji{Name: "customEmojis/../bad"}}, true); err == nil {
		t.Fatal("invalid emoji reached the network")
	}
	for _, name := range []string{"spaces/a/messages/m/reactions/../x", "spaces/a/messages/m/reactions/%2f", "spaces/a/messages/m"} {
		if ReactionMessage(name) != "" {
			t.Fatal("invalid reaction resource was accepted")
		}
	}
}

func TestCustomEmojiPaginationAndReactionEventDecoding(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageToken") == "" {
			fmt.Fprint(w, `{"customEmojis":[{"name":"customEmojis/yep","uid":"yep","emojiName":":yep:","temporaryImageUri":"https://lh3.googleusercontent.com/yep"}],"nextPageToken":"next"}`)
		} else {
			fmt.Fprint(w, `{"customEmojis":[{"name":"customEmojis/hey","uid":"hey","emojiName":":peepohey:"}]}`)
		}
	}))
	defer s.Close()
	c := &Client{http: s.Client(), baseURL: s.URL}
	emojis, err := c.CustomEmojis(context.Background())
	if err != nil || len(emojis) != 2 || emojis[0].EmojiName != ":yep:" || emojis[0].TemporaryImageURI == "" {
		t.Fatalf("custom emoji metadata lost: %v", err)
	}
	for _, kind := range []string{reactionCreated, reactionDeleted} {
		event, _, err := decodeStreamEvent(map[string]string{"ce-type": kind}, []byte(`{"reaction":{"name":"spaces/a/messages/m/reactions/r"}}`))
		if err != nil || event.ReactionMessage != "spaces/a/messages/m" || event.Message != nil {
			t.Fatalf("reaction event did not target message reconciliation: %v", err)
		}
	}
}
