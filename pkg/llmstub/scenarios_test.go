package llmstub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func scriptedPost(t *testing.T, origin, profile, body string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, origin+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Tgfake-Profile", profile)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(raw), resp.Header
}

func TestIndependentScenariosAndToolResults(t *testing.T) {
	s := &Server{Rules: []Rule{
		{Name: "guard", Filters: Filters{Model: "guard", System: "moderate", HasTools: boolPointer(false)}, RepeatLast: true, Steps: []Step{{Answer: `{"allow":true}`}}},
		{Name: "agent", Filters: Filters{Model: "agent", System: "assist", Tools: []string{"generate_image"}}, Steps: []Step{
			{Answer: "Which style?"},
			{ToolCalls: []ToolCall{{Name: "generate_image", Arguments: json.RawMessage(`{"prompt":"blue"}`)}, {Name: "save", Arguments: json.RawMessage(`{}`)}}},
			{ToolName: "generate_image", ToolResult: "ready", Answer: "Done", Usage: map[string]int{"total_tokens": 42}},
		}},
	}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	request := func(user, model, system, extra string) string {
		return `{"user":"` + user + `","model":"` + model + `","messages":[{"role":"system","content":"` + system + `"},{"role":"user","content":"same"}` + extra + `],"tools":[{"type":"function","function":{"name":"generate_image"}},{"type":"function","function":{"name":"save"}}]}`
	}
	guard := `{"user":"a","model":"guard","messages":[{"role":"system","content":"moderate"},{"role":"user","content":"same"}]}`
	_, body, _ := scriptedPost(t, srv.URL, "", guard)
	if !strings.Contains(body, `\"allow\":true`) {
		t.Fatal(body)
	}
	for _, user := range []string{"a", "b"} {
		status, b, _ := scriptedPost(t, srv.URL, "", request(user, "agent", "assist", ""))
		if status != 200 || !strings.Contains(b, "Which style?") {
			t.Fatal(status, b)
		}
	}
	_, body, _ = scriptedPost(t, srv.URL, "", request("a", "agent", "assist", ""))
	var response struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					ID string `json:"id"`
				} `json:"tool_calls"`
			}
		}
	}
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatal(err)
	}
	calls := response.Choices[0].Message.ToolCalls
	if len(calls) != 2 || calls[0].ID == calls[1].ID {
		t.Fatal(body)
	}
	status, body, _ := scriptedPost(t, srv.URL, "", request("a", "agent", "assist", `,{"role":"tool","tool_call_id":"wrong","content":"ready"}`))
	if status != 409 {
		t.Fatal(status, body)
	}
	extra := `,{"role":"assistant","content":null,"tool_calls":[]},{"role":"tool","tool_call_id":"` + calls[0].ID + `","content":"ready"},{"role":"tool","tool_call_id":"` + calls[1].ID + `","content":"saved"}`
	status, body, _ = scriptedPost(t, srv.URL, "", request("a", "agent", "assist", extra))
	if status != 200 || !strings.Contains(body, "Done") || !strings.Contains(body, `"total_tokens":42`) {
		t.Fatal(status, body)
	}
	status, _, _ = scriptedPost(t, srv.URL, "", request("a", "agent", "assist", extra))
	if status != 409 {
		t.Fatal(status)
	}
	_, body, _ = scriptedPost(t, srv.URL, "", request("b", "agent", "assist", ""))
	if !strings.Contains(body, "tool_calls") {
		t.Fatal(body)
	}
	raw, _ := json.Marshal(s.Log())
	if strings.Contains(string(raw), "Bearer") || strings.Contains(string(raw), "secret") {
		t.Fatal("headers leaked")
	}
	s.Reset()
	_, body, _ = scriptedPost(t, srv.URL, "", request("a", "agent", "assist", ""))
	if !strings.Contains(body, "Which style?") {
		t.Fatal(body)
	}
}

func boolPointer(v bool) *bool { return &v }

