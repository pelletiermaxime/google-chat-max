package chat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIdentityNamesExcludeSelfAndReuseMembershipCache(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/me":
			fmt.Fprint(w, `{"sub":"self"}`)
		case "/spaces/dm/members":
			calls++
			fmt.Fprint(w, `{"memberships":[{"member":{"name":"users/self","displayName":"Me"}},{"member":{"name":"users/peer","displayName":"Alex","email":"alex@example.com","avatarUrl":"https://lh3.googleusercontent.com/avatar"}}]}`)
		default:
			t.Errorf("unexpected identity request %s", r.URL.Path)
		}
	}))
	defer s.Close()
	c := &Client{http: s.Client(), baseURL: s.URL, identityURL: s.URL + "/me"}
	spaces := []Space{{Name: "spaces/dm", SpaceType: "DIRECT_MESSAGE"}}
	for range 2 {
		if _, err := c.ResolveIdentity(context.Background()); err != nil {
			t.Fatal(err)
		}
		resolved, err := c.ResolveSpaceIdentity(context.Background(), spaces[0])
		if err != nil {
			t.Fatal(err)
		}
		spaces[0] = resolved
	}
	if c.Self() != "users/self" {
		t.Fatal("authenticated identity was not exposed from the cached profile")
	}
	if calls != 1 || spaces[0].Title() != "Alex" || spaces[0].AvatarURL == "" || len(spaces[0].Participants) != 2 {
		t.Fatalf("identity enrichment failed: requests=%d title=%q", calls, spaces[0].Title())
	}
}

func TestSpacesReturnsBeforeAnyUncachedIdentityRequests(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprint(cached), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/spaces" {
					t.Error("conversation listing must not request profile or memberships")
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				fmt.Fprint(w, `{"spaces":[{"name":"spaces/dm","spaceType":"DIRECT_MESSAGE"}]}`)
			}))
			defer s.Close()
			c := &Client{http: s.Client(), baseURL: s.URL, identityURL: s.URL + "/me", identities: true}
			if cached {
				c.self = "users/self"
				c.members = map[string]memberCache{"spaces/dm": {users: []User{{Name: "users/peer", DisplayName: "Alex"}}, at: time.Now()}}
			}
			spaces, err := c.Spaces(context.Background())
			if err != nil || len(spaces) != 1 || (spaces[0].DisplayName == "Alex") != cached {
				t.Fatalf("cached=%v, spaces=%#v, err=%v", cached, spaces, err)
			}
		})
	}
}
