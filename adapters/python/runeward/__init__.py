"""runeward — Python client and agent-framework adapters for the runeward
governed execution cell.

The core :class:`RunewardClient` depends only on the standard library. The
LangChain and CrewAI helpers live in :mod:`runeward.langchain_tools` and
:mod:`runeward.crewai_tools` and import their frameworks lazily, so importing
this package never requires those extras to be installed.
"""

from __future__ import annotations

from .client import (
    RunewardApprovalRequired,
    RunewardClient,
    RunewardDenied,
    RunewardError,
)

__all__ = [
    "RunewardClient",
    "RunewardError",
    "RunewardDenied",
    "RunewardApprovalRequired",
]

__version__ = "0.1.0"