func TestProviderFailuresRecoveryAndDiagnostics(t *testing.T) {
	malformed := "{invalid"
	s := &Server{Rules: []Rule{
		{Name: "primary", Filters: Filters{Profile: "primary", User: "a"}, Steps: []Step{{Status: 429, Headers: map[string]string{"Retry-After": "3"}, RawBody: stringPointer(`{"error":{"message":"quota"}}`), Negative: true}, {Answer: "recovered", OmitUsage: true}}},
		{Name: "backup", Filters: Filters{Profile: "backup"}, RepeatLast: true, Steps: []Step{{Answer: "backup"}}},
		{Name: "malformed", Filters: Filters{Model: "broken"}, RepeatLast: true, Steps: []Step{{RawBody: &malformed, Negative: true}}},
	}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	req := `{"user":"a","messages":[{"role":"user","content":"same"}]}`
	status, _, headers := scriptedPost(t, srv.URL, "primary", req)
	if status != 429 || headers.Get("Retry-After") != "3" {
		t.Fatal(status, headers)
	}
	status, body, _ := scriptedPost(t, srv.URL, "backup", req)
	if status != 200 || !strings.Contains(body, "backup") {
		t.Fatal(status, body)
	}
	status, body, _ = scriptedPost(t, srv.URL, "primary", req)
	if status != 200 || strings.Contains(body, `"usage"`) {
		t.Fatal(status, body)
	}
	_, body, _ = scriptedPost(t, srv.URL, "", `{"model":"broken"}`)
	if body != malformed {
		t.Fatal(body)
	}
	resp, err := http.Get(srv.URL + "/sim/llm/log")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var log struct {
		Count   int
		Entries []Entry
	}
	if err = json.NewDecoder(resp.Body).Decode(&log); err != nil {
		t.Fatal(err)
	}
	if log.Count != 4 || log.Entries[0].Status != 429 || log.Entries[1].Profile != "backup" {
		t.Fatal(log)
	}
	// Returned snapshots cannot mutate the journal.
	log.Entries[0].Messages[0] = 'x'
	if s.Log()[0].Messages[0] == 'x' {
		t.Fatal("aliased messages")
	}
	reset, err := http.Post(srv.URL+"/sim/llm/reset", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = reset.Body.Close()
	status, _, _ = scriptedPost(t, srv.URL, "primary", req)
	if status != 429 || len(s.Log()) != 1 {
		t.Fatal(status, s.Log())
	}
}

func stringPointer(v string) *string { return &v }

func TestTimeoutDoesNotBlockOtherClients(t *testing.T) {
	s := &Server{Rules: []Rule{{Name: "slow", Filters: Filters{User: "slow"}, Steps: []Step{{DelayMS: 1000}, {Answer: "recovered"}}}}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	client := &http.Client{Timeout: 30 * time.Millisecond}
	resp, err := client.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"user":"slow"}`))
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected timeout")
	}
	status, _, _ := scriptedPost(t, srv.URL, "", `{"user":"fast"}`)
	if status != 200 {
		t.Fatal(status)
	}
	_, body, _ := scriptedPost(t, srv.URL, "", `{"user":"slow"}`)
	if !strings.Contains(body, "recovered") {
		t.Fatal(body)
	}
}

func TestCancelledToolStepAllowsRecovery(t *testing.T) {
	s := &Server{Rules: []Rule{{Name: "timeout", Steps: []Step{
		{DelayMS: 1000, ToolCalls: []ToolCall{{Name: "lookup"}}},
		{ToolCalls: []ToolCall{{Name: "lookup", ID: "recovery-call"}}},
		{ToolName: "lookup", ToolResult: "ready", Answer: "recovered"},
	}}}}
	handler := s.Handler()
	body := `{"user":"a","tools":[{"type":"function","function":{"name":"lookup"}}]}`
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, req)
	if out.Body.Len() != 0 || len(s.Log()) != 1 || !s.Log()[0].Cancelled || s.Log()[0].Status != 499 {
		t.Fatal(out.Body.String(), s.Log())
	}
	post := func(body string) *httptest.ResponseRecorder {
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
		return out
	}
	out = post(body)
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"id":"recovery-call"`) {
		t.Fatal(out.Code, out.Body.String())
	}
	// A delivered recovery call must still require its own tool result.
	if out = post(body); out.Code != 409 {
		t.Fatal(out.Code, out.Body.String())
	}
	out = post(`{"user":"a","messages":[{"role":"tool","tool_call_id":"recovery-call","content":"ready"}]}`)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "recovered") {
		t.Fatal(out.Code, out.Body.String())
	}
}

