package desktop

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"google-chat-max/internal/chat"
)

type editCall struct {
	name, text string
	result     chan sendResult
}

type controlledEdit struct {
	Backend
	calls chan editCall
}

func (b controlledEdit) Edit(ctx context.Context, name, text string) (chat.Message, error) {
	call := editCall{name: name, text: text, result: make(chan sendResult, 1)}
	select {
	case b.calls <- call:
	case <-ctx.Done():
		return chat.Message{}, ctx.Err()
	}
	select {
	case r := <-call.result:
		return r.m, r.err
	case <-ctx.Done():
		return chat.Message{}, ctx.Err()
	}
}

func nextEditCall(t *testing.T, b controlledEdit) editCall {
	t.Helper()
	select {
	case c := <-b.calls:
		return c
	case <-time.After(time.Second):
		t.Fatal("edit request did not start")
		return editCall{}
	}
}

func editFixture(t *testing.T) (*App, chan func(), chat.Message, controlledEdit) {
	t.Helper()
	a, callbacks, m := actionFixture(t)
	b := controlledEdit{Backend: Demo(), calls: make(chan editCall, 2)}
	a.backend, a.selfUser = b, "users/demo"
	m.Sender = chat.User{Name: a.selfUser, DisplayName: "You"}
	a.messages = []chat.Message{m}
	return a, callbacks, m, b
}

func TestNativeEditCancelSaveAndRetryPreserveDraft(t *testing.T) {
	for _, width := range []int{760, 1180, 1920} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a, callbacks, m, b := editFixture(t)
			defer a.Close()
			d := a.draftFor(a.selected + "|")
			d.Text, d.Quote = "Unsent draft", &chat.QuotedMessageMetadata{Name: m.Name, LastUpdateTime: m.CreateTime}
			tt := ui.NewTester(a.View, width, 900)
			hoverMessage(t, tt, m.Text)
			if err := tt.Click("Edit message"); err != nil {
				t.Fatal(err)
			}
			if a.edit == nil || a.edit.text != m.Text || !tt.HasText("Editing message") || len(b.calls) != 0 {
				t.Fatal("edit did not preload text or made an unsolicited request")
			}
			tt.Type(" change")
			if a.edit.text == m.Text {
				t.Fatal("editor was not focused")
			}
			if err := tt.Click("Cancel"); err != nil {
				t.Fatal(err)
			}
			if a.edit != nil || d.Text != "Unsent draft" || d.Quote == nil || a.messages[0].Text != m.Text || len(b.calls) != 0 {
				t.Fatal("cancel changed the sent message or draft")
			}
			hoverMessage(t, tt, m.Text)
			if err := tt.Click("Edit message"); err != nil {
				t.Fatal(err)
			}
			a.edit.text = "Corrected message\nwith a second line"
			tt.Frame()
			if err := tt.Click("Save changes"); err != nil {
				t.Fatal(err)
			}
			first := nextEditCall(t, b)
			a.saveEdit()
			a.cancelEdit()
			if a.edit == nil || len(b.calls) != 0 || first.name != m.Name || first.text != a.edit.text {
				t.Fatal("pending edit was canceled, duplicated, or misdirected")
			}
			first.result <- sendResult{err: errors.New("Edit could not be confirmed")}
			applyNext(t, callbacks)
			tt.Frame()
			if a.edit == nil || a.edit.text != first.text || !tt.HasText("Edit could not be confirmed") || a.messages[0].Text != m.Text {
				t.Fatal("failed edit lost changes or changed message content")
			}
			if err := tt.Click("Save changes"); err != nil {
				t.Fatal(err)
			}
			retry := nextEditCall(t, b)
			if retry.name != first.name || retry.text != first.text {
				t.Fatal("retry changed target or text")
			}
			retry.result <- sendResult{m: chat.Message{Name: m.Name, Text: retry.text, LastUpdateTime: m.CreateTime.Add(time.Minute)}}
			applyNext(t, callbacks)
			tt.Frame()
			if a.edit != nil || a.messages[0].Text != retry.text || !tt.HasText("Edited") || d.Text != "Unsent draft" || d.Quote == nil || len(a.activity) != 0 {
				t.Fatal("confirmed edit did not update message and restore the draft")
			}
		})
	}
}

func TestEditOwnershipAndKeyboard(t *testing.T) {
	a, callbacks, m, b := editFixture(t)
	defer a.Close()
	for _, mutate := range []func(*chat.Message){
		func(m *chat.Message) { m.Sender.Name = "users/someone-else" },
		func(m *chat.Message) { m.Sender.Name = "" },
		func(m *chat.Message) { m.Text = "" },
		func(m *chat.Message) { m.Name = "spaces/other/messages/m" },
	} {
		other := m
		mutate(&other)
		a.editMessage(other)
		if a.edit != nil {
			t.Fatal("non-editable message opened an editor")
		}
	}
	a.editMessage(m)
	a.saveEdit()
	a.edit.text = " \n\t"
	a.saveEdit()
	if len(b.calls) != 0 || a.edit.saving {
		t.Fatal("unchanged or blank edit started a save")
	}
	a.edit.text = m.Text
	tt := ui.NewTester(a.View, 1180, 900)
	tt.Frame()
	tt.TypeKey(0, ui.KeyEnter, "")
	tt.Type("second line")
	if len(b.calls) != 0 {
		t.Fatal("plain Enter saved an edit")
	}
	tt.TypeKey(ui.Ctrl, ui.KeyEnter, "")
	call := nextEditCall(t, b)
	closed := false
	a.Shutdown(func(error) { closed = true })
	if closed || a.ctx.Err() != nil {
		t.Fatal("shutdown canceled a pending edit")
	}
	call.result <- sendResult{m: chat.Message{Name: m.Name, Text: call.text, LastUpdateTime: m.CreateTime.Add(time.Minute)}}
	applyNext(t, callbacks)
	applyNext(t, callbacks)
	if !closed {
		t.Fatal("shutdown did not finish after the edit")
	}
}

