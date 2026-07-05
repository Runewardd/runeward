package server

import (
	"net"
	"net/http"

	"github.com/Runewardd/runeward/internal/backend"
	"github.com/Runewardd/runeward/internal/browser"
	"github.com/Runewardd/runeward/internal/controlplane"
	"github.com/Runewardd/runeward/internal/profile"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleWhoami reports the authenticated caller's identity and capabilities so
// the dashboard can render an interactive login and gate controls (create,
// approve) the caller isn't permitted to use. Reachable only after
// authentication, so a 200 here confirms the presented token is valid.
func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"authenticated": true,
		"rbac":          s.Authz != nil,
	}
	if p := principalFrom(r.Context()); p != nil {
		resp["principal"] = map[string]any{
			"name":             p.Name,
			"admin":            p.Admin,
			"can_approve":      p.MayApprove(),
			"can_launch":       true,
			"allowed_profiles": p.AllowedProfiles,
		}
	} else {
		// Legacy single-token or open mode: no named identity, full rights.
		resp["principal"] = map[string]any{
			"name":        "",
			"admin":       true,
			"can_approve": true,
			"can_launch":  true,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.mgr.ListProfiles()
	if err != nil {
		writeServerError(w, s.logger, err)
		return
	}
	if profiles == nil {
		profiles = []controlplane.ProfileInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": profiles})
}

func (s *Server) handleCreateSandbox(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Profile  string `json:"profile"`
		CopyFrom string `json:"copy_from"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Profile == "" {
		writeError(w, http.StatusBadRequest, "profile is required")
		return
	}
	owner := ""
	if p := principalFrom(r.Context()); p != nil {
		if !p.CanLaunch(req.Profile) {
			writeError(w, http.StatusForbidden, "not authorized to launch profile "+req.Profile)
			return
		}
		owner = p.Name
	}
	sb, err := s.mgr.CreateSandbox(r.Context(), req.Profile, controlplane.CreateOptions{CopyFrom: req.CopyFrom, Owner: owner})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sandboxView(sb, owner))
}

func (s *Server) handleListSandboxes(w http.ResponseWriter, r *http.Request) {
	infos := s.mgr.ListSandboxInfos()
	p := principalFrom(r.Context())
	views := make([]map[string]any, 0, len(infos))
	for i := range infos {
		// Per-principal visibility: a non-admin principal sees only the
		// sandboxes it owns. Legacy/open mode (no principal) sees all.
		if p != nil && !p.Admin && infos[i].Owner != p.Name {
			continue
		}
		views = append(views, sandboxView(&infos[i].Sandbox, infos[i].Owner))
	}
	writeJSON(w, http.StatusOK, map[string]any{"sandboxes": views})
}

func (s *Server) handleGetSandbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sb, ok := s.mgr.Sandbox(id)
	if !ok {
		writeError(w, http.StatusNotFound, "sandbox not found")
		return
	}
	owner, _ := s.mgr.SandboxOwner(id)
	view := sandboxView(sb, owner)
	u := s.mgr.SandboxUsage(id)
	view["usage"] = map[string]any{"tokens": u.Tokens, "cost_usd": u.CostUSD}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleKillSandbox(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.mgr.KillSandbox(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleShell(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command []string `json:"command"`
		Workdir string   `json:"workdir"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.mgr.Shell(r.Context(), r.PathValue("id"), req.Command, req.Workdir)
	s.writeToolResult(w, res, err)
}

func (s *Server) handleBrowser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL  string `json:"url"`
		Mode string `json:"mode"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.mgr.Browser(r.Context(), r.PathValue("id"), req.URL, req.Mode)
	s.writeToolResult(w, res, err)
}

func (s *Server) handleBrowserOpen(w http.ResponseWriter, r *http.Request) {
	sid, res, err := s.mgr.BrowserOpen(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServerError(w, s.logger, err)
		return
	}
	if res != nil && res.Verdict != profile.VerdictAllow {
		s.writeToolResult(w, res, nil)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"session_id": sid})
}

func (s *Server) handleBrowserAct(w http.ResponseWriter, r *http.Request) {
	var cmd browser.Command
	if err := decodeJSON(r, &cmd); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.mgr.BrowserAct(r.Context(), r.PathValue("id"), r.PathValue("sid"), cmd)
	s.writeToolResult(w, res, err)
}

func (s *Server) handleBrowserClose(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.BrowserClose(r.Context(), r.PathValue("id"), r.PathValue("sid")); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handlePython(w http.ResponseWriter, r *http.Request) {
	code, ok := decodeCode(w, r)
	if !ok {
		return
	}
	res, err := s.mgr.Python(r.Context(), r.PathValue("id"), code)
	s.writeToolResult(w, res, err)
}

func (s *Server) handleNode(w http.ResponseWriter, r *http.Request) {
	code, ok := decodeCode(w, r)
	if !ok {
		return
	}
	res, err := s.mgr.Node(r.Context(), r.PathValue("id"), code)
	s.writeToolResult(w, res, err)
}

func (s *Server) handleFileRead(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.mgr.FileRead(r.Context(), r.PathValue("id"), req.Path)
	if handled := s.writeIfBlocked(w, res, err); handled {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": res.Stdout, "verdict": res.Verdict})
}

func (s *Server) handleFileWrite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.mgr.FileWrite(r.Context(), r.PathValue("id"), req.Path, req.Content)
	if handled := s.writeIfBlocked(w, res, err); handled {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bytes": len(req.Content), "verdict": res.Verdict})
}

func (s *Server) handleFileList(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.mgr.FileList(r.Context(), r.PathValue("id"), req.Path)
	if handled := s.writeIfBlocked(w, res, err); handled {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"output": res.Stdout, "verdict": res.Verdict})
}

func (s *Server) handleFileSearch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query string `json:"query"`
		Path  string `json:"path"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.mgr.FileSearch(r.Context(), r.PathValue("id"), req.Query, req.Path)
	if handled := s.writeIfBlocked(w, res, err); handled {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"output": res.Stdout, "verdict": res.Verdict})
}

