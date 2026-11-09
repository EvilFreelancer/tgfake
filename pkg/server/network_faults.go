package server

import (
	"net/http"
	"net/url"
	"time"
)

// faultWriter carries per-request fault state without sharing it with another
// request. The usual response path records its outcome under the server lock.
type faultWriter struct {
	http.ResponseWriter
	s     *Server
	r     *http.Request
	fault *Fault
}

func (s *Server) beginFault(w http.ResponseWriter, r *http.Request, method string, params url.Values, f *Fault) (http.ResponseWriter, bool) {
	if f == nil {
		return w, false
	}
	fw := &faultWriter{ResponseWriter: w, s: s, r: r, fault: f}
	if f.AfterApply {
		return fw, false
	}
	if !fw.wait() {
		fw.record(method, params, 0, nil, false, false)
		panic(http.ErrAbortHandler)
	}
	if f.Disconnect {
		fw.record(method, params, 0, nil, false, false)
		panic(http.ErrAbortHandler)
	}
	if f.Code != 0 {
		s.writeError(fw, method, params, f.Code, f.Description, f.RetryAfter)
		return fw, true
	}
	return fw, false
}

func (w *faultWriter) wait() bool {
	if w.fault.DelayMS <= 0 {
		return true
	}
	timer := time.NewTimer(time.Duration(w.fault.DelayMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-w.r.Context().Done():
		return false
	case <-w.s.closing:
		return false
	}
}

func (w *faultWriter) record(method string, params url.Values, status int, body []byte, applied, sent bool) {
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	w.s.recordLocked(method, params, status, body)
	call := &w.s.calls[len(w.s.calls)-1]
	call.Fault = copyPointer(w.fault)
	call.Applied = applied
	call.ResponseSent = sent
}

func (w *faultWriter) respond(method string, params url.Values, status int, body []byte) {
	applied := status >= 200 && status < 300 && (w.fault.Code == 0 || w.fault.AfterApply)
	if w.fault.AfterApply && applied {
		if !w.wait() {
			w.record(method, params, 0, nil, true, false)
			panic(http.ErrAbortHandler)
		}
		if w.fault.Disconnect {
			w.record(method, params, 0, nil, true, false)
			panic(http.ErrAbortHandler)
		}
	}
	w.ResponseWriter.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err := w.Write(body)
	w.record(method, params, status, body, applied, err == nil)
}

func (s *Server) takeFileFault(params url.Values) *Fault {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A file rule is independent from all Bot API methods, including '*'.
	f := s.faults["file:*"]
	if f == nil || !f.matches(params) {
		return nil
	}
	out := *f
	if f.Times > 0 {
		f.Times--
		if f.Times == 0 {
			delete(s.faults, "file:*")
		}
	}
	return &out
}
