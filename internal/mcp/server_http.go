package mcp

import (
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
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
	mux.HandleFunc("/mcp", s.handle)
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

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
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
			Result:  map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]any{"name": "gpt-tunnel-gatewayd", "version": "0.6.11"}},
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
