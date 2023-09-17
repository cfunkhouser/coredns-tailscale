package corednstailscale

import (
	"context"
	"net/netip"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/miekg/dns"
	"tailscale.com/ipn/ipnstate"
)

func TestAssemble(t *testing.T) {
	testSelf := &ipnstate.PeerStatus{
		DNSName:      "self.magic-dns.ts.net",
		TailscaleIPs: []netip.Addr{ip(t, "100.111.112.113"), ip(t, "fd7a::dead:beef")},
	}

	for tn, tc := range map[string]struct {
		config Config
		peers  []*ipnstate.PeerStatus

		want records
	}{
		"zero": {},
		"no peers": {
			config: fullTestConfig,
			want: records{
				"self.corp.example.com.":   {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
				"ns.corp.example.com.":     {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
				"ns.den.corp.example.com.": {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
				"ns.rdu.corp.example.com.": {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
				"ns.example.com.":          {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
			},
		},
		"peer without ts dns name": {
			config: fullTestConfig,
			peers: []*ipnstate.PeerStatus{
				{
					TailscaleIPs: []netip.Addr{ip(t, "100.101.102.103"), ip(t, "fd7a::abcd")},
				},
			},
			want: records{
				"self.corp.example.com.":   {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
				"ns.corp.example.com.":     {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
				"ns.den.corp.example.com.": {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
				"ns.rdu.corp.example.com.": {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
				"ns.example.com.":          {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
			},
		},
		"peer with no matching tags": {
			config: fullTestConfig,
			peers: []*ipnstate.PeerStatus{
				{
					DNSName:      "foo.magic-dns.ts.net",
					TailscaleIPs: []netip.Addr{ip(t, "100.101.102.103"), ip(t, "fd7a::abcd")},
					Tags:         vs[string](t, []string{"tag:foo", "tag:bar"}),
				},
			},
			want: records{
				"self.corp.example.com.":   {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
				"foo.corp.example.com.":    {"foo.magic-dns.ts.net.", ips(t, "100.101.102.103"), ips(t, "fd7a::abcd")},
				"ns.corp.example.com.":     {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
				"ns.den.corp.example.com.": {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
				"ns.rdu.corp.example.com.": {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
				"ns.example.com.":          {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
			},
		},
		"peer with matching tags": {
			config: fullTestConfig,
			peers: []*ipnstate.PeerStatus{
				{
					DNSName:      "foo.magic-dns.ts.net",
					TailscaleIPs: []netip.Addr{ip(t, "100.101.102.103"), ip(t, "fd7a::abcd")},
					Tags:         vs[string](t, []string{"tag:campus-den", "tag:prod"}),
				},
			},
			want: records{
				"self.corp.example.com.":    {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
				"foo.corp.example.com.":     {"foo.magic-dns.ts.net.", ips(t, "100.101.102.103"), ips(t, "fd7a::abcd")},
				"foo.example.com.":          {"foo.magic-dns.ts.net.", ips(t, "100.101.102.103"), ips(t, "fd7a::abcd")},
				"foo.den.corp.example.com.": {"foo.magic-dns.ts.net.", ips(t, "100.101.102.103"), ips(t, "fd7a::abcd")},
				"ns.corp.example.com.":      {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
				"ns.den.corp.example.com.":  {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
				"ns.rdu.corp.example.com.":  {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
				"ns.example.com.":           {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
			},
		},
	} {
		t.Run(tn, func(t *testing.T) {
			got := assemble(&tc.config, testSelf, tc.peers)
			if diff := cmp.Diff(got, tc.want, cmpOpts...); diff != "" {
				t.Errorf("mismatch: (-got,+want):\n%v", diff)
			}
		})
	}
}

func TestTailscale_Ready(t *testing.T) {
	ts := &Tailscale{
		Config: fullTestConfig,
		client: &fakeLocalClient{},
	}
	if ready := ts.Ready(); ready {
		t.Errorf("should not be ready before first call to Startup")
	}
	ts.Startup()
	if ready := ts.Ready(); !ready {
		t.Errorf("should be ready following call to Startup")
	}
	ts.Shutdown()
	if ready := ts.Ready(); ready {
		t.Errorf("should not be ready following call to Startup")
	}
}

func TestTailscale_ServeDNS(t *testing.T) {
	testTS := Tailscale{
		Config: fullTestConfig,
		serial: 8675309,
		hosts: records{
			"foo.corp.example.com.":     {"foo.magic-dns.ts.net.", ips(t, "100.101.102.103"), ips(t, "fd7a::abcd")},
			"foo.den.corp.example.com.": {"foo.magic-dns.ts.net.", ips(t, "100.101.102.103"), ips(t, "fd7a::abcd")},
			"foo.example.com.":          {"foo.magic-dns.ts.net.", ips(t, "100.101.102.103"), ips(t, "fd7a::abcd")},
			"ns.corp.example.com.":      {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
			"ns.den.corp.example.com.":  {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
			"ns.example.com.":           {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
			"ns.rdu.corp.example.com.":  {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
			"self.corp.example.com.":    {"self.magic-dns.ts.net.", ips(t, "100.111.112.113"), ips(t, "fd7a::dead:beef")},
		},
	}
	for tn, tc := range map[string]struct {
		req  dns.Msg
		want *dns.Msg
	}{
		// the "invalid" cases test handler behavior in various unsupported
		// situations.

		"invalid CHAOS A": { // unsupported qclass
			req: dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassCHAOS}},
			},
		},

		// the "miss" cases test handler behavior when qname is not found.

		"miss IN A": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "bar.corp.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "bar.corp.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true, Rcode: dns.RcodeNameError},
				Compress: true,
				Ns: []dns.RR{
					rr(t, "corp.example.com. 300 IN SOA ns.corp.example.com root.ns.corp.example.com 8675309 300 150 600 150"),
				},
			},
		},
		"miss IN AAAA": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "bar.corp.example.com.", Qtype: dns.TypeAAAA, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "bar.corp.example.com.", Qtype: dns.TypeAAAA, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true, Rcode: dns.RcodeNameError},
				Compress: true,
				Ns: []dns.RR{
					rr(t, "corp.example.com. 300 IN SOA ns.corp.example.com root.ns.corp.example.com 8675309 300 150 600 150"),
				},
			},
		},
		"miss IN ANY": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "bar.corp.example.com.", Qtype: dns.TypeANY, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "bar.corp.example.com.", Qtype: dns.TypeANY, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true, Rcode: dns.RcodeNameError},
				Compress: true,
				Ns: []dns.RR{
					rr(t, "corp.example.com. 300 IN SOA ns.corp.example.com root.ns.corp.example.com 8675309 300 150 600 150"),
				},
			},
		},
		"miss IN CNAME": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "bar.corp.example.com.", Qtype: dns.TypeCNAME, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "bar.corp.example.com.", Qtype: dns.TypeCNAME, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true, Rcode: dns.RcodeNameError},
				Compress: true,
				Ns: []dns.RR{
					rr(t, "corp.example.com. 300 IN SOA ns.corp.example.com root.ns.corp.example.com 8675309 300 150 600 150"),
				},
			},
		},
		"miss IN MX": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "bar.corp.example.com.", Qtype: dns.TypeMX, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "bar.corp.example.com.", Qtype: dns.TypeMX, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true, Rcode: dns.RcodeNameError},
				Compress: true,
				Ns: []dns.RR{
					rr(t, "corp.example.com. 300 IN SOA ns.corp.example.com root.ns.corp.example.com 8675309 300 150 600 150"),
				},
			},
		},

		// the "peer hit" cases test handler behavior when qname matches a peer
		// in our Tailnet.

		"peer hit ANY A": { // tests ANY class behavior
			req: dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassANY}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassANY}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Answer: []dns.RR{
					rr(t, "foo.corp.example.com. 300 IN CNAME foo.magic-dns.ts.net."),
					rr(t, "foo.magic-dns.ts.net. 300 IN A     100.101.102.103"),
					rr(t, "foo.magic-dns.ts.net. 300 IN AAAA  fd7a::abcd"),
				},
			},
		},
		"peer hit IN A": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Answer: []dns.RR{
					rr(t, "foo.corp.example.com. 300 IN CNAME foo.magic-dns.ts.net."),
					rr(t, "foo.magic-dns.ts.net. 300 IN A     100.101.102.103"),
					rr(t, "foo.magic-dns.ts.net. 300 IN AAAA  fd7a::abcd"),
				},
			},
		},
		"peer hit IN AAAA": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeAAAA, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeAAAA, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Answer: []dns.RR{
					rr(t, "foo.corp.example.com. 300 IN CNAME foo.magic-dns.ts.net."),
					rr(t, "foo.magic-dns.ts.net. 300 IN A     100.101.102.103"),
					rr(t, "foo.magic-dns.ts.net. 300 IN AAAA  fd7a::abcd"),
				},
			},
		},
		"peer hit IN ANY": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeANY, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeANY, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Answer: []dns.RR{
					rr(t, "foo.corp.example.com. 300 IN CNAME foo.magic-dns.ts.net."),
					rr(t, "foo.magic-dns.ts.net. 300 IN A     100.101.102.103"),
					rr(t, "foo.magic-dns.ts.net. 300 IN AAAA  fd7a::abcd"),
				},
			},
		},
		"peer hit IN CNAME": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeCNAME, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeCNAME, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Answer: []dns.RR{
					rr(t, "foo.corp.example.com. 300 IN CNAME foo.magic-dns.ts.net."),
					rr(t, "foo.magic-dns.ts.net. 300 IN A     100.101.102.103"),
					rr(t, "foo.magic-dns.ts.net. 300 IN AAAA  fd7a::abcd"),
				},
			},
		},
		"peer hit IN NS": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeNS, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeNS, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Ns: []dns.RR{
					rr(t, "corp.example.com. 300 IN SOA ns.corp.example.com root.ns.corp.example.com 8675309 300 150 600 150"),
				},
			},
		},
		"peer hit IN SOA": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeSOA, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeSOA, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Ns: []dns.RR{
					rr(t, "corp.example.com. 300 IN SOA ns.corp.example.com root.ns.corp.example.com 8675309 300 150 600 150"),
				},
			},
		},
		"peer hit IN MX": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeMX, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeMX, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Ns: []dns.RR{
					rr(t, "corp.example.com. 300 IN SOA ns.corp.example.com root.ns.corp.example.com 8675309 300 150 600 150"),
				},
			},
		},

		// the "zone hit" cases test handler behavior when qname exists in our
		// records, regardless of whether the record type is supported or not.

		"zone hit ANY A": { // tests ANY class behavior
			req: dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassANY}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassANY}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Ns: []dns.RR{
					rr(t, "corp.example.com. 300 IN SOA ns.corp.example.com root.ns.corp.example.com 8675309 300 150 600 150"),
				},
			},
		},
		"zone hit IN A": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Ns: []dns.RR{
					rr(t, "corp.example.com. 300 IN SOA ns.corp.example.com root.ns.corp.example.com 8675309 300 150 600 150"),
				},
			},
		},
		"zone hit IN AAAA": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeAAAA, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeAAAA, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Ns: []dns.RR{
					rr(t, "corp.example.com. 300 IN SOA ns.corp.example.com root.ns.corp.example.com 8675309 300 150 600 150"),
				},
			},
		},
		"zone hit IN ANY": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeANY, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeANY, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Ns: []dns.RR{
					rr(t, "corp.example.com. 300 IN SOA ns.corp.example.com root.ns.corp.example.com 8675309 300 150 600 150"),
				},
			},
		},
		"zone hit IN CNAME": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeCNAME, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeCNAME, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Ns: []dns.RR{
					rr(t, "corp.example.com. 300 IN SOA ns.corp.example.com root.ns.corp.example.com 8675309 300 150 600 150"),
				},
			},
		},
		"zone hit IN NS": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeNS, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeNS, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Answer: []dns.RR{
					rr(t, "corp.example.com. 300 IN NS ns.corp.example.com."),
				},
			},
		},
		"zone hit IN SOA": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeSOA, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeSOA, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Answer: []dns.RR{
					rr(t, "corp.example.com. 300 IN SOA ns.corp.example.com root.ns.corp.example.com 8675309 300 150 600 150"),
				},
			},
		},
		"zone hit IN MX": { // MX is an unsupported record type.
			req: dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeMX, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "corp.example.com.", Qtype: dns.TypeMX, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Ns: []dns.RR{
					rr(t, "corp.example.com. 300 IN SOA ns.corp.example.com root.ns.corp.example.com 8675309 300 150 600 150"),
				},
			},
		},
	} {
		t.Run(tn, func(t *testing.T) {
			rr := &recorder{}
			testTS.ServeDNS(context.Background(), rr, &tc.req)
			if diff := cmp.Diff(rr.got, tc.want, cmpOpts...); diff != "" {
				t.Errorf("mismatch: (-got,+want):\n%v", diff)
			}
		})
	}
}
