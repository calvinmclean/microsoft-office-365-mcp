package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type providerError struct {
	status int
	code   *string
	text   string
}

func (e *providerError) Error() string      { return e.text }
func (e *providerError) GetStatusCode() int { return e.status }
func (e *providerError) GetCode() *string   { return e.code }

type nestedError struct{ *providerError }

func (e *nestedError) Error() string { return e.providerError.Error() }

func (e *nestedError) GetCode() *string                                { return nil }
func (e *nestedError) GetErrorEscaped() interface{ GetCode() *string } { return e.providerError }

func TestSharedClassificationCases(t *testing.T) {
	data, err := os.ReadFile("testdata/error-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string  `json:"name"`
		Status   int     `json:"status"`
		Code     *string `json:"code"`
		Message  string  `json:"message"`
		Result   bool    `json:"result"`
		Category string  `json:"category"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			var err error
			var result mcp.Result
			if tc.Result {
				result = &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: tc.Message}}}
			} else {
				err = &providerError{tc.Status, tc.Code, tc.Message}
			}
			if got := classifyError(err, result); got != tc.Category {
				t.Fatalf("got %s, want %s", got, tc.Category)
			}
		})
	}
}

func TestWrappedAndNestedProviderErrors(t *testing.T) {
	code := "rateLimitExceeded"
	err := &nestedError{&providerError{403, &code, "generic failure"}}
	if got := classifyError(fmt.Errorf("wrapped: %w", err), nil); got != "rate_limit" {
		t.Fatal(got)
	}
	if got := classifyError(fmt.Errorf("wrapped: %w", context.DeadlineExceeded), nil); got != "timeout" {
		t.Fatal(got)
	}
	if got := classifyError(errors.Join(errors.New("opaque"), &providerError{404, nil, "opaque"}), nil); got != "not_found" {
		t.Fatal(got)
	}
}

func TestRealSDKDispatchPreservesCategories(t *testing.T) {
	t.Setenv("MCP_USAGE_HMAC_KEY", "key")
	t.Setenv("MCP_USAGE_SCRAPE_TOKEN", "token")
	telemetry := New("test", "Test", "microsoft")
	server := mcp.NewServer(&mcp.Implementation{Name: "test"}, nil)
	server.AddReceivingMiddleware(telemetry.Middleware(http.Header{}))
	AddTool(server, &mcp.Tool{Name: "typed_error"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return nil, nil, &providerError{429, nil, "private provider message with no status"}
	})
	// Exercise the fallback path for SDK-flattened errors without our wrapper as well.
	mcp.AddTool(server, &mcp.Tool{Name: "text_error"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return nil, nil, errors.New("HTTP status 401")
	})
	AddTool(server, &mcp.Tool{Name: "success"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
	})
	AddTool(server, &mcp.Tool{Name: "validate"}, func(context.Context, *mcp.CallToolRequest, struct {
		Count int `json:"count"`
	}) (*mcp.CallToolResult, any, error) {
		t.Fatal("invalid arguments reached handler")
		return nil, nil, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, name := range []string{"typed_error", "text_error", "success"} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError != (name != "success") {
			t.Fatalf("tool result changed for %s", name)
		}
	}
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "validate", Arguments: map[string]any{"count": "bad"}}); err == nil {
		t.Fatal("expected validation failure")
	}
	if !reflect.DeepEqual(telemetry.errorCategories, map[string]map[string]uint64{
		"typed_error": {"rate_limit": 1}, "text_error": {"authentication": 1}, "validate": {"invalid_request": 1},
	}) {
		t.Fatalf("unexpected categories: %v", telemetry.errorCategories)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/internal/metrics/usage", nil)
	request.Header.Set("X-Obot-Metrics-Token", "token")
	telemetry.Handler().ServeHTTP(recorder, request)
	var snapshot struct {
		Calls []struct {
			Tool            string            `json:"tool"`
			Errors          uint64            `json:"errors"`
			ErrorCategories map[string]uint64 `json:"errorCategories"`
		} `json:"calls"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	for _, row := range snapshot.Calls {
		var total uint64
		for _, count := range row.ErrorCategories {
			total += count
		}
		if total != row.Errors || row.ErrorCategories == nil {
			t.Fatalf("incomplete categories: %+v", row)
		}
	}
	if strings.Contains(recorder.Body.String(), "private provider message") {
		t.Fatal("snapshot included error text")
	}
}

func TestConcurrentCategoriesAndRollover(t *testing.T) {
	t.Setenv("MCP_USAGE_HMAC_KEY", "key")
	t.Setenv("MCP_USAGE_SCRAPE_TOKEN", "token")
	telemetry := New("test", "Test", "microsoft")
	handler := telemetry.Middleware(http.Header{})(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{IsError: true}, &providerError{503, nil, "failed"}
	})
	var workers sync.WaitGroup
	for i := 0; i < 100; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, _ = handler(context.Background(), "tools/call", &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "tool"}})
		}()
	}
	workers.Wait()
	if telemetry.calls["tool"] != 100 || telemetry.errors["tool"] != 100 || telemetry.errorCategories["tool"]["upstream"] != 100 {
		t.Fatalf("counters: %+v", telemetry.errorCategories)
	}
	telemetry.day = "2000-01-01"
	telemetry.rollDay()
	if len(telemetry.errorCategories) != 0 || len(telemetry.errors) != 0 {
		t.Fatal("rollover retained errors")
	}
}
