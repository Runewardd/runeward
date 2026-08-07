"""runeward — Python client and agent-framework adapters for the Runeward
agent governance harness.

The core :class:`RunewardClient` depends only on the standard library. The
framework helpers live in :mod:`runeward.langchain_tools`,
:mod:`runeward.crewai_tools`, :mod:`runeward.llamaindex_tools`,
:mod:`runeward.openai_agents_tools`, and :mod:`runeward.strands_tools`; each
imports its framework lazily, so importing this package never requires those
extras to be installed.
"""

from __future__ import annotations

from .client import (
    RunewardApprovalRequired,
    RunewardClient,
    RunewardAuthorizationError,
    RunewardDenied,
    RunewardError,
)

__all__ = [
    "RunewardClient",
    "RunewardError",
    "RunewardDenied",
    "RunewardAuthorizationError",
    "RunewardApprovalRequired",
]

__version__ = "0.3.0-rc.2"
