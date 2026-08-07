"""A dependency-light Python client for the runeward control plane.

Uses only the Python standard library (``urllib``) so it can be dropped into any
environment without pulling in ``requests`` or an async HTTP stack. The client
mirrors the runeward REST contract 1:1 and translates the two governance
outcomes into exceptions:

* HTTP ``403`` -> :class:`RunewardDenied`
* HTTP ``202`` -> :class:`RunewardApprovalRequired` (carries the ``approval_id``)

Everything else that isn't a 2xx becomes a :class:`RunewardError`.
"""

from __future__ import annotations

import json
import os
import urllib.error
import urllib.parse
import urllib.request
import warnings
from typing import Any, Dict, List, Optional

__all__ = [
    "RunewardClient",
    "RunewardError",
    "RunewardDenied",
    "RunewardAuthorizationError",
    "RunewardApprovalRequired",
]


class RunewardError(Exception):
    """Base error for any non-success response from the control plane."""

    def __init__(self, message: str, *, status: Optional[int] = None,
                 payload: Optional[Dict[str, Any]] = None) -> None:
        super().__init__(message)
        self.status = status
        self.payload = payload or {}


class RunewardDenied(RunewardError):
    """Raised when policy denies an action (HTTP 403).

    ``reason`` explains *why* it was blocked. A denial is a policy decision, not
    a transient failure: do not retry the identical action.
    """

    def __init__(self, reason: str, *, payload: Optional[Dict[str, Any]] = None) -> None:
        super().__init__(f"runeward denied action: {reason}", status=403, payload=payload)
        self.reason = reason


class RunewardAuthorizationError(RunewardError):
    """Raised when an authenticated caller lacks role or resource access."""

    def __init__(self, message: str, *, payload: Optional[Dict[str, Any]] = None) -> None:
        super().__init__(f"runeward authorization denied: {message}", status=403, payload=payload)


class RunewardApprovalRequired(RunewardError):
    """Raised when an action needs human approval (HTTP 202).

    ``approval_id`` identifies the pending request in the approvals inbox. The
    caller should pause and surface this to a human rather than working around
    the gate.
    """

    def __init__(self, approval_id: str, *, reason: str = "",
                 payload: Optional[Dict[str, Any]] = None) -> None:
        super().__init__(
            f"runeward requires approval (id={approval_id}): {reason}".rstrip(": "),
            status=202,
            payload=payload,
        )
        self.approval_id = approval_id
        self.reason = reason


