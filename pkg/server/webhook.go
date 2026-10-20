package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/EvilFreelancer/tgfake/pkg/webapp"
)

type webhookState struct {
	url            string
	secret         string
	token          string
	ip             string
	maxConnections int
	certificate    bool
	lastErrorDate  int64
	lastError      string
	version        int
	ctx            context.Context
	cancel         context.CancelFunc
	client         *http.Client
}

var webhookSecret = regexp.MustCompile(`^[A-Za-z0-9_-]*$`)

func (s *Server) setWebhook(w http.ResponseWriter, req *http.Request, method string, params url.Values) {
	rawURL := params.Get("url")
	if strings.EqualFold(method, "deleteWebhook") {
		rawURL = ""
	}
	problem := ""
	if rawURL != "" {
		u, err := url.Parse(rawURL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") {
			problem = "Bad Request: invalid webhook URL specified"
		} else if !webapp.IsLoopbackHost(u.Hostname()) {
			if u.Scheme != "https" {
				problem = "Bad Request: bad webhook: An HTTPS URL must be provided for webhook"
			} else if port := u.Port(); port != "" && port != "443" && port != "80" && port != "88" && port != "8443" {
				problem = "Bad Request: bad webhook: Webhook can be set up only on ports 80, 88, 443 or 8443"
			}
		}
		if problem == "" && len(params.Get("secret_token")) > 256 {
			problem = "Bad Request: secret token is too long"
		}
		if problem == "" && !webhookSecret.MatchString(params.Get("secret_token")) {
			problem = "Bad Request: secret token contains illegal characters"
		}
	}
	var allowed []string
	changeAllowed := params.Get("allowed_updates") != "" && params.Get("allowed_updates") != "null"
	if changeAllowed && json.Unmarshal([]byte(params.Get("allowed_updates")), &allowed) != nil {
		problem = "Bad Request: can't parse allowed_updates JSON array"
	}
	client := *s.opts.HTTPClient
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	hasCertificate := false
	if rawURL != "" && req.MultipartForm != nil && len(req.MultipartForm.File["certificate"]) > 0 {
		file, err := req.MultipartForm.File["certificate"][0].Open()
		if err != nil {
			problem = "Bad Request: invalid certificate"
		} else {
			certificate, readErr := io.ReadAll(io.LimitReader(file, 3<<20))
			_ = file.Close()
			roots, _ := x509.SystemCertPool()
			if roots == nil {
				roots = x509.NewCertPool()
			}
			if readErr != nil || !roots.AppendCertsFromPEM(certificate) {
				problem = "Bad Request: invalid certificate"
			} else {
				transport := http.DefaultTransport.(*http.Transport).Clone()
				if existing, ok := client.Transport.(*http.Transport); ok {
					transport = existing.Clone()
				}
				if transport.TLSClientConfig == nil {
					transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
				} else {
					transport.TLSClientConfig = transport.TLSClientConfig.Clone()
				}
				transport.TLSClientConfig.RootCAs = roots
				client.Transport = transport
				hasCertificate = true
			}
		}
	}
	if problem != "" {
		s.writeError(w, method, params, 400, problem, 0)
		return
	}
	maxConnections := atoi(params.Get("max_connections"))
	if params.Get("max_connections") == "" {
		maxConnections = 40
	}
	maxConnections = max(1, min(100, maxConnections))
	token := s.Token()
	ctx, cancel := context.WithCancel(s.networkCtx)
	s.mu.Lock()
	if s.webhook.cancel != nil {
		s.webhook.cancel()
	}
	s.webhook = webhookState{url: rawURL, secret: params.Get("secret_token"), token: token, ip: params.Get("ip_address"), maxConnections: maxConnections, certificate: hasCertificate, version: s.webhook.version + 1, ctx: ctx, cancel: cancel, client: &client}
	if changeAllowed {
		s.allowed = append([]string(nil), allowed...)
	}
	if truthy(params.Get("drop_pending_updates")) {
		s.pending = nil
	}
	s.wakeLocked()
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

func (s *Server) webhookInfo(w http.ResponseWriter, method string, params url.Values) {
	s.mu.Lock()
	state := s.webhook
	info := map[string]any{"url": state.url, "has_custom_certificate": state.certificate, "pending_update_count": len(s.pending), "allowed_updates": append([]string(nil), s.allowed...)}
	if state.url != "" {
		info["max_connections"] = state.maxConnections
		if state.ip != "" {
			info["ip_address"] = state.ip
		}
		if state.lastErrorDate != 0 {
			info["last_error_date"], info["last_error_message"] = state.lastErrorDate, state.lastError
		}
	}
	s.mu.Unlock()
	s.writeResult(w, method, params, info)
}

func (s *Server) deliverWebhooks() {
	defer close(s.webhookDone)
	for {
		s.mu.Lock()
		state, wake := s.webhook, s.wake
		if state.url == "" || len(s.pending) == 0 {
			s.mu.Unlock()
			select {
			case <-wake:
				continue
			case <-s.closing:
				return
			}
		}
		update := s.pending[0]
		body, _ := json.Marshal(update)
		s.mu.Unlock()
		req, err := http.NewRequestWithContext(state.ctx, http.MethodPost, state.url, bytes.NewReader(body))
		var responseBody []byte
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			if state.secret != "" {
				req.Header.Set("X-Telegram-Bot-Api-Secret-Token", state.secret)
			}
			var response *http.Response
			response, err = state.client.Do(req)
			if err == nil {
				responseBody, err = io.ReadAll(io.LimitReader(response.Body, 4<<20))
				_ = response.Body.Close()
				if response.StatusCode < 200 || response.StatusCode >= 300 {
					err = fmt.Errorf("Wrong response from the webhook: %d %s", response.StatusCode, http.StatusText(response.StatusCode)) //nolint:staticcheck // Telegram's diagnostic starts with a capital letter.
				}
			}
		}
		s.mu.Lock()
		current := s.webhook.version == state.version
		if current {
			if err == nil {
				for i, queued := range s.pending {
					if queued.UpdateID == update.UpdateID {
						s.pending = append(s.pending[:i], s.pending[i+1:]...)
						break
					}
				}
			} else if state.ctx.Err() == nil {
				s.webhook.lastErrorDate, s.webhook.lastError = s.now().Unix(), err.Error()
			}
		}
		s.mu.Unlock()
		if s.closed() {
			return
		}
		if err == nil && current {
			s.webhookReply(state.ctx, state.token, responseBody)
			continue
		}
		if !current {
			continue
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-timer.C:
		case <-state.ctx.Done():
		case <-s.closing:
			timer.Stop()
			return
		}
		timer.Stop()
	}
}

type webhookResponse struct{ header http.Header }

func (w *webhookResponse) Header() http.Header            { return w.header }
func (w *webhookResponse) WriteHeader(status int)         {}
func (w *webhookResponse) Write(body []byte) (int, error) { return len(body), nil }

func (s *Server) webhookReply(ctx context.Context, token string, body []byte) {
	var params map[string]json.RawMessage
	if json.Unmarshal(body, &params) != nil {
		return
	}
	var method string
	if json.Unmarshal(params["method"], &method) != nil || method == "" || strings.ContainsAny(method, "/?#") {
		return
	}
	delete(params, "method")
	raw, _ := json.Marshal(params)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/bot"+token+"/"+method, bytes.NewReader(raw))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	s.serveBotAPI(&webhookResponse{header: make(http.Header)}, req)
}
