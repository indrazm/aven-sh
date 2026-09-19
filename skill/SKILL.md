---
name: aven
description: Use when a task needs real HTTPS on localhost — trusted local *.aven domains for dev servers, OAuth/callback URLs, Secure cookies, service workers, camera/clipboard APIs, static site previews, or host-based routing across multiple local services. Also use when an agent should create, inspect, pause, or tear down such domains via the aven CLI or MCP tools. Covers setup checks, recipes, and exposure guardrails.
---

# aven — trusted local HTTPS domains

`aven` maps `<name>.aven` → `127.0.0.1` and serves it over HTTPS with a certificate
the system already trusts (a local CA provisioned once by `aven setup`). An
embedded Caddy daemon listens on ports 80/443; adding a domain is a config write
plus a hot reload — no restarts, no password prompts.

## Prerequisites

- One-time machine setup: `aven setup` (trusts the CA, installs DNS routing; one
  password dialog).
- A running daemon: `aven serve` (foreground). Domains only answer while it runs;
  the MCP `daemon_control` tool can start it in the background.
- Health check: `aven doctor`. Domain list with live backend status:
  `aven list --json`.

If `*.aven` names don't resolve or browsers show cert warnings: setup was not run
or the daemon is down. Run `aven doctor` and fix its FAILED checks (usually
`aven setup`, then `aven serve`).

## Creating domains

```bash
aven add api --proxy localhost:3000   # reverse proxy to a dev server
aven add site --root ./dist           # serve a static directory
```

Upstream targets accept `host:port`, `http://`, or `https://`. With the aven MCP
server configured, prefer the tools (`add_domain`, `list_domains`, `pause_domain`,
`remove_domain`, `daemon_control`, `doctor`) — same operations, structured output.

Verify before reporting success: `curl -sI https://api.aven` returns a real status
(200/30x) with no `-k` flag. Cert warnings mean the CA is not trusted; never work
around them with `-k` — rerun `aven setup` instead.

## Common cases

1. **OAuth/callback or Secure-cookie flows** — point the app's redirect URL at
   `https://<app>.aven/callback`; the certificate is trusted and `Secure` cookies
   work.
2. **Browser APIs that require a secure context** (service workers, clipboard,
   camera, geolocation) — serve the dev server through a domain instead of
   `http://localhost:PORT`.
3. **Static previews** — `aven add preview --root ./build` to review a built site
   before deploying.
4. **Host-based routing across services** — one name per service (`api.aven`,
   `web.aven`, `admin.aven`) mirrors production vhost behavior.
5. **Ephemeral test fixtures** — add a domain, exercise the feature, then
   `aven remove <name>` so nothing lingers. List first (`aven list --json`) to
   avoid name clashes with the user's own domains.

## Guardrails — read before adding anything

- **Local machine only.** `*.aven` never answers from another device. For inbound
  webhooks or testing from a phone, use a tunnel product instead — aven is not
  one.
- **Every local process can read every domain.** `*.aven` resolves to `127.0.0.1`
  for all local software, including any website open in the browser. Never point
  `--root` at sensitive directories (home, `~/.ssh`, dotfile dirs) and never serve
  secrets through a domain, even a paused or obscure-named one.
- **Name rules:** lowercase letters, digits, hyphens; 1–63 chars; `localhost` is
  rejected. Invalid names fail immediately at add time.
- **Paused domains still resolve** and return an empty response — when debugging a
  "dead" domain, check `aven list` for `paused` before touching the upstream.
- **Proxy 502s mean the upstream is down**, not that aven is broken — check the
  target port, then `aven doctor`.
- **Prefer CLI/MCP over hand-editing** `~/.aven/config.yaml`; hot reload happens
  through aven commands.

## Cleanup

`aven remove <name>` deletes the domain (config entry + routes). Pausing
(`pause_domain`) keeps the config but stops serving — use pause for temporary
outages, remove for finished work.
