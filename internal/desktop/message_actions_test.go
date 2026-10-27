package desktop

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

func actionFixture(t *testing.T) (*App, chan func(), chat.Message) {
	t.Helper()
	callbacks := make(chan func(), 16)
	a := New(nil, func(fn func()) { callbacks <- fn }, func() {}, true)
	a.backend = demoBackend{}
	a.selected = "spaces/design"
	a.spaces = []chat.Space{{Name: a.selected, DisplayName: "Design studio", SpaceType: "SPACE", SpaceThreadingState: "THREADED_MESSAGES"}}
	messages, _ := a.backend.Messages(context.Background(), a.selected)
	m := messages[0]
	m.Text = "How should we handle this update?"
	a.messages = []chat.Message{m}
	return a, callbacks, m
}

func hoverMessage(t *testing.T, tt *ui.Tester, text string) ui.Rect {
	t.Helper()
	rect, ok := tt.Find(text)
	if !ok {
		t.Fatalf("message %q is missing", text)
	}
	tt.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	tt.Frame()
	return rect
}

func TestNativeHoverToolbarQuoteCancelAndThreadReply(t *testing.T) {
	for _, width := range []int{760, 1180, 1920} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a, callbacks, m := actionFixture(t)
			defer a.Close()
			d := a.draftFor(a.selected + "|")
			d.Text = "My existing draft"
			tt := ui.NewTester(a.View, width, 900)
			tt.Frame()
			if _, ok := tt.Find("Quote reply"); ok {
				t.Fatal("toolbar is visible without hover or focus")
			}
			before := hoverMessage(t, tt, m.Text)
			for _, label := range []string{"Reply in thread", "Quote reply"} {
				rect, ok := tt.Find(label)
				if !ok || rect.X < 303 || rect.X+rect.W > float32(width) {
					t.Fatalf("%s toolbar action is missing or outside the conversation: %v", label, rect)
				}
			}
			after, _ := tt.Find(m.Text)
			if before.Y != after.Y {
				t.Fatal("hover toolbar changed message layout")
			}
			quote, _ := tt.Find("Quote reply")
			tt.Move(quote.X+quote.W/2, quote.Y+quote.H/2)
			tt.Frame()
			if _, ok := tt.Find("Quote reply"); !ok {
				t.Fatal("toolbar disappeared while moving from the message onto its action")
			}
			if path := os.Getenv("CHAT_MAX_ACTION_SCREENSHOT"); path != "" && width == 1180 {
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := png.Encode(f, tt.Image()); err != nil {
					f.Close()
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := tt.Click("Quote reply"); err != nil {
				t.Fatal(err)
			}
			if d.Quote == nil || d.Quote.Name != m.Name || d.Text != "My existing draft" || !tt.HasText("Cancel quote") {
				t.Fatal("quote action lost the source, existing draft, or cancel control")
			}
			if path := os.Getenv("CHAT_MAX_QUOTE_SCREENSHOT"); path != "" && width == 1180 {
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := png.Encode(f, tt.Image()); err != nil {
					f.Close()
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}
			tt.Type(" follow-up")
			if !strings.Contains(d.Text, "follow-up") || !strings.Contains(d.Text, "My existing draft") {
				t.Fatal("quote action did not focus the composer and preserve its text")
			}
			text := d.Text
			if err := tt.Click("Cancel quote"); err != nil {
				t.Fatal(err)
			}
			if d.Quote != nil || d.Text != text {
				t.Fatal("canceling a quote changed the written reply")
			}
			hoverMessage(t, tt, m.Text)
			if err := tt.Click("Reply in thread"); err != nil {
				t.Fatal(err)
			}
			applyNext(t, callbacks)
			tt.Frame()
			if a.threadName != m.Thread.Name || a.threadRoot == nil {
				t.Fatal("hover action did not open the message's thread")
			}
			tt.Type("Thread reply draft")
			if a.draftFor(a.selected+"|"+m.Thread.Name).Text != "Thread reply draft" || d.Text != text {
				t.Fatalf("thread action did not focus its isolated reply draft: thread=%q main=%q before=%q focus=%q", a.draftFor(a.selected+"|"+m.Thread.Name).Text, d.Text, text, a.focusDraft)
			}
		})
	}
}

type quoteCall struct {
	space, thread, text, id string
	quote                   chat.QuotedMessageMetadata
	result                  chan sendResult
}
type controlledQuoteSend struct {
	demoBackend
	calls chan quoteCall
}

func (b controlledQuoteSend) SendQuoted(ctx context.Context, space, thread, text, id string, quote chat.QuotedMessageMetadata) (chat.Message, error) {
	call := quoteCall{space: space, thread: thread, text: text, id: id, quote: quote, result: make(chan sendResult, 1)}
	select {
	case b.calls <- call:
	case <-ctx.Done():
		return chat.Message{}, ctx.Err()
	}
	select {
	case result := <-call.result:
		return result.m, result.err
	case <-ctx.Done():
		return chat.Message{}, ctx.Err()
	}
}
func nextQuoteCall(t *testing.T, b controlledQuoteSend) quoteCall {
	t.Helper()
	select {
	case call := <-b.calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("quoted send did not start")
		return quoteCall{}
	}
}

