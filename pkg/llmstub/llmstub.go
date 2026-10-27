// Package llmstub is a scripted model server in the shape of the OpenAI Chat
// Completions API: GET /v1/models and POST /v1/chat/completions, with and
// without streaming. It exists so a bot built on a language model runs on the
// stand of cmd/tgfake with no key and no network: the answer to a prompt is
// chosen by rule, by turn, or by echoing the prompt back, and streamed word
// by word at a chosen pace so a bot that edits its answer as it streams has
// something to do.
package llmstub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Rule maps a request to an answer or an independent sequence of Steps.
// The legacy fields keep their positions, so a keyed Rule literal from an
// older version still compiles and behaves the same. Optional Filters are
// ANDed; a named sequence has a cursor per explicit client. Legacy rules
// keep their original behavior: the first Match in the last user message
// (case-insensitive) wins; an empty Match matches all.
//
// A rule with a Tool makes the model act before it answers: the first request
// of the turn gets the tool call, and the request that carries the tool's
// result gets Answer. That is enough to script a turn that starts work - a
// background command, a gated tool that asks for permission - without a model.
type Rule struct {
	Match      string    `json:"match"`
	Answer     string    `json:"answer"`
	Tool       *ToolCall `json:"tool,omitempty"`

	Filters
	Name       string    `json:"name,omitempty"`
	Steps      []Step    `json:"steps,omitempty"`
	RepeatLast bool      `json:"repeat_last,omitempty"`
}

// ToolCall is the call a Rule makes. Arguments is the JSON object the tool is
// called with; a script writes it as an object, not as an encoded string.
type ToolCall struct {
	ID           string          `json:"id,omitempty"`
	RawArguments *string         `json:"raw_arguments,omitempty"`
	Name         string          `json:"name"`
	Arguments    json.RawMessage `json:"arguments"`
}

// Server is the scripted model.
type Server struct {
	// Model is the id reported by /v1/models and echoed in answers.
	Model string
	// Answers are used in turn, one per completion call, when no Rule matches.
	Answers []string
	// Rules are checked first, in order.
	Rules []Rule
	// Delay is the pause between streamed chunks.
	Delay time.Duration
	// ChunkWords is how many words each streamed chunk carries; at most one
	// by default.
	ChunkWords int
	// StripTags names the blocks a client appends to the person's words, such
	// as Coddy's <turn_context>: every <tag>...</tag> block of a listed tag is
	// cut out of a user message before the prompt is matched or echoed, and a
	// user message with nothing else in it is skipped. Empty cuts nothing.
	StripTags []string

	// ClientHeader selects the explicit client key instead of request.user.
	ClientHeader string
	cursors      map[scenarioKey]*cursor
	entries      []Entry
	dropped      int
	answerTurn   int
	mu           sync.Mutex
	calls        int
}

// DefaultModel is the model id a Server with no Model reports.
const DefaultModel = "tgfake-demo"

// Handler serves completions/models under /v1 and diagnostics under /sim/llm.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.registerDiagnostics(mux)
	mux.HandleFunc("GET /v1/models", s.models)
	mux.HandleFunc("POST /v1/chat/completions", s.completions)
	return mux
}

func (s *Server) model() string {
	if s.Model == "" {
		return DefaultModel
	}
	return s.Model
}

func (s *Server) models(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"object": "list",
		"data":   []map[string]any{{"id": s.model(), "object": "model", "owned_by": "tgfake"}},
	})
}

