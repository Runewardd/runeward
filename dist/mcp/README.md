# Publishing runeward to MCP registries

[`server.json`](./server.json) is the [Model Context Protocol registry][mcp-reg]
manifest for runeward. It declares the server's reverse-DNS name
(`io.github.adefemi171/runeward`), its source repository, and two ways to run
it:

- a **stdio package** — `runeward mcp` shipped as the OCI image
  `ghcr.io/adefemi171/runeward`, and
- an optional **streamable HTTP remote** at `http://localhost:8080/mcp` (served
  by `runeward serve`).

The MCP tools this server exposes are documented in
[`../skill/SKILL.md`](../skill/SKILL.md):
`runeward_create_sandbox`, `runeward_shell`, `runeward_python`,
`runeward_node`, `runeward_read_file`, `runeward_write_file`,
`runeward_list_files`, `runeward_search_files`, `runeward_list_approvals`,
`runeward_kill_sandbox`.

## Validate and publish to the registry

The MCP registry is published to with the official [`mcp-publisher`][publisher]
CLI. From this directory:

```bash
# 1. Install the publisher CLI (see the docs for your platform).
#    e.g. via Go: go install github.com/modelcontextprotocol/registry/cmd/mcp-publisher@latest

# 2. Authenticate. GitHub auth proves you own the io.github.adefemi171/* namespace.
mcp-publisher login github

# 3. Validate the manifest against the registry schema, then publish.
mcp-publisher publish ./server.json
```

The `name` namespace (`io.github.adefemi171/...`) must match the GitHub account
you authenticate as — that ownership check is how the registry prevents
namespace squatting. Bump `version` on every release and keep it in sync with
the OCI image tag under `packages[].version`.

## Configure runeward as an MCP server in a client

The published registry entry is just discovery metadata — clients still need a
local `mcpServers` config to actually launch the server. runeward runs as a
stdio server via `runeward mcp`.

### Claude Desktop

Edit `claude_desktop_config.json`
(macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "runeward": {
      "command": "runeward",
      "args": ["mcp"],
      "env": {
        "RUNEWARD_CONFIG_DIR": "/absolute/path/to/your/profiles"
      }
    }
  }
}
```

### Cursor

Add to `~/.cursor/mcp.json` (global) or `.cursor/mcp.json` (project):

```json
{
  "mcpServers": {
    "runeward": {
      "command": "runeward",
      "args": ["mcp"]
    }
  }
}
```

`command` must be on your `PATH` (build it with
`go build -o bin/runeward ./cmd/runeward` and install the binary, or point
`command` at the absolute path of the built binary). `env` is optional; set
`RUNEWARD_CONFIG_DIR` to pin profile resolution and `RUNEWARD_STATE_DIR` to pin
where the audit ledger is written.

### Streamable HTTP remote (alternative)

If you'd rather run the control plane once and connect clients to it over HTTP,
start `runeward serve` and point a streamable-HTTP-capable client at
`http://localhost:8080/mcp` (the `remotes` entry in `server.json`).

[mcp-reg]: https://github.com/modelcontextprotocol/registry
[publisher]: https://github.com/modelcontextprotocol/registry/blob/main/docs/guides/publishing/publish-server.md
