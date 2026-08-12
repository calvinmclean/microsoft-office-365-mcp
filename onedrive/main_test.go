package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeRootTrailingSlashes(t *testing.T) {
	for _, path := range []string{"/", "//", "///"} {
		t.Run(path, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/" {
					t.Errorf("handler path = %q, want /", r.URL.Path)
				}
				w.WriteHeader(http.StatusNoContent)
			})

			recorder := httptest.NewRecorder()
			normalizeRootTrailingSlashes(mux).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "http://example.com"+path, nil))

			if recorder.Code != http.StatusNoContent {
				t.Errorf("status = %d, want %d", recorder.Code, http.StatusNoContent)
			}
			if location := recorder.Header().Get("Location"); location != "" {
				t.Errorf("unexpected redirect to %q", location)
			}
		})
	}
}