type chatRequest struct {
	rawMessages json.RawMessage
	rawTools    json.RawMessage
	Model       string `json:"model"`
	User        string `json:"user"`
	Tools       []struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`
	Stream     bool            `json:"stream"`
	Messages   []struct {
		ToolCallID string          `json:"tool_call_id,omitempty"`
		Name       string          `json:"name,omitempty"`
		ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
	} `json:"messages"`
}

func (r *chatRequest) UnmarshalJSON(raw []byte) error {
	type plain chatRequest
	var decoded plain
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	var fields struct {
		Messages json.RawMessage `json:"messages"`
		Tools    json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	*r = chatRequest(decoded)
	r.rawMessages = fields.Messages
	r.rawTools = fields.Tools
	if len(r.rawMessages) == 0 {
		r.rawMessages = json.RawMessage("[]")
	}
	return nil
}

// answersToolResult reports whether the request's newest message - past the
// blocks of tags a client appends to every request - is a tool result: the
// model already made its call this turn and now answers what came back.
func (r *chatRequest) answersToolResult(tags []string) bool {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		m := r.Messages[i]
		if m.Role == "user" && strings.TrimSpace(stripTags(userText(m.Content), tags)) == "" {
			continue
		}
		return m.Role == "tool"
	}
	return false
}

// lastUserText is the text of the newest user message that a person wrote;
// content is a string or an array of typed parts, and only the text parts
// count. A client may append its own state to every request as one more user
// message (Coddy's <turn_context> block), so the newest user message is not
// always the person's: the blocks of tags are cut out and a message with
// nothing else in it is skipped.
func (r *chatRequest) lastUserText(tags []string) string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role != "user" {
			continue
		}
		if text := strings.TrimSpace(stripTags(userText(r.Messages[i].Content), tags)); text != "" {
			return text
		}
	}
	return ""
}

// userText flattens the content of one message.
func userText(content json.RawMessage) string {
	var str string
	if json.Unmarshal(content, &str) == nil {
		return str
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) == nil {
		var sb strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				sb.WriteString(p.Text)
			}
		}
		return sb.String()
	}
	return string(content)
}

// stripTags removes every <tag>...</tag> block of each tag from a message; an
// unclosed block runs to the end.
func stripTags(text string, tags []string) string {
	for _, tag := range tags {
		open, closing := "<"+tag+">", "</"+tag+">"
		for {
			start := strings.Index(text, open)
			if start < 0 {
				break
			}
			end := strings.Index(text[start:], closing)
			if end < 0 {
				text = text[:start]
				break
			}
			text = text[:start] + text[start+end+len(closing):]
		}
	}
	return text
}

// Answer picks the reply to a prompt and counts the call.
func (s *Server) Answer(prompt string) string {
	answer, _, _ := s.pick(prompt)
	return answer
}

// pick chooses the reply and returns the ordinal of the call it counted,
// under one lock, so two completions served at once get distinct ids. tool is
// the matched rule's call, nil when the reply is text only.
func (s *Server) pick(prompt string) (answer string, tool *ToolCall, call int) {
	answer, tool, call, _ = s.pickNamed(prompt)
	return
}

func (s *Server) pickNamed(prompt string) (answer string, tool *ToolCall, call int, rule string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.answerTurn++
	lower := strings.ToLower(prompt)
	for i, r := range s.Rules {
		if !r.extended() && (r.Match == "" || strings.Contains(lower, strings.ToLower(r.Match))) {
			return r.Answer, r.Tool, s.calls, fmt.Sprintf("rule-%d", i)
		}
	}
	if len(s.Answers) > 0 {
		return s.Answers[(s.answerTurn-1)%len(s.Answers)], nil, s.calls, "answers"
	}
	return "You said: " + strings.TrimSpace(prompt), nil, s.calls, "echo"
}

// Calls is how many completions the stub has answered.
func (s *Server) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *Server) completions(w http.ResponseWriter, r *http.Request) {
	if secretClientHeader(s.ClientHeader) {
		http.Error(w, `{"error":{"message":"client header must be a nonsecret test identifier"}}`, 400)
		return
	}
	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON body"}}`, http.StatusBadRequest)
		return
	}
	if s.scripted(w, r, req) {
		return
	}
	start := time.Now()
	capture := &journalWriter{ResponseWriter: w, status: 200}
	w = capture
	answer, tool, call, rule := s.pickNamed(req.lastUserText(s.StripTags))
	defer func() {
		messages, tools := req.rawMessages, req.rawTools
		entry := Entry{Rule: rule, Seq: call, At: start, Model: req.Model, Profile: r.Header.Get("X-Tgfake-Profile"), Client: s.clientKey(r, req), Messages: messages, Tools: tools, ToolChoice: req.ToolChoice, Status: capture.status, Response: capture.body.String(), LatencyMS: float64(time.Since(start)) / float64(time.Millisecond), Step: -1}
		s.journal(entry)
	}()
	id := fmt.Sprintf("chatcmpl-tgfake-%d", call)
	created := time.Now().Unix()
	usage := map[string]any{"prompt_tokens": 1, "completion_tokens": len(strings.Fields(answer)), "total_tokens": 1 + len(strings.Fields(answer))}
	if tool != nil && !req.answersToolResult(s.StripTags) {
		s.callTool(w, req.Stream, id, created, fmt.Sprintf("call_llmstub_%d", call), tool)
		return
	}

	if !req.Stream {
		writeJSON(w, map[string]any{
			"id": id, "object": "chat.completion", "created": created, "model": s.model(),
			"choices": []map[string]any{{"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": answer}}},
			"usage": usage,
		})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	send := func(v any) bool {
		raw, _ := json.Marshal(v)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": s.model(),
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		}
	}
	if !send(chunk(map[string]any{"role": "assistant", "content": ""}, nil)) {
		return
	}
	for _, piece := range splitWords(answer, s.ChunkWords) {
		if s.Delay > 0 {
			select {
			case <-time.After(s.Delay):
			case <-r.Context().Done():
				return
			}
		}
		if !send(chunk(map[string]any{"content": piece}, nil)) {
			return
		}
	}
	if !send(chunk(map[string]any{}, "stop")) {
		return
	}
	// The usage-only chunk the OpenAI stream ends with.
	if !send(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": s.model(),
		"choices": []map[string]any{}, "usage": usage}) {
		return
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// callTool answers with one tool call instead of text, in the shape of the
// request: a whole message, or a stream of one tool_calls delta and a
// tool_calls finish.
func (s *Server) callTool(w http.ResponseWriter, stream bool, id string, created int64, callID string, tool *ToolCall) {
	args := strings.TrimSpace(string(tool.Arguments))
	if args == "" {
		args = "{}"
	}
	call := map[string]any{
		"id": callID, "type": "function",
		"function": map[string]any{"name": tool.Name, "arguments": args},
	}
	usage := map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
	if !stream {
		writeJSON(w, map[string]any{
			"id": id, "object": "chat.completion", "created": created, "model": s.model(),
			"choices": []map[string]any{{"index": 0, "finish_reason": "tool_calls",
				"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []map[string]any{call}}}},
			"usage": usage,
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	send := func(v any) {
		raw, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		if flusher != nil {
			flusher.Flush()
		}
	}
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": s.model(),
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		}
	}
	call["index"] = 0
	send(chunk(map[string]any{"role": "assistant", "tool_calls": []map[string]any{call}}, nil))
	send(chunk(map[string]any{}, "tool_calls"))
	send(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": s.model(),
		"choices": []map[string]any{}, "usage": usage})
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// splitWords cuts text into chunks of n words, each keeping the whitespace
// that follows it, so the pieces concatenate back to the text.
func splitWords(text string, n int) []string {
	if n <= 0 {
		n = 1
	}
	var pieces []string
	var cur strings.Builder
	words := 0
	inSpace := false
	for _, r := range text {
		space := r == ' ' || r == '\n' || r == '\t'
		if inSpace && !space {
			words++
			if words%n == 0 {
				pieces = append(pieces, cur.String())
				cur.Reset()
			}
		}
		inSpace = space
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		pieces = append(pieces, cur.String())
	}
	return pieces
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
