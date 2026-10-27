package llmstub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Filters narrows a rule. All specified filters must match. Text filters
// are case-insensitive substrings; model, profile, user and tool names are exact.
type Filters struct {
	Model    string   `json:"model,omitempty"`
	System   string   `json:"system,omitempty"`
	Profile  string   `json:"profile,omitempty"`
	User     string   `json:"user,omitempty"`
	Tools    []string `json:"tools,omitempty"`
	HasTools *bool    `json:"has_tools,omitempty"`
}

// Step is one response in a rule's sequence. RawBody and Negative deliberately
// permit broken provider responses. Delays are milliseconds and context-aware.
type Step struct {
	Answer       string            `json:"answer,omitempty"`
	ToolCalls    []ToolCall        `json:"tool_calls,omitempty"`
	ToolName     string            `json:"tool_name,omitempty"`
	ToolResult   string            `json:"tool_result,omitempty"`
	Usage        map[string]int    `json:"usage,omitempty"`
	OmitUsage    bool              `json:"omit_usage,omitempty"`
	Status       int               `json:"status,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	RawBody      *string           `json:"raw_body,omitempty"`
	EmptyChoices bool              `json:"empty_choices,omitempty"`
	Negative     bool              `json:"negative,omitempty"`
	DelayMS      int               `json:"delay_ms,omitempty"`
}

type scenarioKey struct {
	rule            int
	profile, client string
}
type cursor struct {
	index       int
	pending     map[string]string
	reservation int
}

// Entry is one completed LLM request, ordered by reservation sequence. No
// request headers are retained. Response is a string so malformed JSON survives.
type Entry struct {
	Seq        int             `json:"seq"`
	At         time.Time       `json:"at"`
	Profile    string          `json:"profile,omitempty"`
	Client     string          `json:"client,omitempty"`
	Model      string          `json:"model"`
	Messages   json.RawMessage `json:"messages"`
	Tools      json.RawMessage `json:"tools,omitempty"`
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`
	Status     int             `json:"status"`
	Rule       string          `json:"rule,omitempty"`
	Step       int             `json:"step"`
	LatencyMS  float64         `json:"latency_ms"`
	Response   string          `json:"response"`
	Cancelled  bool            `json:"cancelled,omitempty"`
}

func (f Filters) matches(req chatRequest, profile, client string) bool {
	if f.Model != "" && f.Model != req.Model || f.Profile != "" && f.Profile != profile || f.User != "" && f.User != client {
		return false
	}
	var system strings.Builder
	for _, m := range req.Messages {
		if m.Role == "system" {
			system.WriteString(userText(m.Content))
			system.WriteByte('\n')
		}
	}
	if !strings.Contains(strings.ToLower(system.String()), strings.ToLower(f.System)) {
		return false
	}
	if f.HasTools != nil && *f.HasTools != (len(req.Tools) > 0) {
		return false
	}
	for _, name := range f.Tools {
		if !req.offers(name) {
			return false
		}
	}
	return true
}

func (r chatRequest) offers(name string) bool {
	for _, t := range r.Tools {
		if t.Type == "function" && t.Function.Name == name {
			return true
		}
	}
	return false
}

// fails reports whether the step answers with a provider failure rather than
// a completion: an error status, a raw body or no choices.
func (s Step) fails() bool {
	return s.Status >= 400 || s.RawBody != nil || s.EmptyChoices
}

func (r Rule) extended() bool {
	return r.Name != "" || r.Steps != nil || r.Model != "" || r.System != "" || r.Profile != "" || r.User != "" || r.Tools != nil || r.HasTools != nil
}

