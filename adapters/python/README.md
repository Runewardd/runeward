# runeward (Python)

A dependency-light Python client and agent-framework adapters for the
[runeward](https://github.com/Runewardd/runeward) **governed execution cell** —
provision isolated Citadels and run shell / Python / Node / file tools through a
policy engine, Chronicle (audit ledger), Rationing, and human-in-the-loop
approval gates.

The core client uses **only the Python standard library** (`urllib`). The
LangChain, CrewAI, LlamaIndex, OpenAI Agents SDK, and Strands helpers are
optional extras and are imported lazily, so the base client works with nothing
else installed.

## Install

```bash
pip install runeward                    # core client only (no third-party deps)
pip install "runeward[langchain]"       # + LangChain tools
pip install "runeward[crewai]"          # + CrewAI tools
pip install "runeward[llamaindex]"      # + LlamaIndex tools
pip install "runeward[openai-agents]"   # + OpenAI Agents SDK tools
pip install "runeward[strands]"         # + Strands Agents SDK tools
```

During local development from this directory:

```bash
pip install -e .
```

## Quick start

Start the control plane first (`runeward serve`, default
`http://localhost:8080`), then:

```python
from runeward import RunewardClient, RunewardDenied, RunewardApprovalRequired

rw = RunewardClient("http://localhost:8080")

sbx = rw.create_sandbox("dev")          # -> {"id": "sbx_...", "backend": "docker", ...}
sid = sbx["id"]

result = rw.shell(sid, ["python3", "--version"])
print(result["stdout"])                 # "Python 3.11.2\n"

rw.write_file(sid, "/workspace/main.py", "print(2 + 2)")
print(rw.python(sid, "exec(open('/workspace/main.py').read())")["stdout"])  # "4\n"

rw.kill_sandbox(sid)                    # always tear down when done
```

Use `allow_insecure=True` (or `RUNEWARD_ALLOW_INSECURE_HTTP=1`) only when you must call a non-loopback `http://` control-plane endpoint.

### Handling governance verdicts

The two governance outcomes are raised as typed exceptions. Handle them
explicitly — a denial must **not** be blindly retried, and an approval gate must
**pause** for a human:

```python
try:
    rw.shell(sid, ["rm", "-rf", "/"])
except RunewardDenied as e:
    print("blocked by policy:", e.reason)     # do NOT retry the same action

try:
    rw.write_file(sid, "/etc/hosts", "127.0.0.1 example")
except RunewardApprovalRequired as e:
    print("needs a human:", e.approval_id)     # pause; ask an operator to approve/deny
```

### Approvals inbox

```python
for a in rw.list_approvals():
    print(a["id"], a["tool"], a["action"], a["reason"])

rw.approve("apr_31c")   # or rw.deny("apr_31c")
```

### Chronicle (audit ledger)

```python
events = rw.audit(sid)          # this Citadel's Chronicle events
assert rw.verify_audit()        # verify the tamper-evident hash chain
```

## Client method surface

| Method | REST endpoint |
| --- | --- |
| `healthz()` | `GET /healthz` |
| `list_profiles()` | `GET /v1/charters` |
| `create_sandbox(profile)` | `POST /v1/citadels` |
| `list_sandboxes()` / `get_sandbox(id)` / `kill_sandbox(id)` | `GET`/`GET`/`DELETE /v1/citadels[/{id}]` |
| `shell(sandbox, command, workdir="")` | `POST .../shell/exec` |
| `python(sandbox, code)` / `node(sandbox, code)` | `POST .../code/{python,node}` |
| `read_file` / `write_file` / `list_files` / `search_files` | `POST .../file/{read,write,list,search}` |
| `audit(sandbox)` / `verify_audit()` | `GET .../chronicle`, `GET /v1/chronicle/verify` |
| `list_approvals()` / `approve(id)` / `deny(id)` | `GET /v1/conclave`, `POST /v1/conclave/{id}/{approve,deny}` |

## LangChain

```python
from runeward import RunewardClient
from runeward.langchain_tools import make_runeward_tools

tools = make_runeward_tools(RunewardClient("http://localhost:8080"))
# Pass `tools` to any LangChain agent / AgentExecutor.
```

The Python framework tools are named `runeward_create_sandbox`,
`runeward_shell`, …. Governance verdicts are returned as descriptive strings so
the agent can reason about a denial or an approval gate. (Note: the standalone
`runeward mcp` server now exposes the renamed `runeward_create_citadel` /
`runeward_kill_citadel` / `runeward_list_conclave` tools; the Python framework
tool names have not yet been updated to match.)

## CrewAI

```python
from runeward import RunewardClient
from runeward.crewai_tools import make_runeward_tools

tools = make_runeward_tools(RunewardClient("http://localhost:8080"))
# Attach `tools` to a crewai.Agent(tools=tools, ...).
```

## LlamaIndex

```python
from runeward import RunewardClient
from runeward.llamaindex_tools import make_runeward_tools

tools = make_runeward_tools(RunewardClient("http://localhost:8080"))
# Pass `tools` to a FunctionAgent / ReActAgent / AgentRunner.
```

Returns `llama_index.core.tools.FunctionTool` instances; the tool schema is
derived from each function's type hints and docstring.

## OpenAI Agents SDK

```python
from agents import Agent, Runner
from runeward import RunewardClient
from runeward.openai_agents_tools import make_runeward_tools

tools = make_runeward_tools(RunewardClient("http://localhost:8080"))
agent = Agent(name="builder", instructions="Use the sandbox tools.", tools=tools)
result = Runner.run_sync(agent, "Create a dev sandbox, run `node --version`, then tear it down.")
```

Returns `@function_tool`-built tools; the SDK derives each schema from the
function's type hints and docstring.

## Strands Agents SDK

```python
from strands import Agent
from runeward import RunewardClient
from runeward.strands_tools import make_runeward_tools

tools = make_runeward_tools(RunewardClient("http://localhost:8080"))
agent = Agent(tools=tools)
agent("Create a dev sandbox, run `node --version`, then tear it down.")
```

Returns `@tool`-decorated functions; Strands derives each schema from the
function's type hints and docstring.

## Notes

- **`deny` is a policy decision, not a transient error.** Don't retry the same
  action; pick a different, allowed approach.
- **`require-approval` is a hard pause.** Surface the `approval_id` to a human
  and wait for the outcome.
- Prefer the tightest profile that lets the task succeed.
