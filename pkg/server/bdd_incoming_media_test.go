package server

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/cucumber/godog"
)

func TestIncomingMediaFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "Incoming media",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var s *stand
			var sent []byte
			var batch []map[string]any
			var fileID, filePath string

			// message is the n-th message of the last batch.
			message := func(n int) map[string]any {
				if n >= len(batch) {
					return nil
				}
				msg, _ := batch[n]["message"].(map[string]any)
				return msg
			}

			sc.Given(`^an offline Telegram stand$`, func() {
				s = newStand(t, Options{})
			})
			sc.When(`^the person sends a (\d+)x(\d+) photo with the caption "([^"]*)"$`, func(w, h int, caption string) error {
				sent = mediaPNG(t, w, h)
				_, _, err := s.fake.InjectMedia(IncomingMedia{Type: "photo", Data: sent, Caption: caption})
				return err
			})
			sc.When(`^the person sends the document "([^"]*)"$`, func(name string) error {
				sent = []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n1 0 obj << >> endobj\n%%EOF\n")
				_, _, err := s.fake.InjectMedia(IncomingMedia{Type: "document", Data: sent, FileName: name})
				return err
			})
			sc.When(`^the person sends an album of (\d+) photos with the caption "([^"]*)"$`, func(n int, caption string) error {
				for i := 0; i < n; i++ {
					in := IncomingMedia{Type: "photo", Data: mediaPNG(t, 10+i, 10), MediaGroupID: "album-1"}
					if i == 0 {
						in.Caption = caption
					}
					if _, _, err := s.fake.InjectMedia(in); err != nil {
						return err
					}
				}
				return nil
			})
			sc.Then(`^the bot receives a photo with the caption "([^"]*)" in (\d+) sizes, the last (\d+)x(\d+)$`, func(caption string, n, w, h int) error {
				_, body := s.call("getUpdates", nil)
				batch = updates(t, body)
				msg := message(0)
				if msg == nil || msg["caption"] != caption {
					return fmt.Errorf("update = %v, want a message captioned %q", batch, caption)
				}
				sizes, _ := msg["photo"].([]any)
				if len(sizes) != n {
					return fmt.Errorf("photo has %d sizes, want %d: %v", len(sizes), n, sizes)
				}
				last, _ := sizes[len(sizes)-1].(map[string]any)
				if last["width"] != float64(w) || last["height"] != float64(h) {
					return fmt.Errorf("last size = %v, want %dx%d", last, w, h)
				}
				fileID, _ = last["file_id"].(string)
				return nil
			})
			sc.Then(`^the bot receives the document "([^"]*)" of type "([^"]*)"$`, func(name, mimeType string) error {
				_, body := s.call("getUpdates", nil)
				batch = updates(t, body)
				msg := message(0)
				doc, _ := msg["document"].(map[string]any)
				if doc == nil || doc["file_name"] != name || doc["mime_type"] != mimeType {
					return fmt.Errorf("update = %v, want the document %q of type %q", batch, name, mimeType)
				}
				fileID, _ = doc["file_id"].(string)
				return nil
			})
			sc.When(`^the bot asks getFile for the (?:last size|document)$`, func() error {
				status, body := s.call("getFile", url.Values{"file_id": {fileID}})
				if status != http.StatusOK || body["ok"] != true {
					return fmt.Errorf("getFile = %d %v", status, body)
				}
				filePath, _ = result(t, body)["file_path"].(string)
				return nil
			})
			download := func() ([]byte, error) {
				resp, err := http.Get(s.srv.URL + "/file/bot123456:TOKEN/" + filePath)
				if err != nil {
					return nil, err
				}
				defer func() { _ = resp.Body.Close() }()
				data, _ := io.ReadAll(resp.Body)
				if resp.StatusCode != http.StatusOK {
					return nil, fmt.Errorf("download = %d %s", resp.StatusCode, data)
				}
				return data, nil
			}
			sc.Then(`^the file at that path holds the bytes the person sent$`, func() error {
				data, err := download()
				if err != nil {
					return err
				}
				if !bytes.Equal(data, sent) {
					return fmt.Errorf("downloaded %q, want %q", data, sent)
				}
				return nil
			})
			sc.Then(`^the bot receives (\d+) photos of one media group$`, func(n int) error {
				_, body := s.call("getUpdates", nil)
				batch = updates(t, body)
				if len(batch) != n {
					return fmt.Errorf("received %d updates, want %d", len(batch), n)
				}
				group := ""
				for i := range batch {
					msg := message(i)
					id, _ := msg["media_group_id"].(string)
					if id == "" || (group != "" && id != group) || msg["photo"] == nil {
						return fmt.Errorf("message %d = %v, want a photo of one media group", i, msg)
					}
					group = id
				}
				return nil
			})
			sc.Then(`^only the first of them carries the caption "([^"]*)"$`, func(caption string) error {
				for i := range batch {
					got, _ := message(i)["caption"].(string)
					want := ""
					if i == 0 {
						want = caption
					}
					if got != want {
						return fmt.Errorf("message %d caption = %q, want %q", i, got, want)
					}
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/incoming_media.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("incoming media feature failed")
	}
}