// selectStep reserves a response under the mutex. Waiting and writing happen
// after unlocking, so a slow provider never blocks another client.
func (s *Server) selectStep(req chatRequest, profile, client string) (step Step, entry Entry, selected bool, problem string, abandon func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prompt := strings.ToLower(req.lastUserText(s.StripTags))
	for i, rule := range s.Rules {
		if !strings.Contains(prompt, strings.ToLower(rule.Match)) || !rule.matches(req, profile, client) {
			continue
		}
		if !rule.extended() {
			return step, entry, false, "", nil
		}
		s.calls++
		entry = Entry{Seq: s.calls, Rule: rule.Name, Step: 0}
		if entry.Rule == "" {
			entry.Rule = fmt.Sprintf("rule-%d", i)
		}
		if req.Stream {
			return step, entry, true, "sequenced responses support non-streaming requests only", nil
		}
		if rule.Steps == nil {
			step.Answer = rule.Answer
			if rule.Tool != nil {
				step.ToolCalls = []ToolCall{*rule.Tool}
			}
			return step, entry, true, validateTools(req, step), nil
		}
		if s.cursors == nil {
			s.cursors = map[scenarioKey]*cursor{}
		}
		key := scenarioKey{i, profile, client}
		c := s.cursors[key]
		if c == nil {
			c = &cursor{}
			s.cursors[key] = c
		}
		index := c.index
		entry.Step = index
		if index >= len(rule.Steps) {
			if !rule.RepeatLast || len(rule.Steps) == 0 {
				return step, entry, true, "scenario exhausted", nil
			}
			index = len(rule.Steps) - 1
		}
		step = rule.Steps[index]
		entry.Step = index
		if problem = matchResults(req, c.pending, step); problem != "" {
			return step, entry, true, problem, nil
		}
		if problem = validateTools(req, step); problem != "" {
			return step, entry, true, problem, nil
		}
		pending := map[string]string{}
		// Copy before assigning IDs: configuration belongs to every client.
		step.ToolCalls = append([]ToolCall(nil), step.ToolCalls...)
		for j := range step.ToolCalls {
			tool := &step.ToolCalls[j]
			if tool.ID == "" {
				tool.ID = fmt.Sprintf("call_llmstub_%d_%d", s.calls, j)
			}
			if _, exists := pending[tool.ID]; exists && !step.Negative {
				return step, entry, true, "duplicate tool_call_id", nil
			}
			pending[tool.ID] = tool.Name
		}
		c.index++
		// A failed provider response delivers nothing: the client retries
		// with the same tool results, which the next step still needs.
		if step.fails() {
			return step, entry, true, "", nil
		}
		previous := c.pending
		c.pending = pending
		c.reservation = entry.Seq
		seq := entry.Seq
		abandon = func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			// Cancellation consumes the step, but the client never received
			// it: the calls it made are not outstanding, the ones it had
			// answered still are. Leave newer reservations untouched.
			if c.reservation == seq {
				c.pending = previous
			}
		}
		return step, entry, true, "", abandon
	}
	return step, entry, false, "", nil
}

func validateTools(req chatRequest, step Step) string {
	if step.Negative {
		return ""
	}
	if step.RawBody != nil || step.EmptyChoices {
		return "raw_body and empty_choices require negative: true"
	}
	for _, tool := range step.ToolCalls {
		if tool.RawArguments != nil {
			return "raw_arguments requires negative: true"
		}
		if !req.offers(tool.Name) {
			return "tool not offered: " + tool.Name
		}
		var args map[string]json.RawMessage
		if len(tool.Arguments) > 0 && (json.Unmarshal(tool.Arguments, &args) != nil || args == nil) {
			return "tool arguments must be a JSON object"
		}
	}
	return ""
}

func matchResults(req chatRequest, pending map[string]string, step Step) string {
	if len(pending) == 0 {
		if step.ToolName != "" || step.ToolResult != "" {
			return "step requires a preceding tool call"
		}
		return ""
	}
	results := map[string]string{}
	// Only results after the most recent assistant tool call can complete it.
	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		if m.Role == "assistant" {
			break
		}
		if m.Role == "tool" {
			results[m.ToolCallID] = userText(m.Content)
		}
	}
	matched := step.ToolName == "" && step.ToolResult == ""
	for id, name := range pending {
		value, ok := results[id]
		if !ok {
			return "missing tool result for " + id
		}
		if (step.ToolName == "" || step.ToolName == name) && strings.Contains(value, step.ToolResult) {
			matched = true
		}
	}
	if !matched {
		return "tool result does not match step"
	}
	return ""
}

func (s *Server) clientKey(r *http.Request, req chatRequest) string {
	if s.ClientHeader != "" {
		return r.Header.Get(s.ClientHeader)
	}
	return req.User
}

