package caddyconf

import (
	"bytes"
	"encoding/json"
	"net"
	"reflect"
	"runtime"
	"testing"

	"aven/config"
)

// guardRoute is the terminal abort route prepended to every server: it
// drops requests from non-loopback peers (see ServerListen).
func guardRoute() map[string]any {
	return map[string]any{
		"match": []any{map[string]any{
			"not": []any{map[string]any{
				"remote_ip": map[string]any{"ranges": []any{"127.0.0.1", "::1"}},
			}},
		}},
		"handle":   []any{map[string]any{"handler": "static_response", "abort": true}},
		"terminal": true,
	}
}

// goldenConfig is a two-domain config: one HTTP proxy, one static site.
func goldenConfig() *config.Config {
	c := config.Default()
	c.Domains = []config.Domain{
		{Name: "api", Kind: config.KindProxy, Target: "localhost:3000"},
		{Name: "site", Kind: config.KindStatic, Root: "/tmp/access-demo"},
		{Name: "off", Kind: config.KindProxy, Target: "localhost:3001", Paused: true},
	}
	return c
}

func TestBuildGoldenShape(t *testing.T) {
	b, err := Build(goldenConfig())
	if err != nil {
		t.Fatal(err)
	}
	var got any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	base := config.BaseDir()
	// macOS only permits unprivileged wildcard binds of privileged ports
	// (see ServerListen); everywhere else the sockets are loopback-only.
	httpsListen, httpListen := []any{"127.0.0.1:443"}, []any{"127.0.0.1:80"}
	if runtime.GOOS == "darwin" {
		httpsListen, httpListen = []any{":443"}, []any{":80"}
	}
	want := map[string]any{
		"admin":   map[string]any{"listen": "127.0.0.1:2019"},
		"storage": map[string]any{"module": "file_system", "root": base + "/caddy"},
		"logging": map[string]any{
			"logs": map[string]any{
				"default": map[string]any{
					"writer": map[string]any{"output": "file", "filename": base + "/caddy.log"},
				},
				"access": map[string]any{
					"writer":  map[string]any{"output": "file", "filename": base + "/access.log"},
					"encoder": map[string]any{"format": "json"},
				},
			},
		},
		"apps": map[string]any{
			"pki": map[string]any{
				"certificate_authorities": map[string]any{"local": map[string]any{
					"name": "Aven", "install_trust": false,
				}},
			},
			"http": map[string]any{
				"http_port":  float64(80),
				"https_port": float64(443),
				"servers": map[string]any{
					"srv0": map[string]any{
						"listen": httpsListen,
						"logs":   map[string]any{"default_logger_name": "access"},
						"routes": append([]any{guardRoute()}, []any{
							map[string]any{
								"match": []any{map[string]any{"host": []any{"api.aven"}}},
								"handle": []any{map[string]any{
									"handler":   "reverse_proxy",
									"upstreams": []any{map[string]any{"dial": "localhost:3000"}},
								}},
								"terminal": true,
							},
							map[string]any{
								"match": []any{map[string]any{"host": []any{"site.aven"}}},
								"handle": []any{map[string]any{
									"handler": "file_server",
									"root":    "/tmp/access-demo",
								}},
								"terminal": true,
							},
						}...),
					},
					"srv1": map[string]any{
						"listen": httpListen,
						"logs":   map[string]any{"default_logger_name": "access"},
						"routes": append([]any{guardRoute()}, []any{
							map[string]any{
								"match": []any{map[string]any{"host": []any{"api.aven"}}},
								"handle": []any{map[string]any{
									"handler":     "static_response",
									"status_code": "308",
									"headers":     map[string]any{"Location": []any{"https://api.aven{http.request.uri}"}},
								}},
								"terminal": true,
							},
							map[string]any{
								"match": []any{map[string]any{"host": []any{"site.aven"}}},
								"handle": []any{map[string]any{
									"handler":     "static_response",
									"status_code": "308",
									"headers":     map[string]any{"Location": []any{"https://site.aven{http.request.uri}"}},
								}},
								"terminal": true,
							},
						}...),
					},
				},
			},
			"tls": map[string]any{
				"automation": map[string]any{
					"policies": []any{map[string]any{
						"subjects": []any{"*.aven"},
						"issuers":  []any{map[string]any{"module": "internal"}},
					}},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.MarshalIndent(got, "", "  ")
		w, _ := json.MarshalIndent(want, "", "  ")
		t.Fatalf("shape mismatch\ngot:\n%s\nwant:\n%s", g, w)
	}
	if bytes.Contains(b, []byte("off.aven")) {
		t.Fatalf("paused domain produced routes:\n%s", b)
	}
}

func TestBuildDeterministic(t *testing.T) {
	a, err := Build(goldenConfig())
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(goldenConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("non-deterministic output:\n%s\n---\n%s", a, b)
	}
}

func TestBuildEmptyDomains(t *testing.T) {
	c := config.Default()
	b, err := Build(c)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	apps := got["apps"].(map[string]any)
	servers := apps["http"].(map[string]any)["servers"].(map[string]any)
	for _, name := range []string{"srv0", "srv1"} {
		routes := servers[name].(map[string]any)["routes"]
		rts, ok := routes.([]any)
		if !ok || len(rts) != 1 {
			t.Fatalf("%s routes should be exactly the loopback guard, got %#v", name, routes)
		}
		if !reflect.DeepEqual(rts[0], guardRoute()) {
			t.Fatalf("%s sole route should be the loopback guard, got %#v", name, rts[0])
		}
	}
}

// TestBuildLoopbackIsolation pins the network-exposure contract: the first
// route on both servers aborts non-loopback peers, and the socket binds
// loopback except on darwin privileged ports, where the OS only allows an
// unprivileged wildcard bind and the guard is what shields it.
func TestBuildLoopbackIsolation(t *testing.T) {
	b, err := Build(goldenConfig())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	servers := got["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	for _, name := range []string{"srv0", "srv1"} {
		srv := servers[name].(map[string]any)
		routes := srv["routes"].([]any)
		if len(routes) == 0 || !reflect.DeepEqual(routes[0], guardRoute()) {
			t.Fatalf("%s: first route must be the loopback guard, got %#v", name, routes)
		}
		for _, a := range srv["listen"].([]any) {
			addr, _ := a.(string)
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				t.Fatalf("%s: bad listen %q: %v", name, addr, err)
			}
			if host == "" && runtime.GOOS != "darwin" {
				t.Fatalf("%s: wildcard listen %q only permitted on darwin", name, addr)
			}
			if host != "" && host != "127.0.0.1" {
				t.Fatalf("%s: listen %q is not loopback", name, addr)
			}
		}
	}
}

func TestBuildHTTPSProxyTransport(t *testing.T) {
	c := config.Default()
	c.Domains = []config.Domain{{Name: "tlsapi", Kind: config.KindProxy, Target: "https://localhost:8443"}}
	b, err := Build(c)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	servers := got["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	// routes[0] is the loopback guard; the domain's route follows it.
	route := servers["srv0"].(map[string]any)["routes"].([]any)[1].(map[string]any)
	handler := route["handle"].([]any)[0].(map[string]any)
	transport, ok := handler["transport"].(map[string]any)
	if !ok || transport["protocol"] != "http" {
		t.Fatalf("https upstream missing http transport: %#v", handler)
	}
	if _, ok := transport["tls"].(map[string]any); !ok {
		t.Fatalf("transport missing tls object: %#v", transport)
	}
	if handler["upstreams"].([]any)[0].(map[string]any)["dial"] != "localhost:8443" {
		t.Fatalf("wrong dial: %#v", handler["upstreams"])
	}
}

// TestServerListen pins the platform binding policy: unprivileged ports are
// loopback-only everywhere; privileged ports bind loopback except on darwin,
// where the OS only permits unprivileged wildcard binds (see ServerListen).
func TestServerListen(t *testing.T) {
	if got := ServerListen(8080); !reflect.DeepEqual(got, []string{"127.0.0.1:8080"}) {
		t.Fatalf("ServerListen(8080) = %v, want loopback-only on every platform", got)
	}
	want := []string{"127.0.0.1:443"}
	if runtime.GOOS == "darwin" {
		want = []string{":443"}
	}
	if got := ServerListen(443); !reflect.DeepEqual(got, want) {
		t.Fatalf("ServerListen(443) = %v, want %v", got, want)
	}
}

func TestParseTarget(t *testing.T) {
	cases := []struct {
		in    string
		dial  string
		https bool
		event bool // want error
	}{
		{in: "localhost:3000", dial: "localhost:3000"},
		{in: "http://localhost:3000", dial: "localhost:3000"},
		{in: "http://127.0.0.1:3000/", dial: "127.0.0.1:3000"},
		{in: "https://localhost:8443", dial: "localhost:8443", https: true},
		{in: "https://localhost", dial: "localhost:443", https: true},
		{in: "http://localhost", dial: "localhost:80"},
		{in: "localhost", event: true},
		{in: "ftp://localhost:21", event: true},
		{in: "", event: true},
	}
	for _, tc := range cases {
		dial, https, err := ParseTarget(tc.in)
		if tc.event {
			if err == nil {
				t.Errorf("ParseTarget(%q): expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseTarget(%q): %v", tc.in, err)
			continue
		}
		if dial != tc.dial || https != tc.https {
			t.Errorf("ParseTarget(%q) = (%q, %v), want (%q, %v)", tc.in, dial, https, tc.dial, tc.https)
		}
	}
}
