package llmstub

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// sequenceRoles is the script of features/scripted_sequences.feature: a guard
// that always allows and an agent that asks, calls a tool and answers its result.
func sequenceRoles() []Rule {
	return []Rule{
		{Name: "guard", Filters: Filters{Model: "guard"}, RepeatLast: true, Steps: []Step{{Answer: `{"allow":true}`}}},
		{Name: "agent", Filters: Filters{Model: "agent", System: "assist", Tools: []string{"generate_image"}}, Steps: []Step{
			{Answer: "Which style?"},
			{ToolCalls: []ToolCall{{Name: "generate_image", Arguments: json.RawMessage(`{"style":"blue"}`)}}},
			{ToolName: "generate_image", ToolResult: "ready", Answer: "Image ready."},
		}},
	}
}

type sequenceRun struct {
	model   *Server
	srv     *httptest.Server
	status  int
	body    string
	retry   string
	message struct {
		Content   string `json:"content"`
		ToolCalls []struct {
			ID       string `json:"id"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	// calls is the raw assistant message of each client's last tool call.
	calls map[string]json.RawMessage
}

func (r *sequenceRun) ask(client, model, extra string) error {
	tools := ""
	if model == "agent" {
		tools = `,"tools":[{"type":"function","function":{"name":"generate_image"}}]`
	}
	body := `{"user":"` + client + `","model":"` + model + `","messages":[{"role":"system","content":"assist"},{"role":"user","content":"Make a blue scene"}` + extra + `]` + tools + `}`
	resp, err := r.srv.Client().Post(r.srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var out struct {
		Choices []struct {
			Message json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	r.status, r.body, r.retry = resp.StatusCode, string(raw), resp.Header.Get("Retry-After")
	r.message.Content, r.message.ToolCalls = "", nil
	if r.status != 200 {
		return nil
	}
	if err := json.Unmarshal([]byte(r.body), &out); err != nil || len(out.Choices) != 1 {
		return fmt.Errorf("not one choice: %s", r.body)
	}
	if err := json.Unmarshal(out.Choices[0].Message, &r.message); err != nil {
		return err
	}
	if len(r.message.ToolCalls) > 0 {
		r.calls[client] = out.Choices[0].Message
	}
	return nil
}

func TestScriptedSequencesFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "Scripted model sequences",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var run *sequenceRun
			sc.Given(`^a scripted model with a guard role and an agent role$`, func() error {
				run = &sequenceRun{model: &Server{}, calls: map[string]json.RawMessage{}}
				run.srv = httptest.NewServer(run.model.Handler())
				t.Cleanup(run.srv.Close)
				return run.model.SetRules(sequenceRoles())
			})
			sc.Given(`^the agent fails once with 503 after its tool call$`, func() error {
				rules := sequenceRoles()
				steps := rules[1].Steps
				failure := Step{Status: 503, Headers: map[string]string{"Retry-After": "2"}}
				rules[1].Steps = append(steps[:2:2], failure, steps[2])
				return run.model.SetRules(rules)
			})
			sc.When(`^client "([^"]*)" asks the (agent|guard)$`, func(client, model string) error {
				return run.ask(client, model, "")
			})
			sc.When(`^client "([^"]*)" sends the result "([^"]*)" of that call$`, func(client, result string) error {
				call := run.calls[client]
				if call == nil {
					return fmt.Errorf("client %s has no tool call", client)
				}
				var assistant struct {
					ToolCalls []struct {
						ID string `json:"id"`
					} `json:"tool_calls"`
				}
				if err := json.Unmarshal(call, &assistant); err != nil {
					return err
				}
				tool, _ := json.Marshal(map[string]string{"role": "tool", "tool_call_id": assistant.ToolCalls[0].ID, "content": result})
				return run.ask(client, "agent", ","+string(call)+","+string(tool))
			})
			sc.When(`^the test resets the model$`, func() { run.model.Reset() })
			sc.Then(`^the model answers "([^"]*)"$`, func(text string) error {
				if run.status != 200 || !strings.Contains(run.message.Content, text) {
					return fmt.Errorf("want an answer with %q, got %d %s", text, run.status, run.body)
				}
				return nil
			})
			sc.Then(`^the model calls the tool "([^"]*)"$`, func(name string) error {
				if run.status != 200 || len(run.message.ToolCalls) != 1 || run.message.ToolCalls[0].Function.Name != name {
					return fmt.Errorf("want a call of %s, got %d %s", name, run.status, run.body)
				}
				return nil
			})
			sc.Then(`^the model fails with (\d+) and Retry-After "([^"]*)"$`, func(status int, retry string) error {
				if run.status != status || run.retry != retry {
					return fmt.Errorf("want %d with Retry-After %s, got %d %q %s", status, retry, run.status, run.retry, run.body)
				}
				return nil
			})
			sc.Then(`^the journal lists the rules "([^"]*)" for client "([^"]*)"$`, func(names, client string) error {
				var got []string
				for _, e := range run.model.Log() {
					if e.Client != client {
						return fmt.Errorf("entry of client %q: %+v", e.Client, e)
					}
					got = append(got, e.Rule)
				}
				if strings.Join(got, ", ") != names {
					return fmt.Errorf("want rules %s, got %v", names, got)
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/scripted_sequences.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("scripted sequences feature failed")
	}
}
