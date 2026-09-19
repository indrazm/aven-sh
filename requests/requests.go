// Package requests parses the daemon's JSON access log for the request
// inspector and implements replay.
package requests

import (
	"encoding/json"
	"os"
	"time"
)

// Request is one handled request as recorded by Caddy's access log.
type Request struct {
	Time       time.Time `json:"time"`
	Host       string    `json:"host"`
	Method     string    `json:"method"`
	URI        string    `json:"uri"`
	Remote     string    `json:"remote"`
	Status     int       `json:"status"`
	Size       int64     `json:"size"`
	DurationMS float64   `json:"duration_ms"`
}

// Tail returns the last max requests for host ("", or "*" for all hosts),
// oldest first, by reading at most the trailing maxBytes of the access log.
func Tail(path, host string, max, maxBytes int64) ([]Request, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	offset := info.Size() - maxBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := f.Seek(offset, 0); err != nil {
		return nil, err
	}
	buf := make([]byte, info.Size()-offset)
	if _, err := f.Read(buf); err != nil {
		return nil, err
	}

	var out []Request
	for _, line := range splitLines(buf) {
		var raw struct {
			TS       float64 `json:"ts"`
			Status   int     `json:"status"`
			Size     int64   `json:"size"`
			Duration float64 `json:"duration"`
			Req      struct {
				Host      string `json:"host"`
				Method    string `json:"method"`
				URI       string `json:"uri"`
				RemoteIP  string `json:"remote_ip"`
				RemoteStr string `json:"remote_addr"`
			} `json:"request"`
		}
		if json.Unmarshal(line, &raw) != nil {
			continue // partial or non-JSON line (e.g. truncated at seek boundary)
		}
		if raw.Req.Host == "" {
			continue
		}
		if host != "" && host != "*" && raw.Req.Host != host {
			continue
		}
		remote := raw.Req.RemoteIP
		out = append(out, Request{
			Time:       time.Unix(int64(raw.TS), 0),
			Host:       raw.Req.Host,
			Method:     raw.Req.Method,
			URI:        raw.Req.URI,
			Remote:     remote,
			Status:     raw.Status,
			Size:       raw.Size,
			DurationMS: raw.Duration * 1000,
		})
	}
	if len(out) > int(max) {
		out = out[len(out)-int(max):]
	}
	return out, nil
}

func splitLines(b []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				lines = append(lines, b[start:i])
			}
			start = i + 1
		}
	}
	if start < len(b) {
		lines = append(lines, b[start:]) // trailing partial line
	}
	return lines
}
