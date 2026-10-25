// Package httpx holds the JSON request/response helpers and the consistent error shape:
//
//	{"error": {"code": "validation_failed", "message": "...", "fields": {"name": "..."}, "requestId": "..."}}
package httpx

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

type Error struct {
	Status  int               `json:"-"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func BadRequest(msg string) *Error { return &Error{Status: 400, Code: "bad_request", Message: msg} }
func Unauthorized() *Error {
	return &Error{Status: 401, Code: "unauthenticated", Message: "Sign in to continue."}
}
func Forbidden(msg string) *Error { return &Error{Status: 403, Code: "forbidden", Message: msg} }
func NotFound(what string) *Error {
	return &Error{Status: 404, Code: "not_found", Message: what + " not found."}
}
func Conflict(msg string) *Error    { return &Error{Status: 409, Code: "conflict", Message: msg} }
func TooMany(msg string) *Error     { return &Error{Status: 429, Code: "rate_limited", Message: msg} }
func Unsupported(msg string) *Error { return &Error{Status: 501, Code: "unsupported", Message: msg} }
func Unavailable(msg string) *Error { return &Error{Status: 503, Code: "unavailable", Message: msg} }

// Invalid builds a 422 with per-field messages for forms.
func Invalid(fields map[string]string) *Error {
	return &Error{Status: 422, Code: "validation_failed", Message: "Some fields need attention.", Fields: fields}
}

type ctxKey int

const requestIDKey ctxKey = 1

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// WithRequestID tags each request with an ID that is returned in headers and errors and logged.
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 8)
		rand.Read(buf)
		id := hex.EncodeToString(buf)
		w.Header().Set("X-Request-Id", id)
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration_ms", time.Since(start).Milliseconds(), "request_id", id)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) { s.status = code; s.ResponseWriter.WriteHeader(code) }

// Hijack lets WebSocket upgrades (the terminal) take over the connection.
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("connection can't be hijacked")
	}
	s.status = http.StatusSwitchingProtocols
	return h.Hijack()
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Recover turns panics into 500s instead of dropping the connection.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				slog.Error("panic", "error", fmt.Sprint(v), "request_id", RequestID(r.Context()))
				WriteError(w, r, errors.New("internal error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		json.NewEncoder(w).Encode(v)
	}
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	if !errors.As(err, &e) {
		slog.Error("unhandled error", "error", err, "request_id", RequestID(r.Context()))
		e = &Error{Status: 500, Code: "internal", Message: "Something went wrong. Check the API logs for request " + RequestID(r.Context()) + "."}
	}
	JSON(w, e.Status, map[string]any{"error": map[string]any{"code": e.Code, "message": e.Message, "fields": e.Fields, "requestId": RequestID(r.Context())}})
}

// Decode reads a JSON body (max 1 MB) and rejects unknown fields so typos fail loudly.
func Decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return BadRequest("Invalid JSON body: " + err.Error())
	}
	return nil
}

// DecodeOptional is Decode for endpoints whose body can be left out (e.g. a bare curl -X POST).
func DecodeOptional(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && err != io.EOF {
		return BadRequest("Invalid JSON body: " + err.Error())
	}
	return nil
}

// Handler adapts handlers that return errors.
type Handler func(w http.ResponseWriter, r *http.Request) error

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h(w, r); err != nil {
		WriteError(w, r, err)
	}
}
