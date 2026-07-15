"""LangChain tool wrappers around :class:`RunewardClient`.

LangChain is imported *lazily* inside :func:`make_runeward_tools` so that the
base ``runeward`` client keeps working without ``langchain`` installed. Install
the extra with ``pip install runeward[langchain]``.

Each returned tool converts governance outcomes into a short, model-readable
string rather than letting the exception escape, so an agent can reason about a
denial or approval gate instead of crashing. The messages deliberately spell out
the required behavior ("do not retry", "pause for a human").
"""

from __future__ import annotations

from typing import TYPE_CHECKING, Any, List

from .client import RunewardApprovalRequired, RunewardClient, RunewardDenied

if TYPE_CHECKING:  # only for type checkers; not evaluated at runtime
    from langchain_core.tools import BaseTool


def make_runeward_tools(client: RunewardClient) -> "List[BaseTool]":
    """Build a list of LangChain tools bound to ``client``.

    Returns ``StructuredTool`` instances covering the runeward tool surface:
    create/kill Citadel, shell, python, node, file read/write/list/search, and
    list-Conclave.
    """
    # Lazy import: keeps langchain optional for users of the bare client.
    try:
        from langchain_core.tools import StructuredTool
    except ImportError as exc:  # pragma: no cover - depends on optional extra
        raise ImportError(
            "LangChain is required for make_runeward_tools(). "
            "Install it with: pip install runeward[langchain]"
        ) from exc

    def _guard(fn):
        """Wrap a call so governance verdicts become model-friendly strings."""

        def wrapped(*args: Any, **kwargs: Any) -> str:
            try:
                result = fn(*args, **kwargs)
                return result if isinstance(result, str) else str(result)
            except RunewardDenied as denied:
                return (
                    f"DENIED by policy: {denied.reason}. "
                    "Do not retry this action; choose a different, allowed approach "
                    "or tell the human it was blocked."
                )
            except RunewardApprovalRequired as approval:
                return (
                    f"APPROVAL REQUIRED (approval_id={approval.approval_id}): "
                    f"{approval.reason or 'a human must sign off before this runs'}. "
                    "Pause the task and ask the human to approve or deny."
                )

        return wrapped

    @_guard
    def create_citadel(profile: str) -> str:
        return str(client.create_sandbox(profile))

    @_guard
    def shell(sandbox: str, command: List[str], workdir: str = "") -> str:
        return str(client.shell(sandbox, command, workdir))

    @_guard
    def python(sandbox: str, code: str) -> str:
        return str(client.python(sandbox, code))

    @_guard
    def node(sandbox: str, code: str) -> str:
        return str(client.node(sandbox, code))

    @_guard
    def read_file(sandbox: str, path: str) -> str:
        return client.read_file(sandbox, path)

    @_guard
    def write_file(sandbox: str, path: str, content: str) -> str:
        return f"wrote {client.write_file(sandbox, path, content)} bytes to {path}"

    @_guard
    def list_files(sandbox: str, path: str) -> str:
        return client.list_files(sandbox, path)

    @_guard
    def search_files(sandbox: str, query: str, path: str) -> str:
        return client.search_files(sandbox, query, path)

    @_guard
    def list_conclave() -> str:
        return str(client.list_approvals())

    @_guard
    def kill_citadel(sandbox: str) -> str:
        client.kill_sandbox(sandbox)
        return f"Citadel {sandbox} terminated"

    return [
        StructuredTool.from_function(
            func=create_citadel,
            name="runeward_create_citadel",
            description="Provision a governed Citadel from a runeward Charter (e.g. 'dev'). Returns the Citadel metadata including its id.",
        ),
        StructuredTool.from_function(
            func=shell,
            name="runeward_shell",
            description="Run a shell command (as an argv list, e.g. ['ls','-la']) in a Citadel. Returns verdict, exit_code, stdout, stderr.",
        ),
        StructuredTool.from_function(
            func=python,
            name="runeward_python",
            description="Run a Python code snippet inside the Citadel.",
        ),
        StructuredTool.from_function(
            func=node,
            name="runeward_node",
            description="Run a Node.js code snippet inside the Citadel.",
        ),
        StructuredTool.from_function(
            func=read_file,
            name="runeward_read_file",
            description="Read a file's contents from the Citadel.",
        ),
        StructuredTool.from_function(
            func=write_file,
            name="runeward_write_file",
            description="Write content to a file in the Citadel.",
        ),
        StructuredTool.from_function(
            func=list_files,
            name="runeward_list_files",
            description="List a directory in the Citadel.",
        ),
        StructuredTool.from_function(
            func=search_files,
            name="runeward_search_files",
            description="Search for a query string under a path in the Citadel.",
        ),
        StructuredTool.from_function(
            func=list_conclave,
            name="runeward_list_conclave",
            description="List pending human-in-the-loop Conclave requests.",
        ),
        StructuredTool.from_function(
            func=kill_citadel,
            name="runeward_kill_citadel",
            description="Tear down a Citadel when the task is finished.",
        ),
    ]
