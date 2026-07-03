"""CrewAI tool wrappers around :class:`RunewardClient`.

CrewAI (and its ``pydantic`` dependency) are imported *lazily* inside
:func:`make_runeward_tools` so the base ``runeward`` client works without the
extra. Install with ``pip install runeward[crewai]``.

Each tool is a ``crewai.tools.BaseTool`` subclass with a typed pydantic
args-schema. Governance verdicts are surfaced as descriptive strings so the crew
can reason about a denial or an approval gate instead of raising.
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
    """Build a list of CrewAI ``BaseTool`` instances bound to ``client``.

    Returns one tool per runeward capability. Requires ``crewai`` to be
    installed (``pip install runeward[crewai]``).
    """
    # Lazy import so crewai stays an optional extra.
    try:
        from crewai.tools import BaseTool
        from pydantic import BaseModel, Field
    except ImportError as exc:  # pragma: no cover - depends on optional extra
        raise ImportError(
            "CrewAI is required for make_runeward_tools(). "
            "Install it with: pip install runeward[crewai]"
        ) from exc

    # --- argument schemas -------------------------------------------------

    class CreateSandboxArgs(BaseModel):
        profile: str = Field(..., description="Profile name, e.g. 'dev' or 'governed'.")

    class ShellArgs(BaseModel):
        sandbox: str = Field(..., description="Sandbox id from create_sandbox.")
        command: List[str] = Field(..., description="argv list, e.g. ['ls','-la'].")
        workdir: str = Field("", description="Optional working directory.")

    class CodeArgs(BaseModel):
        sandbox: str = Field(..., description="Sandbox id.")
        code: str = Field(..., description="Source code to execute.")

    class ReadArgs(BaseModel):
        sandbox: str = Field(..., description="Sandbox id.")
        path: str = Field(..., description="File path to read.")

    class WriteArgs(BaseModel):
        sandbox: str = Field(..., description="Sandbox id.")
        path: str = Field(..., description="File path to write.")
        content: str = Field(..., description="Content to write.")

    class ListArgs(BaseModel):
        sandbox: str = Field(..., description="Sandbox id.")
        path: str = Field(..., description="Directory path to list.")

    class SearchArgs(BaseModel):
        sandbox: str = Field(..., description="Sandbox id.")
        query: str = Field(..., description="Search query.")
        path: str = Field(..., description="Path to search under.")

    class SandboxArgs(BaseModel):
        sandbox: str = Field(..., description="Sandbox id.")

    class NoArgs(BaseModel):
        pass

    # --- tool definitions -------------------------------------------------

    class CreateSandboxTool(BaseTool):
        name: str = "runeward_create_sandbox"
        description: str = "Provision a governed sandbox from a runeward profile. Returns sandbox metadata including its id."
        args_schema: type = CreateSandboxArgs

        def _run(self, profile: str) -> str:
            try:
                return str(client.create_sandbox(profile))
            except RunewardDenied as e:
                return _format_denied(e)
            except RunewardApprovalRequired as e:
                return _format_approval(e)

    class ShellTool(BaseTool):
        name: str = "runeward_shell"
        description: str = "Run a shell command (argv list) in a sandbox. Returns verdict, exit_code, stdout, stderr."
        args_schema: type = ShellArgs

        def _run(self, sandbox: str, command: List[str], workdir: str = "") -> str:
            try:
                return str(client.shell(sandbox, command, workdir))
            except RunewardDenied as e:
                return _format_denied(e)
            except RunewardApprovalRequired as e:
                return _format_approval(e)

    class PythonTool(BaseTool):
        name: str = "runeward_python"
        description: str = "Run a Python code snippet inside the sandbox."
        args_schema: type = CodeArgs

        def _run(self, sandbox: str, code: str) -> str:
            try:
                return str(client.python(sandbox, code))
            except RunewardDenied as e:
                return _format_denied(e)
            except RunewardApprovalRequired as e:
                return _format_approval(e)

    class NodeTool(BaseTool):
        name: str = "runeward_node"
        description: str = "Run a Node.js code snippet inside the sandbox."
        args_schema: type = CodeArgs

        def _run(self, sandbox: str, code: str) -> str:
            try:
                return str(client.node(sandbox, code))
            except RunewardDenied as e:
                return _format_denied(e)
            except RunewardApprovalRequired as e:
                return _format_approval(e)

    class ReadFileTool(BaseTool):
        name: str = "runeward_read_file"
        description: str = "Read a file's contents from the sandbox."
        args_schema: type = ReadArgs

        def _run(self, sandbox: str, path: str) -> str:
            try:
                return client.read_file(sandbox, path)
            except RunewardDenied as e:
                return _format_denied(e)
            except RunewardApprovalRequired as e:
                return _format_approval(e)

    class WriteFileTool(BaseTool):
        name: str = "runeward_write_file"
        description: str = "Write content to a file in the sandbox."
        args_schema: type = WriteArgs

        def _run(self, sandbox: str, path: str, content: str) -> str:
            try:
                return f"wrote {client.write_file(sandbox, path, content)} bytes to {path}"
            except RunewardDenied as e:
                return _format_denied(e)
            except RunewardApprovalRequired as e:
                return _format_approval(e)

    class ListFilesTool(BaseTool):
        name: str = "runeward_list_files"
        description: str = "List a directory in the sandbox."
        args_schema: type = ListArgs

        def _run(self, sandbox: str, path: str) -> str:
            try:
                return client.list_files(sandbox, path)
            except RunewardDenied as e:
                return _format_denied(e)
            except RunewardApprovalRequired as e:
                return _format_approval(e)

    class SearchFilesTool(BaseTool):
        name: str = "runeward_search_files"
        description: str = "Search for a query string under a path in the sandbox."
        args_schema: type = SearchArgs

        def _run(self, sandbox: str, query: str, path: str) -> str:
            try:
                return client.search_files(sandbox, query, path)
            except RunewardDenied as e:
                return _format_denied(e)
            except RunewardApprovalRequired as e:
                return _format_approval(e)

    class ListApprovalsTool(BaseTool):
        name: str = "runeward_list_approvals"
        description: str = "List pending human-in-the-loop approval requests."
        args_schema: type = NoArgs

        def _run(self) -> str:
            return str(client.list_approvals())

    class KillSandboxTool(BaseTool):
        name: str = "runeward_kill_sandbox"
        description: str = "Tear down a sandbox when the task is finished."
        args_schema: type = SandboxArgs

        def _run(self, sandbox: str) -> str:
            client.kill_sandbox(sandbox)
            return f"sandbox {sandbox} terminated"

    return [
        CreateSandboxTool(),
        ShellTool(),
        PythonTool(),
        NodeTool(),
        ReadFileTool(),
        WriteFileTool(),
        ListFilesTool(),
        SearchFilesTool(),
        ListApprovalsTool(),
        KillSandboxTool(),
    ]