class RunewardClient:
    """Thin, synchronous client over the runeward REST control plane.

    Example
    -------
    >>> rw = RunewardClient("http://localhost:8080")
    >>> sbx = rw.create_sandbox("dev")
    >>> rw.shell(sbx["id"], ["echo", "hello"])["stdout"]
    'hello\\n'
    >>> rw.kill_sandbox(sbx["id"])
    """

    def __init__(self, base_url: str = "http://localhost:8080", *,
                 timeout: float = 60.0, token: Optional[str] = None,
                 allow_insecure: bool = False) -> None:
        # Normalize so we can safely join paths without doubling slashes.
        self.base_url = self._normalize_base_url(base_url)
        self._validate_transport(self.base_url, allow_insecure)
        self.timeout = timeout
        self.token = token or os.environ.get("RUNEWARD_API_TOKEN")

    # -- low-level request plumbing ---------------------------------------

    def _request(self, method: str, path: str,
                 body: Optional[Dict[str, Any]] = None) -> Any:
        """Perform an HTTP request and decode the JSON response.

        Maps governance status codes to typed exceptions before returning.
        """
        url = f"{self.base_url}{path}"
        data = None
        headers = {"Accept": "application/json"}
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        if self.token:
            headers["Authorization"] = f"Bearer {self.token}"

        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                return self._decode(resp.status, resp.read())
        except urllib.error.HTTPError as exc:  # 4xx / 5xx arrive here
            payload = self._safe_json(exc.read())
            self._raise_for_status(exc.code, payload)
            # _raise_for_status always raises for non-2xx; keep the type checker happy.
            raise
        except urllib.error.URLError as exc:  # connection refused, DNS, timeout
            raise RunewardError(f"could not reach runeward at {url}: {exc.reason}") from exc

    def _decode(self, status: int, raw: bytes) -> Any:
        """Handle a response the stdlib treated as success (2xx)."""
        payload = self._safe_json(raw)
        # 202 is "success" to urllib but means an approval gate to runeward.
        if status == 202:
            self._raise_for_status(status, payload)
        return payload

    @staticmethod
    def _safe_json(raw: bytes) -> Dict[str, Any]:
        if not raw:
            return {}
        try:
            return json.loads(raw.decode("utf-8"))
        except (ValueError, UnicodeDecodeError):
            return {"raw": raw.decode("utf-8", "replace")}

    @staticmethod
    def _normalize_base_url(base_url: str) -> str:
        base_url = base_url.strip()
        parsed = urllib.parse.urlsplit(base_url)
        if not parsed.scheme:
            base_url = f"https://{base_url}"
        return base_url.rstrip("/")

    @staticmethod
    def _is_loopback_host(hostname: Optional[str]) -> bool:
        if not hostname:
            return False
        host = hostname.lower().strip("[]")
        return host in {"localhost", "127.0.0.1", "::1"}

    @staticmethod
    def _env_allows_insecure() -> bool:
        value = os.environ.get("RUNEWARD_ALLOW_INSECURE_HTTP", "").strip().lower()
        return value in {"1", "true", "yes", "on"}

    @classmethod
    def _validate_transport(cls, base_url: str, allow_insecure: bool) -> None:
        parsed = urllib.parse.urlsplit(base_url)
        if parsed.scheme != "http":
            return
        if cls._is_loopback_host(parsed.hostname):
            return
        if allow_insecure or cls._env_allows_insecure():
            warnings.warn(
                f"runeward client using insecure HTTP transport to non-loopback host: {base_url}",
                RuntimeWarning,
                stacklevel=3,
            )
            return
        raise ValueError(
            "refusing insecure http:// base URL to non-loopback host; "
            "use https://, set allow_insecure=True, or set RUNEWARD_ALLOW_INSECURE_HTTP=1"
        )

    @staticmethod
    def _segment(value: str) -> str:
        return urllib.parse.quote(value, safe="")

    @staticmethod
    def _raise_for_status(status: int, payload: Dict[str, Any]) -> None:
        if payload.get("code") == "authz_denied":
            raise RunewardAuthorizationError(payload.get("error", "not authorized"), payload=payload)
        if payload.get("verdict") == "deny" or (status == 403 and not payload.get("code")):
            raise RunewardDenied(payload.get("reason", "denied by policy"), payload=payload)
        if status == 202 or payload.get("verdict") == "require-approval":
            raise RunewardApprovalRequired(
                payload.get("approval_id", ""),
                reason=payload.get("reason", ""),
                payload=payload,
            )
        raise RunewardError(
            f"runeward returned HTTP {status}: {payload}", status=status, payload=payload
        )

    # -- health & discovery -----------------------------------------------

    def healthz(self) -> Any:
        """``GET /healthz`` — liveness check."""
        return self._request("GET", "/healthz")

    def whoami(self) -> Dict[str, Any]:
        return self._request("GET", "/v1/whoami")

    def readiness(self, profile: str) -> Dict[str, Any]:
        return self._request("GET", f"/v1/readiness?profile={self._segment(profile)}")

    def simulate_policy(self, profile_name: str, actions: List[Dict[str, Any]]) -> Dict[str, Any]:
        return self._request("POST", "/v1/policy/simulate", {
            "profile_name": profile_name, "actions": actions,
        })

    def list_profiles(self) -> List[Dict[str, Any]]:
        """``GET /v1/charters`` — reachable profiles."""
        return self._request("GET", "/v1/charters").get("profiles", [])

    def list_runs(self) -> List[Dict[str, Any]]:
        return self._request("GET", "/v1/runs").get("runs", [])

    def get_run(self, run_id: str) -> Dict[str, Any]:
        return self._request("GET", f"/v1/runs/{self._segment(run_id)}")

    # -- sandbox lifecycle -------------------------------------------------

    def create_sandbox(self, profile: str, *, copy_from: str = "",
                       parent_citadel: str = "", run_id: str = "",
                       agent: str = "", provider: str = "", model: str = "") -> Dict[str, Any]:
        """``POST /v1/citadels`` — provision a sandbox from ``profile``."""
        body = {"profile": profile}
        optional = {
            "copy_from": copy_from, "parent_citadel": parent_citadel,
            "run_id": run_id, "agent": agent, "provider": provider, "model": model,
        }
        body.update({key: value for key, value in optional.items() if value})
        return self._request("POST", "/v1/citadels", body)

    def list_sandboxes(self) -> List[Dict[str, Any]]:
        """``GET /v1/citadels``."""
        return self._request("GET", "/v1/citadels").get("sandboxes", [])

    def get_sandbox(self, sandbox: str) -> Dict[str, Any]:
        """``GET /v1/citadels/{id}``."""
        return self._request("GET", f"/v1/citadels/{self._segment(sandbox)}")

    def kill_sandbox(self, sandbox: str) -> Any:
        """``DELETE /v1/citadels/{id}`` — tear the sandbox down."""
        return self._request("DELETE", f"/v1/citadels/{self._segment(sandbox)}")

    # -- execution ---------------------------------------------------------

    def shell(self, sandbox: str, command: List[str], workdir: str = "") -> Dict[str, Any]:
        """``POST .../shell/exec`` — run ``command`` (an argv list) in the sandbox.

        Returns ``{"verdict","exit_code","stdout","stderr","duration_ms"}``. An
        ``allow`` verdict with a non-zero ``exit_code`` is a normal program
        error, not a policy denial.
        """
        return self._request(
            "POST", f"/v1/citadels/{self._segment(sandbox)}/shell/exec",
            {"command": list(command), "workdir": workdir},
        )

    def python(self, sandbox: str, code: str) -> Dict[str, Any]:
        """``POST .../code/python`` — run a Python snippet in the sandbox."""
        return self._request("POST", f"/v1/citadels/{self._segment(sandbox)}/code/python", {"code": code})

    def node(self, sandbox: str, code: str) -> Dict[str, Any]:
        """``POST .../code/node`` — run a Node.js snippet in the sandbox."""
        return self._request("POST", f"/v1/citadels/{self._segment(sandbox)}/code/node", {"code": code})

    def browser(self, sandbox: str, url: str, mode: str = "text") -> Dict[str, Any]:
        return self._request("POST", f"/v1/citadels/{self._segment(sandbox)}/browser", {"url": url, "mode": mode})

    def open_browser(self, sandbox: str) -> str:
        return self._request("POST", f"/v1/citadels/{self._segment(sandbox)}/browser/sessions")["session_id"]

    def browser_act(self, sandbox: str, session: str, **action: Any) -> Dict[str, Any]:
        return self._request("POST", f"/v1/citadels/{self._segment(sandbox)}/browser/sessions/{self._segment(session)}/act", action)

    def close_browser(self, sandbox: str, session: str) -> Any:
        return self._request("DELETE", f"/v1/citadels/{self._segment(sandbox)}/browser/sessions/{self._segment(session)}")

    def report_usage(self, sandbox: str, tokens: int, cost_usd: float) -> Dict[str, Any]:
        return self._request("POST", f"/v1/citadels/{self._segment(sandbox)}/usage", {"tokens": tokens, "cost_usd": cost_usd})

    def perimeter(self, sandbox: str) -> List[Dict[str, Any]]:
        return self._request("GET", f"/v1/citadels/{self._segment(sandbox)}/perimeter").get("decisions", [])

    # -- files -------------------------------------------------------------

    def read_file(self, sandbox: str, path: str) -> str:
        """``POST .../file/read`` — return the file's ``content``."""
        return self._request(
            "POST", f"/v1/citadels/{self._segment(sandbox)}/file/read", {"path": path}
        ).get("content", "")

    def write_file(self, sandbox: str, path: str, content: str) -> int:
        """``POST .../file/write`` — write ``content``; return ``bytes`` written."""
        return self._request(
            "POST", f"/v1/citadels/{self._segment(sandbox)}/file/write",
            {"path": path, "content": content},
        ).get("bytes", 0)

    def list_files(self, sandbox: str, path: str) -> str:
        """``POST .../file/list`` — list a directory; return the raw ``output``."""
        return self._request(
            "POST", f"/v1/citadels/{self._segment(sandbox)}/file/list", {"path": path}
        ).get("output", "")

    def search_files(self, sandbox: str, query: str, path: str) -> str:
        """``POST .../file/search`` — search for ``query`` under ``path``."""
        return self._request(
            "POST", f"/v1/citadels/{self._segment(sandbox)}/file/search",
            {"query": query, "path": path},
        ).get("output", "")

    # -- audit -------------------------------------------------------------

    def audit(self, sandbox: str) -> List[Dict[str, Any]]:
        """``GET .../chronicle`` — this sandbox's ledger events."""
        return self._request("GET", f"/v1/citadels/{self._segment(sandbox)}/chronicle").get("events", [])

    def verify_audit(self) -> bool:
        """``GET /v1/chronicle/verify`` — verify the ledger hash chain."""
        return bool(self._request("GET", "/v1/chronicle/verify").get("ok", False))

    def export_evidence(self, sandbox: str) -> Dict[str, Any]:
        """Return the portable signed evidence document for a Citadel."""
        return self._request("GET", f"/v1/citadels/{self._segment(sandbox)}/evidence")

    def create_cohort(self, profile: str) -> Dict[str, Any]:
        return self._request("POST", "/v1/cohorts", {"profile": profile})

    def list_cohorts(self) -> List[Dict[str, Any]]:
        return self._request("GET", "/v1/cohorts").get("fleets", [])

    def get_cohort(self, cohort: str) -> Dict[str, Any]:
        return self._request("GET", f"/v1/cohorts/{self._segment(cohort)}")

    def kill_cohort(self, cohort: str) -> Any:
        return self._request("DELETE", f"/v1/cohorts/{self._segment(cohort)}")

    def list_tasks(self, cohort: str) -> List[Dict[str, Any]]:
        return self._request("GET", f"/v1/cohorts/{self._segment(cohort)}/tasks").get("tasks", [])

    def add_task(self, cohort: str, payload: str) -> Dict[str, Any]:
        return self._request("POST", f"/v1/cohorts/{self._segment(cohort)}/tasks", {"payload": payload})

    def claim_task(self, cohort: str, owner: str = "") -> Dict[str, Any]:
        return self._request("POST", f"/v1/cohorts/{self._segment(cohort)}/claim", {"owner": owner})

    def heartbeat_task(self, cohort: str, task: str, lease_token: str, owner: str = "") -> Any:
        return self._request("POST", f"/v1/cohorts/{self._segment(cohort)}/tasks/{self._segment(task)}/heartbeat", {"owner": owner, "lease_token": lease_token})

    def complete_task(self, cohort: str, task: str, lease_token: str, result: str = "", owner: str = "") -> Any:
        return self._request("POST", f"/v1/cohorts/{self._segment(cohort)}/tasks/{self._segment(task)}/complete", {"owner": owner, "lease_token": lease_token, "result": result})

    def fail_task(self, cohort: str, task: str, lease_token: str, error: str, requeue: bool = False, owner: str = "") -> Any:
        return self._request("POST", f"/v1/cohorts/{self._segment(cohort)}/tasks/{self._segment(task)}/fail", {"owner": owner, "lease_token": lease_token, "error": error, "requeue": requeue})

    def create_snapshot(self, sandbox: str, name: str = "") -> Dict[str, Any]:
        return self._request("POST", f"/v1/citadels/{self._segment(sandbox)}/snapshot", {"name": name})

    def list_snapshots(self) -> List[Dict[str, Any]]:
        return self._request("GET", "/v1/snapshots").get("snapshots", [])

    def restore_snapshot(self, snapshot: str) -> Dict[str, Any]:
        return self._request("POST", f"/v1/snapshots/{self._segment(snapshot)}/restore")

    # -- approvals ---------------------------------------------------------

    def list_approvals(self) -> List[Dict[str, Any]]:
        """``GET /v1/conclave`` — pending human-in-the-loop requests."""
        return self._request("GET", "/v1/conclave").get("approvals", [])

    def approve(self, approval_id: str) -> Any:
        """``POST /v1/conclave/{id}/approve``."""
        return self._request("POST", f"/v1/conclave/{self._segment(approval_id)}/approve")

    def deny(self, approval_id: str) -> Any:
        """``POST /v1/conclave/{id}/deny``."""
        return self._request("POST", f"/v1/conclave/{self._segment(approval_id)}/deny")
