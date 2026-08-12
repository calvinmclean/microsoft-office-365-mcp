import asyncio
from unittest import TestCase
from unittest.mock import ANY, patch

from httpx import ASGITransport, AsyncClient

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

    @patch("app.main.mcp.run")
    def test_mcp_endpoint_accepts_trailing_slash_variants_without_redirect(
        self, mock_run
    ):
        with patch.object(main, "setup_server"):
            main.streamable_http_server()
        config = mock_run.call_args.kwargs
        app = main.mcp.http_app(
            transport=config["transport"],
            path=config["path"],
            middleware=config["middleware"],
        )

        async def exercise_endpoint():
            async with app.router.lifespan_context(app):
                transport = ASGITransport(app=app)
                async with AsyncClient(
                    transport=transport,
                    base_url="http://testserver",
                    follow_redirects=False,
                ) as client:
                    return [
                        await client.post(
                            path,
                            content=b"{}",
                            headers={"content-type": "application/json"},
                        )
                        for path in (
                            main.MCP_PATH,
                            f"{main.MCP_PATH}/",
                            f"{main.MCP_PATH}///",
                        )
                    ]

        responses = asyncio.run(exercise_endpoint())

        self.assertEqual(len({response.status_code for response in responses}), 1)
        for response in responses:
            self.assertNotEqual(response.status_code, 404)
            self.assertFalse(300 <= response.status_code < 400)
            self.assertNotIn("location", response.headers)
