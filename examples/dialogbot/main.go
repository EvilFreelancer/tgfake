// Command dialogbot runs a self-contained HTTP integration example: a tiny
// artificial bot, tgfake and two scripted model roles. No credentials required.
package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/EvilFreelancer/tgfake/pkg/botapi"
	"github.com/EvilFreelancer/tgfake/pkg/llmstub"
	"github.com/EvilFreelancer/tgfake/pkg/server"
)

//go:embed script.json
var script []byte

func main() {
	transcript, err := demo()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(transcript)
}

type wireMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCalls  []wireTool `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type wireTool struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// demo deliberately uses HTTP on both sides of tgfake, as an arbitrary bot
// library would. Assertions make the example an executable integration check.
func demo() (string, error) {
	fake := server.New(server.Options{})
	model := &llmstub.Server{}
	var rules []llmstub.Rule
	if err := json.Unmarshal(script, &rules); err != nil {
		fake.Close()
		return "", err
	}
	if err := model.SetRules(rules); err != nil {
		fake.Close()
		return "", err
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/", model.Handler())
	mux.Handle("/", fake.Handler())
	srv := httptest.NewServer(mux)
	defer srv.Close()
	defer fake.Close()
	post := func(path string, body, out any) error {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		res, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(raw))
		if err != nil {
			return err
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != 200 {
			raw, _ := io.ReadAll(res.Body)
			return fmt.Errorf("%s: %d %s", path, res.StatusCode, raw)
		}
		if out != nil {
			return json.NewDecoder(res.Body).Decode(out)
		}
		return nil
	}
	send := func(text string, markup any) (int, error) {
		var res struct{ Result botapi.Message }
		err := post("/bot1/sendMessage", map[string]any{"chat_id": 4242, "text": text, "reply_markup": markup}, &res)
		return res.Result.MessageID, err
	}
	updates := func() ([]botapi.Update, error) {
		var result struct{ Result []botapi.Update }
		err := post("/bot1/getUpdates", map[string]any{}, &result)
		return result.Result, err
	}
	if err := post("/sim/message", map[string]any{"text": "/start"}, nil); err != nil {
		return "", err
	}
	if _, err := send("Choose a mode", botapi.ReplyKeyboardMarkup{Keyboard: [][]botapi.KeyboardButton{{{Text: "Photo"}, {Text: "Video"}, {Text: "Wallet"}}}, ResizeKeyboard: true}); err != nil {
		return "", err
	}
	if err := post("/sim/keyboard/press", server.KeyboardPress{Column: 1}, nil); err != nil {
		return "", err
	}
	received, err := updates()
	if err != nil {
		return "", err
	}
	last := received[len(received)-1]
	if last.Message == nil || last.Message.Text != "Video" || last.CallbackQuery != nil {
		return "", fmt.Errorf("reply button did not send an ordinary message")
	}
	if _, err = send("Choose a model", botapi.InlineKeyboardMarkup{InlineKeyboard: [][]botapi.InlineKeyboardButton{{{Text: "Demo model", CallbackData: "demo"}}}}); err != nil {
		return "", err
	}
	if fake.Chat(4242).ReplyKeyboard == nil {
		return "", fmt.Errorf("inline keyboard removed the reply menu")
	}
	complete := func(role string, messages []wireMessage) (wireMessage, error) {
		var res struct {
			Choices []struct{ Message wireMessage }
		}
		req := map[string]any{"user": "4242", "model": role, "messages": messages}
		if role == "agent" {
			req["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{"name": "generate_image", "parameters": map[string]any{"type": "object"}}}}
		}
		err := post("/v1/chat/completions", req, &res)
		if err != nil {
			return wireMessage{}, err
		}
		if len(res.Choices) != 1 {
			return wireMessage{}, fmt.Errorf("expected one choice")
		}
		return res.Choices[0].Message, nil
	}
	history := []wireMessage{{Role: "user", Content: "Make a blue scene"}}
	guard, err := complete("guard", history)
	if err != nil {
		return "", err
	}
	if guard.Content != `{"allow":true}` {
		return "", fmt.Errorf("guard did not allow the message")
	}
	clarification, err := complete("agent", history)
	if err != nil {
		return "", err
	}
	if _, err = send(fmt.Sprint(clarification.Content), nil); err != nil {
		return "", err
	}
	history = append(history, clarification, wireMessage{Role: "user", Content: "Blue"})
	// An independent guard call must not advance the agent cursor.
	if _, err = complete("guard", history); err != nil {
		return "", err
	}
	action, err := complete("agent", history)
	if err != nil {
		return "", err
	}
	if len(action.ToolCalls) != 1 || action.ToolCalls[0].Function.Name != "generate_image" {
		return "", fmt.Errorf("expected generate_image call")
	}
	// The tool is intentionally artificial: it returns a local fixture result.
	history = append(history, action, wireMessage{Role: "tool", ToolCallID: action.ToolCalls[0].ID, Content: `{"status":"ready"}`})
	answer, err := complete("agent", history)
	if err != nil {
		return "", err
	}
	target, err := send(fmt.Sprint(answer.Content), botapi.ForceReply{ForceReply: true, InputFieldPlaceholder: "Describe changes"})
	if err != nil {
		return "", err
	}
	if err = post("/sim/message", server.IncomingMessage{Text: "Brighter", ReplyToMessageID: target}, nil); err != nil {
		return "", err
	}
	received, err = updates()
	if err != nil {
		return "", err
	}
	reply := received[len(received)-1].Message
	if reply == nil || reply.ReplyToMessage == nil || reply.ReplyToMessage.MessageID != target {
		return "", fmt.Errorf("ForceReply response lost its message reference")
	}
	return fake.Chat(4242).Text(), nil
}