func (s *Server) scripted(w http.ResponseWriter, r *http.Request, req chatRequest) bool {
	start := time.Now()
	profile := r.Header.Get("X-Tgfake-Profile")
	client := s.clientKey(r, req)
	step, entry, selected, problem, abandon := s.selectStep(req, profile, client)
	if !selected {
		return false
	}
	entry.At = start
	entry.Profile = profile
	entry.Client = client
	entry.Model = req.Model
	entry.Messages = req.rawMessages
	entry.Tools = req.rawTools
	entry.ToolChoice = req.ToolChoice
	status := step.Status
	if status == 0 {
		status = 200
	}
	var body []byte
	if problem != "" {
		status = 409
		body, _ = json.Marshal(map[string]any{"error": map[string]string{"message": problem}})
	} else {
		body = s.stepBody(step, req, entry.Seq)
	}
	if status < 200 || status > 599 {
		status = 500
		body = []byte(`{"error":{"message":"invalid scripted status"}}`)
	}
	defer func() {
		entry.Status = status
		entry.Response = string(body)
		entry.LatencyMS = float64(time.Since(start)) / float64(time.Millisecond)
		s.journal(entry)
	}()
	if problem == "" && step.DelayMS > 0 {
		timer := time.NewTimer(time.Duration(step.DelayMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			if abandon != nil {
				abandon()
			}
			status = 499
			entry.Cancelled = true
			body = nil
			return true
		}
	}
	w.Header().Set("Content-Type", "application/json")
	for key, value := range step.Headers {
		w.Header().Set(key, value)
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
	return true
}

// CheckClientHeader refuses a Server.ClientHeader name that looks like it
// carries a credential: the header's value is journalled as the client key
// and served by GET /sim/llm/log. A completion is refused with 400 under such
// a header anyway; this lets a caller refuse it before serving.
func CheckClientHeader(name string) error {
	if secretClientHeader(name) {
		return fmt.Errorf("client header %q looks like a credential; name a nonsecret test identifier", name)
	}
	return nil
}

// secretClientHeader reports whether a header name looks like it carries a
// credential, whatever separates its words.
func secretClientHeader(name string) bool {
	name = strings.NewReplacer("_", "-", ".", "-").Replace(strings.ToLower(name))
	for _, secret := range []string{"authorization", "cookie", "token", "secret", "password", "credential", "api-key", "apikey", "access-key", "private-key"} {
		if strings.Contains(name, secret) {
			return true
		}
	}
	for _, word := range strings.Split(name, "-") {
		if word == "auth" {
			return true
		}
	}
	return false
}

func (s *Server) stepBody(step Step, req chatRequest, seq int) []byte {
	if step.RawBody != nil {
		return []byte(*step.RawBody)
	}
	if step.Status >= 400 {
		body, _ := json.Marshal(map[string]any{"error": map[string]any{"message": "scripted provider failure", "code": step.Status}})
		return body
	}
	message := map[string]any{"role": "assistant", "content": step.Answer}
	finish := "stop"
	if len(step.ToolCalls) > 0 {
		calls := []any{}
		for i, tool := range step.ToolCalls {
			id := tool.ID
			if id == "" {
				id = fmt.Sprintf("call_llmstub_%d_%d", seq, i)
			}
			args := string(tool.Arguments)
			if args == "" {
				args = "{}"
			}
			if tool.RawArguments != nil {
				args = *tool.RawArguments
			}
			calls = append(calls, map[string]any{"id": id, "type": "function", "function": map[string]any{"name": tool.Name, "arguments": args}})
		}
		message["content"] = nil
		message["tool_calls"] = calls
		finish = "tool_calls"
	}
	choices := []any{}
	if !step.EmptyChoices {
		choices = append(choices, map[string]any{"index": 0, "finish_reason": finish, "message": message})
	}
	model := req.Model
	if model == "" {
		model = s.model()
	}
	body := map[string]any{"id": fmt.Sprintf("chatcmpl-tgfake-%d", seq), "object": "chat.completion", "created": time.Now().Unix(), "model": model, "choices": choices}
	if !step.OmitUsage {
		usage := step.Usage
		if usage == nil {
			usage = map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
		}
		body["usage"] = usage
	}
	raw, _ := json.Marshal(body)
	return raw
}

// journalLimit is how many completed requests the journal keeps; older ones
// are dropped, so a long-running stand does not grow without bound.
const journalLimit = 1000

// journal files a completed request in reservation order, so the entries
// dropped past journalLimit are the oldest reservations, not the earliest
// completions.
func (s *Server) journal(entry Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := sort.Search(len(s.entries), func(i int) bool { return s.entries[i].Seq > entry.Seq })
	s.entries = append(s.entries, Entry{})
	copy(s.entries[i+1:], s.entries[i:])
	s.entries[i] = entry
	if extra := len(s.entries) - journalLimit; extra > 0 {
		s.entries = append([]Entry(nil), s.entries[extra:]...)
		s.dropped += extra
	}
}

// Log returns an independent snapshot in request order, including failures.
// It holds the newest 1000 entries; GET /sim/llm/log counts the dropped ones.
func (s *Server) Log() []Entry {
	s.mu.Lock()
	entries := append([]Entry(nil), s.entries...)
	s.mu.Unlock()
	// Stored entries are never changed, so the deep copy needs no lock.
	raw, _ := json.Marshal(entries)
	out := []Entry{}
	_ = json.Unmarshal(raw, &out)
	return out
}

// ClearLog forgets completed requests, without rewinding sequence numbers.
func (s *Server) ClearLog() { s.mu.Lock(); defer s.mu.Unlock(); s.entries, s.dropped = nil, 0 }

// Reset rewinds scenario cursors, legacy answer rotation and the journal.
// Request sequence numbers keep growing. Reset after in-flight calls finish.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cursors = nil
	s.entries, s.dropped = nil, 0
	s.answerTurn = 0
}

// SetRules replaces the script and resets cursors. Configure public fields
// before serving; use SetRules for changes while serving.
func (s *Server) SetRules(rules []Rule) error {
	if _, err := json.Marshal(rules); err != nil {
		return err
	}
	copied := make([]Rule, len(rules))
	for i, rule := range rules {
		copied[i] = rule
		if rule.Tools != nil {
			copied[i].Tools = append([]string{}, rule.Tools...)
		}
		if rule.HasTools != nil {
			value := *rule.HasTools
			copied[i].HasTools = &value
		}
		if rule.Tool != nil {
			tool := cloneTool(*rule.Tool)
			copied[i].Tool = &tool
		}
		if rule.Steps == nil {
			continue
		}
		copied[i].Steps = make([]Step, len(rule.Steps))
		for j, step := range rule.Steps {
			copied[i].Steps[j] = step
			dest := &copied[i].Steps[j]
			if step.RawBody != nil {
				value := *step.RawBody
				dest.RawBody = &value
			}
			if step.Usage != nil {
				dest.Usage = map[string]int{}
				for k, v := range step.Usage {
					dest.Usage[k] = v
				}
			}
			if step.Headers != nil {
				dest.Headers = map[string]string{}
				for k, v := range step.Headers {
					dest.Headers[k] = v
				}
			}
			if step.ToolCalls != nil {
				dest.ToolCalls = make([]ToolCall, len(step.ToolCalls))
				for k, t := range step.ToolCalls {
					dest.ToolCalls[k] = cloneTool(t)
				}
			}
		}
	}
	for _, r := range copied {
		for _, step := range r.Steps {
			if !step.Negative && (step.RawBody != nil || step.EmptyChoices) {
				return fmt.Errorf("raw_body and empty_choices require negative: true")
			}
			for _, tool := range step.ToolCalls {
				if tool.RawArguments != nil && !step.Negative {
					return fmt.Errorf("raw_arguments requires negative: true")
				}
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Rules = copied
	s.cursors = nil
	return nil
}

func cloneTool(tool ToolCall) ToolCall {
	tool.Arguments = append(json.RawMessage(nil), tool.Arguments...)
	if tool.RawArguments != nil {
		value := *tool.RawArguments
		tool.RawArguments = &value
	}
	return tool
}

func (s *Server) registerDiagnostics(mux *http.ServeMux) {
	mux.HandleFunc("GET /sim/llm/log", func(w http.ResponseWriter, _ *http.Request) {
		entries := s.Log()
		s.mu.Lock()
		dropped := s.dropped
		s.mu.Unlock()
		writeJSON(w, map[string]any{"entries": entries, "count": len(entries), "dropped": dropped, "calls": s.Calls()})
	})
	mux.HandleFunc("DELETE /sim/llm/log", func(w http.ResponseWriter, _ *http.Request) { s.ClearLog(); writeJSON(w, map[string]any{"ok": true}) })
	mux.HandleFunc("POST /sim/llm/reset", func(w http.ResponseWriter, _ *http.Request) { s.Reset(); writeJSON(w, map[string]any{"ok": true}) })
	mux.HandleFunc("PUT /sim/llm/script", func(w http.ResponseWriter, r *http.Request) {
		var rules []Rule
		// null decodes to no rules; only [] empties the script.
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&rules); err != nil || rules == nil {
			refuse(w, "invalid script")
			return
		}
		if err := s.SetRules(rules); err != nil {
			refuse(w, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
}

// refuse answers a /sim/llm/ request 400 with {"error": message}, the shape
// of every refusal of the simulation API.
func refuse(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// journalWriter captures legacy JSON/SSE responses while preserving flushing.
type journalWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *journalWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *journalWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}
func (w *journalWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
