package consoleapi

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"aven/caddyconf"
	"aven/config"
	"aven/doctor"
	"aven/domain"
	"aven/requests"
)

const Version = "0.1.0"

// browser. Local dev origins (localhost/127.0.0.1, any port) are also
// accepted; every request still requires the bearer token.

// StartConsole runs the console API listener on 127.0.0.1. It requires the
// CA to be provisioned (for the leaf certificate); a missing CA is
// reported as an error by setup/doctor. daemonUp probes whether the
// embedded Caddy engine is currently serving. The returned func shuts the
// listener down.
func StartConsole(cfg *config.Config, probe func() bool) (stop func() error, err error) {
	host := "daemon." + cfg.Suffix
	certFile, keyFile, err := EnsureLeafCert(
		caddyconf.RootCertPath(), caddyconf.RootKeyPath(), caddyconf.StorageDir(), host)
	if err != nil {
		return nil, fmt.Errorf("console certificate: %w", err)
	}
	tlsCfg, err := tlsConfig(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("console TLS: %w", err)
	}

	mux := http.NewServeMux()
	registerRoutes(mux, probe)
	// The catch-all owns CORS/PNA/auth: Go's method-specific patterns would
	// otherwise answer OPTIONS preflights with 405 before middleware runs,
	// which breaks browser calls from console.aven.sh.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && !originAllowed(origin) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", originIfAllowed(origin))
		w.Header().Set("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !authorized(token) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not paired or bad token"})
			return
		}
		writeJSON(w, nil, map[string]string{"error": "not found"})
	})

	srv := &http.Server{
		Addr:              net.JoinHostPort("127.0.0.1", fmt.Sprint(cfg.ConsolePort)),
		Handler:           mux,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return nil, fmt.Errorf("console API bind %s: %w", srv.Addr, err)
	}
	go func() { _ = srv.ServeTLS(ln, "", "") }()
	return func() error { return srv.Close() }, nil
}

func tlsConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func registerRoutes(mux *http.ServeMux, probe func() bool) {
	mux.HandleFunc("GET /api/status", jsonHandler(func(r *http.Request) (any, error) {
		cfg, err := config.Load()
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"version":   Version,
			"daemon_up": probe(),
			"suffix":    cfg.Suffix,
		}, nil
	}))

	mux.HandleFunc("GET /api/domains", jsonHandler(func(r *http.Request) (any, error) {
		daemonUp, rows, err := domain.List()
		if err != nil {
			return nil, err
		}
		return map[string]any{"daemon_up": daemonUp, "domains": rows}, nil
	}))

	mux.HandleFunc("POST /api/domains", jsonHandler(func(r *http.Request) (any, error) {
		var in struct {
			Name   string `json:"name"`
			Kind   string `json:"kind"`
			Target string `json:"target"`
			Root   string `json:"root"`
		}
		if err := decodeBody(r, &in); err != nil {
			return nil, badRequest(err)
		}
		warnings, err := domain.Add(in.Name, in.Kind, in.Target, in.Root)
		if err != nil {
			return nil, badRequest(err)
		}
		return map[string]any{"ok": true, "warnings": warnings}, nil
	}))

	mux.HandleFunc("DELETE /api/domains/{name}", jsonHandler(func(r *http.Request) (any, error) {
		warnings, err := domain.Remove(r.PathValue("name"))
		if err != nil {
			return nil, badRequest(err)
		}
		return map[string]any{"ok": true, "warnings": warnings}, nil
	}))

	mux.HandleFunc("POST /api/domains/{name}/pause", jsonHandler(func(r *http.Request) (any, error) {
		var in struct {
			Paused bool `json:"paused"`
		}
		if err := decodeBody(r, &in); err != nil {
			return nil, badRequest(err)
		}
		warnings, err := domain.SetPaused(r.PathValue("name"), in.Paused)
		if err != nil {
			return nil, badRequest(err)
		}
		return map[string]any{"ok": true, "warnings": warnings}, nil
	}))

	mux.HandleFunc("GET /api/doctor", jsonHandler(func(r *http.Request) (any, error) {
		return doctor.Run(), nil
	}))

	mux.HandleFunc("GET /api/requests", jsonHandler(func(r *http.Request) (any, error) {
		q := r.URL.Query()
		limit := int64(100)
		if v := q.Get("limit"); v != "" {
			if _, err := fmt.Sscanf(v, "%d", &limit); err != nil || limit < 1 || limit > 500 {
				return nil, badRequest(fmt.Errorf("limit must be 1..500"))
			}
		}
		rows, err := requests.Tail(caddyconf.AccessLogPath(), q.Get("host"), limit, 2<<20)
		if err != nil {
			return nil, err
		}
		return map[string]any{"requests": rows}, nil
	}))
}

type handler func(*http.Request) (any, error)

func jsonHandler(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && !originAllowed(origin) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", originIfAllowed(origin))
		w.Header().Set("Vary", "Origin")

		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !authorized(token) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not paired or bad token"})
			return
		}

		body, err := h(r)
		writeJSON(w, err, body)
	}
}

func writeJSON(w http.ResponseWriter, err error, body any) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		status := http.StatusInternalServerError
		var br *badRequestError
		if errors.As(err, &br) {
			status = http.StatusBadRequest
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

func decodeBody(r *http.Request, into any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	return dec.Decode(into)
}

type badRequestError struct{ error }

func badRequest(err error) error { return badRequestError{err} }

// originAllowed checks the static console origin, localhost dev origins,
// and any extra origins configured in config.yaml (console_origins).
func originAllowed(origin string) bool {
	if origin == "https://console.aven.sh" {
		return true
	}
	if strings.HasPrefix(origin, "http://localhost:") ||
		strings.HasPrefix(origin, "http://127.0.0.1:") {
		return true
	}
	if cfg, err := config.Load(); err == nil {
		for _, o := range cfg.ConsoleOrigins {
			if o != "" && o == origin {
				return true
			}
		}
	}
	return false
}

// originIfAllowed echoes the origin for CORS (falls back to the console
// origin when absent). Callers have already been through originAllowed.
func originIfAllowed(origin string) string {
	if origin != "" && originAllowed(origin) {
		return origin
	}
	return "https://console.aven.sh"
}
