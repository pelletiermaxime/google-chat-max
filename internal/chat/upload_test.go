package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func uploadFixture(t *testing.T) ImageUpload {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 800, 400))); err != nil {
		t.Fatal(err)
	}
	upload, preview, err := PrepareImageUpload(data.Bytes())
	if err != nil || preview.Bounds().Dx() != 240 || preview.Bounds().Dy() != 120 || !bytes.Equal(upload.Data, data.Bytes()) {
		t.Fatalf("image preparation changed the original or failed to bound its preview: %v", err)
	}
	return upload
}

func TestUploadImageAndSendAttachmentOnlyThreadQuoteWithSafeRetry(t *testing.T) {
	upload := uploadFixture(t)
	uploads, posts := 0, 0
	var previousBody []byte
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-only" {
			t.Error("attachment operation lost user authentication")
		}
		if r.Method != http.MethodPost {
			t.Error("attachment operation used an unexpected method")
		}
		switch r.URL.Path {
		case "/upload/v1/spaces/a/attachments:upload":
			uploads++
			if r.URL.Query().Get("uploadType") != "multipart" {
				t.Error("upload used the wrong protocol")
			}
			kind, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || kind != "multipart/related" {
				t.Error("upload lacks multipart metadata and bytes")
				return
			}
			reader := multipart.NewReader(r.Body, params["boundary"])
			metadata, err := reader.NextPart()
			if err != nil {
				t.Error(err)
				return
			}
			var body map[string]string
			if json.NewDecoder(metadata).Decode(&body) != nil || len(body) != 1 || body["filename"] != upload.Filename {
				t.Error("upload filename metadata was lost")
			}
			file, err := reader.NextPart()
			if err != nil {
				t.Error(err)
				return
			}
			data, _ := io.ReadAll(file)
			if file.Header.Get("Content-Type") != "image/png" || !bytes.Equal(data, upload.Data) {
				t.Error("upload altered the image or MIME type")
			}
			fmt.Fprint(w, `{"attachmentDataRef":{"attachmentUploadToken":"test-upload-reference"}}`)
		case "/v1/spaces/a/messages":
			posts++
			data, _ := io.ReadAll(r.Body)
			var body struct {
				Text        string                `json:"text"`
				Attachments []Attachment          `json:"attachment"`
				Quote       QuotedMessageMetadata `json:"quotedMessageMetadata"`
				Thread      struct {
					Name string `json:"name"`
				} `json:"thread"`
			}
			if json.Unmarshal(data, &body) != nil || body.Text != "" || len(body.Attachments) != 1 || body.Attachments[0].AttachmentDataRef.AttachmentUploadToken != "test-upload-reference" || body.Quote.Name != "spaces/a/messages/source" || body.Thread.Name != "spaces/a/threads/t" || r.URL.Query().Get("messageReplyOption") != "REPLY_MESSAGE_OR_FAIL" || r.URL.Query().Get("requestId") != "12345678-1234-1234-1234-123456789abc" {
				t.Error("image-only quoted thread send changed destination, request identity, or attachment")
			}
			if posts == 1 {
				previousBody = data
				w.WriteHeader(503)
				return
			}
			if !bytes.Equal(previousBody, data) {
				t.Error("retry changed the attachment send payload")
			}
			fmt.Fprint(w, `{"name":"spaces/a/messages/sent","createTime":"2026-10-27T16:00:00Z","thread":{"name":"spaces/a/threads/t"},"attachment":[{"contentName":"pasted-image.png","contentType":"image/png","source":"UPLOADED_CONTENT","attachmentDataRef":{"resourceName":"download-reference"}}]}`)
		default:
			t.Errorf("unexpected attachment endpoint %s", r.URL.Path)
		}
	}))
	defer s.Close()
	c := &Client{http: oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-only"})), baseURL: s.URL + "/v1"}
	ref, err := c.UploadImage(context.Background(), "spaces/a", upload)
	if err != nil || ref.Space != "spaces/a" {
		t.Fatalf("image upload failed: %v", err)
	}
	quote := &QuotedMessageMetadata{Name: "spaces/a/messages/source", LastUpdateTime: time.Now()}
	for i := range 2 {
		m, err := c.SendAttached(context.Background(), "spaces/a", "spaces/a/threads/t", "", "12345678-1234-1234-1234-123456789abc", quote, []UploadedAttachment{ref})
		if (i == 0 && err == nil) || (i == 1 && (err != nil || len(m.Attachment) != 1 || !m.ThreadReply)) {
			t.Fatalf("attachment send/retry failed: %v", err)
		}
	}
	if _, err := c.SendAttached(context.Background(), "spaces/other", "", "Caption", "12345678-1234-1234-1234-123456789abc", nil, []UploadedAttachment{ref}); err == nil {
		t.Fatal("cross-conversation attachment was accepted")
	}
	if uploads != 1 || posts != 2 {
		t.Fatal("retry reuploaded the image or invalid send reached the server")
	}
}

func TestImageUploadValidationAndRedactedErrors(t *testing.T) {
	upload := uploadFixture(t)
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(403)
		fmt.Fprint(w, "private response body")
	}))
	defer s.Close()
	c := &Client{http: s.Client(), baseURL: s.URL}
	if _, err := c.UploadImage(context.Background(), "spaces/a", upload); err == nil || strings.Contains(err.Error(), "private response") {
		t.Fatal("upload denial was hidden or leaked server data")
	}
	for _, invalid := range []ImageUpload{{Filename: "image.png", ContentType: "image/png", Data: []byte("not an image")}, {Filename: "image.png", ContentType: "image/png", Data: make([]byte, MaxImageUploadBytes+1)}, {Filename: "image.png", ContentType: "image/jpeg", Data: upload.Data}} {
		if _, err := c.UploadImage(context.Background(), "spaces/a", invalid); err == nil {
			t.Fatal("invalid image reached the upload endpoint")
		}
	}
	if requests != 1 {
		t.Fatal("invalid upload made a network request")
	}
}

func TestImageUploadRefusesRedirectAndMissingReference(t *testing.T) {
	upload := uploadFixture(t)
	for _, redirect := range []bool{true, false} {
		t.Run(fmt.Sprint(redirect), func(t *testing.T) {
			hits := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				if redirect {
					w.Header().Set("Location", "/different-host")
					w.WriteHeader(302)
					return
				}
				fmt.Fprint(w, `{"attachmentDataRef":{}}`)
			}))
			defer s.Close()
			c := &Client{http: s.Client(), baseURL: s.URL}
			if _, err := c.UploadImage(context.Background(), "spaces/a", upload); err == nil || hits != 1 {
				t.Fatal("upload followed a redirect or accepted a missing reference")
			}
		})
	}
}
