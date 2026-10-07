package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func mediaPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{R: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// upload posts a multipart form with one file part, the way a Bot API library
// (tgbotapi's UploadFiles) sends sendPhoto and sendDocument.
func (s *stand) upload(method string, fields map[string]string, fileField, fileName string, data []byte) (int, map[string]any) {
	s.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			s.t.Fatal(err)
		}
	}
	if fileField != "" {
		part, err := mw.CreateFormFile(fileField, fileName)
		if err != nil {
			s.t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			s.t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		s.t.Fatal(err)
	}
	resp, err := http.Post(s.srv.URL+"/bot123456:TOKEN/"+method, mw.FormDataContentType(), &body)
	if err != nil {
		s.t.Fatalf("%s: %v", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		s.t.Fatalf("%s: body %q is not JSON: %v", method, raw, err)
	}
	return resp.StatusCode, out
}

func TestSendPhotoKeepsThePictureInTheChat(t *testing.T) {
	st := newStand(t, Options{})
	pic := mediaPNG(t, 4, 3)

	code, out := st.upload("sendPhoto", map[string]string{"chat_id": "42", "caption": "shot.png"}, "photo", "shot.png", pic)
	if code != http.StatusOK || out["ok"] != true {
		t.Fatalf("sendPhoto = %d %v", code, out)
	}
	result, _ := out["result"].(map[string]any)
	if result["caption"] != "shot.png" {
		t.Errorf("result caption = %v", result["caption"])
	}
	sizes, _ := result["photo"].([]any)
	if len(sizes) == 0 {
		t.Fatalf("result carries no photo sizes: %v", result)
	}
	size, _ := sizes[len(sizes)-1].(map[string]any)
	if size["width"] != float64(4) || size["height"] != float64(3) || size["file_id"] == "" {
		t.Errorf("photo size = %v, want a file_id and 4x3", size)
	}

	view := st.fake.Chat(42)
	if len(view.Messages) != 1 {
		t.Fatalf("chat holds %d messages, want 1", len(view.Messages))
	}
	msg := view.Messages[0]
	if msg.From != "bot" || msg.Caption != "shot.png" || msg.Photo == nil {
		t.Fatalf("message view = %+v, want the bot's photo captioned shot.png", msg)
	}
	if msg.Photo.Name != "shot.png" || msg.Photo.Width != 4 || msg.Photo.Height != 3 || msg.Photo.Size != len(pic) {
		t.Errorf("photo view = %+v", msg.Photo)
	}
	if text := view.Text(); !strings.Contains(text, "[photo shot.png 4x3] shot.png") {
		t.Errorf("chat text %q does not show the photo", text)
	}
	if got, ok := st.fake.File(msg.Photo.FileID); !ok || !bytes.Equal(got, pic) {
		t.Error("the fake does not keep the uploaded bytes")
	}

	resp, err := http.Get(st.srv.URL + "/sim/file/" + url.PathEscape(msg.Photo.FileID))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	served, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" || !bytes.Equal(served, pic) {
		t.Errorf("/sim/file = %d %q (%d bytes), want the PNG", resp.StatusCode, resp.Header.Get("Content-Type"), len(served))
	}

	calls := st.fake.Calls("sendPhoto")
	if len(calls) != 1 || calls[0].Params["chat_id"] != "42" || calls[0].Params["caption"] != "shot.png" ||
		!strings.Contains(calls[0].Params["photo"], "shot.png") || !strings.Contains(calls[0].Params["photo"], strconv.Itoa(len(pic))) {
		t.Errorf("recorded call = %+v, want chat_id, caption and the upload named with its size", calls)
	}
}

func TestSendDocumentKeepsTheFileInTheChat(t *testing.T) {
	st := newStand(t, Options{})
	data := []byte("plain notes\n")
	code, out := st.upload("sendDocument", map[string]string{"chat_id": "7"}, "document", "notes.txt", data)
	if code != http.StatusOK || out["ok"] != true {
		t.Fatalf("sendDocument = %d %v", code, out)
	}
	result, _ := out["result"].(map[string]any)
	doc, _ := result["document"].(map[string]any)
	if doc["file_name"] != "notes.txt" || doc["file_id"] == "" {
		t.Errorf("result document = %v", doc)
	}
	msg := st.fake.Chat(7).Messages[0]
	if msg.Document == nil || msg.Document.Name != "notes.txt" || msg.Document.Size != len(data) {
		t.Fatalf("message view = %+v", msg)
	}
	if text := st.fake.Chat(7).Text(); !strings.Contains(text, "[document notes.txt]") {
		t.Errorf("chat text %q does not show the document", text)
	}
}

func TestSendPhotoWithoutAFileIsRefused(t *testing.T) {
	st := newStand(t, Options{})
	code, out := st.upload("sendPhoto", map[string]string{"chat_id": "42"}, "", "", nil)
	if code != http.StatusBadRequest || !strings.Contains(out["description"].(string), "photo") {
		t.Fatalf("sendPhoto without a file = %d %v, want 400 naming the photo", code, out)
	}
	if len(st.fake.Chat(42).Messages) != 0 {
		t.Error("a refused photo reached the chat")
	}
}

func TestSendPhotoHonoursAScheduledFault(t *testing.T) {
	st := newStand(t, Options{})
	st.fake.SetFault(Fault{Method: "sendPhoto", Code: http.StatusBadRequest, Description: "Bad Request: PHOTO_INVALID_DIMENSIONS", Times: 1})
	code, _ := st.upload("sendPhoto", map[string]string{"chat_id": "42"}, "photo", "shot.png", mediaPNG(t, 2, 2))
	if code != http.StatusBadRequest {
		t.Fatalf("sendPhoto under a fault = %d, want 400", code)
	}
	code, _ = st.upload("sendPhoto", map[string]string{"chat_id": "42"}, "photo", "shot.png", mediaPNG(t, 2, 2))
	if code != http.StatusOK {
		t.Fatalf("sendPhoto after the fault = %d, want 200", code)
	}
}

// A photo named by a file_id or a URL is a variant the fake does not keep:
// it is a request with no file in it, not a method the fake does not know.
func TestSendPhotoWithoutAnUploadIsRefusedAsHavingNoFile(t *testing.T) {
	st := newStand(t, Options{})
	code, out := st.call("sendPhoto", url.Values{"chat_id": {"42"}, "photo": {"AgACAgIAAxkBAAIBY2"}})
	if code != http.StatusBadRequest || !strings.Contains(out["description"].(string), "no photo") {
		t.Fatalf("sendPhoto without an upload = %d %v, want 400 naming the missing photo", code, out)
	}
}
