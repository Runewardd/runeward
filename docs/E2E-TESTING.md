# runeward — end-to-end local testing

A hands-on walkthrough for exercising the whole stack on your laptop: the
**Docker** and **Kubernetes** backends, deny-by-default and **strict (L3)**
egress, the governed REST API, snapshots, multi-agent fleets, and wiring the
**MCP server** into **Claude Desktop**, **Cursor**, and **VS Code**.

Everything below assumes macOS + [OrbStack](https://orbstack.dev) (which gives
you both Docker and a one-click Kubernetes cluster), but any Docker + kubectl
setup works.

---

## 0. Prerequisites

| Tool | Why | Check |
|------|-----|-------|
| Go ≥ 1.25 | build the binaries | `go version` |
| Docker / OrbStack | Docker backend | `docker info` |
| Kubernetes (OrbStack/kind/minikube) | K8s backend | `kubectl get nodes` |
| `curl`, `jq` | drive the REST API | `jq --version` |
| Node/Python (optional) | adapter smoke tests | — |

Pre-pull the images the example profiles use so the first run is fast:

```bash
docker pull debian:stable-slim
docker pull rancher/mirrored-library-busybox:1.37.0   # egress-demo (has wget)
docker pull curlimages/curl:8.11.0                    # egress-strict (k8s)
```

### Build

```bash
cd /path/to/sandbox
go build -o bin/runeward ./cmd/runeward
go build -o bin/runeward-egress ./cmd/runeward-egress   # only needed for k8s egress image

./bin/runeward version
```

### Handy environment variables

| Var | Purpose | Default |
|-----|---------|---------|
| `RUNEWARD_CONFIG_DIR` | pin the profile search dir | (unset; use `--config-dir`) |
| `RUNEWARD_STATE_DIR` | where the audit ledger is written | OS cache dir |
| `RUNEWARD_KUBE_CONTEXT` | kube-context for the k8s backend | current context |
| `RUNEWARD_K8S_NAMESPACE` | namespace for sandbox pods | `runeward` |
| `RUNEWARD_EGRESS_IMAGE` | egress sidecar/init image ref | `runeward-egress:latest` |

> The **backend is chosen per profile** by `[host].type` (`container` →
> Docker, `kubernetes` → K8s). There is no global backend switch — you pick by
> which profile you run.

Use a scratch state dir so test runs are isolated and easy to wipe:

```bash
export RUNEWARD_STATE_DIR="$(mktemp -d)/runeward-state"
```

