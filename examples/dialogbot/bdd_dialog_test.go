package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

func TestDialogFeature(t *testing.T) {
	var transcript string
	suite := godog.TestSuite{
		Name: "Artificial dialog bot",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Given(`^a self-contained HTTP bot test without credentials$`, func() { transcript = "" })
			sc.When(`^the person selects a mode and completes a guarded tool dialog$`, func() error { var err error; transcript, err = demo(); return err })
			sc.Then(`^the transcript contains the reply menu, clarification and a reply to the result$`, func() error {
				for _, text := range []string{"[Photo]", "[Video]", "[Wallet]", "Video", "Which style?", "Image ready.", "Brighter"} {
					if !strings.Contains(transcript, text) {
						return fmt.Errorf("transcript missing %q: %s", text, transcript)
					}
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/dialog_bot.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("dialog feature failed")
	}
}
