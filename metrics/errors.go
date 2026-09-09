package usage

import (
	"context"
	"errors"
	"net"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Keep these wire categories and recognition rules in sync with Python telemetry.
var categoryCodes = []struct {
	category string
	codes    []string
}{
	{"rate_limit", []string{"ratelimitexceeded", "userratelimitexceeded", "toomanyrequests", "throttledrequest", "activitylimitreached", "resource_exhausted"}},
	{"authentication", []string{"invalidauthenticationtoken", "invalidcredentials", "autherror", "unauthenticated", "invalid_grant", "unauthorized"}},
	{"permission", []string{"accessdenied", "erroraccessdenied", "authorization_requestdenied", "insufficientpermissions", "permission_denied", "forbidden"}},
	{"not_found", []string{"notfound", "itemnotfound", "erroritemnotfound", "resourcenotfound", "not_found"}},
	{"invalid_request", []string{"badrequest", "invalidrequest", "invalidargument", "invalid_argument", "invalidparams", "-32700", "-32600", "-32601", "-32602"}},
	{"timeout", []string{"timeout", "requesttimeout", "errortimeout", "deadline_exceeded"}},
	{"upstream", []string{"internalservererror", "serviceunavailable", "backenderror", "unavailable", "-32603"}},
}

type categoryPattern struct {
	category string
	pattern  *regexp.Regexp
}

var codePatterns = func() []categoryPattern {
	ambiguous := map[string]bool{"timeout": true, "unauthorized": true, "forbidden": true, "unavailable": true, "notfound": true, "badrequest": true}
	var result []categoryPattern
	for _, rule := range categoryCodes {
		var specific, contextual, patterns []string
		for _, code := range rule.codes {
			if ambiguous[code] {
				contextual = append(contextual, code)
			} else {
				specific = append(specific, code)
			}
		}
		if len(specific) > 0 {
			patterns = append(patterns, `(?:^|[^\w])(?:`+strings.Join(specific, "|")+`)(?:$|[^\w])`)
		}
		if len(contextual) > 0 {
			patterns = append(patterns, `\b(?:code|reason)[\s"':=]+(?:`+strings.Join(contextual, "|")+`)(?:$|[^\w])`)
		}
		result = append(result, categoryPattern{rule.category, regexp.MustCompile(`(?i)(?:` + strings.Join(patterns, "|") + `)`)})
	}
	return result
}()

var statusPattern = regexp.MustCompile(`(?i)\b(?:http(?:error)?(?:\s+status)?|status(?:_code|\s+code)?|response_status_code|code)[\s"':=]+([45]\d{2})\b`)
var phrasePatterns = []categoryPattern{
	{"rate_limit", regexp.MustCompile(`(?i)\b(?:rate limit exceeded|too many requests|request was throttled)\b`)},
	{"authentication", regexp.MustCompile(`(?i)\b(?:no access token (?:found|provided)|missing (?:access|authentication|bearer) token|invalid authentication token|(?:access )?token (?:has )?expired)\b`)},
	{"permission", regexp.MustCompile(`(?i)\b(?:permission denied|access (?:is )?denied|insufficient (?:permissions|scopes|privileges))\b`)},
	{"not_found", regexp.MustCompile(`(?i)\b(?:resource|file|item) not found\b`)},
	{"invalid_request", regexp.MustCompile(`(?i)\b(?:invalid (?:tool )?arguments|validation errors? for|unmarshaling arguments|validating tool input|unknown tool)\b`)},
	{"timeout", regexp.MustCompile(`(?i)\b(?:request timed out|deadline exceeded)\b`)},
	{"upstream", regexp.MustCompile(`(?i)\b(?:connection refused|connection reset|service unavailable|bad gateway)\b`)},
}

func statusCategory(status int) string {
	switch status {
	case 401:
		return "authentication"
	case 403:
		return "permission"
	case 429:
		return "rate_limit"
	case 404:
		return "not_found"
	case 408, 504:
		return "timeout"
	}
	if status >= 400 && status < 500 {
		return "invalid_request"
	}
	if status >= 500 && status < 600 {
		return "upstream"
	}
	return ""
}

func codeCategory(code string) string {
	for _, rule := range categoryCodes {
		for _, known := range rule.codes {
			if strings.EqualFold(code, known) {
				return rule.category
			}
		}
	}
	return ""
}

// Graph's generated errors return a provider-specific interface from GetErrorEscaped.
// Reflection avoids coupling this small telemetry module to the full Graph SDK.
func nestedProviderCode(err error) (code string) {
	defer func() {
		if recover() != nil {
			code = ""
		}
	}()
	nested := reflect.ValueOf(err).MethodByName("GetErrorEscaped")
	if !nested.IsValid() || nested.Type().NumIn() != 0 || nested.Type().NumOut() != 1 {
		return ""
	}
	value := nested.Call(nil)[0]
	if value.Kind() == reflect.Interface {
		value = value.Elem()
	}
	method := value.MethodByName("GetCode")
	if !method.IsValid() || method.Type().NumIn() != 0 || method.Type().NumOut() != 1 {
		return ""
	}
	result := method.Call(nil)[0]
	if result.Kind() == reflect.Pointer && !result.IsNil() && result.Elem().Kind() == reflect.String {
		return result.Elem().String()
	}
	return ""
}

// classifyError inspects failures locally. No error text is kept in counters.
func classifyError(err error, result mcp.Result) (category string) {
	category = "other"
	defer func() {
		if recover() != nil {
			category = "other"
		}
	}()
	var text strings.Builder
	appendText := func(value string) {
		remaining := 32768 - text.Len()
		if remaining <= 0 {
			return
		}
		if len(value) > remaining {
			value = value[:remaining]
		}
		text.WriteString(value)
		text.WriteByte('\n')
	}
	status := 0
	typedCategory := ""
	// Bound wrapping inspection, including errors.Join branches.
	pending := []error{err}
	for visited := 0; len(pending) > 0 && visited < 24; visited++ {
		current := pending[0]
		pending = pending[1:]
		if current == nil {
			continue
		}
		appendText(current.Error())
		if coded, ok := current.(interface{ GetCode() *string }); ok {
			if code := coded.GetCode(); code != nil {
				if value := codeCategory(*code); value != "" {
					return value
				}
			}
		}
		if value := nestedProviderCode(current); value != "" {
			if category := codeCategory(value); category != "" {
				return category
			}
		}
		if wire, ok := current.(*jsonrpc.Error); ok {
			if value := codeCategory(strconv.FormatInt(wire.Code, 10)); value != "" {
				return value
			}
		}
		if value, ok := current.(interface{ GetStatusCode() int }); ok && status == 0 {
			if candidate := value.GetStatusCode(); statusCategory(candidate) != "" {
				status = candidate
			}
		}
		if current == context.DeadlineExceeded {
			typedCategory = "timeout"
		}
		if network, ok := current.(net.Error); ok {
			if network.Timeout() {
				typedCategory = "timeout"
			} else if typedCategory == "" {
				typedCategory = "upstream"
			}
		}
		if joined, ok := current.(interface{ Unwrap() []error }); ok {
			children := joined.Unwrap()
			if len(children) > 8 {
				children = children[:8]
			}
			pending = append(pending, children...)
		} else if wrapped := errors.Unwrap(current); wrapped != nil {
			pending = append(pending, wrapped)
		}
	}
	if toolResult, ok := result.(*mcp.CallToolResult); ok && toolResult != nil {
		for i, content := range toolResult.Content {
			if i >= 8 {
				break
			}
			if value, ok := content.(*mcp.TextContent); ok && value != nil {
				appendText(value.Text)
			}
		}
	}
	message := text.String()
	textCategory := ""
	for _, rule := range codePatterns {
		if rule.pattern.MatchString(message) {
			textCategory = rule.category
			break
		}
	}
	if value := statusCategory(status); value != "" {
		if status == 403 && textCategory == "rate_limit" {
			return "rate_limit"
		}
		return value
	}
	if typedCategory != "" {
		return typedCategory
	}
	if textCategory != "" {
		return textCategory
	}
	if match := statusPattern.FindStringSubmatch(message); match != nil {
		status, _ := strconv.Atoi(match[1])
		return statusCategory(status)
	}
	for _, rule := range phrasePatterns {
		if rule.pattern.MatchString(message) {
			return rule.category
		}
	}
	return "other"
}

type errorCategoryContextKey struct{}
type callErrorCategory struct{ category string }

// AddTool preserves the category before the MCP SDK replaces typed errors with text.
// It has the same signature and tool behavior as mcp.AddTool.
func AddTool[In, Out any](server *mcp.Server, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, args In) (*mcp.CallToolResult, Out, error) {
		result, out, err := handler(ctx, req, args)
		if state, ok := ctx.Value(errorCategoryContextKey{}).(*callErrorCategory); ok && (err != nil || (result != nil && result.IsError)) {
			state.category = classifyError(err, result)
		}
		return result, out, err
	})
}
