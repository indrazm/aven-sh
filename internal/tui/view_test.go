package tui

import (
	"strings"
	"testing"
	"time"

	"aven/internal/config"
	"aven/internal/domain"
	"aven/internal/requests"
)

func reqRowsFixture() []requests.Request {
	return []requests.Request{
		{Time: time.Now(), Host: "api.aven", Method: "GET", URI: "/hello", Status: 200},
		{Time: time.Now(), Host: "api.aven", Method: "GET", URI: "/missing", Status: 404},
	}
}

func modelForTest() model {
	cfg := config.Default()
	cfg.Domains = []config.Domain{
		{Name: "api", Kind: config.KindProxy, Target: "localhost:3000"},
		{Name: "site", Kind: config.KindStatic, Root: "/tmp/site", Paused: true},
	}
	return model{
		width:     110,
		height:    34,
		cfg:       cfg,
		daemonUp:  true,
		caTrusted: true,
		resolver:  true,
		rows: []domain.Status{
			{Name: "api", Kind: config.KindProxy, FQDN: "api.aven", Spec: "localhost:3000", BackendUp: true},
			{Name: "site", Kind: config.KindStatic, FQDN: "site.aven", Paused: true, Spec: "/tmp/site"},
		},
	}
}

// TestViewDashboardComposition verifies the lazydocker-style layout is
// fully composed: both columns, all panel titles, the selected domain, and
// the legend.
func TestViewDashboardComposition(t *testing.T) {
	m := modelForTest()
	v := m.View()
	for _, want := range []string{
		"Domains", "System", "api.aven",
		"daemon", "CA", "resolver", "suffix",
		"↑/↓ select", "tab/1-4 panel", "q quit",
		"● running", "✓ trusted", "✓ installed",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("View() missing %q", want)
		}
	}
}

// TestViewDetailsAndTabs verifies main-panel tab content follows the model.
func TestViewDetailsAndTabs(t *testing.T) {
	m := modelForTest()
	m.tab = tabDetails
	v := m.View()
	for _, want := range []string{"Details", "Requests", "Caddy", "Config", "url", "https://api.aven", "target", "localhost:3000"} {
		if !strings.Contains(v, want) {
			t.Errorf("details View() missing %q", want)
		}
	}
	if strings.Contains(v, "space to resume") {
		t.Errorf("unpaused selection should not show resume hint in details")
	}

	m.cursor = 1
	v = m.View()
	if !strings.Contains(v, "⏸") || !strings.Contains(v, "site.aven") {
		t.Errorf("paused selection not rendered")
	}

	m.v = viewConfirm
	m.confirmName = "site"
	if v := m.View(); !strings.Contains(v, "Delete site?") {
		t.Errorf("confirm prompt missing captured domain name: %v", v)
	}
}

// TestViewRequestsTab verifies the requests table renders rows.
func TestViewRequestsTab(t *testing.T) {
	m := modelForTest()
	m.tab = tabRequests
	m.reqRows = reqRowsFixture()
	v := m.View()
	for _, want := range []string{"GET", "200", "/hello", "404", "/missing"} {
		if !strings.Contains(v, want) {
			t.Errorf("requests View() missing %q", want)
		}
	}
}
