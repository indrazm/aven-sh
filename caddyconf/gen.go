// Package caddyconf converts a aven config into a Caddy v2 JSON config.
// The output shape is fixed; everything derives from config values.
package caddyconf

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"

	"aven/config"
)

// CAName is the pki CA name; it becomes the certificate issuer name so the
// root is identifiable in Keychain. Subjects/CNs are composed by the pki app.
const CAName = "Aven"

// RootCertPath returns the on-disk path of the provisioned root certificate
// inside the Caddy storage directory.
func RootCertPath() string {
	return filepath.Join(config.BaseDir(), "caddy", "pki", "authorities", "local", "root.crt")
}

// RootKeyPath returns the on-disk path of the root CA private key.
func RootKeyPath() string {
	return filepath.Join(config.BaseDir(), "caddy", "pki", "authorities", "local", "root.key")
}

// StorageDir returns the Caddy storage root (~/\.aven/caddy).
func StorageDir() string { return filepath.Join(config.BaseDir(), "caddy") }

// LogPath returns the Caddy log file path (~/\.aven/caddy.log).
func LogPath() string { return filepath.Join(config.BaseDir(), "caddy.log") }

// AccessLogPath returns the JSON request log path (~/\.aven/access.log).
func AccessLogPath() string { return filepath.Join(config.BaseDir(), "access.log") }

// ParseTarget resolves a proxy target to a dial address plus a flag for
// HTTPS upstreams. Accepts "host:port", "http://host:port" and
// "https://host[:port]".
func ParseTarget(raw string) (dial string, httpsUpstream bool, err error) {
	t := strings.TrimSpace(raw)
	if t == "" {
		return "", false, fmt.Errorf("empty proxy target")
	}
	if u, perr := url.Parse(t); perr == nil && u.Host != "" {
		switch u.Scheme {
		case "http":
		case "https":
			httpsUpstream = true
		default:
			return "", false, fmt.Errorf("unsupported scheme %q in target %q", u.Scheme, raw)
		}
		port := u.Port()
		if port == "" {
			if httpsUpstream {
				port = "443"
			} else {
				port = "80"
			}
		}
		return net.JoinHostPort(u.Hostname(), port), httpsUpstream, nil
	}
	// "localhost:3000" parses as scheme "localhost" + opaque "3000", so bare
	// host:port lands here.
	host, port, serr := net.SplitHostPort(t)
	if serr != nil {
		return "", false, fmt.Errorf("target %q must be [scheme://]host:port", raw)
	}
	return net.JoinHostPort(host, port), false, nil
}

// Build renders cfg as a Caddy v2 JSON document. Pure function of cfg (plus
// the fixed base directory); identical input yields identical output bytes.
func Build(cfg *config.Config) ([]byte, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	httpsRoutes := make([]any, 0, len(cfg.Domains))
	httpRoutes := make([]any, 0, len(cfg.Domains))
	for _, d := range cfg.Domains {
		if d.Paused {
			continue // paused domains keep their config entry but serve nothing
		}
		fqdn := cfg.FQDN(d.Name)
		handler, err := httpsHandler(d)
		if err != nil {
			return nil, fmt.Errorf("domain %s: %w", d.Name, err)
		}
		httpsRoutes = append(httpsRoutes, map[string]any{
			"match":    []any{map[string]any{"host": []string{fqdn}}},
			"handle":   []any{handler},
			"terminal": true,
		})
		httpRoutes = append(httpRoutes, map[string]any{
			"match": []any{map[string]any{"host": []string{fqdn}}},
			"handle": []any{map[string]any{
				"handler":     "static_response",
				"status_code": "308",
				"headers":     map[string]any{"Location": []string{"https://" + fqdn + "{http.request.uri}"}},
			}},
			"terminal": true,
		})
	}

	doc := map[string]any{
		"admin":   map[string]any{"listen": net.JoinHostPort("127.0.0.1", fmt.Sprint(cfg.AdminPort))},
		"storage": map[string]any{"module": "file_system", "root": StorageDir()},
		"logging": map[string]any{
			"logs": map[string]any{
				"default": map[string]any{
					// Logging writer namespace declares inline_key=output.
					"writer": map[string]any{"output": "file", "filename": LogPath()},
				},
				"access": map[string]any{
					"writer":  map[string]any{"output": "file", "filename": AccessLogPath()},
					"encoder": map[string]any{"format": "json"},
				},
			},
		},
		"apps": map[string]any{
			"pki": map[string]any{
				// v2.11 pki app: CAs map is "certificate_authorities".
				"certificate_authorities": map[string]any{"local": map[string]any{"name": CAName}},
			},
			"http": map[string]any{
				"http_port":  cfg.HTTPPort,
				"https_port": cfg.HTTPSPort,
				"servers": map[string]any{
					"srv0": map[string]any{
						"listen": []string{fmt.Sprintf(":%d", cfg.HTTPSPort)},
						"routes": httpsRoutes,
						"logs":   map[string]any{"default_logger_name": "access"},
					},
					"srv1": map[string]any{
						"listen": []string{fmt.Sprintf(":%d", cfg.HTTPPort)},
						"routes": httpRoutes,
						"logs":   map[string]any{"default_logger_name": "access"},
					},
				},
			},
			"tls": map[string]any{
				"automation": map[string]any{
					"policies": []any{map[string]any{
						"subjects": []string{"*." + cfg.Suffix},
						"issuers":  []any{map[string]any{"module": "internal"}},
					}},
				},
			},
		},
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func httpsHandler(d config.Domain) (map[string]any, error) {
	switch d.Kind {
	case config.KindProxy:
		dial, httpsUpstream, err := ParseTarget(d.Target)
		if err != nil {
			return nil, err
		}
		handler := map[string]any{
			"handler":   "reverse_proxy",
			"upstreams": []any{map[string]any{"dial": dial}},
		}
		if httpsUpstream {
			// Transport namespace declares inline_key=protocol.
			handler["transport"] = map[string]any{"protocol": "http", "tls": map[string]any{}}
		}
		return handler, nil
	case config.KindStatic:
		abs, err := filepath.Abs(config.ExpandTilde(d.Root))
		if err != nil {
			abs = d.Root
		}
		return map[string]any{"handler": "file_server", "root": abs}, nil
	}
	// Unreachable: config.Validate restricts kind.
	return nil, fmt.Errorf("unknown kind %q", d.Kind)
}
