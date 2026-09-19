// Package mcpserver exposes aven's domain service to AI agents over
// the Model Context Protocol (stdio). Tools mirror the CLI: list, add,
// remove, pause, daemon control and doctor.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"aven/admin"
	"aven/config"
	"aven/daemon"
	"aven/doctor"
	"aven/domain"
)

const version = "1.0.0"

// Run starts the stdio MCP server; it blocks until the client disconnects.
func Run() error {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "aven",
		Title:   "Local HTTPS development domains",
		Version: version,
	}, nil)

	type empty struct{}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_domains",
		Description: "List configured local HTTPS domains with live backend status and whether the daemon is running.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		daemonUp, rows, err := domain.List()
		if err != nil {
			return nil, nil, err
		}
		cfg, err := config.Load()
		if err != nil {
			return nil, nil, err
		}
		out := struct {
			DaemonUp bool            `json:"daemon_up"`
			Suffix   string          `json:"suffix"`
			Domains  []domain.Status `json:"domains"`
		}{daemonUp, cfg.Suffix, rows}
		return textResult(mustJSON(out)), out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "add_domain",
		Description: "Add a local HTTPS domain. Pass either target (proxy upstream like localhost:3000) or root (static directory).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in addInput) (*mcp.CallToolResult, any, error) {
		kind, target, root := in.kindTargetRoot()
		warnings, err := domain.Add(in.Name, kind, target, root)
		if err != nil {
			return nil, nil, err
		}
		return textResult(withWarnings(fmt.Sprintf("added %s (%s)", in.Name, kind), warnings)), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "remove_domain",
		Description: "Remove a local HTTPS domain by name.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in nameInput) (*mcp.CallToolResult, any, error) {
		warnings, err := domain.Remove(in.Name)
		if err != nil {
			return nil, nil, err
		}
		return textResult(withWarnings("removed "+in.Name, warnings)), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "pause_domain",
		Description: "Pause or resume a domain. Paused domains keep their config but serve no routes.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in pauseInput) (*mcp.CallToolResult, any, error) {
		warnings, err := domain.SetPaused(in.Name, in.Paused)
		if err != nil {
			return nil, nil, err
		}
		action := "resumed"
		if in.Paused {
			action = "paused"
		}
		return textResult(withWarnings(action+" "+in.Name, warnings)), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "daemon_control",
		Description: "Start or stop the aven HTTPS daemon (ports 80/443 plus the DNS responder).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in daemonInput) (*mcp.CallToolResult, any, error) {
		cfg, err := config.Load()
		if err != nil {
			return nil, nil, err
		}
		switch in.Action {
		case "start":
			if err := daemon.StartInBackground(cfg); err != nil {
				return nil, nil, err
			}
			return textResult("daemon started"), nil, nil
		case "stop":
			if err := admin.NewClient(cfg.AdminPort).Stop(); err != nil {
				return nil, nil, err
			}
			return textResult("daemon stopped"), nil, nil
		default:
			return nil, nil, fmt.Errorf("action must be \"start\" or \"stop\"")
		}
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "doctor",
		Description: "Run all health checks: config, ports, CA trust, DNS resolver, daemon, and per-domain backend status.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		rep := doctor.Run()
		return textResult(mustJSON(rep)), rep, nil
	})

	return server.Run(context.Background(), &mcp.StdioTransport{})
}

type addInput struct {
	Name   string `json:"name" jsonschema:"domain name label, e.g. myapp"`
	Kind   string `json:"kind,omitempty" jsonschema:"proxy or static (default proxy)"`
	Target string `json:"target,omitempty" jsonschema:"proxy upstream host:port"`
	Root   string `json:"root,omitempty" jsonschema:"static file root directory"`
}

func (a addInput) kindTargetRoot() (kind, target, root string) {
	if a.Kind == "static" || (a.Target == "" && a.Root != "") {
		return "static", "", a.Root
	}
	return "proxy", a.Target, ""
}

type nameInput struct {
	Name string `json:"name" jsonschema:"domain name label"`
}

type pauseInput struct {
	Name   string `json:"name" jsonschema:"domain name label"`
	Paused bool   `json:"paused" jsonschema:"true to pause, false to resume"`
}

type daemonInput struct {
	Action string `json:"action" jsonschema:"start or stop"`
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func withWarnings(msg string, warnings []string) string {
	out := msg
	for _, w := range warnings {
		out += "\nwarning: " + w
	}
	return out
}
