package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMiddlewareCountsAttemptsErrorsAndUsers(t *testing.T) {
	t.Setenv("MCP_USAGE_HMAC_KEY", "test-hmac-key")
	t.Setenv("MCP_USAGE_SCRAPE_TOKEN", "test-token")
	telemetry := New("calendar", "Calendar", "microsoft")
	headers := http.Header{"X-Forwarded-Email": []string{"User@Example.com"}}
	next := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{IsError: true}, nil
	}
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "list_events"}}
	if _, err := telemetry.Middleware(headers)(next)(context.Background(), "tools/call", request); err != nil {
		t.Fatal(err)
	}
	if telemetry.calls["list_events"] != 1 || telemetry.errors["list_events"] != 1 {
		t.Fatalf("unexpected counters: calls=%d errors=%d", telemetry.calls["list_events"], telemetry.errors["list_events"])
	}
	if len(telemetry.users) != 1 || telemetry.unidentifiedCalls != 0 {
		t.Fatalf("unexpected identity counters: users=%d unidentified=%d", len(telemetry.users), telemetry.unidentifiedCalls)
	}
}

func TestHandlerRequiresTokenAndReturnsSnapshot(t *testing.T) {
	t.Setenv("MCP_USAGE_HMAC_KEY", "test-hmac-key")
	t.Setenv("MCP_USAGE_SCRAPE_TOKEN", "test-token")
	telemetry := New("calendar", "Calendar", "microsoft")

	unauthorized := httptest.NewRecorder()
	telemetry.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/internal/metrics/usage", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/internal/metrics/usage", nil)
	request.Header.Set("X-Obot-Metrics-Token", "test-token")
	response := httptest.NewRecorder()
	telemetry.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("snapshot status = %d", response.Code)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["schemaVersion"] != "v1" || snapshot["day"] == "" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}

func TestHandlerIsUnavailableWhenDisabled(t *testing.T) {
	t.Setenv("MCP_USAGE_HMAC_KEY", "")
	t.Setenv("MCP_USAGE_SCRAPE_TOKEN", "")
	response := httptest.NewRecorder()
	New("calendar", "Calendar", "microsoft").Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/internal/metrics/usage", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled status = %d", response.Code)
	}
}
