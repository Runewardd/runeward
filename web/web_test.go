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
