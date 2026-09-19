// Package doctor aggregates every health check into one report for the
// `doctor` command, the TUI system panel, and the MCP doctor tool.
package doctor

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"

	"aven/internal/config"
	"aven/internal/daemon"
	"aven/internal/domain"
	"aven/internal/setup"
	"aven/internal/trust"
)

type Level string

const (
	LevelOK   Level = "ok"
	LevelWarn Level = "warn"
	LevelFail Level = "fail"
)

type Check struct {
	Name   string `json:"name"`
	Level  Level  `json:"level"`
	Detail string `json:"detail"`
}

type Report struct {
	DaemonUp bool            `json:"daemon_up"`
	Checks   []Check         `json:"checks"`
	Domains  []domain.Status `json:"domains"`
}

// Run executes all checks. Load errors are reported as a single failed
// check so the report is always renderable.
func Run() Report {
	cfg, err := config.Load()
	if err != nil {
		return Report{Checks: []Check{{Name: "config", Level: LevelFail, Detail: err.Error()}}}
	}
	adminC := daemon.NewClient(cfg.AdminPort)
	daemonUp := adminC.Alive()
	rep := Report{DaemonUp: daemonUp}

	rep.add("config", LevelOK, fmt.Sprintf("%s valid (%d domains)", config.Path(), len(cfg.Domains)))

	if daemonUp {
		rep.add("daemon", LevelOK, fmt.Sprintf("running (admin 127.0.0.1:%d)", cfg.AdminPort))
	} else {
		rep.add("daemon", LevelWarn, "not running; start it with `aven serve` (or `s` in the TUI)")
		for _, p := range []struct {
			name string
			port int
		}{{"https", cfg.HTTPSPort}, {"http", cfg.HTTPPort}, {"admin", cfg.AdminPort}, {"dns", cfg.DNSPort}} {
			level, detail := portFree(p.port)
			rep.add("port "+p.name, level, detail)
		}
	}

	if _, err := os.Stat(trust.RootCertPath()); err != nil {
		rep.add("CA", LevelFail, "not provisioned; run `aven setup`")
	} else if trust.Trusted() {
		rep.add("CA", LevelOK, "provisioned and trusted")
	} else {
		rep.add("CA", LevelWarn, "provisioned but not trusted; run `aven setup`")
	}

	if setup.ResolverInstalled(cfg) {
		rep.add("resolver", LevelOK, setup.ResolverPath(cfg)+" → 127.0.0.1")
	} else {
		rep.add("resolver", LevelFail, fmt.Sprintf("missing; *.%s will not resolve; run `aven setup`", cfg.Suffix))
	}

	_, rows, err := domain.List()
	if err != nil {
		rep.add("domains", LevelFail, err.Error())
		return rep
	}
	rep.Domains = rows
	return rep
}

func (r *Report) add(name string, level Level, detail string) {
	r.Checks = append(r.Checks, Check{Name: name, Level: level, Detail: detail})
}

// portFree reports whether a TCP port is bindable (i.e. not occupied).
// macOS intermittently returns EPERM for privileged ports even to
// processes that may bind them, so that case is a warning, not a failure.
func portFree(port int) (Level, string) {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err == nil {
		l.Close()
		return LevelOK, fmt.Sprintf("%d free", port)
	}
	if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		return LevelWarn, fmt.Sprintf("%d: bind permission denied (cannot verify)", port)
	}
	return LevelFail, fmt.Sprintf("%d in use", port)
}
