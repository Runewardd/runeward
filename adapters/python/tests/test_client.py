from __future__ import annotations

import json
import unittest
from unittest import mock

from runeward import (
    RunewardApprovalRequired,
    RunewardClient,
    RunewardDenied,
)


class _Response:
    def __init__(self, status: int, payload: dict[str, object]) -> None:
        self.status = status
        self._body = json.dumps(payload).encode("utf-8")

    def __enter__(self) -> "_Response":
        return self

    def __exit__(self, *_args: object) -> None:
        return None

    def read(self) -> bytes:
        return self._body


class RunewardClientTests(unittest.TestCase):
    def test_rejects_insecure_remote_http(self) -> None:
        with self.assertRaisesRegex(ValueError, "refusing insecure http"):
            RunewardClient("http://control.example.com")

    @mock.patch("runeward.client.urllib.request.urlopen")
    def test_create_sandbox_uses_existing_citadel_endpoint(self, urlopen: mock.Mock) -> None:
        urlopen.return_value = _Response(201, {"id": "cit_123", "profile": "dev"})

        created = RunewardClient().create_sandbox("dev")

        self.assertEqual(created["id"], "cit_123")
        request = urlopen.call_args.args[0]
        self.assertEqual(request.full_url, "http://localhost:8080/v1/citadels")
        self.assertEqual(json.loads(request.data), {"profile": "dev"})

    def test_governance_verdicts_remain_typed(self) -> None:
        with self.assertRaises(RunewardDenied):
            RunewardClient._raise_for_status(403, {"reason": "blocked"})
        with self.assertRaises(RunewardApprovalRequired):
            RunewardClient._raise_for_status(
                202,
                {"approval_id": "apr_123", "reason": "human review"},
            )


if __name__ == "__main__":
    unittest.main()
