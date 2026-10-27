package desktop

import (
	"google-chat-max/internal/chat"
	"testing"
)

func TestParticipantSearchAndLiveAuthorResolution(t *testing.T) {
	a := New(nil, func(func()) {}, func() {}, false)
	defer a.Close()
	s := chat.Space{Name: "spaces/a", DisplayName: "Group", Participants: []chat.User{{Name: "users/a", DisplayName: "Alex", Email: "alex@example.com"}}}
	a.rememberUsers(s)
	a.query = "alex@example"
	if !a.matches(s) {
		t.Fatal("participant search did not resolve names/email")
	}
	m := chat.Message{Sender: chat.User{Name: "users/a"}}
	if a.author(m).DisplayName != "Alex" {
		t.Fatal("live sender did not use known membership identity")
	}
	for _, bad := range []string{"http://lh3.googleusercontent.com/a", "https://127.0.0.1/a", "https://google.com.evil.test/a", "https://user:secret@google.com/a"} {
		if avatarHost(bad) {
			t.Fatal("unsafe avatar URL accepted")
		}
	}
	if !avatarHost("https://lh3.googleusercontent.com/a") {
		t.Fatal("Google avatar rejected")
	}
}
