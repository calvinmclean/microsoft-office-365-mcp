package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