The example profiles live in [`examples/`](https://github.com/Runewardd/runeward/tree/main/examples):

| Profile | Backend | Demonstrates |
|---------|---------|--------------|
| `dev` | Docker | open profile, interactive shell |
| `governed` | Docker | policy `deny` + human-in-the-loop approval |
| `rego` | Docker | OPA/Rego policy engine (`policy_engine = "rego"`) |
| `policy-bundle` | Docker | signed OCI policy bundle (`[policy_bundle]`) |
| `egress-demo` | Docker | deny-by-default egress (cooperative proxy) |
| `egress-strict` | K8s | strict L3 egress (iptables + transparent proxy) |
| `fleet-demo` | Docker | multi-agent fleet + atomic task board |
| `ns-auto` | Docker | fully worked autonomous-agent contract |
| `k8s` | K8s | minimal Kubernetes-backed sandbox |

---

## 1. CLI smoke test (Docker)

```bash
# Inspect a profile's resolved, secret-redacted contract
./bin/runeward --config-dir examples print ns-auto

# List discoverable profiles
./bin/runeward --config-dir examples list

# Run a one-off command in a throwaway sandbox, then tear it down
./bin/runeward --config-dir examples dev -- uname -a

# Drop into an interactive shell (Ctrl-D to exit + clean up)
./bin/runeward --config-dir examples dev
```

Expected: `uname -a` prints a Linux kernel string (the container's), and the
interactive shell gives you a prompt inside `/workspace`.

---

## 2. Control plane + dashboard (Docker)

Start the governed control plane (REST API + web dashboard on `:8080`):

```bash
./bin/runeward --config-dir examples serve
# runeward: control plane listening on http://localhost:8080
# runeward: dashboard at http://localhost:8080/
```

Open <http://localhost:8080/> — you get the dashboard (create a sandbox, live
terminal, files, shell/code, audit timeline, approvals inbox, and the **Fleets**
view). Leave `serve` running and drive it from a second terminal.

### 2a. Sandbox lifecycle over REST

```bash
BASE=http://localhost:8080

# Create a sandbox from the dev profile
SID=$(curl -s $BASE/v1/sandboxes -d '{"profile":"dev"}' | jq -r .id)
echo "sandbox=$SID"

# Run a shell command
curl -s $BASE/v1/sandboxes/$SID/shell/exec \
  -d '{"command":["echo","hello from runeward"]}' | jq

# Run Python (NB: the sandbox image must ship python3 — the `dev` profile's
# debian:stable-slim does not. See the note below to test the code runner.)
curl -s $BASE/v1/sandboxes/$SID/code/python \
  -d '{"code":"print(6*7)"}' | jq '.stdout'

# Write + read a file (read returns content under .content)
curl -s $BASE/v1/sandboxes/$SID/file/write \
  -d '{"path":"note.txt","content":"remember me"}' | jq '.verdict'
curl -s $BASE/v1/sandboxes/$SID/file/read \
  -d '{"path":"note.txt"}' | jq -r '.content'

# List sandboxes (array is under .sandboxes)
curl -s $BASE/v1/sandboxes | jq '.sandboxes[].id'

# Tear down
curl -s -X DELETE $BASE/v1/sandboxes/$SID | jq
```

Every response carries a `verdict` (`allow`/`deny`/`require-approval`),
`exit_code`, `stdout`/`stderr`, and `duration_ms`.

> The Python/code runner shells out to `python3` **inside the sandbox**, so it
> only works when the profile's image includes Python. To see it return `42`,
> point a profile at a Python image, e.g.
> `printf '[host]\ntype="container"\nimage="python:3.12-slim"\nworkdir="/workspace"\n' > examples/py.toml`,
> restart `serve`, then create a sandbox from the `py` profile.

### 2b. Audit ledger + tamper-evidence

```bash
# This sandbox's events (events are under .events)
curl -s $BASE/v1/sandboxes/$SID/audit | jq '.events[].tool'

# Verify the hash chain across the whole ledger
curl -s $BASE/v1/audit/verify | jq
# => {"ok":true,"signed":true}
```

You can also confirm the on-disk ledger grows and is append-only:

```bash
ls -la "$RUNEWARD_STATE_DIR"
```

---

## 3. Policy + human-in-the-loop approvals (Docker)

Use the `governed` profile, which **denies** `rm *` and **requires approval**
for writes under `/etc`.

# NB: don't name the variable GID/UID/EUID — those are read-only in zsh.
```bash
SB=$(curl -s $BASE/v1/sandboxes -d '{"profile":"governed"}' | jq -r .id)

# Denied outright by policy -> HTTP 403 + verdict "deny"
curl -s -o /dev/null -w "%{http_code}\n" \
  $BASE/v1/sandboxes/$SB/shell/exec -d '{"command":["rm","-rf","/tmp/x"]}'

# Requires approval: this call blocks up to 5 min waiting for an operator, then
# returns 202 + approval_id. Run it in the background and approve from the inbox.
curl -s $BASE/v1/sandboxes/$SB/file/write \
  -d '{"path":"/etc/motd","content":"reviewed"}' &

# See the pending approval (list is under .approvals)
sleep 1
curl -s $BASE/v1/approvals | jq
AID=$(curl -s $BASE/v1/approvals | jq -r '.approvals[0].id')

# Approve it (or /deny) — the blocked call now proceeds
curl -s -X POST $BASE/v1/approvals/$AID/approve | jq
```

In the dashboard, the same flow shows up in the **Approvals** drawer (the count
badge in the header increments); approve/deny there and watch the call resolve.

### 3b. Alternative policy engines (CEL / Rego)

The same verdicts can be expressed in CEL or OPA/Rego by setting `policy_engine`
in the profile. `examples/rego.toml` mirrors `governed`'s rules in Rego:

```bash
RID=$(curl -s $BASE/v1/sandboxes -d '{"profile":"rego"}' | jq -r .id)

# Denied by the Rego module (data.runeward.decision)
curl -s $BASE/v1/sandboxes/$RID/shell/exec \
  -d '{"command":["rm","-rf","/tmp/x"]}' | jq '{verdict,reason}'
# => {"verdict":"deny","reason":"destructive command blocked by policy"}

# Allowed (default decision)
curl -s $BASE/v1/sandboxes/$RID/shell/exec \
  -d '{"command":["echo","hi"]}' | jq '{verdict,exit_code}'
# => {"verdict":"allow","exit_code":0}
```

The Rego query (default `data.runeward.decision`) returns either a bare verdict
string or `{"verdict":..., "reason":...}` over the input `{tool, arg}`. Modules
use Rego v1 syntax (`if`/`contains`).

### 3c. Governed browser tool

The browser runs headless Chromium *inside* the sandbox, so it obeys the same
policy verdicts and egress allowlist. The profile's image must ship a Chromium
binary; the default sandbox image ([`deploy/Dockerfile.sandbox`](https://github.com/Runewardd/runeward/blob/main/deploy/Dockerfile.sandbox))
bundles both Chromium and the `runeward-browser` CDP driver.

**One-shot render** — fetch a single URL:

```bash
# mode "text" returns rendered DOM; "screenshot" returns a base64 PNG
curl -s $BASE/v1/sandboxes/$SID/browser \
  -d '{"url":"https://example.com/","mode":"text"}' | jq '{verdict,exit_code}'
```

**Stateful, CDP-driven session** — a persistent Chromium page the control plane
drives across calls (cookies/DOM/storage persist between actions). Each action
is individually governed and audited:

```bash
# open -> returns a session id
SESS=$(curl -s -X POST $BASE/v1/sandboxes/$SID/browser/sessions | jq -r .session_id)

# act: navigate, then evaluate JS / extract text / screenshot on the SAME page
curl -s $BASE/v1/sandboxes/$SID/browser/sessions/$SESS/act \
  -d '{"action":"navigate","url":"https://example.com/"}' | jq '{verdict,exit_code}'
curl -s $BASE/v1/sandboxes/$SID/browser/sessions/$SESS/act \
  -d '{"action":"title"}' | jq -r .stdout
curl -s $BASE/v1/sandboxes/$SID/browser/sessions/$SESS/act \
  -d '{"action":"eval","expr":"document.querySelectorAll(\"a\").length"}' | jq -r .stdout
curl -s $BASE/v1/sandboxes/$SID/browser/sessions/$SESS/act \
  -d '{"action":"screenshot"}' | jq -r .stdout | head -c 40   # base64 PNG

# close -> shuts down the in-sandbox Chromium
curl -s -X DELETE $BASE/v1/sandboxes/$SID/browser/sessions/$SESS | jq
```

Over MCP the session is `runeward_browser_open` → `runeward_browser_act`
(`action` one of navigate|eval|text|html|screenshot|click|type|wait|title|url)
→ `runeward_browser_close`; the one-shot render is `runeward_browser`. A
deny-by-default profile constrains what the browser can reach — the session's
HTTP(S) proxy is threaded into Chromium via `--proxy-server`.

### 3d. Signed OCI policy bundles

Ship a policy as a signed, versioned OCI artifact and have a profile pull +
verify it. This uses a throwaway local registry, so add `plain_http = true` to
the profile's `[policy_bundle]` block for the test.

```bash
# 1. Local registry + signing key
docker run -d --rm -p 5000:5000 --name rw-registry registry:2
./bin/runeward bundle keygen --out /tmp/rw-keys      # writes bundle.key + bundle.pub

# 2. Author a policy and push it (rego here; --engine cel also works)
cat > /tmp/prod.rego <<'EOF'
package runeward
import rego.v1
default decision := "allow"
decision := {"verdict": "deny", "reason": "destructive command blocked by bundle"} if {
  input.tool == "shell"
  contains(input.arg, "rm ")
}
EOF
./bin/runeward bundle push oci://localhost:5000/policies:v1 \
  --policy /tmp/prod.rego --engine rego --key /tmp/rw-keys/bundle.key --plain-http

# 3. Verify the pull independently (tamper the tag or key to see it fail closed)
./bin/runeward bundle pull oci://localhost:5000/policies:v1 \
  --verify-key /tmp/rw-keys/bundle.pub --plain-http

# 4. A profile that consumes it (examples/policy-bundle.toml as a template):
#    [policy_bundle]
#    ref        = "oci://localhost:5000/policies:v1"
#    verify_key = "<contents of /tmp/rw-keys/bundle.pub>"
#    plain_http = true
# Creating a sandbox from it pulls+verifies the bundle before the first action;
# an invalid signature or wrong key fails sandbox creation (fail-closed).

docker rm -f rw-registry
```

---

## 4. Deny-by-default egress (Docker, cooperative proxy)

The `egress-demo` profile denies all egress except `example.com`. runeward
starts an in-process host proxy and points the container at it via
`HTTP(S)_PROXY`.

```bash
EID=$(curl -s $BASE/v1/sandboxes -d '{"profile":"egress-demo"}' | jq -r .id)

# Allowed host -> succeeds
curl -s $BASE/v1/sandboxes/$EID/shell/exec \
  -d '{"command":["wget","-qO-","http://example.com/"]}' | jq '.exit_code'
# => 0

# Disallowed host -> blocked by the proxy (non-zero exit)
curl -s $BASE/v1/sandboxes/$EID/shell/exec \
  -d '{"command":["wget","-qO-","http://api.github.com/"]}' | jq '.exit_code'
# => non-zero
```

> Docker enforcement is **cooperative**: an app that ignores `HTTP(S)_PROXY`
> could bypass it. For bypass-resistant enforcement, use strict mode on
> Kubernetes (§6).

---

## 5. Snapshots (Docker)

```bash
SID=$(curl -s $BASE/v1/sandboxes -d '{"profile":"dev"}' | jq -r .id)
curl -s $BASE/v1/sandboxes/$SID/file/write -d '{"path":"state.txt","content":"v1"}' >/dev/null

# Capture a snapshot of the workspace
SNAP=$(curl -s $BASE/v1/sandboxes/$SID/snapshot -d '{"name":"v1"}' | jq -r .id)
curl -s $BASE/v1/snapshots | jq

# Restore into a brand-new governed sandbox
RID=$(curl -s -X POST $BASE/v1/snapshots/$SNAP/restore | jq -r .id)
curl -s $BASE/v1/sandboxes/$RID/file/read -d '{"path":"state.txt"}' | jq -r .stdout
# => v1
```

---

## 6. Kubernetes backend

Point runeward at your cluster (OrbStack exposes it as context
`orbstack`). Create the namespace and, for **strict egress**, allow the
privileged capabilities the iptables init container needs:

```bash
export RUNEWARD_KUBE_CONTEXT=orbstack        # or: kind-kind, minikube, ...
kubectl create namespace runeward

# Only needed for strict L3 egress (NET_ADMIN in an init container):
kubectl label namespace runeward \
  pod-security.kubernetes.io/enforce=privileged --overwrite
```

### 6a. Basic k8s sandbox

Add a k8s profile (or reuse `egress-strict` without the network block). Quick
inline test using the CLI against a k8s profile — create `examples/k8s.toml`:

```toml
[host]
type    = "k8s"
image   = "debian:stable-slim"
workdir = "/workspace"
[limits]
max_execs = 100
```

Then:

```bash
./bin/runeward --config-dir examples serve      # same control plane, now k8s-capable
KID=$(curl -s $BASE/v1/sandboxes -d '{"profile":"k8s"}' | jq -r .id)

# Watch the pod come up
kubectl -n runeward get pods

# Exec through the governed API (goes via client-go remotecommand)
curl -s $BASE/v1/sandboxes/$KID/shell/exec -d '{"command":["hostname"]}' | jq -r .stdout

curl -s -X DELETE $BASE/v1/sandboxes/$KID    # deletes the Pod + PVC
```

### 6b. Strict L3 egress (iptables + transparent SNI proxy)

Build the egress image **with iptables** and make it available to the cluster
(on OrbStack/Docker Desktop the local Docker images are shared with the built-in
Kubernetes, so no push is needed):

```bash
docker build -f deploy/Dockerfile.egress -t runeward-egress:latest .
```

Run a sandbox from the `egress-strict` profile (allowlist: `example.com`,
`*.githubusercontent.com`):

```bash
SID=$(curl -s $BASE/v1/sandboxes -d '{"profile":"egress-strict"}' | jq -r .id)

# Inspect the pod — you should see an init container (egress-init) and the
# egress sidecar alongside the sandbox container:
kubectl -n runeward get pod -l runeward.profile=egress-strict -o jsonpath='{.items[0].spec.initContainers[*].name}{"\n"}{.items[0].spec.containers[*].name}{"\n"}'

# Allowed host succeeds (traffic is transparently redirected through the proxy,
# which reads the TLS SNI / HTTP Host and matches the allowlist):
curl -s $BASE/v1/sandboxes/$SID/shell/exec \
  -d '{"command":["curl","-sS","-o","/dev/null","-w","%{http_code}","https://example.com/"]}' | jq -r .stdout

# Disallowed host is dropped even though nothing set HTTP_PROXY inside the app:
curl -s $BASE/v1/sandboxes/$SID/shell/exec \
  -d '{"command":["curl","-sS","--max-time","8","https://api.github.com/"]}' | jq '.exit_code'
# => non-zero (connection refused/reset by the proxy)

# See the proxy's allow/deny decisions:
kubectl -n runeward logs -l runeward.profile=egress-strict -c egress
```

> Strict mode is Linux/Kubernetes-only. The init container needs `NET_ADMIN`;
> the transparent proxy runs as uid `1337` and iptables exempts it, so the
> sandbox container must run as any other uid (the stock images do).

### 6c. Declarative CRDs + controller (`runeward up`)

Instead of the imperative REST API, you can manage sandboxes and fleets as
Kubernetes custom resources reconciled by the runeward controller.

Install the CRDs and controller in one command (idempotent, server-side apply):

```bash
# Build the image so the controller Deployment can start (shared with OrbStack k8s)
docker build -f deploy/Dockerfile -t runeward:latest .

# Install CRDs + namespace + RBAC + controller Deployment
./bin/runeward up

# Give the controller profiles to resolve
kubectl -n runeward create configmap runeward-profiles --from-file=examples/
kubectl -n runeward rollout restart deploy/runeward-controller
```

Or install just the CRDs and run the controller locally (handy for iterating):

```bash
./bin/runeward up --crds-only
RUNEWARD_K8S_NAMESPACE=runeward ./bin/runeward --config-dir examples controller
```

Then drive it declaratively (create `examples/k8s.toml` per §6a first):

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: runeward.dev/v1alpha1
kind: Sandbox
metadata: { name: demo, namespace: runeward }
spec: { profile: k8s }
EOF

# The controller provisions the pod and fills in .status
kubectl -n runeward get sandboxes
kubectl -n runeward get sandbox demo -o jsonpath='{.status}' ; echo
# => {"phase":"Running","sandboxId":"...","backend":"k8s","image":"debian:stable-slim"}
kubectl -n runeward get pods

# A Fleet works the same way
cat <<'EOF' | kubectl apply -f -
apiVersion: runeward.dev/v1alpha1
kind: Fleet
metadata: { name: crew, namespace: runeward }
spec: { profile: fleet-demo }
EOF
kubectl -n runeward get fleets

# Deleting a CR tears down the backing pods/fleet via a finalizer
kubectl -n runeward delete sandbox demo
```

Via Helm instead of `runeward up`:

```bash
helm install runeward deploy/helm/runeward -n runeward --create-namespace \
  --set image.tag=latest
helm lint deploy/helm/runeward   # what CI runs
```

Uninstall:

```bash
./bin/runeward up  # (no uninstall subcommand yet) — remove manually:
kubectl delete namespace runeward
kubectl delete crd sandboxes.runeward.dev fleets.runeward.dev clusterpolicies.runeward.dev clustersandboxes.runeward.dev clusterfleets.runeward.dev
```

### 6d. Org-wide policy defaults (`ClusterPolicy` + admission webhook)

Install the third CRD and enable the webhook via Helm (it self-registers its
validating/mutating configs and mints its own serving cert on startup):

```bash
./bin/runeward up --crds-only        # installs clusterpolicies.runeward.dev too
helm upgrade --install runeward deploy/helm/runeward -n runeward --create-namespace \
  --set image.tag=latest --set webhook.enabled=true
kubectl -n runeward rollout status deploy/runeward-webhook
```

Apply an org policy, then watch it default and gate Sandbox/Fleet creation:

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: runeward.dev/v1alpha1
kind: ClusterPolicy
metadata: { name: org-defaults }
spec:
  allowedProfiles: ["k8s", "fleet-*"]
  deniedProfiles: ["*-admin"]
  defaultProfile: "k8s"
  requiredLabels: ["owner"]
EOF

# Missing spec.profile -> mutating webhook fills in defaultProfile ("k8s")
cat <<'EOF' | kubectl apply -f -
apiVersion: runeward.dev/v1alpha1
kind: Sandbox
metadata: { name: defaulted, namespace: runeward, labels: { owner: alice } }
spec: {}
EOF
kubectl -n runeward get sandbox defaulted -o jsonpath='{.spec.profile}'; echo   # => k8s

# A denied profile is rejected by the validating webhook
cat <<'EOF' | kubectl apply -f -
apiVersion: runeward.dev/v1alpha1
kind: Sandbox
metadata: { name: blocked, namespace: runeward, labels: { owner: alice } }
spec: { profile: super-admin }
EOF
# => admission webhook "...": profile "super-admin" is denied by ClusterPolicy ...
```

> The webhook fails open (`failurePolicy: Ignore`), so a webhook outage never
> blocks legitimate work. The decision logic (defaulting, allow/deny globs,
> namespace + required-label checks) is unit-tested in `internal/webhook`.

### 6e. Cluster-scoped cells (`ClusterSandbox` / `ClusterFleet`)

For org-shared cells that shouldn't belong to any single team namespace, the
controller also reconciles the cluster-scoped `ClusterSandbox` / `ClusterFleet`.
`runeward up` installs all five CRDs; the controller watches the cluster-scoped
ones cluster-wide (via a `ClusterRole`) regardless of `--all-namespaces`:

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: runeward.dev/v1alpha1
kind: ClusterSandbox
metadata: { name: shared-cell }
spec: { profile: k8s }
EOF

kubectl get clustersandboxes            # short name: csbx
kubectl get clustersandbox shared-cell -o jsonpath='{.status.phase} {.status.sandboxId}'; echo

# A ClusterFleet fans out the profile's [fleet] replicas onto a shared board.
cat <<'EOF' | kubectl apply -f -
apiVersion: runeward.dev/v1alpha1
kind: ClusterFleet
metadata: { name: shared-crew }
spec: { profile: fleet-demo }
EOF
kubectl get clusterfleets               # short name: cflt

# Deleting the CR tears down the backing sandbox/fleet via its finalizer.
kubectl delete clustersandbox shared-cell
kubectl delete clusterfleet shared-crew
```

Cluster-scoped cells carry no `namespace`; the controller provisions their
backing Pods/PVCs in its own namespace. When the admission webhook is enabled,
`ClusterPolicy` governs them too (profile allow/deny + required labels; the
`allowedNamespaces` constraint is skipped for cluster-scoped resources).

---

## 7. Multi-agent fleets

A fleet is N sandboxes sharing one atomic task board.

```bash
# Create the fleet (fleet-demo => 2 sandboxes seeded with 3 tasks)
FID=$(curl -s $BASE/v1/fleets -d '{"profile":"fleet-demo"}' | jq -r .id)
curl -s $BASE/v1/fleets/$FID | jq '{sandboxes, stats}'

# A worker atomically claims the next task
TASK=$(curl -s $BASE/v1/fleets/$FID/claim -d '{"owner":"worker-1"}')
echo "$TASK" | jq
TID=$(echo "$TASK" | jq -r '.task.id')

# Complete it (or /fail with {"error":"...","requeue":true})
curl -s -X POST $BASE/v1/fleets/$FID/tasks/$TID/complete -d '{"result":"done"}' | jq

# Add a new task and list the board
curl -s $BASE/v1/fleets/$FID/tasks -d '{"payload":"extra work"}' | jq
curl -s $BASE/v1/fleets/$FID/tasks | jq '.tasks[] | {id, state, owner}'

# Tear the whole fleet down
curl -s -X DELETE $BASE/v1/fleets/$FID | jq
```

The dashboard's **Fleets** view shows the same board live, with per-task
claim/complete/fail controls.

---

## 8. MCP integration (Claude Desktop, Cursor, VS Code)

runeward speaks the Model Context Protocol over **stdio** (for editor/desktop
clients) or **streamable HTTP** (mounted at `/mcp` on `serve`, or standalone via
`runeward mcp --http`). Tools exposed:

- Sandboxes: `runeward_create_sandbox`, `runeward_shell`, `runeward_python`,
  `runeward_node`, `runeward_browser`, `runeward_read_file`, `runeward_write_file`,
  `runeward_list_files`, `runeward_search_files`, `runeward_list_approvals`,
  `runeward_kill_sandbox`.
- Fleets: `runeward_create_fleet`, `runeward_list_fleets`, `runeward_list_tasks`,
  `runeward_add_task`, `runeward_claim_task`, `runeward_complete_task`,
  `runeward_fail_task`, `runeward_kill_fleet`.

Use an **absolute path** to the binary and profiles in client configs. Get them:

```bash
echo "$(pwd)/bin/runeward"
echo "$(pwd)/examples"
```

### 8a. Quick HTTP sanity check (no client needed)

The streamable-HTTP transport requires the MCP `initialize` handshake before any
other call, and replies as Server-Sent Events. The handshake returns an
`Mcp-Session-Id` header you must echo on subsequent requests:

```bash
./bin/runeward --config-dir examples mcp --http --port 8081 &
# runeward: MCP (streamable HTTP) at http://localhost:8081/mcp

H=(-H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream')

# 1) initialize — grab the session id from the response headers
SID=$(curl -s -D - -o /dev/null http://localhost:8081/mcp "${H[@]}" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}' \
  | tr -d '\r' | awk -F': ' 'tolower($1)=="mcp-session-id"{print $2}')

# 2) tell the server we're initialized
curl -s -o /dev/null http://localhost:8081/mcp "${H[@]}" -H "Mcp-Session-Id: $SID" \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'

# 3) list tools — expect all sandbox + fleet tools
curl -s http://localhost:8081/mcp "${H[@]}" -H "Mcp-Session-Id: $SID" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  | sed -n 's/^data: //p' | grep -o '"name":"runeward_[a-z_]*"' | sort -u
```

(Real MCP clients — Claude, Cursor, VS Code — do this handshake for you; the
manual steps above are just for a dependency-free smoke test.)

### 8b. Claude Desktop (stdio)

Edit `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "runeward": {
      "command": "/ABSOLUTE/PATH/sandbox/bin/runeward",
      "args": ["--config-dir", "/ABSOLUTE/PATH/sandbox/examples", "mcp"],
      "env": { "RUNEWARD_STATE_DIR": "/ABSOLUTE/PATH/runeward-state" }
    }
  }
}
```

Restart Claude Desktop → the runeward tools appear under the tools (plug) icon.
Try: *"Create a runeward sandbox from the `dev` profile and run `uname -a`."*

### 8c. Cursor (stdio)

Create `~/.cursor/mcp.json` (global) or `.cursor/mcp.json` (per-project):

```json
{
  "mcpServers": {
    "runeward": {
      "command": "/ABSOLUTE/PATH/sandbox/bin/runeward",
      "args": ["--config-dir", "/ABSOLUTE/PATH/sandbox/examples", "mcp"]
    }
  }
}
```

Open **Cursor → Settings → MCP** and confirm `runeward` is connected (green),
then ask the agent to use a runeward tool.

### 8d. VS Code (Copilot agent mode, stdio)

Create `.vscode/mcp.json` in the workspace:

```json
{
  "servers": {
    "runeward": {
      "type": "stdio",
      "command": "/ABSOLUTE/PATH/sandbox/bin/runeward",
      "args": ["--config-dir", "/ABSOLUTE/PATH/sandbox/examples", "mcp"]
    }
  }
}
```

Open the **Chat** view, switch to **Agent** mode, press the tools icon and
enable `runeward`. (Requires VS Code ≥ 1.102 with MCP support.) You can also
point any HTTP-capable client at `serve`'s `/mcp` endpoint instead of stdio.

---

## 9. Framework adapters (optional)

The [`adapters/`](https://github.com/Runewardd/runeward/tree/main/adapters) directory has thin clients over the REST API.

```bash
# TypeScript
cd adapters/typescript && npm install && npm run build   # then import the client

# Python
cd adapters/python && pip install -e .                   # then import the client
```

Point each client at `http://localhost:8080` (a running `serve`) and call the
sandbox tools. Both packages also ship lazy-loaded framework tool factories —
LangChain, CrewAI, LlamaIndex, the OpenAI Agents SDK, and Strands (Python); the
Vercel AI SDK, LangChain.js, and Strands (TypeScript) — see [Adapters](adapters.md)
and `adapters/README.md`.

---

## 10. Cleanup

```bash
# Stop `serve` / `mcp` with Ctrl-C.

# Docker: remove leftover sandbox containers (labeled by runeward)
docker ps -a --filter "label=runeward.profile" -q | xargs -r docker rm -f

# Kubernetes: drop the namespace (Pods, PVCs, ConfigMaps go with it)
kubectl delete namespace runeward

# Wipe the audit ledger / snapshots for this run
rm -rf "$RUNEWARD_STATE_DIR"
```

---

## Troubleshooting

| Symptom | Likely cause / fix |
|---------|--------------------|
| `serve` errors creating a sandbox | Docker/OrbStack not running (`docker info`), or the profile's image isn't pulled. |
| K8s sandbox stuck `Pending` | No default StorageClass for the workspace PVC, or the image can't be pulled by the cluster. `kubectl -n runeward describe pod ...`. |
| Strict egress: init container `CreateContainerError`/denied | Namespace PodSecurity blocks `NET_ADMIN` — label it `privileged` (§6). |
| Strict egress: `runeward-egress` ImagePullBackOff | Build the image (`deploy/Dockerfile.egress`) or set `RUNEWARD_EGRESS_IMAGE` to a pushed ref. |
| Strict egress: allowed host still blocked | The app connected by raw IP with no SNI/Host — add a `cidr` rule, or use a hostname. Check the `egress` sidecar logs. |
| MCP client shows no tools | Use absolute paths in the config; check the client's MCP logs; verify `bin/runeward mcp` runs standalone. |
| Approval call never returns | Approve/deny it via `GET /v1/approvals` + `POST /v1/approvals/{id}/approve`, or the dashboard drawer. |