// handleReportUsage records model token/cost usage against a sandbox so it
// counts toward the profile's budget. Agents or fleet workers post the usage
// they observe from the model provider.
func (s *Server) handleReportUsage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tokens  int64   `json:"tokens"`
		CostUSD float64 `json:"cost_usd"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	if err := s.mgr.RecordUsage(id, req.Tokens, req.CostUSD); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	u := s.mgr.SandboxUsage(id)
	writeJSON(w, http.StatusOK, map[string]any{"tokens": u.Tokens, "cost_usd": u.CostUSD})
}

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil && err.Error() != "EOF" {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ref, err := s.mgr.Snapshot(r.Context(), r.PathValue("id"), req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, ref)
}

func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	snaps := s.mgr.ListSnapshots()
	if snaps == nil {
		snaps = []backend.SnapshotRef{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snaps})
}

func (s *Server) handleRestoreSnapshot(w http.ResponseWriter, r *http.Request) {
	owner := ""
	if p := principalFrom(r.Context()); p != nil {
		owner = p.Name
	}
	sb, err := s.mgr.RestoreSnapshot(r.Context(), r.PathValue("id"), owner)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sandboxView(sb, owner))
}

func (s *Server) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	list := s.mgr.Approvals().List()
	if list == nil {
		list = []controlplane.ApprovalView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": list})
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	s.resolveApproval(w, r, true)
}

func (s *Server) handleDeny(w http.ResponseWriter, r *http.Request) {
	s.resolveApproval(w, r, false)
}

func (s *Server) resolveApproval(w http.ResponseWriter, r *http.Request, approve bool) {
	if p := principalFrom(r.Context()); p != nil && !p.MayApprove() {
		writeError(w, http.StatusForbidden, "not authorized to resolve approvals")
		return
	}
	if ok := s.mgr.ResolveApproval(r.PathValue("id"), approve, approver(r)); !ok {
		writeError(w, http.StatusNotFound, "approval not found or already resolved")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// approver identifies who resolved an approval for the audit record. It prefers
// an explicit X-Runeward-Actor header (or ?actor= query), falling back to the
// peer address so a decision is never recorded as fully anonymous.
func approver(r *http.Request) string {
	// A resolved RBAC principal is the most trustworthy actor identity.
	if p := principalFrom(r.Context()); p != nil && p.Name != "" {
		return p.Name
	}
	if a := r.Header.Get("X-Runeward-Actor"); a != "" {
		return a
	}
	if a := r.URL.Query().Get("actor"); a != "" {
		return a
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	events, err := s.mgr.Ledger().Replay(r.PathValue("id"))
	if err != nil {
		writeServerError(w, s.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) handleAuditVerify(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.VerifyLedger(); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "signed": s.mgr.Signed(), "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "signed": s.mgr.Signed()})
}

func (s *Server) handleAuditPubKey(w http.ResponseWriter, r *http.Request) {
	pub, keyID := s.mgr.LedgerPublicKey()
	if pub == "" {
		writeJSON(w, http.StatusOK, map[string]any{"signed": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signed": true, "public_key": pub, "key_id": keyID})
}

// handleAuditExport streams a verifiable transcript bundle; ?session=<id>
// narrows it to one session.
func (s *Server) handleAuditExport(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=runeward-audit-bundle.json")
	if err := s.mgr.ExportBundle(w, session); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
}

func sandboxView(sb *backend.Sandbox, owner string) map[string]any {
	v := map[string]any{
		"id":      sb.ID,
		"profile": sb.Profile,
		"backend": sb.Backend,
		"image":   sb.Image,
		"status":  sb.Status,
	}
	if owner != "" {
		v["owner"] = owner
	}
	return v
}

func decodeCode(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", false
	}
	return req.Code, true
}

// writeToolResult maps a ToolResult to HTTP status: 403 deny, 202 pending
// approval, 200 otherwise.
func (s *Server) writeToolResult(w http.ResponseWriter, res *controlplane.ToolResult, err error) {
	if err != nil {
		writeServerError(w, s.logger, err)
		return
	}
	switch res.Verdict {
	case profile.VerdictDeny:
		writeJSON(w, http.StatusForbidden, res)
	case profile.VerdictRequireApprove:
		writeJSON(w, http.StatusAccepted, res)
	default:
		writeJSON(w, http.StatusOK, res)
	}
}

// writeIfBlocked handles the error/deny/pending cases shared by the file
// endpoints; it returns true when it has written a response.
func (s *Server) writeIfBlocked(w http.ResponseWriter, res *controlplane.ToolResult, err error) bool {
	if err != nil {
		writeServerError(w, s.logger, err)
		return true
	}
	switch res.Verdict {
	case profile.VerdictDeny:
		writeJSON(w, http.StatusForbidden, res)
		return true
	case profile.VerdictRequireApprove:
		writeJSON(w, http.StatusAccepted, res)
		return true
	}
	return false
}
