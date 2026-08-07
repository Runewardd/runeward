from __future__ import annotations

import json
import unittest
from unittest import mock

from runeward import (
    RunewardAuthorizationError,
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
        with self.assertRaises(RunewardAuthorizationError):
            RunewardClient._raise_for_status(403, {"code": "authz_denied", "error": "wrong owner"})
        with self.assertRaises(RunewardDenied):
            RunewardClient._raise_for_status(403, {"reason": "blocked"})
        with self.assertRaises(RunewardApprovalRequired):
            RunewardClient._raise_for_status(
                202,
                {"approval_id": "apr_123", "reason": "human review"},
            )

    @mock.patch("runeward.client.urllib.request.urlopen")
    def test_run_and_signed_lease_endpoints(self, urlopen: mock.Mock) -> None:
        urlopen.side_effect = [
            _Response(200, {"runs": [{"id": "run-1"}]}),
            _Response(200, {"ok": True}),
        ]
        client = RunewardClient()

        self.assertEqual(client.list_runs()[0]["id"], "run-1")
        client.complete_task("cohort/1", "task/1", "signed-lease", "done")

        request = urlopen.call_args_list[1].args[0]
        self.assertEqual(
            request.full_url,
            "http://localhost:8080/v1/cohorts/cohort%2F1/tasks/task%2F1/complete",
        )
        self.assertEqual(
            json.loads(request.data),
            {"owner": "", "lease_token": "signed-lease", "result": "done"},
        )


if __name__ == "__main__":
    unittest.main()
