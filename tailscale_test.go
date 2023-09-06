package corednstailscale

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/coredns/coredns/plugin/test"
	"github.com/google/go-cmp/cmp"
	"github.com/miekg/dns"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/views"
)

var (
	// fullTestConfig in which all fields are populated and can be used to
	// exercise all code paths.
	fullTestConfig = Config{
		DefaultZone: "corp.example.com.",
		Zones: map[string]string{
			"campus-den": "den.corp.example.com.",
			"campus-rdu": "rdu.corp.example.com.",
			"prod":       "example.com.",
		},
		ReloadInterval: time.Second * 300,
	}

	// The following are options for the cmp package, to appropriately compare
	// the types in the test cases below.

	baseOpts = []cmp.Option{
		cmp.AllowUnexported(record{}),
		cmp.Comparer(func(l, r netip.Addr) bool {
			return l.Compare(r) == 0
		}),
	}

	hostsCmpOpt = cmp.Comparer(func(l, r map[string]*record) bool {
		cl, cr := l, r // Comparers must be pure, so make shallow copies.
		if cl == nil {
			cl = map[string]*record{}
		}
		if cr == nil {
			cr = map[string]*record{}
		}
		return cmp.Equal(cl, cr, baseOpts...) // to compare peer, netip.Addr.
	})

	cmpOpts = append([]cmp.Option{hostsCmpOpt}, baseOpts...)
)

// vs creates a *views.Slice[T] for testing.
func vs[T any](tb testing.TB, vals []T) *views.Slice[T] {
	tb.Helper()
	s := views.SliceOf[T](vals)
	return &s
}

// ip creates a netip.Addr for testing.
func ip(tb testing.TB, addr string) netip.Addr {
	tb.Helper()
	return netip.MustParseAddr(addr)
}

// ips creates a slice of netip.Addr for testing.
func ips(tb testing.TB, addrs ...string) []netip.Addr {
	tb.Helper()
	ret := make([]netip.Addr, len(addrs))
	for i, a := range addrs {
		ret[i] = ip(tb, a)
	}
	return ret
}

func TestBuildRecords(t *testing.T) {
	for tn, tc := range map[string]struct {
		config Config
		peers  []*ipnstate.PeerStatus

		want map[string]*record
	}{
		"zero": {},
		"no peers": {
			config: fullTestConfig,
		},
		"peer without ts dns": {
			config: fullTestConfig,
			peers: []*ipnstate.PeerStatus{
				{
					TailscaleIPs: []netip.Addr{
						ip(t, "100.101.102.103"),
						ip(t, "fd7a::abcd"),
					},
				},
			},
		},
		"peer with no matching tags": {
			config: fullTestConfig,
			peers: []*ipnstate.PeerStatus{
				{
					DNSName: "foo.magic-dns.ts.net",
					TailscaleIPs: []netip.Addr{
						ip(t, "100.101.102.103"),
						ip(t, "fd7a::abcd"),
					},
					Tags: vs[string](t, []string{"foo", "bar"}),
				},
			},
			want: map[string]*record{
				"foo.corp.example.com.": {
					"foo.magic-dns.ts.net.",
					ips(t, "100.101.102.103"),
					ips(t, "fd7a::abcd"),
				},
			},
		},
		"peer with matching tags": {
			config: fullTestConfig,
			peers: []*ipnstate.PeerStatus{
				{
					DNSName: "foo.magic-dns.ts.net",
					TailscaleIPs: []netip.Addr{
						ip(t, "100.101.102.103"),
						ip(t, "fd7a::abcd"),
					},
					Tags: vs[string](t, []string{"campus-den", "prod"}),
				},
			},
			want: map[string]*record{
				"foo.corp.example.com.": {
					"foo.magic-dns.ts.net.",
					ips(t, "100.101.102.103"),
					ips(t, "fd7a::abcd"),
				},
				"foo.example.com.": {
					"foo.magic-dns.ts.net.",
					ips(t, "100.101.102.103"),
					ips(t, "fd7a::abcd"),
				},
				"foo.den.corp.example.com.": {
					"foo.magic-dns.ts.net.",
					ips(t, "100.101.102.103"),
					ips(t, "fd7a::abcd"),
				},
			},
		},
	} {
		t.Run(tn, func(t *testing.T) {
			got := buildRecords(&tc.config, tc.peers)
			if diff := cmp.Diff(got, tc.want, cmpOpts...); diff != "" {
				t.Errorf("mismatch: (-got,+want):\n%v", diff)
			}
		})
	}
}

