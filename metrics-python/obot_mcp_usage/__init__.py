from __future__ import annotations

import asyncio
import hashlib
import hmac
import os
import uuid
from collections import defaultdict
from collections.abc import Awaitable, Callable
from datetime import UTC, datetime
from typing import Any

from fastmcp.server.dependencies import get_http_headers
from fastmcp.server.middleware import Middleware, MiddlewareContext
from starlette.requests import Request
from starlette.responses import JSONResponse

from .errors import classify_error

MAX_DAILY_USERS = 25_000


class UsageTelemetry(Middleware):
    def __init__(self, server_id: str, display_name: str, provider: str) -> None:
        self.server_id, self.display_name, self.provider = server_id, display_name, provider
        self._hmac_key = os.getenv("MCP_USAGE_HMAC_KEY", "").encode()
        self._scrape_token = os.getenv("MCP_USAGE_SCRAPE_TOKEN", "")
        self._enabled = bool(self._hmac_key and self._scrape_token)
        self._instance_id = str(uuid.uuid4())
        self._started_at = datetime.now(UTC).isoformat().replace("+00:00", "Z")
        self._day = self._utc_day()
        self._calls: dict[str, int] = defaultdict(int)
        self._errors: dict[str, int] = defaultdict(int)
        self._error_categories: dict[str, dict[str, int]] = defaultdict(lambda: defaultdict(int))
        self._users: set[str] = set()
        self._unidentified_calls = 0
        self._lock = asyncio.Lock()

    @staticmethod
    def _utc_day() -> str:
        return datetime.now(UTC).date().isoformat()

    def _roll_day(self) -> str:
        day = self._utc_day()
        if day != self._day:
            self._day, self._calls, self._errors, self._users, self._unidentified_calls = day, defaultdict(int), defaultdict(int), set(), 0
            self._error_categories.clear()
        return day

    def _identity_hash(self, day: str) -> str | None:
        headers = get_http_headers()
        email = headers.get("x-forwarded-email", "").strip().lower()
        user = headers.get("x-forwarded-user", "").strip().lower()
        identity = email if email else f"{self.provider}:{user}" if user else ""
        return hmac.new(self._hmac_key, f"{day}\n{identity}".encode(), hashlib.sha256).hexdigest() if identity else None

    async def on_call_tool(self, context: MiddlewareContext[Any], call_next: Callable[[MiddlewareContext[Any]], Awaitable[Any]]) -> Any:
        if not self._enabled:
            return await call_next(context)
        tool = str(getattr(context.message, "name", "unknown"))
        async with self._lock:
            day = self._roll_day()
            self._calls[tool] += 1
            identity_hash = self._identity_hash(day)
            if identity_hash and (len(self._users) < MAX_DAILY_USERS or identity_hash in self._users):
                self._users.add(identity_hash)
            elif not identity_hash:
                self._unidentified_calls += 1
        try:
            result = await call_next(context)
        except BaseException as error:
            category = classify_error(error=error)
            async with self._lock:
                if self._roll_day() == day:
                    self._errors[tool] += 1
                    self._error_categories[tool][category] += 1
            raise
        if bool(getattr(result, "isError", False)) or bool(getattr(result, "is_error", False)):
            category = classify_error(result=result)
            async with self._lock:
                if self._roll_day() == day:
                    self._errors[tool] += 1
                    self._error_categories[tool][category] += 1
        return result

    async def handle_request(self, request: Request) -> JSONResponse:
        if not self._enabled:
            return JSONResponse({"error": "usage_metrics_disabled"}, status_code=503)
        if not hmac.compare_digest(request.headers.get("x-obot-metrics-token", ""), self._scrape_token):
            return JSONResponse({"error": "unauthorized"}, status_code=401)
        async with self._lock:
            self._roll_day()
            return JSONResponse({
                "schemaVersion": "v1",
                "server": {"id": self.server_id, "displayName": self.display_name, "provider": self.provider},
                "instance": {"id": self._instance_id, "startedAt": self._started_at}, "day": self._day,
                "calls": [{"tool": tool, "calls": calls, "errors": self._errors.get(tool, 0), "errorCategories": dict(self._error_categories.get(tool, {}))} for tool, calls in sorted(self._calls.items())],
                "userHashes": sorted(self._users), "unidentifiedCalls": self._unidentified_calls,
            })
