// Package mcp exposes runeward's governed tools over the Model Context
// Protocol, going through the same policy/guardrails/Chronicle (audit) path as
// the REST API. A policy deny surfaces as a tool error; require-approval returns
// guidance telling the agent to pause for a human rather than retry.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Runewardd/runeward/internal/authz"
	"github.com/Runewardd/runeward/internal/browser"
	"github.com/Runewardd/runeward/internal/controlplane"
	"github.com/Runewardd/runeward/internal/credentials"
	"github.com/Runewardd/runeward/internal/profile"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is the reported MCP server implementation version.
const Version = "0.3.0"

const (
	// EnvMCPDefaultPrincipal names the stdio principal to use when no HTTP
	// request context exists.
	EnvMCPDefaultPrincipal = "RUNEWARD_MCP_DEFAULT_PRINCIPAL"
	// EnvMCPDefaultToken maps stdio sessions to an authz principal when
	// RUNEWARD_AUTHZ_FILE is configured.
	EnvMCPDefaultToken = "RUNEWARD_MCP_DEFAULT_TOKEN" // #nosec G101 -- environment variable name, not a credential value
)

type principalIdentity struct {
	Owner     string
	Actor     string
	Principal *authz.Principal
}

func (p principalIdentity) canLaunch(profileName string) bool {
	if p.Principal == nil {
		return true
	}
	return p.Principal.CanLaunch(profileName)
}

func (p principalIdentity) isAdmin() bool {
	return p.Principal != nil && p.Principal.Admin
}

func (p principalIdentity) mayApprove() bool {
	return p.Principal == nil || p.Principal.MayApprove()
}

type principalResolver struct {
	store          *authz.Store
	stdioOwner     string
	stdioActor     string
	stdioPrincipal *authz.Principal
}

func newPrincipalResolver() (*principalResolver, error) {
	store, err := authz.FromEnv()
	if err != nil {
		return nil, err
	}
	r := &principalResolver{
		store:      store,
		stdioOwner: strings.TrimSpace(os.Getenv(EnvMCPDefaultPrincipal)),
	}
	if r.stdioOwner == "" {
		r.stdioOwner = "mcp-stdio"
	}
	r.stdioActor = r.stdioOwner
	if store == nil {
		return r, nil
	}
	tok := strings.TrimSpace(os.Getenv(EnvMCPDefaultToken))
	if tok == "" {
		tok = credentials.LoadToken()
	}
	if tok == "" {
		// HTTP sessions resolve the bearer token per request. Stdio calls will
		// receive a clear error from resolve unless a default or stored token exists.
		return r, nil
	}
	p, ok := store.Identify(tok)
	if !ok {
		return nil, fmt.Errorf("%s does not match any principal in %s", EnvMCPDefaultToken, authz.EnvFile)
	}
	r.stdioOwner = p.TenantID()
	r.stdioActor = p.Name
	r.stdioPrincipal = p
	return r, nil
}

func (r *principalResolver) resolve(req *sdk.CallToolRequest) (principalIdentity, error) {
	if req == nil || req.GetExtra() == nil {
		if r.store != nil && r.stdioPrincipal == nil {
			return principalIdentity{}, fmt.Errorf("%s or `runeward auth login` is required for MCP stdio when authentication is configured", EnvMCPDefaultToken)
		}
		return principalIdentity{Owner: r.stdioOwner, Actor: r.stdioActor, Principal: r.stdioPrincipal}, nil
	}
	authHeader := ""
	if h := req.GetExtra().Header; h != nil {
		authHeader = h.Get("Authorization")
	}
	tok, ok := parseBearerToken(authHeader)
	if !ok {
		return principalIdentity{}, fmt.Errorf("missing bearer token")
	}
	if r.store == nil {
		// Legacy HTTP mode is authenticated by the outer shared-token middleware.
		// It has one shared ownership boundary rather than per-principal RBAC.
		return principalIdentity{Owner: "mcp-http", Actor: "mcp-http"}, nil
	}
	p, ok := r.store.Identify(tok)
	if !ok {
		return principalIdentity{}, fmt.Errorf("unknown bearer token")
	}
	return principalIdentity{Owner: p.TenantID(), Actor: p.Name, Principal: p}, nil
}

func authorizeSandbox(req *sdk.CallToolRequest, resolver *principalResolver, resolverErr error, mgr *controlplane.Manager, id string) (principalIdentity, error) {
	if resolverErr != nil {
		return principalIdentity{}, resolverErr
	}
	p, err := resolver.resolve(req)
	if err != nil {
		return principalIdentity{}, err
	}
	owner, ok := mgr.SandboxOwner(id)
	if !ok || (p.Principal != nil && !p.isAdmin() && owner != p.Owner) {
		return principalIdentity{}, fmt.Errorf("citadel not found")
	}
	return p, nil
}

