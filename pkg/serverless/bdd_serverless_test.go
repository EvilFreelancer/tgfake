package serverless

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/tgfake/pkg/server"
	"github.com/cucumber/godog"
)

func TestServerlessFeature(t *testing.T) {
	requireDocker(t)
	suite := godog.TestSuite{
		Name: "Local Telegram Serverless",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var r *Runtime
			var fake *server.Server
			sc.Given(`^a local serverless counter project with its schema applied$`, func() error {
				project, err := filepath.Abs("../../examples/serverless")
				if err != nil {
					return err
				}
				r, fake = newRuntime(t, project)
				return r.Migrate(context.Background())
			})
			sc.When(`^its message handler receives two messages$`, func() error {
				for id := 1; id <= 2; id++ {
					_, err := r.Run(context.Background(), "handlers/message",
						map[string]any{"chat": map[string]any{"id": 4242}, "text": "hi"},
						map[string]any{"update": map[string]any{"update_id": id}})
					if err != nil {
						return err
					}
				}
				return nil
			})
			sc.Then(`^the bot answers with a count of two$`, func() error {
				if !strings.Contains(fake.Chat(4242).Text(), "I've seen 2 message(s)") {
					return fmt.Errorf("chat: %s", fake.Chat(4242).Text())
				}
				return nil
			})
			sc.Then(`^the Mini App endpoint sees the same count$`, func() error {
				req := httptest.NewRequest(http.MethodPost, "/api/getCounter", strings.NewReader(`{}`))
				req.Header.Set("Authorization", authorization("123:TEST", `{"id":4242}`))
				w := httptest.NewRecorder()
				r.Handler().ServeHTTP(w, req)
				var response struct {
					OK     bool `json:"ok"`
					Result struct {
						Seen int `json:"seen"`
					} `json:"result"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					return err
				}
				if w.Code != 200 || !response.OK || response.Result.Seen != 2 {
					return fmt.Errorf("endpoint: %d %s", w.Code, w.Body)
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/serverless.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("serverless feature failed")
	}
}