type fakeLocalClient struct {
	status ipnstate.Status
	err    error
}

func (c *fakeLocalClient) Status(context.Context) (*ipnstate.Status, error) {
	return &c.status, c.err
}

func TestTailscale_Ready(t *testing.T) {
	ts := &Tailscale{
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

type recorder struct {
	test.ResponseWriter

	got *dns.Msg
}

func (r *recorder) WriteMsg(m *dns.Msg) error {
	r.got = m
	return nil
}

func rr(tb testing.TB, s string) dns.RR {
	tb.Helper()
	rr, err := dns.NewRR(s)
	if err != nil {
		tb.Fatal(err)
	}
	return rr
}

func TestServeDNS(t *testing.T) {
	testTS := Tailscale{
		hosts: map[string]*record{
			"foo.corp.example.com.": {
				"foo.magic-dns.ts.net.",
				ips(t, "100.101.102.103"),
				ips(t, "fd7a::abcd"),
			},
		},
	}
	for tn, tc := range map[string]struct {
		req  dns.Msg
		want *dns.Msg
	}{
		"CHAOS class": { // tests the unsupported class case.
			req: dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassCHAOS}},
			},
		},
		"MX": { // tests the unsupported type case.
			req: dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeMX, Qclass: dns.ClassINET}},
			},
		},
		"A miss": { // the "miss" cases test handler behavior when qname is not found.
			req: dns.Msg{
				Question: []dns.Question{{Name: "bar.corp.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET}},
			},
		},
		"AAAA miss": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "bar.corp.example.com.", Qtype: dns.TypeAAAA, Qclass: dns.ClassINET}},
			},
		},
		"ANY miss": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "bar.corp.example.com.", Qtype: dns.TypeANY, Qclass: dns.ClassINET}},
			},
		},
		"CNAME miss": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "bar.corp.example.com.", Qtype: dns.TypeCNAME, Qclass: dns.ClassINET}},
			},
		},

		"ANY class, A found": { // tests that qclass ANY behaves as INET
			req: dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassANY}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassANY}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Answer:   []dns.RR{rr(t, "foo.corp.example.com. 0 IN A 100.101.102.103")},
			},
		},
		"A hit": { // the "hit" cases test handler behavior when qname is found.
			req: dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeA, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Answer:   []dns.RR{rr(t, "foo.corp.example.com. 0 IN A 100.101.102.103")},
			},
		},
		"AAAA hit": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeAAAA, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeAAAA, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Answer:   []dns.RR{rr(t, "foo.corp.example.com. 0 IN AAAA fd7a::abcd")},
			},
		},
		"ANY hit": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeANY, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeANY, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Answer: []dns.RR{
					rr(t, "foo.corp.example.com. 0 IN CNAME foo.magic-dns.ts.net."),
					rr(t, "foo.corp.example.com. 0 IN A 100.101.102.103"),
					rr(t, "foo.corp.example.com. 0 IN AAAA fd7a::abcd"),
				},
			},
		},
		"CNAME hit": {
			req: dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeCNAME, Qclass: dns.ClassINET}},
			},
			want: &dns.Msg{
				Question: []dns.Question{{Name: "foo.corp.example.com.", Qtype: dns.TypeCNAME, Qclass: dns.ClassINET}},
				MsgHdr:   dns.MsgHdr{Response: true, Authoritative: true},
				Compress: true,
				Answer: []dns.RR{
					rr(t, "foo.corp.example.com. 0 IN CNAME foo.magic-dns.ts.net."),
					rr(t, "foo.corp.example.com. 0 IN A 100.101.102.103"),
					rr(t, "foo.corp.example.com. 0 IN AAAA fd7a::abcd"),
				},
			},
		},
	} {
		t.Run(tn, func(t *testing.T) {
			rr := &recorder{}
			testTS.ServeDNS(context.Background(), rr, &tc.req)
			if diff := cmp.Diff(rr.got, tc.want); diff != "" {
				t.Errorf("mismatch: (-got,+want):\n%v", diff)
			}
		})
	}
}
