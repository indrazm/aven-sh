package daemon

import (
	"bytes"
	"fmt"
	"net/http"
	"time"
)

// Client talks to the embedded Caddy admin API on 127.0.0.1. It is the
// control plane for reloads and shutdown; there is no custom protocol.
type Client struct {
	base string
	hc   *http.Client
}

// NewClient targets the admin API on 127.0.0.1:port.
func NewClient(port int) *Client {
	return &Client{
		base: fmt.Sprintf("http://127.0.0.1:%d", port),
		hc:   &http.Client{Timeout: 500 * time.Millisecond},
	}
}

// withTimeout returns a shallow copy of the client with a different timeout
// for slow operations (reload can provision certificates).
func (c *Client) withTimeout(d time.Duration) *Client {
	cp := *c
	cp.hc = &http.Client{Timeout: d}
	return &cp
}

// Alive reports whether the admin API answers GET /config within the
// status timeout.
func (c *Client) Alive() bool {
	resp, err := c.hc.Get(c.base + "/config")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// WaitForReady polls the admin API until it answers or the timeout elapses.
func (c *Client) WaitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if c.Alive() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("admin API on %s not ready after %s", c.base, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Reload swaps the running config via POST /load.
func (c *Client) Reload(cfgJSON []byte) error {
	c = c.withTimeout(10 * time.Second)
	resp, err := c.hc.Post(c.base+"/load", "application/json", bytes.NewReader(cfgJSON))
	if err != nil {
		return fmt.Errorf("daemon reload failed (is it running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("daemon reload failed: HTTP %d: %s", resp.StatusCode, readErrBody(resp))
	}
	return nil
}

// Stop shuts the daemon down via POST /stop.
func (c *Client) Stop() error {
	c = c.withTimeout(10 * time.Second)
	resp, err := c.hc.Post(c.base+"/stop", "text/plain", nil)
	if err != nil {
		return fmt.Errorf("daemon stop failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("daemon stop failed: HTTP %d: %s", resp.StatusCode, readErrBody(resp))
	}
	return nil
}

func readErrBody(resp *http.Response) string {
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	out := buf.String()
	if len(out) > 500 {
		out = out[:500]
	}
	return out
}
