package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStatelessHTTPFromEnv(t *testing.T) {
	tests := []struct {
		name, value string
		set         bool
		want        bool
		wantError   bool
	}{
		{name: "unset"},
		{name: "false", value: "false", set: true},
		{name: "true", value: "true", set: true, want: true},
		{name: "invalid", value: "sometimes", set: true, wantError: true},
		{name: "empty", value: "", set: true, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("MCP_STATELESS_HTTP", tt.value)
			} else {
				t.Setenv("MCP_STATELESS_HTTP", "")
				if err := os.Unsetenv("MCP_STATELESS_HTTP"); err != nil {
					t.Fatal(err)
				}
			}
			got, err := statelessHTTPFromEnv()
			if (err != nil) != tt.wantError {
				t.Fatalf("statelessHTTPFromEnv() error = %v, wantError %t", err, tt.wantError)
			}
			if err != nil {
				if !strings.Contains(err.Error(), "MCP_STATELESS_HTTP") {
					t.Errorf("error %q does not identify MCP_STATELESS_HTTP", err)
				}
				return
			}
			if got != tt.want {
				t.Errorf("statelessHTTPFromEnv() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestStreamableHTTPAcrossHandlers(t *testing.T) {
	for _, stateless := range []bool{false, true} {
		name := "stateful"
		if stateless {
			name = "stateless"
		}
		t.Run(name, func(t *testing.T) {
			factory := func(*http.Request) *mcp.Server {
				return mcp.NewServer(&mcp.Implementation{Name: "word-mcp-server", Version: "test"}, nil)
			}
			first := mcp.NewStreamableHTTPHandler(factory, &mcp.StreamableHTTPOptions{Stateless: stateless})
			second := mcp.NewStreamableHTTPHandler(factory, &mcp.StreamableHTTPOptions{Stateless: stateless})

			post := func(handler http.Handler, body, sessionID string) *httptest.ResponseRecorder {
				t.Helper()
				request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Accept", "application/json, text/event-stream")
				if sessionID != "" {
					request.Header.Set("Mcp-Session-Id", sessionID)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				return response
			}

			init := post(first, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`, "")
			if init.Code != http.StatusOK {
				t.Fatalf("initialize status = %d, body = %s", init.Code, init.Body.String())
			}
			sessionID := init.Header().Get("Mcp-Session-Id")
			if !stateless && sessionID == "" {
				t.Fatal("stateful initialization did not return a session ID")
			}

			list := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
			if !stateless {
				response := post(first, list, sessionID)
				if response.Code != http.StatusOK {
					t.Fatalf("same handler status = %d, body = %s", response.Code, response.Body.String())
				}
			}
			response := post(second, list, sessionID)
			if stateless {
				if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"tools"`) {
					t.Errorf("second handler status = %d, body = %s", response.Code, response.Body.String())
				}
			} else if response.Code != http.StatusNotFound {
				t.Errorf("second handler status = %d, want 404; body = %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestNormalizeTrailingSlashes(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "/", want: "/"},
		{path: "//", want: "/"},
		{path: "///", want: "/"},
		{path: "/mcp", want: "/mcp"},
		{path: "/mcp/", want: "/mcp"},
		{path: "/mcp//", want: "/mcp"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.want {
					t.Errorf("handler path = %q, want %q", r.URL.Path, tt.want)
				}
				w.WriteHeader(http.StatusNoContent)
			})

			recorder := httptest.NewRecorder()
			normalizeTrailingSlashes(mux).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "http://example.com"+tt.path, nil))

			if recorder.Code != http.StatusNoContent {
				t.Errorf("status = %d, want %d", recorder.Code, http.StatusNoContent)
			}
			if location := recorder.Header().Get("Location"); location != "" {
				t.Errorf("unexpected redirect to %q", location)
			}
		})
	}
}

func TestNormalizeTrailingSlashesPreservesEscapedSegments(t *testing.T) {
	tests := []struct {
		path        string
		wantHandler string
		wantRawPath string
	}{
		{path: "/health/", wantHandler: "health"},
		{path: "/health%2F", wantHandler: "mcp", wantRawPath: "/health%2F"},
		{path: "/health%2F/", wantHandler: "mcp", wantRawPath: "/health%2F"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			var handler string
			mux := http.NewServeMux()
			mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
				handler = "health"
				w.WriteHeader(http.StatusNoContent)
			})
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				handler = "mcp"
				w.WriteHeader(http.StatusNoContent)
			})

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "http://example.com"+tt.path, nil)
			normalizeTrailingSlashes(mux).ServeHTTP(recorder, request)

			if handler != tt.wantHandler {
				t.Errorf("handler = %q, want %q", handler, tt.wantHandler)
			}
			if request.URL.RawPath != tt.wantRawPath {
				t.Errorf("raw path = %q, want %q", request.URL.RawPath, tt.wantRawPath)
			}
			if recorder.Code != http.StatusNoContent {
				t.Errorf("status = %d, want %d", recorder.Code, http.StatusNoContent)
			}
			if location := recorder.Header().Get("Location"); location != "" {
				t.Errorf("unexpected redirect to %q", location)
			}
		})
	}
}
