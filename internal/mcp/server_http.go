package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
)

func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ready\n"))
	})
	mux.Handle("/mcp", s.responseBoundary(http.HandlerFunc(s.handle)))
	return s.security(mux)
}

func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(w, "non-loopback Host rejected", http.StatusForbidden)
			return
		}
		if r.RemoteAddr != "" {
			remote, _, e := net.SplitHostPort(r.RemoteAddr)
			if e == nil {
				rip := net.ParseIP(remote)
				if rip == nil || !rip.IsLoopback() {
					http.Error(w, "remote caller rejected", http.StatusForbidden)
					return
				}
			}
		}
		if origin := r.Header.Get("Origin"); origin != "" && !isLoopbackOrigin(origin) {
			http.Error(w, "Origin rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackOrigin(v string) bool {
	return strings.HasPrefix(v, "http://127.0.0.1:") || strings.HasPrefix(v, "http://localhost:") || strings.HasPrefix(v, "https://127.0.0.1:") || strings.HasPrefix(v, "https://localhost:")
}

type responseReleaseKey struct{}

type responseReleaseQueue struct {
	mu        sync.Mutex
	released  bool
	releasing bool
	callbacks []func()
}

func withResponseRelease(ctx context.Context, release func(func())) context.Context {
	return context.WithValue(ctx, responseReleaseKey{}, release)
}

func responseReleaseFromContext(ctx context.Context) (func(func()), bool) {
	release, ok := ctx.Value(responseReleaseKey{}).(func(func()))
	return release, ok && release != nil
}

func (q *responseReleaseQueue) add(callback func()) {
	if callback == nil {
		return
	}
	q.mu.Lock()
	if !q.released {
		q.callbacks = append(q.callbacks, callback)
		q.mu.Unlock()
		return
	}
	q.mu.Unlock()
	callback()
}

func (q *responseReleaseQueue) release(w http.ResponseWriter) {
	_ = q.releaseAfter(w, nil)
}

func (q *responseReleaseQueue) releaseAfter(w http.ResponseWriter, complete func(bool) error) error {
	q.mu.Lock()
	if q.released || q.releasing {
		q.mu.Unlock()
		return nil
	}
	q.releasing = true
	callbacks := append([]func(){}, q.callbacks...)
	q.callbacks = nil
	q.mu.Unlock()
	deferred := len(callbacks) > 0
	if complete != nil {
		if err := complete(deferred); err != nil {
			q.mu.Lock()
			q.callbacks = append(callbacks, q.callbacks...)
			q.releasing = false
			q.mu.Unlock()
			return err
		}
	}
	if deferred {
		_ = http.NewResponseController(w).Flush()
	}
	q.mu.Lock()
	callbacks = append(callbacks, q.callbacks...)
	q.callbacks = nil
	q.released = true
	q.releasing = false
	q.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
	return nil
}

func (s *Server) responseBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		release := &responseReleaseQueue{}
		r = r.WithContext(withResponseRelease(r.Context(), release.add))
		buffered := newBufferedResponseWriter(w)
		next.ServeHTTP(buffered, r)
		if err := release.releaseAfter(w, func(deferred bool) error {
			return buffered.commit(w, deferred)
		}); err != nil {
			http.Error(w, "MCP response could not be completed", http.StatusInternalServerError)
		}
	})
}

// bufferedResponseWriter makes the response-completion boundary explicit for
// the rare MCP response that schedules work capable of stopping this process.
// The complete response is written with Content-Length and flushed before the
// deferred worker is released, so process termination cannot leave an
// unterminated chunked response behind.
type bufferedResponseWriter struct {
	destination http.ResponseWriter
	header      http.Header
	body        []byte
	status      int
	wroteHeader bool
	overflow    bool
}

const maxBufferedMCPResponseBytes = 2 << 20

var errMCPResponseTooLarge = errors.New("MCP response exceeds bounded response buffer")

func newBufferedResponseWriter(destination http.ResponseWriter) *bufferedResponseWriter {
	return &bufferedResponseWriter{destination: destination, header: destination.Header().Clone()}
}

func (w *bufferedResponseWriter) Header() http.Header {
	return w.header
}

func (w *bufferedResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
}

func (w *bufferedResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if len(p) > maxBufferedMCPResponseBytes-len(w.body) {
		w.overflow = true
		return 0, errMCPResponseTooLarge
	}
	w.body = append(w.body, p...)
	return len(p), nil
}