func authorizeSandboxContext(ctx context.Context, req *sdk.CallToolRequest, resolver *principalResolver, resolverErr error, mgr *controlplane.Manager, id string) (context.Context, error) {
	p, err := authorizeSandbox(req, resolver, resolverErr, mgr, id)
	if err != nil {
		return ctx, err
	}
	return controlplane.WithActor(ctx, p.Actor), nil
}

func authorizeFleet(req *sdk.CallToolRequest, resolver *principalResolver, resolverErr error, mgr *controlplane.Manager, id string) (principalIdentity, error) {
	if resolverErr != nil {
		return principalIdentity{}, resolverErr
	}
	p, err := resolver.resolve(req)
	if err != nil {
		return principalIdentity{}, err
	}
	owner, ok := mgr.FleetOwner(id)
	if !ok || (p.Principal != nil && !p.isAdmin() && owner != p.Owner) {
		return principalIdentity{}, fmt.Errorf("cohort not found")
	}
	return p, nil
}

func parseBearerToken(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", false
	}
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", false
	}
	if parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

// NewServer builds an MCP server with runeward's governed tools registered
// against mgr.
func NewServer(mgr *controlplane.Manager) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "runeward", Version: Version}, nil)
	resolver, resolverErr := newPrincipalResolver()

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_whoami",
		Description: "Return the authenticated Runeward principal and its effective launch and approval scopes.",
	}, func(_ context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
		if resolverErr != nil {
			return errText(resolverErr), nil, nil
		}
		p, err := resolver.resolve(req)
		if err != nil {
			return errText(err), nil, nil
		}
		principal := map[string]any{"name": p.Actor, "tenant": p.Owner, "admin": true, "can_launch": true, "can_approve": true}
		if p.Principal != nil {
			principal = map[string]any{
				"name": p.Actor, "tenant": p.Owner, "admin": p.Principal.Admin,
				"can_launch": p.Principal.MayLaunch(), "can_approve": p.Principal.MayApprove(),
				"allowed_profiles":  p.Principal.AllowedProfiles,
				"approval_profiles": p.Principal.ApprovalProfiles,
			}
		}
		return structured(map[string]any{"authenticated": true, "rbac": p.Principal != nil, "principal": principal}), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_list_charters",
		Description: "List the Runeward Charters this principal is allowed to launch.",
	}, func(_ context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
		if resolverErr != nil {
			return errText(resolverErr), nil, nil
		}
		p, err := resolver.resolve(req)
		if err != nil {
			return errText(err), nil, nil
		}
		profiles, err := mgr.ListProfiles()
		if err != nil {
			return errText(err), nil, nil
		}
		if p.Principal != nil && !p.isAdmin() {
			filtered := profiles[:0]
			for _, info := range profiles {
				if p.canLaunch(info.Name) {
					filtered = append(filtered, info)
				}
			}
			profiles = filtered
		}
		return structured(map[string]any{"charters": profiles}), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_readiness",
		Description: "Validate one launchable Charter and report policy, runtime, and image readiness.",
	}, func(_ context.Context, req *sdk.CallToolRequest, in struct {
		Profile string `json:"profile" jsonschema:"the Charter to validate"`
	}) (*sdk.CallToolResult, any, error) {
		if resolverErr != nil {
			return errText(resolverErr), nil, nil
		}
		p, err := resolver.resolve(req)
		if err != nil {
			return errText(err), nil, nil
		}
		if !p.canLaunch(in.Profile) {
			return errText(fmt.Errorf("charter not found")), nil, nil
		}
		return structured(mgr.CheckReadiness(in.Profile)), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_create_citadel",
		Description: "Provision a governed, isolated Citadel (sandbox) from a named runeward Charter (profile) and return its id. Use this before running any other tool.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Profile       string `json:"profile" jsonschema:"the runeward Charter (profile) to provision (e.g. dev)"`
		CopyFrom      string `json:"copy_from,omitempty" jsonschema:"optional admin-only local directory copied into the fresh workspace"`
		ParentCitadel string `json:"parent_citadel,omitempty" jsonschema:"optional parent Citadel for delegated-agent lineage"`
		RunID         string `json:"run_id,omitempty" jsonschema:"optional caller-supplied run correlation id"`
		Agent         string `json:"agent,omitempty" jsonschema:"agent name, such as codex or claude"`
		Provider      string `json:"provider,omitempty" jsonschema:"model provider"`
		Model         string `json:"model,omitempty" jsonschema:"model identifier"`
	}) (*sdk.CallToolResult, any, error) {
		if resolverErr != nil {
			return errText(resolverErr), nil, nil
		}
		principal, err := resolver.resolve(req)
		if err != nil {
			return errText(err), nil, nil
		}
		if !principal.canLaunch(in.Profile) {
			return errText(fmt.Errorf("principal %q is not allowed to launch charter %q", principal.Actor, in.Profile)), nil, nil
		}
		if strings.TrimSpace(in.CopyFrom) != "" && principal.Principal != nil && !principal.isAdmin() {
			return errText(fmt.Errorf("copy_from overrides require an administrator")), nil, nil
		}
		if strings.TrimSpace(in.CopyFrom) != "" && principal.Principal != nil && strings.TrimSpace(os.Getenv("RUNEWARD_COPY_FROM_ROOTS")) == "" {
			return errText(fmt.Errorf("copy_from is disabled under RBAC until RUNEWARD_COPY_FROM_ROOTS is configured")), nil, nil
		}
		sb, err := mgr.CreateSandbox(ctx, in.Profile, controlplane.CreateOptions{
			CopyFrom: in.CopyFrom, Owner: principal.Owner, Actor: principal.Actor, ParentSandbox: in.ParentCitadel,
			RunID: in.RunID, Agent: in.Agent, Provider: in.Provider, Model: in.Model,
		})
		if err != nil {
			return errText(err), nil, nil
		}
		return structured(map[string]any{"id": sb.ID, "profile": sb.Profile, "backend": sb.Backend, "image": sb.Image, "status": sb.Status}), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_list_citadels",
		Description: "List Citadels visible to the authenticated principal.",
	}, func(_ context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
		if resolverErr != nil {
			return errText(resolverErr), nil, nil
		}
		p, err := resolver.resolve(req)
		if err != nil {
			return errText(err), nil, nil
		}
		items := make([]map[string]any, 0)
		for _, info := range mgr.ListSandboxInfos() {
			if p.Principal != nil && !p.isAdmin() && info.Owner != p.Owner {
				continue
			}
			items = append(items, map[string]any{"id": info.Sandbox.ID, "profile": info.Sandbox.Profile, "backend": info.Sandbox.Backend, "image": info.Sandbox.Image, "status": info.Sandbox.Status, "owner": info.Owner})
		}
		return structured(map[string]any{"citadels": items}), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_list_runs",
		Description: "List durable agent runs and parent/child lineage visible to the authenticated tenant.",
	}, func(_ context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
		if resolverErr != nil {
			return errText(resolverErr), nil, nil
		}
		p, err := resolver.resolve(req)
		if err != nil {
			return errText(err), nil, nil
		}
		runs := mgr.ListRuns()
		if p.Principal != nil && !p.isAdmin() {
			filtered := make([]controlplane.Run, 0, len(runs))
			for _, run := range runs {
				if run.Tenant == p.Owner {
					filtered = append(filtered, run)
				}
			}
			runs = filtered
		}
		return structured(map[string]any{"runs": runs}), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_shell",
		Description: "Run a shell command (argv form) in a Citadel. Subject to policy: may be denied or require human approval.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Sandbox string   `json:"sandbox" jsonschema:"the citadel id"`
		Command []string `json:"command" jsonschema:"the command as an argv array, e.g. [\"ls\",\"-la\"]"`
		Workdir string   `json:"workdir,omitempty" jsonschema:"optional working directory"`
	}) (*sdk.CallToolResult, any, error) {
		var err error
		if ctx, err = authorizeSandboxContext(ctx, req, resolver, resolverErr, mgr, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		res, err := mgr.Shell(ctx, in.Sandbox, in.Command, in.Workdir)
		return execResult(res, err), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_browser",
		Description: "Fetch a URL with a headless browser inside the Citadel and return the rendered page. mode 'text' returns the rendered DOM HTML; 'screenshot' returns a base64 PNG. Subject to policy (tool 'browser', arg = url) and the profile's egress allowlist.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Sandbox string `json:"sandbox" jsonschema:"the citadel id"`
		URL     string `json:"url" jsonschema:"the URL to load"`
		Mode    string `json:"mode,omitempty" jsonschema:"'text' (default) or 'screenshot'"`
	}) (*sdk.CallToolResult, any, error) {
		var err error
		if ctx, err = authorizeSandboxContext(ctx, req, resolver, resolverErr, mgr, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		res, err := mgr.Browser(ctx, in.Sandbox, in.URL, in.Mode)
		return execResult(res, err), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_browser_open",
		Description: "Open a STATEFUL, CDP-driven browser session inside the Citadel and return its session id. The page (cookies, DOM, storage) persists across runeward_browser_act calls until runeward_browser_close. Subject to policy (tool 'browser') and the profile's egress allowlist.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Sandbox string `json:"sandbox" jsonschema:"the citadel id"`
	}) (*sdk.CallToolResult, any, error) {
		var err error
		if ctx, err = authorizeSandboxContext(ctx, req, resolver, resolverErr, mgr, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		sid, res, err := mgr.BrowserOpen(ctx, in.Sandbox)
		if err != nil {
			return errText(err), nil, nil
		}
		if res != nil && res.Verdict != profile.VerdictAllow {
			return execResult(res, nil), nil, nil
		}
		return text(fmt.Sprintf("browser session %s opened", sid)), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_browser_act",
		Description: "Run one action against an open browser session. action is one of navigate|eval|text|html|screenshot|click|type|wait|title|url. Provide url (navigate), selector (click/type/wait), expr (eval JS), or text (type). Returns the textual value, or a base64 PNG for screenshot. Governed per action.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Sandbox   string `json:"sandbox" jsonschema:"the citadel id"`
		Session   string `json:"session" jsonschema:"the browser session id from runeward_browser_open"`
		Action    string `json:"action" jsonschema:"navigate|eval|text|html|screenshot|click|type|wait|title|url"`
		URL       string `json:"url,omitempty" jsonschema:"URL for action=navigate"`
		Selector  string `json:"selector,omitempty" jsonschema:"CSS selector for click/type/wait"`
		Expr      string `json:"expr,omitempty" jsonschema:"JavaScript source for action=eval"`
		Text      string `json:"text,omitempty" jsonschema:"text to type for action=type"`
		TimeoutMS int    `json:"timeout_ms,omitempty" jsonschema:"optional per-action timeout in ms"`
	}) (*sdk.CallToolResult, any, error) {
		var err error
		if ctx, err = authorizeSandboxContext(ctx, req, resolver, resolverErr, mgr, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		res, err := mgr.BrowserAct(ctx, in.Sandbox, in.Session, browser.Command{
			Action: in.Action, URL: in.URL, Selector: in.Selector,
			Expr: in.Expr, Text: in.Text, TimeoutMS: in.TimeoutMS,
		})
		return execResult(res, err), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_browser_close",
		Description: "Close an open browser session (shuts down the in-sandbox Chromium and frees its resources).",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Sandbox string `json:"sandbox" jsonschema:"the citadel id"`
		Session string `json:"session" jsonschema:"the browser session id"`
	}) (*sdk.CallToolResult, any, error) {
		var err error
		if ctx, err = authorizeSandboxContext(ctx, req, resolver, resolverErr, mgr, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		if err := mgr.BrowserClose(ctx, in.Sandbox, in.Session); err != nil {
			return errText(err), nil, nil
		}
		return text(fmt.Sprintf("browser session %s closed", in.Session)), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_python",
		Description: "Run a Python 3 snippet in a Citadel via python3 -c.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Sandbox string `json:"sandbox" jsonschema:"the citadel id"`
		Code    string `json:"code" jsonschema:"Python source to execute"`
	}) (*sdk.CallToolResult, any, error) {
		var err error
		if ctx, err = authorizeSandboxContext(ctx, req, resolver, resolverErr, mgr, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		res, err := mgr.Python(ctx, in.Sandbox, in.Code)
		return execResult(res, err), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_node",
		Description: "Run a JavaScript snippet in a Citadel via node -e.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Sandbox string `json:"sandbox" jsonschema:"the citadel id"`
		Code    string `json:"code" jsonschema:"JavaScript source to execute"`
	}) (*sdk.CallToolResult, any, error) {
		var err error
		if ctx, err = authorizeSandboxContext(ctx, req, resolver, resolverErr, mgr, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		res, err := mgr.Node(ctx, in.Sandbox, in.Code)
		return execResult(res, err), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_read_file",
		Description: "Read a file from a Citadel and return its contents.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Sandbox string `json:"sandbox" jsonschema:"the citadel id"`
		Path    string `json:"path" jsonschema:"the file path to read"`
	}) (*sdk.CallToolResult, any, error) {
		var err error
		if ctx, err = authorizeSandboxContext(ctx, req, resolver, resolverErr, mgr, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		res, err := mgr.FileRead(ctx, in.Sandbox, in.Path)
		return rawResult(res, err), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_write_file",
		Description: "Write content to a file in a Citadel (creating parent directories). May require approval.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Sandbox string `json:"sandbox" jsonschema:"the citadel id"`
		Path    string `json:"path" jsonschema:"the file path to write"`
		Content string `json:"content" jsonschema:"the file content"`
	}) (*sdk.CallToolResult, any, error) {
		var err error
		if ctx, err = authorizeSandboxContext(ctx, req, resolver, resolverErr, mgr, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		res, err := mgr.FileWrite(ctx, in.Sandbox, in.Path, in.Content)
		if blocked := blockedResult(res, err); blocked != nil {
			return blocked, nil, nil
		}
		return structured(map[string]any{"bytes": len(in.Content), "path": in.Path, "verdict": "allow"}), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_list_files",
		Description: "List a directory in a Citadel (ls -la).",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Sandbox string `json:"sandbox" jsonschema:"the citadel id"`
		Path    string `json:"path,omitempty" jsonschema:"the directory to list (default .)"`
	}) (*sdk.CallToolResult, any, error) {
		var err error
		if ctx, err = authorizeSandboxContext(ctx, req, resolver, resolverErr, mgr, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		res, err := mgr.FileList(ctx, in.Sandbox, in.Path)
		return rawResult(res, err), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_search_files",
		Description: "Recursively search for text in a Citadel (grep -rn).",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Sandbox string `json:"sandbox" jsonschema:"the citadel id"`
		Query   string `json:"query" jsonschema:"the text to search for"`
		Path    string `json:"path,omitempty" jsonschema:"the root to search under (default .)"`
	}) (*sdk.CallToolResult, any, error) {
		var err error
		if ctx, err = authorizeSandboxContext(ctx, req, resolver, resolverErr, mgr, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		res, err := mgr.FileSearch(ctx, in.Sandbox, in.Query, in.Path)
		return rawResult(res, err), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_list_conclave",
		Description: "List pending human-in-the-loop Conclave (approval) requests across all Citadels.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
		if resolverErr != nil {
			return errText(resolverErr), nil, nil
		}
		principal, err := resolver.resolve(req)
		if err != nil {
			return errText(err), nil, nil
		}
		if !principal.mayApprove() {
			return errText(fmt.Errorf("not authorized to view conclave decisions")), nil, nil
		}
		list := mgr.Approvals().List()
		if principal.Principal != nil && !principal.isAdmin() {
			filtered := list[:0]
			for _, approval := range list {
				sb, ok := mgr.Sandbox(approval.Sandbox)
				if ok && principal.Principal.CanApproveProfile(sb.Profile) {
					filtered = append(filtered, approval)
				}
			}
			list = filtered
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Created.Before(list[j].Created) })
		return structured(map[string]any{"approvals": list}), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_kill_citadel",
		Description: "Tear down a Citadel (sandbox) and free its resources.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Sandbox string `json:"sandbox" jsonschema:"the citadel id"`
	}) (*sdk.CallToolResult, any, error) {
		var err error
		if ctx, err = authorizeSandboxContext(ctx, req, resolver, resolverErr, mgr, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		if err := mgr.KillSandbox(ctx, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		return structured(map[string]any{"ok": true, "citadel": in.Sandbox}), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_report_usage",
		Description: "Report model token and cost usage for budget enforcement on a Citadel.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Sandbox string  `json:"sandbox"`
		Tokens  int64   `json:"tokens"`
		CostUSD float64 `json:"cost_usd"`
	}) (*sdk.CallToolResult, any, error) {
		var err error
		if ctx, err = authorizeSandboxContext(ctx, req, resolver, resolverErr, mgr, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		if err := mgr.RecordUsageContext(ctx, in.Sandbox, in.Tokens, in.CostUSD); err != nil {
			return errText(err), nil, nil
		}
		return structured(mgr.SandboxUsage(in.Sandbox)), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_publish_conversation",
		Description: "Publish one user, assistant, tool, or system turn to a Citadel's read-only Live chat TTY. Agent harnesses should call this for each turn they want human observers to follow.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Sandbox string `json:"sandbox" jsonschema:"the citadel id"`
		Role    string `json:"role" jsonschema:"turn role: user, assistant, tool, or system"`
		Content string `json:"content" jsonschema:"turn content; secrets are redacted before broadcast"`
		RunID   string `json:"run_id,omitempty" jsonschema:"optional agent run id"`
	}) (*sdk.CallToolResult, any, error) {
		var err error
		if ctx, err = authorizeSandboxContext(ctx, req, resolver, resolverErr, mgr, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		msg, err := mgr.PublishConversation(ctx, in.Sandbox, in.Role, in.Content, in.RunID)
		if err != nil {
			return errText(err), nil, nil
		}
		return structured(msg), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_list_conversation",
		Description: "Return the bounded, redacted Live chat history for a Citadel.",
	}, func(_ context.Context, req *sdk.CallToolRequest, in struct {
		Sandbox string `json:"sandbox" jsonschema:"the citadel id"`
		AfterID uint64 `json:"after_id,omitempty" jsonschema:"return messages newer than this id"`
		Limit   int    `json:"limit,omitempty" jsonschema:"maximum messages, 1-500"`
	}) (*sdk.CallToolResult, any, error) {
		if _, err := authorizeSandbox(req, resolver, resolverErr, mgr, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		messages, err := mgr.ConversationHistory(in.Sandbox, in.AfterID, in.Limit)
		if err != nil {
			return errText(err), nil, nil
		}
		return structured(map[string]any{"messages": messages}), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_snapshot_citadel",
		Description: "Create a tenant-scoped recovery snapshot of a Citadel workspace.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Sandbox string `json:"sandbox"`
		Name    string `json:"name,omitempty"`
	}) (*sdk.CallToolResult, any, error) {
		var err error
		if ctx, err = authorizeSandboxContext(ctx, req, resolver, resolverErr, mgr, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		ref, err := mgr.Snapshot(ctx, in.Sandbox, in.Name)
		if err != nil {
			return errText(err), nil, nil
		}
		return structured(ref), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_list_snapshots",
		Description: "List recovery snapshots visible to the authenticated principal.",
	}, func(_ context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
		if resolverErr != nil {
			return errText(resolverErr), nil, nil
		}
		p, err := resolver.resolve(req)
		if err != nil {
			return errText(err), nil, nil
		}
		refs := mgr.ListSnapshots()
		if p.Principal != nil && !p.isAdmin() {
			filtered := refs[:0]
			for _, ref := range refs {
				if owner, ok := mgr.SnapshotOwner(ref.ID); ok && owner == p.Owner {
					filtered = append(filtered, ref)
				}
			}
			refs = filtered
		}
		return structured(map[string]any{"snapshots": refs}), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_export_evidence",
		Description: "Return the portable signed evidence document for a Citadel, including its resolved Charter and Chronicle bundle.",
	}, func(_ context.Context, req *sdk.CallToolRequest, in struct {
		Sandbox string `json:"sandbox" jsonschema:"the citadel id"`
	}) (*sdk.CallToolResult, any, error) {
		if _, err := authorizeSandbox(req, resolver, resolverErr, mgr, in.Sandbox); err != nil {
			return errText(err), nil, nil
		}
		doc, err := mgr.Evidence(in.Sandbox, Version)
		if err != nil {
			return errText(err), nil, nil
		}
		return structured(doc), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_verify_chronicle",
		Description: "Verify the Chronicle hash chain and signatures before trusting or exporting evidence.",
	}, func(_ context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
		if resolverErr != nil {
			return errText(resolverErr), nil, nil
		}
		if _, err := resolver.resolve(req); err != nil {
			return errText(err), nil, nil
		}
		if err := mgr.VerifyLedger(); err != nil {
			return structured(map[string]any{"ok": false, "error": err.Error()}), nil, nil
		}
		return structured(map[string]any{"ok": true}), nil, nil
	})

	registerFleetTools(s, mgr, resolver, resolverErr)
	return s
}

// registerFleetTools adds the Cohort orchestration tools (a Cohort is N Citadels
// sharing a Command Board).
func registerFleetTools(s *sdk.Server, mgr *controlplane.Manager, resolver *principalResolver, resolverErr error) {
	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_create_cohort",
		Description: "Provision a Cohort (fleet): N governed Citadels (from the Charter's [cohort].replicas) sharing an atomic Command Board seeded from the Charter's task_board. Returns the cohort id and member citadel ids.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Profile string `json:"profile" jsonschema:"the runeward Charter (profile) to provision the Cohort from"`
	}) (*sdk.CallToolResult, any, error) {
		if resolverErr != nil {
			return errText(resolverErr), nil, nil
		}
		principal, err := resolver.resolve(req)
		if err != nil {
			return errText(err), nil, nil
		}
		if !principal.canLaunch(in.Profile) {
			return errText(fmt.Errorf("principal %q is not allowed to launch charter %q", principal.Actor, in.Profile)), nil, nil
		}
		v, err := mgr.CreateFleetForIdentity(ctx, in.Profile, principal.Owner, principal.Actor)
		if err != nil {
			return errText(err), nil, nil
		}
		return structured(v), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_list_cohorts",
		Description: "List all Cohorts with their Citadel members and Command Board stats.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
		if resolverErr != nil {
			return errText(resolverErr), nil, nil
		}
		principal, err := resolver.resolve(req)
		if err != nil {
			return errText(err), nil, nil
		}
		fleets := mgr.ListFleets()
		if principal.Principal != nil && !principal.isAdmin() {
			filtered := fleets[:0]
			for _, f := range fleets {
				if f.Owner == principal.Owner {
					filtered = append(filtered, f)
				}
			}
			fleets = filtered
		}
		return structured(map[string]any{"cohorts": fleets}), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_list_tasks",
		Description: "List the tasks on a Cohort's Command Board with their state, owner, and results.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Fleet string `json:"fleet" jsonschema:"the cohort id"`
	}) (*sdk.CallToolResult, any, error) {
		if _, err := authorizeFleet(req, resolver, resolverErr, mgr, in.Fleet); err != nil {
			return errText(err), nil, nil
		}
		tasks, err := mgr.ListTasks(in.Fleet)
		if err != nil {
			return errText(err), nil, nil
		}
		return structured(map[string]any{"tasks": tasks}), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_add_task",
		Description: "Add a task to a Cohort's Command Board.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Fleet   string `json:"fleet" jsonschema:"the cohort id"`
		Payload string `json:"payload" jsonschema:"the task description/payload"`
	}) (*sdk.CallToolResult, any, error) {
		if _, err := authorizeFleet(req, resolver, resolverErr, mgr, in.Fleet); err != nil {
			return errText(err), nil, nil
		}
		t, err := mgr.AddTask(in.Fleet, in.Payload)
		if err != nil {
			return errText(err), nil, nil
		}
		return structured(t), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_claim_task",
		Description: "Atomically claim the next pending task from a Cohort's Command Board for a worker. Returns the task, or reports the board is empty.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Fleet string `json:"fleet" jsonschema:"the cohort id"`
	}) (*sdk.CallToolResult, any, error) {
		if resolverErr != nil {
			return errText(resolverErr), nil, nil
		}
		principal, err := resolver.resolve(req)
		if err != nil {
			return errText(err), nil, nil
		}
		if _, err := authorizeFleet(req, resolver, resolverErr, mgr, in.Fleet); err != nil {
			return errText(err), nil, nil
		}
		t, ok, err := mgr.ClaimTask(in.Fleet, principal.Actor)
		if err != nil {
			return errText(err), nil, nil
		}
		if !ok {
			return structured(map[string]any{"claimed": false}), nil, nil
		}
		return structured(map[string]any{"claimed": true, "task": t}), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_heartbeat_task",
		Description: "Extend the lease on a task a worker still holds so the Cohort sweeper does not requeue it as a dead worker.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Fleet      string `json:"fleet" jsonschema:"the cohort id"`
		Task       string `json:"task" jsonschema:"the task id"`
		LeaseToken string `json:"lease_token" jsonschema:"the signed lease token returned by claim or heartbeat"`
	}) (*sdk.CallToolResult, any, error) {
		if resolverErr != nil {
			return errText(resolverErr), nil, nil
		}
		principal, err := resolver.resolve(req)
		if err != nil {
			return errText(err), nil, nil
		}
		if _, err := authorizeFleet(req, resolver, resolverErr, mgr, in.Fleet); err != nil {
			return errText(err), nil, nil
		}
		t, err := mgr.HeartbeatTask(in.Fleet, in.Task, principal.Actor, in.LeaseToken)
		if err != nil {
			return errText(err), nil, nil
		}
		return structured(map[string]any{"ok": true, "task": t}), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_complete_task",
		Description: "Mark a claimed task as done with its result.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Fleet      string `json:"fleet" jsonschema:"the cohort id"`
		Task       string `json:"task" jsonschema:"the task id"`
		LeaseToken string `json:"lease_token" jsonschema:"the signed lease token returned by claim or heartbeat"`
		Result     string `json:"result,omitempty" jsonschema:"the successful result output"`
	}) (*sdk.CallToolResult, any, error) {
		if resolverErr != nil {
			return errText(resolverErr), nil, nil
		}
		principal, err := resolver.resolve(req)
		if err != nil {
			return errText(err), nil, nil
		}
		if _, err := authorizeFleet(req, resolver, resolverErr, mgr, in.Fleet); err != nil {
			return errText(err), nil, nil
		}
		if err := mgr.CompleteTask(in.Fleet, in.Task, principal.Actor, in.LeaseToken, in.Result); err != nil {
			return errText(err), nil, nil
		}
		return structured(map[string]any{"ok": true, "task": in.Task, "state": "done"}), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_fail_task",
		Description: "Mark a claimed task as failed. Set requeue=true to return it to the pending pool for retry.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Fleet      string `json:"fleet" jsonschema:"the cohort id"`
		Task       string `json:"task" jsonschema:"the task id"`
		LeaseToken string `json:"lease_token" jsonschema:"the signed lease token returned by claim or heartbeat"`
		Error      string `json:"error,omitempty" jsonschema:"the failure message"`
		Requeue    bool   `json:"requeue,omitempty" jsonschema:"whether to requeue the task for retry"`
	}) (*sdk.CallToolResult, any, error) {
		if resolverErr != nil {
			return errText(resolverErr), nil, nil
		}
		principal, err := resolver.resolve(req)
		if err != nil {
			return errText(err), nil, nil
		}
		if _, err := authorizeFleet(req, resolver, resolverErr, mgr, in.Fleet); err != nil {
			return errText(err), nil, nil
		}
		if err := mgr.FailTask(in.Fleet, in.Task, principal.Actor, in.LeaseToken, in.Error, in.Requeue); err != nil {
			return errText(err), nil, nil
		}
		verb := "failed"
		if in.Requeue {
			verb = "failed and requeued"
		}
		return structured(map[string]any{"ok": true, "task": in.Task, "state": verb}), nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "runeward_kill_cohort",
		Description: "Tear down a Cohort (fleet) and all its Citadels.",
	}, func(ctx context.Context, req *sdk.CallToolRequest, in struct {
		Fleet string `json:"fleet" jsonschema:"the cohort id"`
	}) (*sdk.CallToolResult, any, error) {
		if _, err := authorizeFleet(req, resolver, resolverErr, mgr, in.Fleet); err != nil {
			return errText(err), nil, nil
		}
		if err := mgr.KillFleet(ctx, in.Fleet); err != nil {
			return errText(err), nil, nil
		}
		return structured(map[string]any{"ok": true, "cohort": in.Fleet}), nil, nil
	})
}

