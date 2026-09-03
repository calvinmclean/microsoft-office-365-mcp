package usage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxDailyUsers = 25_000

type toolCount struct {
	Calls  uint64 `json:"calls"`
	Errors uint64 `json:"errors"`
}

type Telemetry struct {
	mu                sync.Mutex
	serverID          string
	displayName       string
	provider          string
	hmacKey           []byte
	scrapeToken       string
	enabled           bool
	instanceID        string
	startedAt         string
	day               string
	calls             map[string]uint64
	errors            map[string]uint64
	users             map[string]struct{}
	unidentifiedCalls uint64
}

func New(serverID, displayName, provider string) *Telemetry {
	key := []byte(os.Getenv("MCP_USAGE_HMAC_KEY"))
	token := os.Getenv("MCP_USAGE_SCRAPE_TOKEN")
	return &Telemetry{
		serverID: serverID, displayName: displayName, provider: provider,
		hmacKey: key, scrapeToken: token, enabled: len(key) > 0 && token != "",
		instanceID: uuid.New().String(), startedAt: time.Now().UTC().Format(time.RFC3339Nano),
		day: utcDay(), calls: map[string]uint64{}, errors: map[string]uint64{}, users: map[string]struct{}{},
	}
}

func utcDay() string { return time.Now().UTC().Format("2006-01-02") }

func (t *Telemetry) rollDay() string {
	day := utcDay()
	if day != t.day {
		t.day = day
		t.calls = map[string]uint64{}
		t.errors = map[string]uint64{}
		t.users = map[string]struct{}{}
		t.unidentifiedCalls = 0
	}
	return day
}

func (t *Telemetry) identity(headers http.Header) string {
	if email := strings.ToLower(strings.TrimSpace(headers.Get("X-Forwarded-Email"))); email != "" {
		return email
	}
	if user := strings.ToLower(strings.TrimSpace(headers.Get("X-Forwarded-User"))); user != "" {
		return t.provider + ":" + user
	}
	return ""
}

func (t *Telemetry) userHash(day, identity string) string {
	digest := hmac.New(sha256.New, t.hmacKey)
	digest.Write([]byte(day + "\n" + identity))
	return hex.EncodeToString(digest.Sum(nil))
}

func (t *Telemetry) Middleware(headers http.Header) mcp.Middleware {
	identity := t.identity(headers)
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			call, ok := req.(*mcp.CallToolRequest)
			if !t.enabled || !ok {
				return next(ctx, method, req)
			}
			tool := call.Params.Name
			t.mu.Lock()
			day := t.rollDay()
			t.calls[tool]++
			if identity == "" {
				t.unidentifiedCalls++
			} else {
				hash := t.userHash(day, identity)
				if len(t.users) < maxDailyUsers {
					t.users[hash] = struct{}{}
				} else if _, exists := t.users[hash]; exists {
					t.users[hash] = struct{}{}
				}
			}
			t.mu.Unlock()

			result, err := next(ctx, method, req)
			failed := err != nil
			if toolResult, ok := result.(*mcp.CallToolResult); ok && toolResult.IsError {
				failed = true
			}
			if failed {
				t.mu.Lock()
				if t.rollDay() == day {
					t.errors[tool]++
				}
				t.mu.Unlock()
			}
			return result, err
		}
	}
}

func (t *Telemetry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !t.enabled {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "usage_metrics_disabled"})
			return
		}
		if !hmac.Equal([]byte(r.Header.Get("X-Obot-Metrics-Token")), []byte(t.scrapeToken)) {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}

		t.mu.Lock()
		defer t.mu.Unlock()
		t.rollDay()
		tools := make([]string, 0, len(t.calls))
		for tool := range t.calls {
			tools = append(tools, tool)
		}
		sort.Strings(tools)
		calls := make([]map[string]any, 0, len(tools))
		for _, tool := range tools {
			calls = append(calls, map[string]any{"tool": tool, "calls": t.calls[tool], "errors": t.errors[tool]})
		}
		users := make([]string, 0, len(t.users))
		for hash := range t.users {
			users = append(users, hash)
		}
		sort.Strings(users)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schemaVersion": "v1",
			"server":        map[string]string{"id": t.serverID, "displayName": t.displayName, "provider": t.provider},
			"instance":      map[string]string{"id": t.instanceID, "startedAt": t.startedAt},
			"day":           t.day, "calls": calls, "userHashes": users, "unidentifiedCalls": t.unidentifiedCalls,
		})
	})
}
