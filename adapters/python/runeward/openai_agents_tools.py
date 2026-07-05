"""OpenAI Agents SDK tool wrappers around :class:`RunewardClient`.

The `OpenAI Agents SDK <https://openai.github.io/openai-agents-python/>`_
(``pip install openai-agents``) is imported *lazily* inside
:func:`make_runeward_tools` so the base ``runeward`` client keeps working
without it. Install the extra with ``pip install runeward[openai-agents]``.

Each returned tool is built with ``@function_tool``: the SDK derives its JSON
schema from the function's type hints and docstring. Governance outcomes are
converted into a short, model-readable string rather than raising, so the agent
can reason about a denial or approval gate instead of crashing.
"""

from __future__ import annotations

from typing import Any, List

from .client import RunewardApprovalRequired, RunewardClient, RunewardDenied


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


def make_runeward_tools(client: RunewardClient) -> List[Any]:
    """Build a list of OpenAI Agents SDK ``FunctionTool`` objects bound to ``client``.

    Pass the result to an ``agents.Agent(tools=...)``. Requires
    ``openai-agents`` (``pip install runeward[openai-agents]``).
    """
    # Lazy import so openai-agents stays an optional extra.
    try:
        from agents import function_tool
    except ImportError as exc:  # pragma: no cover - depends on optional extra
        raise ImportError(
            "The OpenAI Agents SDK is required for make_runeward_tools(). "
            "Install it with: pip install runeward[openai-agents]"
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

    @function_tool
    def runeward_create_sandbox(profile: str) -> str:
        """Provision a governed sandbox from a runeward profile (e.g. 'dev').

        Args:
            profile: Profile name, e.g. 'dev' or 'governed'.
        """
        return _guard(client.create_sandbox)(profile)

    @function_tool
    def runeward_shell(sandbox: str, command: List[str], workdir: str = "") -> str:
        """Run a shell command in a sandbox; returns verdict, exit_code, stdout, stderr.

        Args:
            sandbox: Sandbox id from runeward_create_sandbox.
            command: argv list, e.g. ['ls', '-la'].
            workdir: Optional working directory.
        """
        return _guard(client.shell)(sandbox, command, workdir)

    @function_tool
    def runeward_python(sandbox: str, code: str) -> str:
        """Run a Python code snippet inside the sandbox.

        Args:
            sandbox: Sandbox id.
            code: Python source to execute.
        """
        return _guard(client.python)(sandbox, code)

    @function_tool
    def runeward_node(sandbox: str, code: str) -> str:
        """Run a Node.js code snippet inside the sandbox.

        Args:
            sandbox: Sandbox id.
            code: JavaScript source to execute.
        """
        return _guard(client.node)(sandbox, code)

    @function_tool
    def runeward_read_file(sandbox: str, path: str) -> str:
        """Read a file's contents from the sandbox.

        Args:
            sandbox: Sandbox id.
            path: File path to read.
        """
        return _guard(client.read_file)(sandbox, path)

    @function_tool
    def runeward_write_file(sandbox: str, path: str, content: str) -> str:
        """Write content to a file in the sandbox.

        Args:
            sandbox: Sandbox id.
            path: File path to write.
            content: Content to write.
        """

        def _write() -> str:
            return f"wrote {client.write_file(sandbox, path, content)} bytes to {path}"

        return _guard(_write)()

    @function_tool
    def runeward_list_files(sandbox: str, path: str) -> str:
        """List a directory in the sandbox.

        Args:
            sandbox: Sandbox id.
            path: Directory path to list.
        """
        return _guard(client.list_files)(sandbox, path)

    @function_tool
    def runeward_search_files(sandbox: str, query: str, path: str) -> str:
        """Search for a query string under a path in the sandbox.

        Args:
            sandbox: Sandbox id.
            query: Search query.
            path: Path to search under.
        """
        return _guard(client.search_files)(sandbox, query, path)

    @function_tool
    def runeward_list_approvals() -> str:
        """List pending human-in-the-loop approval requests."""
        return _guard(client.list_approvals)()

    @function_tool
    def runeward_kill_sandbox(sandbox: str) -> str:
        """Tear down a sandbox when the task is finished.

        Args:
            sandbox: Sandbox id.
        """

        def _kill() -> str:
            client.kill_sandbox(sandbox)
            return f"sandbox {sandbox} terminated"

        return _guard(_kill)()

    return [
        runeward_create_sandbox,
        runeward_shell,
        runeward_python,
        runeward_node,
        runeward_read_file,
        runeward_write_file,
        runeward_list_files,
        runeward_search_files,
        runeward_list_approvals,
        runeward_kill_sandbox,
    ]
