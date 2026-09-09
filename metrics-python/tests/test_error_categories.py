import asyncio
import json
import os
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from fastmcp import Client, FastMCP
from starlette.requests import Request

from obot_mcp_usage import UsageTelemetry
from obot_mcp_usage.errors import classify_error


class ProviderError(Exception):
    def __init__(self, status=0, code=None, message="provider rejected the request"):
        super().__init__(message)
        self.response_status_code = status
        self.code = code


class ErrorCategoryTests(unittest.IsolatedAsyncioTestCase):
    def usage(self):
        with patch.dict(os.environ, {"MCP_USAGE_HMAC_KEY": "key", "MCP_USAGE_SCRAPE_TOKEN": "token"}):
            return UsageTelemetry("test", "Test", "google")

    async def scrape(self, usage):
        response = await usage.handle_request(Request({"type": "http", "headers": [(b"x-obot-metrics-token", b"token")]}))
        return json.loads(response.body)

    def test_shared_classification_cases(self):
        for case in json.loads(Path(__file__).with_name("error-cases.json").read_text()):
            with self.subTest(case=case["name"]):
                if case.get("result"):
                    category = classify_error(result=SimpleNamespace(content=[SimpleNamespace(text=case.get("message", ""))]))
                else:
                    error = ProviderError(case.get("status", 0), case.get("code"), case.get("message", "provider rejected the request"))
                    category = classify_error(error)
                self.assertEqual(category, case["category"])

    def test_wrapped_provider_errors_and_google_reasons(self):
        root = ProviderError(403)
        root.error_details = [{"reason": "userRateLimitExceeded"}]
        wrapped = RuntimeError("tool failed")
        wrapped.__cause__ = root
        self.assertEqual(classify_error(wrapped), "rate_limit")
        root.error_details = []
        self.assertEqual(classify_error(wrapped), "permission")
        wrapped.__cause__ = None
        wrapped.__context__ = ProviderError(404)
        self.assertEqual(classify_error(wrapped), "not_found")

    def test_exception_types_and_inspection_failures(self):
        self.assertEqual(classify_error(TimeoutError()), "timeout")
        self.assertEqual(classify_error(ConnectionError()), "upstream")
        self.assertEqual(classify_error(ValueError("internal conversion failed")), "other")
        from pydantic import BaseModel, ValidationError

        class Args(BaseModel):
            count: int

        try:
            Args(count="no")
        except ValidationError as error:
            self.assertEqual(classify_error(error), "invalid_request")

        class BrokenError(Exception):
            @property
            def status_code(self):
                raise RuntimeError("broken property")

        self.assertEqual(classify_error(BrokenError()), "other")
        cycle = RuntimeError("cycle")
        cycle.__cause__ = cycle
        self.assertEqual(classify_error(cycle), "other")

    async def test_counters_are_complete_under_concurrency_and_rollover(self):
        usage = self.usage()
        async def next_call(context):
            await asyncio.sleep(0)
            if context.index % 3 == 0:
                raise ProviderError(429, message="private provider message")
            return SimpleNamespace(isError=context.index % 3 == 1, content=[])

        with patch("obot_mcp_usage.get_http_headers", return_value={}):
            results = await asyncio.gather(*(usage.on_call_tool(SimpleNamespace(message=SimpleNamespace(name="tool"), index=i), next_call) for i in range(90)), return_exceptions=True)
        self.assertEqual(sum(isinstance(value, ProviderError) for value in results), 30)
        payload = await self.scrape(usage)
        self.assertEqual(payload["calls"], [{"tool": "tool", "calls": 90, "errors": 60, "errorCategories": {"rate_limit": 30, "other": 30}}])
        self.assertNotIn("private provider message", json.dumps(payload))
        with patch.object(usage, "_utc_day", return_value="2099-01-01"):
            self.assertEqual((await self.scrape(usage))["calls"], [])
            self.assertFalse(usage._error_categories)

    async def test_cancellation_preserves_existing_error_accounting(self):
        usage = self.usage()
        async def next_call(context):
            raise asyncio.CancelledError()
        with patch("obot_mcp_usage.get_http_headers", return_value={}):
            with self.assertRaises(asyncio.CancelledError):
                await usage.on_call_tool(SimpleNamespace(message=SimpleNamespace(name="cancelled")), next_call)
        self.assertEqual((await self.scrape(usage))["calls"][0]["errorCategories"], {"other": 1})

    async def test_cross_midnight_completion_does_not_create_orphan_categories(self):
        usage = self.usage()
        start_day = usage._day
        async def next_call(context):
            usage._day = "2099-01-01"
            return SimpleNamespace(is_error=True)
        with patch.object(usage, "_utc_day", side_effect=[start_day, "2099-01-01"]), patch("obot_mcp_usage.get_http_headers", return_value={}):
            await usage.on_call_tool(SimpleNamespace(message=SimpleNamespace(name="tool")), next_call)
        self.assertFalse(usage._error_categories)

    async def test_real_fastmcp_dispatch_preserves_provider_cause(self):
        usage = self.usage()
        server = FastMCP("category-test", middleware=[usage])

        @server.tool
        async def fails() -> str:
            try:
                raise ProviderError(429)
            except ProviderError as error:
                raise RuntimeError("wrapped failure") from error

        @server.tool
        async def succeeds() -> str:
            return "ok"

        @server.tool
        async def validated(count: int) -> str:
            return str(count)

        with patch("obot_mcp_usage.get_http_headers", return_value={}):
            async with Client(server) as client:
                await client.call_tool("fails", raise_on_error=False)
                await client.call_tool("succeeds")
                await client.call_tool("validated", {"count": "bad"}, raise_on_error=False)
        counters = {item["tool"]: item for item in (await self.scrape(usage))["calls"]}
        self.assertEqual(counters["fails"]["errorCategories"], {"rate_limit": 1})
        self.assertEqual(counters["fails"]["errors"], 1)
        self.assertEqual(counters["succeeds"]["errorCategories"], {})
        self.assertEqual(counters["validated"]["errorCategories"], {"invalid_request": 1})


if __name__ == "__main__":
    unittest.main()