func content(s string) []sdk.Content { return []sdk.Content{&sdk.TextContent{Text: s}} }

func text(s string) *sdk.CallToolResult { return &sdk.CallToolResult{Content: content(s)} }

func structured(v any) *sdk.CallToolResult {
	b, err := json.Marshal(v)
	if err != nil {
		return &sdk.CallToolResult{IsError: true, Content: content("error: encode structured result: " + err.Error())}
	}
	return &sdk.CallToolResult{Content: content(string(b)), StructuredContent: v}
}

func errText(err error) *sdk.CallToolResult {
	code := "tool_error"
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "bearer token") || strings.Contains(lower, "not authorized") || strings.Contains(lower, "not allowed"):
		code = "authz_denied"
	case strings.Contains(lower, "not found"):
		code = "not_found"
	}
	out := structured(map[string]any{"code": code, "error": msg})
	out.IsError = true
	return out
}

// blockedResult is non-nil when the call errored, was denied, or is pending
// approval; nil means the caller formats success.
func blockedResult(res *controlplane.ToolResult, err error) *sdk.CallToolResult {
	if err != nil {
		return errText(err)
	}
	switch res.Verdict {
	case profile.VerdictDeny:
		out := structured(map[string]any{"verdict": "deny", "code": "policy_denied", "reason": res.Reason})
		out.IsError = true
		return out
	case profile.VerdictRequireApprove:
		return structured(map[string]any{"verdict": "require-approval", "code": "approval_required", "approval_id": res.ApprovalID, "reason": res.Reason})
	}
	return nil
}

// execResult formats an exec-style result (exit code plus stdout/stderr).
func execResult(res *controlplane.ToolResult, err error) *sdk.CallToolResult {
	if blocked := blockedResult(res, err); blocked != nil {
		return blocked
	}
	return structured(map[string]any{"verdict": res.Verdict, "exit_code": res.ExitCode, "stdout": res.Stdout, "stderr": res.Stderr, "duration_ms": res.DurationMS})
}

// rawResult returns just the stdout on success (read/list/search).
func rawResult(res *controlplane.ToolResult, err error) *sdk.CallToolResult {
	if blocked := blockedResult(res, err); blocked != nil {
		return blocked
	}
	return structured(map[string]any{"verdict": res.Verdict, "output": res.Stdout})
}