func TestQuotedComposerSafeRetryKeepsQuoteVersionAndDestination(t *testing.T) {
	a, callbacks, m := actionFixture(t)
	defer a.Close()
	b := controlledQuoteSend{calls: make(chan quoteCall, 4)}
	a.backend = b
	m.LastUpdateTime = m.CreateTime.Add(time.Minute)
	a.messages[0] = m
	a.quoteMessage(m)
	d := a.draftFor(a.selected + "|")
	d.Text = "My quoted reply"
	if d.Quote == nil || !d.Quote.LastUpdateTime.Equal(m.LastUpdateTime) {
		t.Fatal("edited message quote did not use its latest version")
	}
	tt := ui.NewTester(a.View, 1180, 900)
	if len(b.calls) != 0 {
		t.Fatal("selecting/rendering a quote sent a message")
	}
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	first := nextQuoteCall(t, b)
	if first.thread != "" || first.text != d.Text || first.quote.Name != m.Name || !first.quote.LastUpdateTime.Equal(m.LastUpdateTime) {
		t.Fatal("quoted send changed its payload or destination")
	}
	a.quoteMessage(m)
	a.submitDraft(a.selected, "")
	if len(b.calls) != 0 {
		t.Fatal("pending quoted send accepted a duplicate")
	}
	first.result <- sendResult{err: &chat.SendError{Uncertain: true, Detail: "Uncertain delivery"}}
	applyNext(t, callbacks)
	tt.Frame()
	if d.Quote == nil || !d.Uncertain || d.Text != "My quoted reply" {
		t.Fatal("uncertain send lost the quote or draft")
	}
	changed := m
	changed.LastUpdateTime = m.LastUpdateTime.Add(time.Minute)
	a.quoteMessage(changed)
	if !d.Quote.LastUpdateTime.Equal(m.LastUpdateTime) {
		t.Fatal("an uncertain request's quote version was replaced")
	}
	if err := tt.Click("Retry safely"); err != nil {
		t.Fatal(err)
	}
	retry := nextQuoteCall(t, b)
	if retry.id != first.id || retry.thread != first.thread || retry.text != first.text || retry.quote.Name != first.quote.Name || !retry.quote.LastUpdateTime.Equal(first.quote.LastUpdateTime) {
		t.Fatal("quoted retry did not preserve request identity and payload")
	}
	quote := retry.quote
	sent := liveMessage(a.selected, "quoted", "My quoted reply", 1)
	sent.QuotedMessageMetadata = &quote
	retry.result <- sendResult{m: sent}
	applyNext(t, callbacks)
	tt.Frame()
	if d.Quote != nil || d.Text != "" || !tt.HasText(m.Text) {
		t.Fatal("confirmed send did not clear the quote draft or render quote context")
	}
}

func TestQuoteThreadReplyStaysInItsThreadAndDraftsStayIsolated(t *testing.T) {
	a, callbacks, m := actionFixture(t)
	defer a.Close()
	b := controlledQuoteSend{calls: make(chan quoteCall, 4)}
	a.backend = b
	rootDraft := a.draftFor(a.selected + "|")
	rootDraft.Text = "Main draft"
	m.ThreadReply = true
	m.Name = a.selected + "/messages/thread-reply"
	m.Text = "A reply inside the thread"
	a.quoteMessage(m)
	applyNext(t, callbacks)
	threadDraft := a.draftFor(a.selected + "|" + m.Thread.Name)
	if a.threadName != m.Thread.Name || threadDraft.Quote == nil || rootDraft.Quote != nil || rootDraft.Text != "Main draft" {
		t.Fatal("thread quote escaped its thread or changed the main draft")
	}
	threadDraft.Text = "Thread quote reply"
	a.submitDraft(a.selected, m.Thread.Name)
	call := nextQuoteCall(t, b)
	if call.thread != m.Thread.Name || call.quote.Name != m.Name {
		t.Fatal("quoted thread reply has wrong destination")
	}
	call.result <- sendResult{err: &chat.SendError{Detail: "Definite rejection"}}
	applyNext(t, callbacks)
	if threadDraft.Quote == nil || threadDraft.Text == "" || threadDraft.Uncertain {
		t.Fatal("failed quoted thread reply lost its editable draft")
	}
	a.closeThread()
	if rootDraft.Quote != nil || threadDraft.Quote == nil {
		t.Fatal("thread quote leaked to main draft")
	}
	a.selected = "spaces/other"
	if a.draftFor(a.selected+"|").Quote != nil {
		t.Fatal("quote leaked across conversations")
	}
}

func TestNativeActionsOnAttachmentsAndUnthreadedConversations(t *testing.T) {
	a, _, m := actionFixture(t)
	defer a.Close()
	m.Text = ""
	m.Attachment = []chat.Attachment{{ContentName: "diagram.png"}}
	a.messages = []chat.Message{m}
	a.spaces[0].SpaceType = "DIRECT_MESSAGE"
	tt := ui.NewTester(a.View, 1180, 900)
	hoverMessage(t, tt, "Attachment · diagram.png")
	if _, ok := tt.Find("Quote reply"); !ok {
		t.Fatal("attachment-only message lacks quote action")
	}
	if _, ok := tt.Find("Reply in thread"); ok {
		t.Fatal("unthreaded conversation offers an unsupported thread action")
	}
	if err := tt.Click("Quote reply"); err != nil {
		t.Fatal(err)
	}
	d := a.draftFor(a.selected + "|")
	if d.Quote == nil || d.Quote.QuotedMessageSnapshot.Text != "Attachment · diagram.png" {
		t.Fatal("attachment quote has no useful preview")
	}
}
