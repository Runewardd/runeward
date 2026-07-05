"""LlamaIndex tool wrappers around :class:`RunewardClient`.

LlamaIndex is imported *lazily* inside :func:`make_runeward_tools` so the base
``runeward`` client keeps working without ``llama-index`` installed. Install the
extra with ``pip install runeward[llamaindex]``.

Each returned :class:`~llama_index.core.tools.FunctionTool` converts governance
outcomes into a short, model-readable string rather than letting the exception
escape, so an agent can reason about a denial or approval gate instead of
crashing. The messages deliberately spell out the required behavior ("do not
retry", "pause for a human").
"""

from __future__ import annotations

from typing import TYPE_CHECKING, Any, List

from .client import RunewardApprovalRequired, RunewardClient, RunewardDenied

if TYPE_CHECKING:  # only for type checkers; not evaluated at runtime
    from llama_index.core.tools import FunctionTool


def _format_denied(exc: RunewardDenied) -> str:
    return (
        f"DENIED by policy: {exc.reason}. Do not retry this action; choose a "
        "different, allowed approach or report the block to the human."
    )


def _format_approval(exc: RunewardApprovalRequired) -> str:
    return (
        f"APPROVAL REQUIRED (approval_id={exc.approval_id}): "
        f"{exc.reason or 'a human must sign off before this runs'}. "
        "Pause the task and ask the human to approve or deny."
    )


def make_runeward_tools(client: RunewardClient) -> "List[FunctionTool]":
    """Build a list of LlamaIndex ``FunctionTool`` instances bound to ``client``.

    Covers the runeward tool surface: create/kill sandbox, shell, python, node,
    file read/write/list/search, and list-approvals. Requires ``llama-index``
    (``pip install runeward[llamaindex]``).
    """
    # Lazy import: keeps llama-index optional for users of the bare client.
    try:
        from llama_index.core.tools import FunctionTool
    except ImportError as exc:  # pragma: no cover - depends on optional extra
        raise ImportError(
            "LlamaIndex is required for make_runeward_tools(). "
            "Install it with: pip install runeward[llamaindex]"
        ) from exc

    def _guard(fn):
        """Wrap a call so governance verdicts become model-friendly strings."""

        def wrapped(*args: Any, **kwargs: Any) -> str:
            try:
                result = fn(*args, **kwargs)
                return result if isinstance(result, str) else str(result)
            except RunewardDenied as denied:
                return _format_denied(denied)
            except RunewardApprovalRequired as approval:
                return _format_approval(approval)

        return wrapped

    @_guard
    def runeward_create_sandbox(profile: str) -> str:
        """Provision a governed sandbox from a runeward profile (e.g. 'dev').

        Returns the sandbox metadata including its id.
        """
        return str(client.create_sandbox(profile))

    @_guard
    def runeward_shell(sandbox: str, command: List[str], workdir: str = "") -> str:
        """Run a shell command (argv list, e.g. ['ls','-la']) in a sandbox.

        Returns verdict, exit_code, stdout, stderr.
        """
        return str(client.shell(sandbox, command, workdir))

    @_guard
    def runeward_python(sandbox: str, code: str) -> str:
        """Run a Python code snippet inside the sandbox."""
        return str(client.python(sandbox, code))

    @_guard
    def runeward_node(sandbox: str, code: str) -> str:
        """Run a Node.js code snippet inside the sandbox."""
        return str(client.node(sandbox, code))

    @_guard
    def runeward_read_file(sandbox: str, path: str) -> str:
        """Read a file's contents from the sandbox."""
        return client.read_file(sandbox, path)

    @_guard
    def runeward_write_file(sandbox: str, path: str, content: str) -> str:
        """Write content to a file in the sandbox."""
        return f"wrote {client.write_file(sandbox, path, content)} bytes to {path}"

    @_guard
    def runeward_list_files(sandbox: str, path: str) -> str:
        """List a directory in the sandbox."""
        return client.list_files(sandbox, path)

    @_guard
    def runeward_search_files(sandbox: str, query: str, path: str) -> str:
        """Search for a query string under a path in the sandbox."""
        return client.search_files(sandbox, query, path)

    @_guard
    def runeward_list_approvals() -> str:
        """List pending human-in-the-loop approval requests."""
        return str(client.list_approvals())

    @_guard
    def runeward_kill_sandbox(sandbox: str) -> str:
        """Tear down a sandbox when the task is finished."""
        client.kill_sandbox(sandbox)
        return f"sandbox {sandbox} terminated"

    return [
        FunctionTool.from_defaults(fn=runeward_create_sandbox),
        FunctionTool.from_defaults(fn=runeward_shell),
        FunctionTool.from_defaults(fn=runeward_python),
        FunctionTool.from_defaults(fn=runeward_node),
        FunctionTool.from_defaults(fn=runeward_read_file),
        FunctionTool.from_defaults(fn=runeward_write_file),
        FunctionTool.from_defaults(fn=runeward_list_files),
        FunctionTool.from_defaults(fn=runeward_search_files),
        FunctionTool.from_defaults(fn=runeward_list_approvals),
        FunctionTool.from_defaults(fn=runeward_kill_sandbox),
    ]
