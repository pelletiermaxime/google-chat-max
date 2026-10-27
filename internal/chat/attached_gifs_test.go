package chat

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google-chat-max/internal/media"
)

type gifTransport func(*http.Request) (*http.Response, error)

func (f gifTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAttachedGIFHistoryAndStreamKeepPickerMedia(t *testing.T) {
	const payload = `{"name":"spaces/a/messages/gif","createTime":"2026-10-27T15:37:45Z","attachedGifs":[{"uri":"https://media.tenor.com/example/clip.gif"}]}`
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Query().Get("fields"), "attachedGifs(uri)") {
			t.Error("history excludes picker GIFs")
		}
		fmt.Fprint(w, `{"messages":[`+payload+`]}`)
	}))
	defer s.Close()
	c := &Client{http: s.Client(), baseURL: s.URL}
	page, err := c.History(context.Background(), "spaces/a", "", "")
	if err != nil || len(page.Messages) != 1 || len(page.Messages[0].AttachedGIFs) != 1 {
		t.Fatalf("history lost the GIF-only message: %v", err)
	}
	event, _, err := decodeStreamEvent(map[string]string{"ce-type": messageCreated}, []byte(`{"message":`+payload+`}`))
	if err != nil || event.Message == nil || len(event.Message.AttachedGIFs) != 1 || event.Message.AttachedGIFs[0] != page.Messages[0].AttachedGIFs[0] {
		t.Fatalf("stream delivery lost picker media: %v", err)
	}
}

func TestAttachedGIFDownloadsPublicMediaAndPreservesAnimation(t *testing.T) {
	frame := image.NewPaletted(image.Rect(0, 0, 20, 10), color.Palette{color.Black, color.White})
	var data bytes.Buffer
	if err := gif.EncodeAll(&data, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{5, 10}}); err != nil {
		t.Fatal(err)
	}
	requests := 0
	transport := gifTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Method != http.MethodGet || r.URL.Host != "media.tenor.com" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("public GIF download used an unsafe request or credentials")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data.Bytes())), ContentLength: int64(data.Len())}, nil
	})
	img, err := downloadAttachedGIF(context.Background(), AttachedGIF{URI: "https://media.tenor.com/example/clip.gif"}, transport)
	clip, ok := img.(*media.Animation)
	if err != nil || !ok || len(clip.Frames) != 2 || requests != 1 {
		t.Fatalf("picker GIF did not animate: %v", err)
	}
	for _, raw := range []string{"http://media.tenor.com/clip.gif", "https://localhost/clip.gif", "https://media.tenor.com.evil.example/clip.gif", "https://user@media.tenor.com/clip.gif", "https://media.tenor.com:8080/clip.gif", "file:///clip.gif"} {
		if _, err := downloadAttachedGIF(context.Background(), AttachedGIF{URI: raw}, transport); err == nil {
			t.Fatalf("unsupported GIF URL accepted: %s", raw)
		}
	}
	if requests != 1 {
		t.Fatal("an unsupported URL reached the network")
	}
}

func TestAttachedGIFRedirectsAndDownloadLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		header http.Header
		body   string
		length int64
	}{
		{name: "foreign redirect", status: 302, header: http.Header{"Location": {"https://other.example/private"}}},
		{name: "declared size", status: 200, length: maxImageDownloadBytes + 1},
		{name: "stream size", status: 200, body: strings.Repeat("x", maxImageDownloadBytes+1)},
		{name: "invalid image", status: 200, body: "private response body"},
		{name: "access denied", status: 403, body: "private response body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			transport := gifTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: tc.status, Header: tc.header, Body: io.NopCloser(strings.NewReader(tc.body)), ContentLength: tc.length}, nil
			})
			_, err := downloadAttachedGIF(context.Background(), AttachedGIF{URI: "https://media.tenor.com/example/clip.gif"}, transport)
			if err == nil || strings.Contains(err.Error(), "private response") || requests != 1 {
				t.Fatalf("unsafe GIF download: requests=%d error=%v", requests, err)
			}
		})
	}
}