func TestLegacyClientHeaderJournal(t *testing.T) {
	for _, mode := range []string{"rule", "answers", "echo", "stream"} {
		t.Run(mode, func(t *testing.T) {
			s := &Server{ClientHeader: "X-Test-Client"}
			switch mode {
			case "rule":
				s.Rules = []Rule{{Answer: "matched"}}
			case "answers":
				s.Answers = []string{"answer"}
			}
			body := `{"user":"shared","messages":[{"role":"user","content":"hello"}]}`
			if mode == "stream" {
				body = `{"stream":true}`
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			req.Header.Set("X-Test-Client", "client-a")
			out := httptest.NewRecorder()
			s.Handler().ServeHTTP(out, req)
			log := s.Log()
			if out.Code != 200 || len(log) != 1 || log[0].Client != "client-a" {
				t.Fatal(out.Code, log)
			}
		})
	}
}

func TestCancelledReservationPreservesNewerToolCalls(t *testing.T) {
	s := &Server{Rules: []Rule{{Name: "overlap", Steps: []Step{
		{ToolCalls: []ToolCall{{Name: "lookup", ID: "old-call"}}},
		{ToolCalls: []ToolCall{{Name: "lookup", ID: "new-call"}}},
		{Answer: "done"},
	}}}}
	var req chatRequest
	if err := json.Unmarshal([]byte(`{"tools":[{"type":"function","function":{"name":"lookup"}}]}`), &req); err != nil {
		t.Fatal(err)
	}
	_, _, selected, problem, abandon := s.selectStep(req, "", "a")
	if !selected || problem != "" || abandon == nil {
		t.Fatal(selected, problem)
	}
	if err := json.Unmarshal([]byte(`{"tools":[{"type":"function","function":{"name":"lookup"}}],"messages":[{"role":"tool","tool_call_id":"old-call","content":"ready"}]}`), &req); err != nil {
		t.Fatal(err)
	}
	_, _, selected, problem, _ = s.selectStep(req, "", "a")
	if !selected || problem != "" {
		t.Fatal(selected, problem)
	}
	// A late cancellation must not erase another request's pending results.
	abandon()
	req.Messages = nil
	_, _, _, problem, _ = s.selectStep(req, "", "a")
	if problem != "missing tool result for new-call" {
		t.Fatal(problem)
	}
}

func TestUnknownToolsRequireExplicitNegativeScenario(t *testing.T) {
	s := &Server{Rules: []Rule{{Name: "strict", Steps: []Step{{ToolCalls: []ToolCall{{Name: "unknown"}}}}}}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	status, _, _ := scriptedPost(t, srv.URL, "", `{}`)
	if status != 409 {
		t.Fatal(status)
	}
	raw := "{broken"
	if err := s.SetRules([]Rule{{Name: "negative", Steps: []Step{{Negative: true, ToolCalls: []ToolCall{{Name: "unknown", RawArguments: &raw}}, EmptyChoices: false, OmitUsage: true}}}}); err != nil {
		t.Fatal(err)
	}
	status, body, _ := scriptedPost(t, srv.URL, "", `{}`)
	if status != 200 || !strings.Contains(body, `{broken`) {
		t.Fatal(status, body)
	}
	if err := s.SetRules([]Rule{{Name: "bad", Steps: []Step{{RawBody: &raw}}}}); err == nil {
		t.Fatal("raw body accepted without negative")
	}
}

func TestConcurrentScenarioCursors(t *testing.T) {
	s := &Server{Rules: []Rule{{Name: "sequence", Steps: []Step{{Answer: "one"}, {Answer: "two"}}}}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for _, want := range []string{"one", "two"} {
				_, body, _ := scriptedPost(t, srv.URL, "", `{"user":"`+string(rune('a'+i))+`"}`)
				if !strings.Contains(body, want) {
					t.Error(body)
				}
			}
		}(i)
	}
	wg.Wait()
	if len(s.Log()) != 24 {
		t.Fatal(len(s.Log()))
	}
}

func TestHTTPFailureStatusesAndExplicitEmptyChoices(t *testing.T) {
	for _, status := range []int{401, 402, 403, 429, 500, 502, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s := &Server{Rules: []Rule{{Name: "failure", RepeatLast: true, Steps: []Step{{Status: status}}}}}
			srv := httptest.NewServer(s.Handler())
			defer srv.Close()
			for i := 0; i < 2; i++ {
				got, body, _ := scriptedPost(t, srv.URL, "", `{}`)
				if got != status || !strings.Contains(body, "error") {
					t.Fatal(got, body)
				}
			}
		})
	}
	s := &Server{Rules: []Rule{{Name: "empty", Steps: []Step{{Negative: true, EmptyChoices: true, OmitUsage: true}}}}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	_, body, _ := scriptedPost(t, srv.URL, "", `{}`)
	if !strings.Contains(body, `"choices":[]`) || strings.Contains(body, `"usage"`) {
		t.Fatal(body)
	}
}

