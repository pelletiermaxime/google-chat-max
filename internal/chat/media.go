package chat

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"google-chat-max/internal/media"
)

const maxImageDownloadBytes = 10 << 20
const maxImagePixels = 16_000_000
const previewWidth, previewHeight = 960, 720

// Picker GIFs are separate from uploaded attachments in Google's message API.
type AttachedGIF struct {
	URI string `json:"uri"`
}

func attachedGIFURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && len(raw) <= 8192 && u.Scheme == "https" && u.User == nil && u.Port() == "" && strings.EqualFold(u.Hostname(), "media.tenor.com")
}

// AttachedGIFImage fetches public Tenor media without the OAuth transport used
// by Chat API calls. Redirects must stay on the same allowed media host.
func (c *Client) AttachedGIFImage(ctx context.Context, gif AttachedGIF) (image.Image, error) {
	return downloadAttachedGIF(ctx, gif, http.DefaultTransport)
}

func downloadAttachedGIF(ctx context.Context, gif AttachedGIF, transport http.RoundTripper) (image.Image, error) {
	if !attachedGIFURL(gif.URI) {
		return nil, errors.New("inline preview is not supported for this GIF URL")
	}
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || !attachedGIFURL(req.URL.String()) {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gif.URI, nil)
	if err != nil {
		return nil, errors.New("could not prepare the GIF preview")
	}
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("could not download the GIF preview")
	}
	defer res.Body.Close()
	return readImagePreview(ctx, res)
}

type Attachment struct {
	Name              string            `json:"name"`
	ContentName       string            `json:"contentName"`
	ContentType       string            `json:"contentType"`
	Source            string            `json:"source"`
	AttachmentDataRef AttachmentDataRef `json:"attachmentDataRef"`
}

type AttachmentDataRef struct {
	ResourceName          string `json:"resourceName,omitempty"`
	AttachmentUploadToken string `json:"attachmentUploadToken,omitempty"`
}

func (a Attachment) IsImage() bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(a.ContentType, ";")[0])) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return a.AttachmentDataRef.ResourceName != "" && a.Source != "DRIVE_FILE"
	}
	return false
}

// AttachmentImage uses the authenticated media endpoint, never a browser download
// URL from the payload. Its result is a bounded preview, not a retained original.
// Call from a worker: download, decoding and resizing can all be expensive.
func (c *Client) AttachmentImage(ctx context.Context, a Attachment) (image.Image, error) {
	resource := a.AttachmentDataRef.ResourceName
	if !a.IsImage() || len(resource) > 8192 {
		return nil, errors.New("inline preview is not supported for this attachment")
	}
	endpoint := c.baseURL + "/media/" + url.PathEscape(resource) + "?alt=media"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("could not prepare the image preview")
	}
	// OAuth belongs only to the trusted API request. Do not follow a redirect
	// with this transport: it would attach credentials to the redirected host.
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("could not download the image preview")
	}
	defer res.Body.Close()
	return readImagePreview(ctx, res)
}

func readImagePreview(ctx context.Context, res *http.Response) (image.Image, error) {
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("image preview is unavailable; check access or try again")
	}
	if res.ContentLength > maxImageDownloadBytes {
		return nil, errors.New("image exceeds the 10 MiB inline preview limit")
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxImageDownloadBytes+1))
	if err != nil {
		return nil, errors.New("could not read the image preview")
	}
	if len(data) > maxImageDownloadBytes {
		return nil, errors.New("image exceeds the 10 MiB inline preview limit")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return decodeImagePreview(data)
}

func decodeImagePreview(data []byte) (image.Image, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg" && format != "gif" && format != "webp") {
		return nil, errors.New("unsupported or invalid image")
	}
	if config.Width < 1 || config.Height < 1 || config.Width > 16384 || config.Height > 16384 || int64(config.Width)*int64(config.Height) > maxImagePixels {
		return nil, errors.New("image dimensions exceed the inline preview limit")
	}
	if format == "gif" {
		if animation, err := media.DecodeGIF(data, 480, 360, 8<<20); err == nil {
			return animation, nil
		}
		// Large or malformed later animation frames retain the bounded still preview.
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("could not decode the image preview")
	}
	w, h := config.Width, config.Height
	if w > previewWidth {
		h = max(1, h*previewWidth/w)
		w = previewWidth
	}
	if h > previewHeight {
		w = max(1, w*previewHeight/h)
		h = previewHeight
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst, nil
}
