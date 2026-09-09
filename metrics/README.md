# Hosted MCP usage telemetry

The usage endpoint reports cumulative tool attempts, errors, and `errorCategories` for each process and UTC day. Every counted error belongs to one category:

| Key | Meaning |
| --- | --- |
| `authentication` | HTTP 401 or recognized credential failures. |
| `permission` | HTTP 403 or explicit access/scope denial. |
| `rate_limit` | HTTP 429 or provider throttling codes, including throttling returned as 403. |
| `invalid_request` | Other HTTP 4xx responses, invalid MCP requests/arguments, or input validation failures. |
| `not_found` | HTTP 404 or explicit missing-resource codes. |
| `timeout` | HTTP 408/504 or deadline/timeout failures. |
| `upstream` | Other HTTP 5xx responses or network connection failures. |
| `other` | Unrecognized failures. |

Specific categories take precedence over generic HTTP groups. Classification inspects structured status/provider codes and wrapped causes first, then a bounded allowlist of error-text formats. Inspection failures fall back to `other`. Error messages, tool inputs, and results are never included in telemetry.

Category counts sum to `errors`; successful tools emit `{}`. Each process restart gets a new instance ID and daily rollover clears all counters. Existing attempt, error, cancellation, and user accounting semantics are preserved.

This is an additive `schemaVersion: "v1"` field. Roll out the Managed Stats server/schema, then its collector, then these producer images. Older reports appear under Other / unknown; historical categories cannot be reconstructed.

The Python and Go classifiers use matching rules and copies of `error-cases.json`. Keep these rules and fixtures in sync across the Google and Microsoft repositories.

Register typed Go tools with `usage.AddTool` (the same signature as `mcp.AddTool`). It captures the category in the telemetry middleware's per-call context before the MCP SDK replaces typed provider errors with tool-error text. The wrapper preserves tool results and errors and does not collect telemetry when the middleware is disabled.

Run the Go telemetry tests, including actual SDK dispatch and race detection:

```sh
go -C metrics test -race ./...
```

Run Python telemetry tests with Outlook's locked dependencies:

```sh
cd outlook
PYTHONPATH=../metrics-python uv run --frozen --no-dev python -m unittest discover -s ../metrics-python/tests
```