func (w *bufferedResponseWriter) commit(destination http.ResponseWriter, deferred bool) error {
	if w.overflow {
		return errMCPResponseTooLarge
	}
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	for key := range destination.Header() {
		destination.Header().Del(key)
	}
	for key, values := range w.header {
		for _, value := range values {
			destination.Header().Add(key, value)
		}
	}
	if deferred && destination.Header().Get("Content-Length") == "" {
		destination.Header().Set("Content-Length", strconv.Itoa(len(w.body)))
	}
	destination.WriteHeader(w.status)
	if len(w.body) == 0 {
		return nil
	}
	_, err := io.Copy(destination, bytes.NewReader(w.body))
	return err
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r = r.WithContext(runtime_log.WithRequestID(r.Context(), runtime_log.NewRequestID()))
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	var req request
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.write(w, response{
			JSONRPC: "2.0",
			ID:      nil,
			Error: &rpcError{
				Code:    -32700,
				Message: "parse error",
				Data:    err.Error(),
			},
		})
		return
	}
	switch req.Method {
	case "initialize":
		s.write(w, response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]any{"name": "gpt-tunnel-gatewayd", "version": "0.6.11", "gateway_id": s.Service.Config.GatewayID}},
		})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "ping":
		s.write(w, response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{},
		})
	case "tools/list":
		tools := s.publicTools()
		names := make([]string, 0, len(tools))
		for name := range tools {
			names = append(names, name)
		}
		sort.Strings(names)
		list := make([]Tool, 0, len(names))
		for _, name := range names {
			tool := tools[name]
			tool.Execute = nil
			list = append(list, tool)
		}
		s.write(w, response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"tools": list},
		})
	case "tools/call":
		var call toolCall
		if err := decode(req.Params, &call); err != nil {
			s.write(w, response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &rpcError{
					Code:    -32602,
					Message: "invalid params",
					Data:    err.Error(),
				},
			})
			return
		}
		if err := validateToolCallMeta(call.Meta); err != nil {
			s.write(w, response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &rpcError{
					Code:    -32602,
					Message: "invalid params",
					Data:    err.Error(),
				},
			})
			return
		}
		tool, ok := s.publicTools()[call.Name]
		if !ok {
			s.write(w, response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &rpcError{
					Code:    -32601,
					Message: "unknown tool",
				},
			})
			return
		}
		trustedContext := authority.Attach(r.Context(), s.AuthorityContext)
		executionContext := trustedContext
		executionArguments := call.Arguments
		var err error
		if authorityErr := requireToolAuthority(trustedContext, call.Name); authorityErr != nil {
			_, typed := typedSessionAuthorityContract(call.Name)
			if !typed {
				s.write(w, response{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result:  toolResult(tool, map[string]any{"error": authorityErr.Error()}, true),
				})
				return
			}
			if _, ok := sessionIDFromRaw(call.Arguments); !ok {
				s.write(w, response{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result:  toolResult(tool, map[string]any{"error": authorityErr.Error()}, true),
				})
				return
			}
			bootstrapContext, bootstrapErr := authority.BootstrapSessionAuthority(trustedContext)
			if bootstrapErr != nil {
				s.write(w, response{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result:  toolResult(tool, map[string]any{"error": authorityErr.Error()}, true),
				})
				return
			}
			executionContext, executionArguments, err = s.resolveTypedSessionAuthority(bootstrapContext, call.Name, tool.InputSchema, call.Arguments)
			if err == nil {
				err = requireToolAuthority(executionContext, call.Name)
			}
		} else {
			executionContext, executionArguments, err = s.resolveTypedSessionAuthority(trustedContext, call.Name, tool.InputSchema, call.Arguments)
		}
		if err != nil {
			s.write(w, response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  toolResult(tool, map[string]any{"error": err.Error()}, true),
			})
			return
		}
		if err := validateToolArguments(tool.InputSchema, call.Arguments); err != nil {
			s.write(w, response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &rpcError{
					Code:    -32602,
					Message: "invalid params",
					Data:    err.Error(),
				},
			})
			return
		}
		value, err := tool.Execute(executionContext, executionArguments)
		if err != nil {
			s.write(w, response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  toolResult(tool, map[string]any{"error": err.Error()}, true),
			})
			return
		}
		s.write(w, response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  toolResult(tool, value, false),
		})
	default:
		s.write(w, response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcError{
				Code:    -32601,
				Message: "method not found",
			},
		})
	}
}

func (s *Server) write(w http.ResponseWriter, v response) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
