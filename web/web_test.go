package web

import (
	"io/fs"
	"strings"
	"testing"
)

func TestPreparedIDELinkUsesSameTab(t *testing.T) {
	b, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	start := strings.Index(html, `id="ide-link"`)
	if start < 0 {
		t.Fatal("prepared IDE link is missing")
	}
	end := strings.Index(html[start:], "</a>")
	if end < 0 {
		t.Fatal("prepared IDE link is malformed")
	}
	link := html[start : start+end]
	if strings.Contains(link, `target="_blank"`) {
		t.Fatal("prepared IDE fallback must use the current tab so it still works when new tabs are blocked")
	}
}

func TestAgentGroupPickerDrivesReadiness(t *testing.T) {
	htmlBytes, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(htmlBytes), `id="fleet-setup-note"`) {
		t.Fatal("agent groups must show readiness for the selected Charter")
	}

	appBytes, err := fs.ReadFile(FS(), "app.js")
	if err != nil {
		t.Fatal(err)
	}
	app := string(appBytes)
	if !strings.Contains(app, `$("#fleet-profile-select").addEventListener("change", refreshSelectedProfileReadiness)`) {
		t.Fatal("agent-group Charter changes must refresh readiness")
	}
	if !strings.Contains(app, `state.activeView === "fleets" ? $("#fleet-create-btn") : $("#create-btn")`) {
		t.Fatal("agent-group creation must be gated by readiness")
	}
}

func TestSandboxTabsExposeAccessibleState(t *testing.T) {
	htmlBytes, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(htmlBytes)
	for _, marker := range []string{
		`id="tabs" role="tablist"`,
		`id="tab-terminal" class="tab active" data-tab="terminal" role="tab" aria-selected="true"`,
		`id="pane-terminal" class="tab-pane active" data-pane="terminal" role="tabpanel"`,
	} {
		if !strings.Contains(html, marker) {
			t.Fatalf("dashboard is missing accessible tab marker %q", marker)
		}
	}

	appBytes, err := fs.ReadFile(FS(), "app.js")
	if err != nil {
		t.Fatal(err)
	}
	app := string(appBytes)
	if !strings.Contains(app, `t.setAttribute("aria-selected", String(active))`) {
		t.Fatal("tab activation must update aria-selected")
	}
	if !strings.Contains(app, `enableArrowKeyTabs("#tabs .tab")`) {
		t.Fatal("sandbox tabs must support arrow-key navigation")
	}
}

func TestDashboardAvoidsGenericHTMLInjectionHelper(t *testing.T) {
	appBytes, err := fs.ReadFile(FS(), "app.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(appBytes), `k === "html"`) {
		t.Fatal("DOM helper must not expose a generic innerHTML injection path")
	}
}

func TestLiveChatExplainsEmptyConnectedState(t *testing.T) {
	appBytes, err := fs.ReadFile(FS(), "app.js")
	if err != nil {
		t.Fatal(err)
	}
	app := string(appBytes)
	if !strings.Contains(app, "connected; waiting for an agent to publish conversation turns") {
		t.Fatal("live chat must explain why a connected stream is empty")
	}
}
