import asyncio
from unittest import TestCase
from unittest.mock import ANY, patch

from app import main


class StreamableHTTPServerTest(TestCase):
    def test_server_configures_canonical_path_and_compatibility_middleware(self):
        with (
            patch.object(main, "setup_server") as setup_server_mock,
            patch.object(main.mcp, "run") as run,
        ):
            main.streamable_http_server()

        setup_server_mock.assert_called_once_with()
        run.assert_called_once_with(
            transport="streamable-http",
            host="0.0.0.0",
            port=9000,
            path=main.MCP_PATH,
            middleware=ANY,
        )

        middleware = run.call_args.kwargs["middleware"]
        self.assertEqual(len(middleware), 1)
        self.assertIs(middleware[0].cls, main.LegacyTrailingSlashMiddleware)
        self.assertEqual(middleware[0].kwargs, {"path": main.MCP_PATH})

    def test_legacy_trailing_slash_is_rewritten_without_redirect(self):
        seen_paths = []

        async def app(scope, receive, send):
            seen_paths.append((scope["path"], scope["raw_path"]))

        async def exercise_middleware():
            middleware = main.LegacyTrailingSlashMiddleware(app, path=main.MCP_PATH)
            for path in (main.MCP_PATH, f"{main.MCP_PATH}/"):
                await middleware(
                    {"type": "http", "path": path, "raw_path": path.encode()},
                    None,
                    None,
                )

        asyncio.run(exercise_middleware())

        expected = (main.MCP_PATH, main.MCP_PATH.encode())
        self.assertEqual(seen_paths, [expected, expected])
