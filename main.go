// Command aven manages local HTTPS development domains backed by an
// embedded Caddy daemon. With no arguments it starts the TUI.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/spf13/cobra"

	"aven/caddyconf"
	"aven/config"
	"aven/consoleapi"
	"aven/daemon"
	"aven/doctor"
	"aven/domain"
	"aven/mcpserver"
	"aven/setup"
	"aven/trust"
	"aven/upgrade"
)

// version is stamped at release build time via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	root := &cobra.Command{
		Use:     "aven",
		Version: version,
		Short:   "Local HTTPS development domains on localhost",
		Long: `aven creates local development domains like myapp.aven that serve
HTTPS from localhost via reverse proxy or static file serving.

First-time setup:
` + "`aven setup`" + ` (one password dialog, then domains resolve via the local
DNS responder with zero prompts). Start the daemon with ` + "`aven serve`" + `.

https://aven.sh`,
		SilenceUsage: true,
	}

	var serveConfig string
	serve := &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTPS daemon in the foreground",
		RunE: func(cmd *cobra.Command, args []string) error {
			if serveConfig != config.Path() {
				// Only the default path is supported; config.Path() is
				// derived from $HOME and the flag exists for documentation.
				return fmt.Errorf("--config must be %s", config.Path())
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			return daemon.Serve(cfg)
		},
	}
	serve.Flags().StringVar(&serveConfig, "config", config.Path(), "config file path")

	setupCmd := &cobra.Command{
		Use:   "setup",
		Short: "One-time machine setup: trust CA and install the DNS resolver",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			return setup.Run(cfg)
		},
	}

	trustCmd := &cobra.Command{
		Use:   "trust",
		Short: "Provision and trust the aven root CA",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			return trust.Ensure(cfg)
		},
	}

	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a domain (exactly one of --proxy/--root)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			proxy, _ := cmd.Flags().GetString("proxy")
			root, _ := cmd.Flags().GetString("root")
			switch {
			case proxy != "" && root != "":
				return fmt.Errorf("--proxy and --root are mutually exclusive")
			case proxy == "" && root == "":
				return fmt.Errorf("pass exactly one of --proxy <host:port> or --root <dir>")
			}
			kind, target, r := config.KindProxy, proxy, ""
			if root != "" {
				kind, target, r = config.KindStatic, "", root
			}
			warnings, err := domain.Add(args[0], kind, target, r)
			printWarnings(warnings)
			if err != nil {
				return err
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			fmt.Printf("added %s (%s)\n", cfg.FQDN(args[0]), kind)
			return nil
		},
	}
	add.Flags().String("proxy", "", "reverse-proxy target, e.g. localhost:3000")
	add.Flags().String("root", "", "static file root directory")

	remove := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a domain",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			warnings, err := domain.Remove(args[0])
			printWarnings(warnings)
			if err != nil {
				return err
			}
			fmt.Printf("removed %s\n", args[0])
			return nil
		},
	}

	pause := &cobra.Command{
		Use:   "pause <name>",
		Short: "Pause a domain (keep its config, stop serving it)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			warnings, err := domain.SetPaused(args[0], true)
			printWarnings(warnings)
			if err != nil {
				return err
			}
			fmt.Printf("paused %s\n", args[0])
			return nil
		},
	}

	resume := &cobra.Command{
		Use:   "resume <name>",
		Short: "Resume a paused domain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			warnings, err := domain.SetPaused(args[0], false)
			printWarnings(warnings)
			if err != nil {
				return err
			}
			fmt.Printf("resumed %s\n", args[0])
			return nil
		},
	}

	var listJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List domains and their live status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			daemonUp, rows, err := domain.List()
			if err != nil {
				return err
			}
			if listJSON {
				out := struct {
					DaemonUp bool            `json:"daemon_up"`
					Suffix   string          `json:"suffix"`
					Domains  []domain.Status `json:"domains"`
				}{daemonUp, cfg.Suffix, rows}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}
			fmt.Printf("daemon: %s   suffix: .%s\n\n", onOff(daemonUp), cfg.Suffix)
			if len(rows) == 0 {
				fmt.Println("no domains; add one with `aven add <name> --proxy <host:port>`")
				return nil
			}
			fmt.Printf("%-20s %-7s %-28s %s\n", "NAME", "KIND", "TARGET/ROOT", "STATUS")
			for _, r := range rows {
				status := "up"
				if r.Paused {
					status = "paused"
				} else if !r.BackendUp {
					status = "down: " + r.BackendNote
				}
				fmt.Printf("%-20s %-7s %-28s %s\n", r.Name, r.Kind, r.Spec, status)
			}
			return nil
		},
	}
	list.Flags().BoolVar(&listJSON, "json", false, "output machine-readable JSON")

	doctorCmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check config, ports, CA, resolver, daemon and domains",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			rep := doctor.Run()
			fmt.Printf("aven doctor — daemon: %s   suffix: .%s\n\n", onOff(rep.DaemonUp), cfg.Suffix)
			failed := false
			for _, c := range rep.Checks {
				icon, styled := "✓", "ok  "
				switch c.Level {
				case doctor.LevelWarn:
					icon, styled = "!", "warn"
				case doctor.LevelFail:
					icon, styled = "✗", "FAIL"
					failed = true
				}
				fmt.Printf("  [%s] %-14s %-4s %s\n", icon, c.Name, styled, c.Detail)
			}
			if len(rep.Domains) > 0 {
				fmt.Println()
				for _, r := range rep.Domains {
					status := "up"
					if r.Paused {
						status = "paused"
					} else if !r.BackendUp {
						status = "down: " + r.BackendNote
					}
					fmt.Printf("  %-24s %s\n", r.FQDN, status)
				}
			}
			if failed {
				os.Exit(1)
			}
			return nil
		},
	}

	validate := &cobra.Command{
		Use:   "validate",
		Short: "Validate config and the generated Caddy JSON",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			b, err := caddyconf.Build(cfg)
			if err != nil {
				return err
			}
			var cc caddy.Config
			if err := caddy.StrictUnmarshalJSON(b, &cc); err != nil {
				return fmt.Errorf("generated config rejected: %w", err)
			}
			if err := caddy.Validate(&cc); err != nil {
				return fmt.Errorf("invalid: %w", err)
			}
			fmt.Println("OK")
			return nil
		},
	}

	resolver := &cobra.Command{
		Use:   "resolver",
		Short: "Scoped DNS routing for aven domains",
	}
	resolverApply := &cobra.Command{
		Use:   "apply",
		Short: "Re-apply scoped DNS routing (Linux; run as root, e.g. from the boot unit)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			return setup.ApplyResolver(cfg)
		},
	}
	resolver.AddCommand(resolverApply)

	var upgradeCheck bool
	upgrade := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade aven to the latest release",
		RunE: func(cmd *cobra.Command, args []string) error {
			return upgrade.Run(version, upgradeCheck)
		},
	}
	upgrade.Flags().BoolVar(&upgradeCheck, "check", false, "only check whether an update is available")

	mcp := &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server for AI agents (stdio)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return mcpserver.Run()
		},
	}

	console := &cobra.Command{
		Use:   "console",
		Short: "Pair the local daemon with the web console (console.aven.sh)",
	}
	consolePair := &cobra.Command{
		Use:   "pair",
		Short: "Generate a pairing token for console.aven.sh",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !consoleapi.IsPaired() {
				if _, err := consoleapi.NewToken(); err != nil {
					return err
				}
			}
			token, err := os.ReadFile(consoleapi.TokenPath())
			if err != nil {
				return err
			}
			t := strings.TrimSpace(string(token))
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			fmt.Printf("token:  %s\n", t)
			fmt.Printf("open:   https://console.aven.sh/pair#T=%s\n", t)
			fmt.Printf("local:  https://daemon.%s:%d (daemon must be running)\n", cfg.Suffix, cfg.ConsolePort)
			return nil
		},
	}
	consoleRevoke := &cobra.Command{
		Use:   "revoke",
		Short: "Revoke the console pairing token",
		RunE: func(cmd *cobra.Command, args []string) error {
			removed, err := consoleapi.Revoke()
			if err != nil {
				return err
			}
			if removed {
				fmt.Println("console pairing revoked")
			} else {
				fmt.Println("no console pairing found")
			}
			return nil
		},
	}
	console.AddCommand(consolePair, consoleRevoke)

	root.AddCommand(serve, setupCmd, trustCmd, add, remove, pause, resume, list, doctorCmd, validate, resolver, console, upgrade, mcp)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func printWarnings(warnings []string) {
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
}

func onOff(b bool) string {
	if b {
		return "running"
	}
	return "stopped"
}