func TestEditSurvivesNavigationAndDoesNotCountAsIncoming(t *testing.T) {
	a, callbacks, m, b := editFixture(t)
	defer a.Close()
	a.historyLoaded, a.historyFetched = true, time.Now()
	a.editMessage(m)
	a.edit.text = "Changed in the original conversation"
	a.saveEdit()
	call := nextEditCall(t, b)
	a.selectSpace("spaces/engineering")
	applyNext(t, callbacks)
	other := a.draftFor(a.selected + "|")
	other.Text = "Other conversation draft"
	call.result <- sendResult{m: chat.Message{Name: m.Name, Text: call.text, LastUpdateTime: m.CreateTime.Add(time.Minute)}}
	applyNext(t, callbacks)
	if a.selected != "spaces/engineering" || other.Text != "Other conversation draft" || len(a.activity) != 0 || a.historyCache["spaces/design"].messages[0].Text != call.text {
		t.Fatal("edit crossed conversations, lost cached text, or added unread activity")
	}
}

type editThreads struct {
	controlledEdit
	requests chan threadRequest
}

func (b editThreads) Thread(ctx context.Context, space, thread string) (chat.ThreadHistory, error) {
	return delayedThreads{Backend: b.Backend, requests: b.requests}.Thread(ctx, space, thread)
}

func TestEditedThreadRootSurvivesOlderHistorySnapshot(t *testing.T) {
	a, callbacks, m, editor := editFixture(t)
	defer a.Close()
	b := editThreads{controlledEdit: editor, requests: make(chan threadRequest, 1)}
	a.backend, a.threadName = b, m.Thread.Name
	a.threadRoot = &m
	a.threadMessages = []chat.Message{m}
	a.refreshThread()
	read := <-b.requests
	a.reading = true
	a.editMessage(m)
	a.edit.text = "Edited original"
	a.saveEdit()
	call := nextEditCall(t, editor)
	call.result <- sendResult{m: chat.Message{Name: m.Name, Text: call.text, LastUpdateTime: m.CreateTime.Add(time.Minute)}}
	applyNext(t, callbacks)
	read.result <- chat.ThreadHistory{Root: &m, Page: chat.HistoryPage{Messages: []chat.Message{m}}}
	applyNext(t, callbacks)
	if a.threadRoot.Text != call.text || a.threadMessages[0].Text != call.text || a.messages[0].Text != call.text || len(a.duringRead) != 1 {
		t.Fatal("stale history reverted the edited root or missed the main history overlay")
	}
	a.historyView(false).acceptRecent(chat.HistoryPage{Messages: []chat.Message{m}})
	if a.messages[0].Text != call.text {
		t.Fatal("stale conversation history reverted an edit")
	}
}

func TestNativeEditThreadReplyAndEscape(t *testing.T) {
	a, callbacks, m, b := editFixture(t)
	defer a.Close()
	m.ThreadReply = true
	a.threadName, a.threadMessages = m.Thread.Name, []chat.Message{m}
	d := a.draftFor(a.selected + "|" + a.threadName)
	d.Text = "Unsent thread reply"
	tt := ui.NewTester(a.View, 1180, 900)
	hoverMessage(t, tt, m.Text)
	if err := tt.Click("Edit message"); err != nil {
		t.Fatal(err)
	}
	tt.Type("cancel this change")
	tt.TypeKey(0, ui.KeyEscape, "")
	if a.edit != nil || d.Text != "Unsent thread reply" || len(b.calls) != 0 {
		t.Fatal("Escape did not cancel the thread edit and preserve its draft")
	}
	hoverMessage(t, tt, m.Text)
	if err := tt.Click("Edit message"); err != nil {
		t.Fatal(err)
	}
	tt.Type(" updated reply")
	tt.TypeKey(ui.Ctrl, ui.KeyEnter, "")
	call := nextEditCall(t, b)
	if call.name != m.Name {
		t.Fatal("thread edit changed its target")
	}
	call.result <- sendResult{m: chat.Message{Name: m.Name, Text: call.text, LastUpdateTime: m.CreateTime.Add(time.Minute)}}
	applyNext(t, callbacks)
	tt.Frame()
	if a.threadMessages[0].Text != call.text || !a.threadMessages[0].ThreadReply || d.Text != "Unsent thread reply" || !tt.HasText("Edited") {
		t.Fatal("thread edit lost its reply metadata, draft, or edited marker")
	}
}