func TestClientHeaderAndFullRequestLog(t *testing.T) {
	s := &Server{ClientHeader: "X-Test-Client", Rules: []Rule{{Name: "agent", Steps: []Step{{Answer: "first"}, {Answer: "second"}}}}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	for _, client := range []string{"a", "b"} {
		req, err := http.NewRequest("POST", srv.URL+"/v1/chat/completions", strings.NewReader(`{"user":"shared","tools":[{"type":"function","function":{"name":"test","parameters":{"type":"object"}}}],"tool_choice":"auto"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Test-Client", client)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if !strings.Contains(string(body), "first") {
			t.Fatal(string(body))
		}
	}
	log := s.Log()
	if len(log) != 2 || !strings.Contains(string(log[0].Tools), "parameters") || string(log[0].ToolChoice) != `"auto"` {
		t.Fatal(log)
	}
	s.ClearLog()
	if len(s.Log()) != 0 || s.Calls() != 2 {
		t.Fatal("clear log rewound counters")
	}
}

func TestScriptReplacementOverHTTP(t *testing.T) {
	s := &Server{}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	req, err := http.NewRequest("PUT", srv.URL+"/sim/llm/script", strings.NewReader(`[{"name":"block","model":"guard","steps":[{"answer":"{\"allow\":false}"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	_, body, _ := scriptedPost(t, srv.URL, "", `{"model":"guard"}`)
	if !strings.Contains(body, `\"allow\":false`) {
		t.Fatal(body)
	}
	req, err = http.NewRequest("DELETE", srv.URL+"/sim/llm/log", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if len(s.Log()) != 0 || s.Calls() != 1 {
		t.Fatal(s.Log(), s.Calls())
	}
}

func TestSetRulesPreservesOmittedLegacyArgumentsAndEmptySequences(t *testing.T) {
	s := &Server{}
	if err := s.SetRules([]Rule{{Tool: &ToolCall{Name: "test"}}}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	_, body, _ := scriptedPost(t, srv.URL, "", `{}`)
	if !strings.Contains(body, `"arguments":"{}"`) {
		t.Fatal(body)
	}
	if err := s.SetRules([]Rule{{Name: "empty", Steps: []Step{}}}); err != nil {
		t.Fatal(err)
	}
	status, _, _ := scriptedPost(t, srv.URL, "", `{}`)
	if status != 409 {
		t.Fatal(status)
	}
}

func TestCredentialHeaderCannotBecomeLoggedClientKey(t *testing.T) {
	s := &Server{ClientHeader: "Authorization", Rules: []Rule{{Name: "test", Steps: []Step{{Answer: "ok"}}}}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	status, _, _ := scriptedPost(t, srv.URL, "", `{}`)
	if status != 400 || len(s.Log()) != 0 {
		t.Fatal(status, s.Log())
	}
}

func TestCancelledProviderFailureKeepsOutstandingToolCalls(t *testing.T) {
	s := &Server{Rules: []Rule{{Name: "agent", Steps: []Step{
		{ToolCalls: []ToolCall{{Name: "lookup", ID: "call-1"}}},
		{Status: 503, DelayMS: 1000},
		{ToolName: "lookup", ToolResult: "ready", Answer: "recovered"},
	}}}}
	handler := s.Handler()
	tools := `"tools":[{"type":"function","function":{"name":"lookup"}}]`
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"user":"a",`+tools+`}`)))
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"id":"call-1"`) {
		t.Fatal(out.Code, out.Body.String())
	}
	result := `{"user":"a",` + tools + `,"messages":[{"role":"tool","tool_call_id":"call-1","content":"ready"}]}`
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out = httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(result)).WithContext(ctx))
	if log := s.Log(); len(log) != 2 || !log[1].Cancelled {
		t.Fatal(log)
	}
	out = httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(result)))
	if out.Code != 200 || !strings.Contains(out.Body.String(), "recovered") {
		t.Fatal(out.Code, out.Body.String())
	}
}

// A cancelled step was never delivered, so the client retries with the tool
// results it already sent; the calls they answer are still outstanding.
func TestCancelledAnswerKeepsTheResultsItWasGiven(t *testing.T) {
	s := &Server{Rules: []Rule{{Name: "agent", Steps: []Step{
		{ToolCalls: []ToolCall{{Name: "lookup", ID: "call-1"}}},
		{DelayMS: 1000, ToolName: "lookup", Answer: "slow"},
		{ToolName: "lookup", ToolResult: "ready", Answer: "recovered"},
	}}}}
	handler := s.Handler()
	tools := `"tools":[{"type":"function","function":{"name":"lookup"}}]`
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"user":"a",`+tools+`}`)))
	result := `{"user":"a",` + tools + `,"messages":[{"role":"tool","tool_call_id":"call-1","content":"ready"}]}`
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out = httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(result)).WithContext(ctx))
	out = httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(result)))
	if out.Code != 200 || !strings.Contains(out.Body.String(), "recovered") {
		t.Fatal(out.Code, out.Body.String())
	}
}

// The journal keeps the newest journalLimit entries and counts the rest.
func TestJournalKeepsTheNewestEntries(t *testing.T) {
	s := &Server{}
	handler := s.Handler()
	for i := 0; i < journalLimit+5; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`)))
	}
	log := s.Log()
	if len(log) != journalLimit || log[0].Seq != 6 {
		t.Fatalf("%d entries, first seq %d", len(log), log[0].Seq)
	}
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/sim/llm/log", nil))
	var body struct{ Count, Dropped int }
	if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil || body.Count != journalLimit || body.Dropped != 5 {
		t.Fatal(err, out.Body.String()[:80])
	}
	s.ClearLog()
	out = httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/sim/llm/log", nil))
	if !strings.Contains(out.Body.String(), `"dropped":0`) {
		t.Fatal(out.Body.String())
	}
}

