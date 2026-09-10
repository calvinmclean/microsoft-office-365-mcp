import unittest

from fastmcp.exceptions import ToolError
from msgraph.generated.models.o_data_errors.o_data_error import ODataError
from msgraph.generated.models.o_data_errors.main_error import MainError

from obot_mcp_usage.errors import classify_error


class MicrosoftErrorTests(unittest.TestCase):
    def test_wrapped_graph_errors(self):
        for status, code, category in [(401, 'InvalidAuthenticationToken', 'authentication'), (403, 'ErrorAccessDenied', 'permission'), (403, 'TooManyRequests', 'rate_limit'), (404, 'ErrorItemNotFound', 'not_found')]:
            with self.subTest(status=status, code=code):
                error = ODataError(response_status_code=status, error=MainError(code=code, message='private'))
                try:
                    try:
                        raise error
                    except ODataError:
                        raise ToolError('Failed to list groups')
                except ToolError as wrapped:
                    self.assertEqual(classify_error(wrapped), category)
