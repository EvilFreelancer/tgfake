package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestContainerWebhookDelivery(t *testing.T) {
	delivered := make(chan map[string]any, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Host != "bot:8080" || req.URL.Path != "/webhook" || req.Header.Get("X-Telegram-Bot-Api-Secret-Token") != "test_secret" {
			t.Errorf("unexpected webhook: host=%s path=%s secret=%s", req.Host, req.URL.Path, req.Header.Get("X-Telegram-Bot-Api-Secret-Token"))
		}
		var update map[string]any
		if err := json.NewDecoder(req.Body).Decode(&update); err != nil {
			t.Error(err)
		}
		delivered <- update
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()
	transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != "bot:8080" {
			t.Errorf("unexpected destination: %s", addr)
		}
		return (&net.Dialer{}).DialContext(ctx, network, receiver.Listener.Addr().String())
	}}
	defer transport.CloseIdleConnections()
	s := newStand(t, Options{AllowHTTPWebhook: true, HTTPClient: &http.Client{Transport: transport, Timeout: time.Second}})
	defer s.fake.Close()
	status, body := s.call("setWebhook", url.Values{"url": {"http://bot:8080/webhook"}, "secret_token": {"test_secret"}})
	if status != http.StatusOK {
		t.Fatalf("setWebhook: %d %v", status, body)
	}
	s.fake.InjectMessage(IncomingMessage{Text: "container webhook"})
	select {
	case update := <-delivered:
		if update["message"].(map[string]any)["text"] != "container webhook" {
			t.Fatalf("update: %v", update)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no container webhook delivery")
	}
}

func TestContainerWebhookValidation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		allow       bool
		target      string
		secret      string
		status      int
		description string
	}{
		{"strict HTTP", false, "http://bot:8080/webhook", "", 400, "Bad Request: bad webhook: An HTTPS URL must be provided for webhook"},
		{"opt-in HTTP", true, "http://bot:8080/webhook", "", 200, ""},
		{"strict HTTPS port", true, "https://bot:8080/webhook", "", 400, "Bad Request: bad webhook: Webhook can be set up only on ports 80, 88, 443 or 8443"},
		{"invalid scheme", true, "ftp://bot:8080/webhook", "", 400, "Bad Request: invalid webhook URL specified"},
		{"missing host", true, "http:///webhook", "", 400, "Bad Request: invalid webhook URL specified"},
		{"invalid secret", true, "http://bot:8080/webhook", "!", 400, "Bad Request: secret token contains illegal characters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStand(t, Options{AllowHTTPWebhook: tc.allow})
			status, body := s.call("setWebhook", url.Values{"url": {tc.target}, "secret_token": {tc.secret}})
			if status != tc.status || (tc.description != "" && body["description"] != tc.description) {
				t.Fatalf("setWebhook: %d %v", status, body)
			}
		})
	}
}
