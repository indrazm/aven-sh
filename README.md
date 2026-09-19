# aven

**Local HTTPS development domains, done right.**

`myapp.aven` → `localhost:3000` with a real, trusted certificate. No cert warnings, no
deploying to see your work over HTTPS, no hosts-file surgery. One binary, one setup
dialog, then domains are a one-liner.

https://aven.sh

---

## Why aven

Local development keeps colliding with the modern web: OAuth callbacks, service
workers, cookies with `Secure`, camera and clipboard APIs — they all demand real
HTTPS on a real hostname. aven gives every project its own `*.aven` domain that
browsers trust, served straight from your machine.

- **Trusted HTTPS automatically** — aven provisions a local root CA once and issues
  wildcard certificates for every domain you create. `curl`, Safari, Chrome —
  everything just works, no `-k` flags.
- **Zero prompts after setup** — domains resolve through aven's built-in DNS
  responder, so adding or removing a domain never asks for your password again.
- **Fast like it should be** — domains hot-reload in ~100 ms. No restarts, ever.
- **One binary** — Caddy v2 is embedded; there is nothing else to install, run, or
  maintain.

## Quick start

```bash
curl -fsSL https://install.aven.sh/install.sh | sh

aven setup              # once: trust the CA + install the DNS resolver (1 password dialog)
aven serve              # start the HTTPS daemon

aven add myapp --proxy localhost:3000     # reverse proxy
aven add site  --root ~/sites/demo        # static files

open https://myapp.aven                    # real HTTPS, trusted, no warnings
```

That's the whole loop. Remove, pause, list, and health-check domains with the
`aven` commands — `aven --help` shows them all.

## For AI agents

aven ships an MCP server, so Claude Code, Cursor, or any MCP client can stand up
domains, test against them, and tear them down as part of a task. Point your MCP
config at `aven mcp` and you're done.

## Console

aven pairs with a browser console at [console.aven.sh](https://console.aven.sh) —
a static web app that talks straight to your local daemon over HTTPS. No cloud
control plane, no tunnel, no account: nothing leaves your machine.

## How it works

`aven setup` routes every `*.aven` query to aven's built-in DNS responder, which
answers `127.0.0.1`. Domains are a config write plus a hot reload — nothing
privileged, nothing persistent outside `~/.aven`. Every request is logged as
structured JSON, ready for `jq` or your own tooling.

**Security model:** your domains are reachable only from this machine — never from
the LAN. Anything running locally can reach them (including any website in your
browser, since the aven CA is trusted), so treat a `*.aven` domain like
`localhost:<port>`, not as a private network. Trusting the root CA is always an
explicit step in `aven setup` — the daemon never modifies the system trust store
on its own.

## Requirements

- **macOS** (10.14+) or **Linux** (systemd-resolved)
- Ports 80/443 need no privileges on macOS; on Linux it's once per binary:
  `sudo setcap 'cap_net_bind_service=+ep' aven`

MIT — see [LICENSE](LICENSE).
