"""Bounded, best-effort classification. Only the resulting category is exported.

Keep the wire categories and recognition rules in sync with Microsoft's telemetry.
"""

from __future__ import annotations

import re
from collections.abc import Mapping
from typing import Any

CATEGORY_CODES = {
    "rate_limit": {"ratelimitexceeded", "userratelimitexceeded", "toomanyrequests", "throttledrequest", "activitylimitreached", "resource_exhausted"},
    "authentication": {"invalidauthenticationtoken", "invalidcredentials", "autherror", "unauthenticated", "invalid_grant", "unauthorized"},
    "permission": {"accessdenied", "erroraccessdenied", "authorization_requestdenied", "insufficientpermissions", "permission_denied", "forbidden"},
    "not_found": {"notfound", "itemnotfound", "erroritemnotfound", "resourcenotfound", "not_found"},
    "invalid_request": {"badrequest", "invalidrequest", "invalidargument", "invalid_argument", "invalidparams", "-32700", "-32600", "-32601", "-32602"},
    "timeout": {"timeout", "requesttimeout", "errortimeout", "deadline_exceeded"},
    "upstream": {"internalservererror", "serviceunavailable", "backenderror", "unavailable", "-32603"},
}
AMBIGUOUS_CODES = {"timeout", "unauthorized", "forbidden", "unavailable", "notfound", "badrequest"}
CODE_PATTERNS = {}
for category, codes in CATEGORY_CODES.items():
    specific = "|".join(re.escape(code) for code in sorted(codes - AMBIGUOUS_CODES))
    contextual = "|".join(re.escape(code) for code in sorted(codes & AMBIGUOUS_CODES))
    patterns = [r"(?<![\w])(?:" + specific + r")(?![\w])"] if specific else []
    if contextual:
        patterns.append(r"\b(?:code|reason)[\s\"':=]+(?:" + contextual + r")(?![\w])")
    CODE_PATTERNS[category] = re.compile("|".join(patterns), re.I)
STATUS_PATTERN = re.compile(
    r"\b(?:http(?:error)?(?:\s+status)?|status(?:_code|\s+code)?|response_status_code|code)"
    r"[\s\"':=]+([45]\d{2})\b", re.I
)
PHRASES = {
    "rate_limit": r"\b(?:rate limit exceeded|too many requests|request was throttled)\b",
    "authentication": r"\b(?:no access token (?:found|provided)|missing (?:access|authentication|bearer) token|invalid authentication token|(?:access )?token (?:has )?expired)\b",
    "permission": r"\b(?:permission denied|access (?:is )?denied|insufficient (?:permissions|scopes|privileges))\b",
    "not_found": r"\b(?:resource|file|item) not found\b",
    "invalid_request": r"\b(?:invalid (?:tool )?arguments|validation errors? for|unmarshaling arguments|validating tool input|unknown tool)\b",
    "timeout": r"\b(?:request timed out|deadline exceeded)\b",
    "upstream": r"\b(?:connection refused|connection reset|service unavailable|bad gateway)\b",
}
PHRASE_PATTERNS = {category: re.compile(pattern, re.I) for category, pattern in PHRASES.items()}


def _status_category(status: Any) -> str | None:
    if isinstance(status, bool) or not isinstance(status, int):
        return None
    if status == 401:
        return "authentication"
    if status == 403:
        return "permission"
    if status == 429:
        return "rate_limit"
    if status == 404:
        return "not_found"
    if status in (408, 504):
        return "timeout"
    if 400 <= status < 500:
        return "invalid_request"
    if 500 <= status < 600:
        return "upstream"
    return None


def _field(value: Any, name: str) -> Any:
    return value.get(name) if isinstance(value, Mapping) else getattr(value, name, None)


def _classify(error: BaseException | None, result: Any) -> str:
    codes: set[str] = set()
    statuses: list[int] = []
    types: set[str] = set()
    texts: list[str] = []
    current = error
    seen: set[int] = set()
    for _ in range(8):
        if current is None or id(current) in seen:
            break
        seen.add(id(current))
        texts.append(str(current)[:4096])
        types.update(cls.__name__ for cls in type(current).__mro__)
        # Provider SDKs expose status and code information on different wrappers.
        pending = [current]
        visited: set[int] = set()
        for _ in range(24):
            if not pending:
                break
            item = pending.pop(0)
            if item is None or id(item) in visited:
                continue
            visited.add(id(item))
            if isinstance(item, (list, tuple)):
                pending.extend(item[:8])
                continue
            for name in ("status_code", "response_status_code", "status"):
                value = _field(item, name)
                if _status_category(value):
                    statuses.append(value)
            for name in ("code", "reason"):
                value = _field(item, name)
                if isinstance(value, (str, int)):
                    codes.add(str(value).lower())
            for name in ("error", "errors", "error_details", "details", "resp", "response"):
                child = _field(item, name)
                if child is not None and not isinstance(child, (str, bytes)):
                    pending.append(child)
        current = current.__cause__ or current.__context__

    if result is not None:
        for content in (_field(result, "content") or [])[:8]:
            value = _field(content, "text")
            if isinstance(value, str):
                texts.append(value[:4096])
    text = "\n".join(texts)[:32768]
    text_categories = [category for category, pattern in CODE_PATTERNS.items() if pattern.search(text)]
    for category, known_codes in CATEGORY_CODES.items():
        if codes & known_codes:
            return category
    if statuses:
        if statuses[0] == 403 and "rate_limit" in text_categories:
            return "rate_limit"
        return _status_category(statuses[0]) or "other"
    if types & {"TimeoutError", "TimeoutException", "ReadTimeout", "ConnectTimeout", "WriteTimeout", "PoolTimeout"}:
        return "timeout"
    if types & {"ValidationError", "RequestValidationError", "ValidationException"}:
        return "invalid_request"
    if types & {"ConnectionError", "ConnectError", "NetworkError", "ConnectionResetError", "ConnectionRefusedError"}:
        return "upstream"
    # Narrow fallback for SDKs that replace typed exceptions with tool-error text.
    if text_categories:
        return text_categories[0]
    match = STATUS_PATTERN.search(text)
    if match:
        return _status_category(int(match[1])) or "other"
    for category, pattern in PHRASE_PATTERNS.items():
        if pattern.search(text):
            return category
    return "other"


def classify_error(error: BaseException | None = None, result: Any = None) -> str:
    try:
        return _classify(error, result)
    except Exception:
        # Error inspection must never replace a tool's original result/exception.
        return "other"
