package chat

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"google-chat-max/internal/media"
)

func imageAttachment(contentType, resource string) Attachment {
	a := Attachment{ContentName: "sample.png", ContentType: contentType, Source: "UPLOADED_CONTENT"}
	a.AttachmentDataRef.ResourceName = resource
	return a
}

func TestAttachmentPreviewUsesAuthenticatedMediaAndResizes(t *testing.T) {
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 1440, 1000))); err != nil {
		t.Fatal(err)
	}
	attachment := imageAttachment("image/png", "opaque+/resource==")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/media/"+url.PathEscape(attachment.AttachmentDataRef.ResourceName) || r.URL.Query().Get("alt") != "media" {
			t.Error("incorrect media download request")
		}
		if r.Header.Get("Authorization") != "Bearer test-only" {
			t.Error("media download did not use user authentication")
		}
		w.Write(data.Bytes())
	}))
	defer s.Close()
	c := &Client{baseURL: s.URL, http: oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-only"}))}
	img, err := c.AttachmentImage(context.Background(), attachment)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 960 || img.Bounds().Dy() != 666 {
		t.Fatalf("unexpected preview dimensions: %v", img.Bounds())
	}
}

func TestAttachmentPreviewDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer s.Close()
	c := &Client{baseURL: s.URL, http: oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-only"}))}
	if _, err := c.AttachmentImage(context.Background(), imageAttachment("image/png", "opaque")); err == nil || hits.Load() != 0 {
		t.Fatal("authenticated media request followed a redirect")
	}
}

func TestAttachmentPreviewBoundsAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header bool
		status int
		body   string
		want   string
	}{
		{name: "declared size", header: true, body: "", want: "10 MiB"},
		{name: "stream size", body: strings.Repeat("x", maxImageDownloadBytes+1), want: "10 MiB"},
		{name: "invalid image", body: "not an image", want: "invalid image"},
		{name: "access error", status: 403, body: "private response body", want: "access"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.header {
					w.Header().Set("Content-Length", strconv.Itoa(maxImageDownloadBytes+1))
				}
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				w.Write([]byte(tc.body))
			}))
			defer s.Close()
			c := &Client{baseURL: s.URL, http: s.Client()}
			_, err := c.AttachmentImage(context.Background(), imageAttachment("image/png", "opaque"))
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "private response") {
				t.Fatalf("unexpected bounded error: %v", err)
			}
		})
	}
	// A valid IHDR with enormous dimensions must be rejected before decoding
	// its pixel data, even though the compressed input is tiny.
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	encoded := data.Bytes()
	binary.BigEndian.PutUint32(encoded[16:20], 8192)
	binary.BigEndian.PutUint32(encoded[20:24], 8192)
	binary.BigEndian.PutUint32(encoded[29:33], crc32.ChecksumIEEE(encoded[12:29]))
	if _, err := decodeImagePreview(encoded); err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("large decoded image was not rejected: %v", err)
	}
}

func TestImageFormatsAndUnsupportedAttachment(t *testing.T) {
	src := image.NewPaletted(image.Rect(0, 0, 20, 10), color.Palette{color.Black, color.White})
	for _, format := range []string{"png", "jpeg", "gif"} {
		var data bytes.Buffer
		var err error
		switch format {
		case "png":
			err = png.Encode(&data, src)
		case "jpeg":
			err = jpeg.Encode(&data, src, nil)
		case "gif":
			err = gif.Encode(&data, src, nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		img, err := decodeImagePreview(data.Bytes())
		if err != nil || img.Bounds() != src.Bounds() {
			t.Fatalf("%s preview failed: %v", format, err)
		}
	}
	c := &Client{}
	for _, a := range []Attachment{imageAttachment("application/pdf", "opaque"), imageAttachment("image/svg+xml", "opaque"), imageAttachment("image/png", "")} {
		if _, err := c.AttachmentImage(context.Background(), a); err == nil {
			t.Fatal("unsupported attachment reached the network")
		}
	}
}

func TestImageDownloadCancellation(t *testing.T) {
	started := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Client{baseURL: s.URL, http: s.Client()}
	done := make(chan error, 1)
	go func() { _, err := c.AttachmentImage(ctx, imageAttachment("image/png", "opaque")); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("download ignored cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled image download is still running")
	}
}

func TestGIFPreviewAnimatesAndFallsBackAboveFrameLimit(t *testing.T) {
	frame := image.NewPaletted(image.Rect(0, 0, 20, 10), color.Palette{color.Black, color.White})
	for _, count := range []int{2, 257} {
		g := &gif.GIF{Image: make([]*image.Paletted, count), Delay: make([]int, count)}
		for i := range count {
			g.Image[i] = frame
			g.Delay[i] = 5
		}
		var data bytes.Buffer
		if err := gif.EncodeAll(&data, g); err != nil {
			t.Fatal(err)
		}
		img, err := decodeImagePreview(data.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		clip, animated := img.(*media.Animation)
		if count == 2 && (!animated || len(clip.Frames) != 2) {
			t.Fatal("GIF preview lost animation")
		}
		if count == 257 && animated {
			t.Fatal("oversized animation did not fall back to a still preview")
		}
	}
}
