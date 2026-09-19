package daemon

import (
	"fmt"
	"net"
	"strings"

	"github.com/miekg/dns"

	"aven/config"
)

// StartDNS runs a minimal authoritative-style responder on 127.0.0.1 that
// answers A queries for *.<suffix> (and the suffix itself) with 127.0.0.1.
// macOS routes only *.aven queries here via /etc/resolver/<suffix>
// (created by `aven setup`), so it never needs to forward other names.
// The returned func shuts both listeners down.
func StartDNS(cfg *config.Config) (stop func() error, err error) {
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(cfg.DNSPort))
	suffixDot := strings.ToLower(cfg.Suffix) + "."

	dns.HandleFunc(suffixDot, func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)
		if len(r.Question) == 1 {
			q := r.Question[0]
			name := strings.ToLower(q.Name)
			inZone := name == suffixDot || strings.HasSuffix(name, "."+suffixDot)
			switch {
			case inZone && q.Qtype == dns.TypeA:
				m.Answer = append(m.Answer, &dns.A{
					Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 30},
					A:   net.IPv4(127, 0, 0, 1),
				})
			case !inZone:
				m.SetRcode(r, dns.RcodeNameError)
			}
			// AAAA inside the zone: empty NOERROR (no IPv6 address).
		}
		_ = w.WriteMsg(m)
	})

	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("bind UDP %s: %w", addr, err)
	}
	lc, err := net.Listen("tcp", addr)
	if err != nil {
		pc.Close()
		return nil, fmt.Errorf("bind TCP %s: %w", addr, err)
	}
	udpSrv := &dns.Server{PacketConn: pc}
	tcpSrv := &dns.Server{Listener: lc}
	go func() { _ = udpSrv.ActivateAndServe() }()
	go func() { _ = tcpSrv.ActivateAndServe() }()

	return func() error {
		_ = udpSrv.Shutdown()
		_ = tcpSrv.Shutdown()
		return nil
	}, nil
}
