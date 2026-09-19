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

- **Trusted HTTPS automatically** — aven provisions a local root CA ("Aven") once and
  issues wildcard certificates for every domain you create. `curl`, Safari, Chrome —
  everything just works, no `-k` flags.
- **Zero prompts after setup** — domains resolve through aven's built-in DNS responder
  and a scoped macOS resolver file, not `/etc/hosts`. Adding or removing a domain never
  asks for your password again.
- **Fast like it should be** — `aven add` takes ~100 ms and hot-reloads the daemon.
  No restarts, ever.
- **One binary** — Caddy v2 is embedded; there is nothing else to install, run, or
  upgrade.

## Quick start

```bash
go build -o aven .        # Go 1.27+
./aven setup              # once: trust the CA + install the DNS resolver (1 password dialog)
./aven serve              # start the HTTPS daemon (ports 80/443)

./aven add myapp --proxy localhost:3000     # reverse proxy
./aven add site  --root ~/sites/demo        # static files

open https://myapp.aven                     # real HTTPS, trusted, no warnings
```

That's the whole loop. Remove with `aven remove myapp`, pause with `aven pause myapp`,
resume with `aven resume myapp`.

## CLI

| Command | What it does |
|---|---|
| `aven setup` | One-time machine setup: trust the CA, install the scoped resolver |
| `aven serve` | Run the HTTPS daemon in the foreground |
| `aven add <name> --proxy <host:port> \| --root <dir>` | Add a domain |
| `aven remove <name>` | Remove a domain |
| `aven pause <name>` / `aven resume <name>` | Stop/resume serving a domain without deleting it |
| `aven list [--json]` | Domains with live status; `--json` for scripts and agents |
| `aven doctor` | Health check: config, ports, CA, resolver, daemon, backends |
| `aven validate` | Validate the config against the embedded Caddy schema |
| `aven trust` | Provision and trust the root CA |
| `aven mcp` | MCP server (stdio) for AI agents |

## For AI agents

aven ships an MCP server so Claude Code, Cursor, or any MCP client can manage local
domains:

```json
{
  "mcpServers": {
    "aven": { "command": "/path/to/aven", "args": ["mcp"] }
  }
}
```

Tools: `list_domains`, `add_domain`, `remove_domain`, `pause_domain`,
`daemon_control`, `doctor`. Every CLI mutation is also available as a tool — agents
can stand up `api.aven`, test against it, inspect the traffic, and tear it down again.

## Console

aven pairs with a browser console at [console.aven.sh](https://console.aven.sh):

```bash
aven console pair     # prints a deep link carrying a one-time pairing token
aven console revoke   # revoke the token
```

The console is a static web app that talks **straight to your local daemon** over
HTTPS (`daemon.aven:9443`, authenticated with your pairing token). No cloud control
plane, no tunnel, no account — nothing leaves your machine. The daemon must be
running (`aven serve`).

## How it works

`aven serve` runs Caddy v2 embedded in-process: port 443 for HTTPS with wildcard
certificates from the aven CA, port 80 redirecting to HTTPS, and an admin API on
`127.0.0.1:2019` used for zero-downtime config reloads.

`aven setup` writes `/etc/resolver/<suffix>` pointing at aven's DNS responder, so the
operating system routes every `*.aven` query to the daemon, which answers
`127.0.0.1`. Because resolution is name-agnostic, creating a domain is only a config
write plus a reload — nothing privileged, nothing persistent outside `~/.aven`.

Every request is recorded to `~/.aven/access.log` as structured JSON — method, path,
status, size, duration — ready for `jq`, tailing, or your own tooling.

## Configuration

`~/.aven/config.yaml` — created with defaults on first run:

```yaml
suffix: aven
http_port: 80
https_port: 443
admin_port: 2019
dns_port: 5354
domains:
  - name: api
    kind: proxy
    target: localhost:3000
  - name: site
    kind: static
    root: /Users/you/sites/demo
  - name: lab
    kind: proxy
    target: localhost:9000
    paused: true
```

Proxy targets accept `host:port`, `http://`, or `https://` (TLS upstreams supported).

## Requirements

- **macOS** (10.14+, Apple Silicon or Intel) or **Linux** (systemd-resolved for
  zero-prompt DNS routing — Ubuntu, Debian, Fedora, Arch, ...)
- Go 1.27+ to build

### Platform notes

**macOS** — `aven setup` writes a scoped resolver file (`/etc/resolver/<suffix>`)
through a single password dialog; ports 80/443 bind without privileges.

**Linux** — `aven setup` uses `sudo` (passwordless or a fresh sudo timestamp; run
`sudo aven setup` otherwise) and routes `*.aven` through systemd-resolved
(`resolvectl dns/domain` on your default link). A `aven-resolver.service` oneshot
unit re-applies the routing at boot. Two things to know:

- Ports 80/443 require a capability — once per binary:
  `sudo setcap 'cap_net_bind_service=+ep' aven`
- The root CA goes into the system store (`update-ca-certificates` /
  `update-ca-trust`), which covers curl and Chromium; Firefox keeps its own store —
  import the root manually if you browse with it.

## License

MIT — see [LICENSE](LICENSE).