func TestScriptedContentTypeIsKept(t *testing.T) {
	page := "<html>Bad Gateway</html>"
	s := &Server{Rules: []Rule{{Name: "proxy", Steps: []Step{{Status: 502, Negative: true, RawBody: &page, Headers: map[string]string{"Content-Type": "text/html"}}}}}}
	out := httptest.NewRecorder()
	s.Handler().ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)))
	if out.Code != 502 || out.Header().Get("Content-Type") != "text/html" || out.Body.String() != page {
		t.Fatal(out.Code, out.Header(), out.Body.String())
	}
}

func TestCredentialHeaderSpellingsAreRefused(t *testing.T) {
	for _, name := range []string{"X_API_KEY", "X-Api_Key", "X.Access.Key", "X-Password", "X-Auth", "X-Client-Credential"} {
		s := &Server{ClientHeader: name}
		out := httptest.NewRecorder()
		s.Handler().ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)))
		if out.Code != 400 {
			t.Errorf("%s: %d", name, out.Code)
		}
	}
	s := &Server{ClientHeader: "X-Author-Session"}
	out := httptest.NewRecorder()
	s.Handler().ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)))
	if out.Code != 200 {
		t.Errorf("X-Author-Session: %d", out.Code)
	}
}

func TestScriptRouteRefusesInJSON(t *testing.T) {
	s := &Server{}
	for _, body := range []string{`{`, `[{"steps":[{"raw_body":"x"}]}]`} {
		out := httptest.NewRecorder()
		s.Handler().ServeHTTP(out, httptest.NewRequest(http.MethodPut, "/sim/llm/script", strings.NewReader(body)))
		var refusal struct{ Error string }
		if out.Code != 400 || out.Header().Get("Content-Type") != "application/json" || json.Unmarshal(out.Body.Bytes(), &refusal) != nil || refusal.Error == "" {
			t.Errorf("%s: %d %v %q", body, out.Code, out.Header(), out.Body.String())
		}
	}
}
