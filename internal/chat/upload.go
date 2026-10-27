package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"

	"golang.org/x/image/draw"
)

const MaxImageUploadBytes = 10 << 20

// ImageUpload is immutable image data retained in a draft until explicit Send.
type ImageUpload struct {
	Filename, ContentType string
	Data                  []byte
}

// UploadedAttachment binds the opaque upload reference to its destination.
// Keep it in memory for retries; never log or persist upload tokens.
type UploadedAttachment struct {
	Space, Filename, ContentType string
	DataRef                      AttachmentDataRef
}

func imageUploadFormat(data []byte) (image.Config, string, string, error) {
	if len(data) == 0 || len(data) > MaxImageUploadBytes {
		return image.Config{}, "", "", errors.New("pasted image exceeds the 10 MiB attachment limit or is empty")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 16384 || config.Height > 16384 || int64(config.Width)*int64(config.Height) > maxImagePixels {
		return image.Config{}, "", "", errors.New("pasted image is invalid or exceeds the 16-megapixel limit")
	}
	kind, extension := "image/"+format, format
	switch format {
	case "png", "gif", "webp":
	case "jpeg":
		extension = "jpg"
	default:
		return image.Config{}, "", "", errors.New("paste a PNG, JPEG, GIF, or WebP image")
	}
	return config, kind, extension, nil
}

// PrepareImageUpload validates without allocating full pixels first, then makes
// a small still preview. Call off the GUI thread; the original bytes are sent.
func PrepareImageUpload(data []byte) (ImageUpload, image.Image, error) {
	config, kind, extension, err := imageUploadFormat(data)
	if err != nil {
		return ImageUpload{}, nil, err
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return ImageUpload{}, nil, errors.New("could not decode the pasted image")
	}
	w, h := config.Width, config.Height
	if w > 240 {
		h, w = max(1, h*240/w), 240
	}
	if h > 160 {
		w, h = max(1, w*160/h), 160
	}
	preview := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(preview, preview.Bounds(), src, src.Bounds(), draw.Src, nil)
	return ImageUpload{Filename: "pasted-image." + extension, ContentType: kind, Data: data}, preview, nil
}

// UploadImage uploads only after Send. A successful reference can be reused by
// a message retry without uploading the image again.
func (c *Client) UploadImage(ctx context.Context, space string, upload ImageUpload) (UploadedAttachment, error) {
	_, kind, _, err := imageUploadFormat(upload.Data)
	if err != nil {
		return UploadedAttachment{}, err
	}
	if !validSpace(space) || upload.ContentType != kind || upload.Filename == "" || len(upload.Filename) > 255 || strings.ContainsAny(upload.Filename, "/\\\x00\r\n") {
		return UploadedAttachment{}, errors.New("invalid image attachment or destination")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	metadata, _ := json.Marshal(map[string]string{"filename": upload.Filename})
	part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/json; charset=UTF-8"}})
	if err == nil {
		_, err = part.Write(metadata)
	}
	if err == nil {
		part, err = writer.CreatePart(textproto.MIMEHeader{"Content-Type": {kind}})
	}
	if err == nil {
		_, err = part.Write(upload.Data)
	}
	if err == nil {
		err = writer.Close()
	}
	if err != nil {
		return UploadedAttachment{}, errors.New("could not prepare the image upload")
	}
	endpoint := strings.TrimSuffix(c.baseURL, "/v1") + "/upload/v1/" + space + "/attachments:upload?uploadType=multipart"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return UploadedAttachment{}, errors.New("could not prepare the image upload")
	}
	req.Header.Set("Content-Type", "multipart/related; boundary="+writer.Boundary())
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		return UploadedAttachment{}, errors.New("could not confirm the image upload. Your image is kept; retry Send")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return UploadedAttachment{}, fmt.Errorf("Google Chat could not upload the image (HTTP %d). Your image is kept", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	var result struct {
		DataRef AttachmentDataRef `json:"attachmentDataRef"`
	}
	if err != nil || len(data) > maxResponseBytes || json.Unmarshal(data, &result) != nil || !validUploadRef(result.DataRef) {
		return UploadedAttachment{}, errors.New("Google Chat returned incomplete image upload information. Your image is kept; retry Send")
	}
	return UploadedAttachment{Space: space, Filename: upload.Filename, ContentType: kind, DataRef: result.DataRef}, nil
}

func validUploadRef(ref AttachmentDataRef) bool {
	return (ref.AttachmentUploadToken != "" || ref.ResourceName != "") && len(ref.AttachmentUploadToken) <= 16000 && len(ref.ResourceName) <= 8192
}

func (c *Client) SendAttached(ctx context.Context, space, thread, text, requestID string, quote *QuotedMessageMetadata, attachments []UploadedAttachment) (Message, error) {
	return c.send(ctx, space, thread, text, requestID, quote, attachments)
}
