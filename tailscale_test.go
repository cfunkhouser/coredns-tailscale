package corednstailscale

import (
	"net/netip"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/views"
)

var fullConfig = Config{
	DefaultZone: "corp.example.com.",
	Zones: map[string]string{
		"campus-den": "den.corp.example.com.",
		"campus-rdu": "rdu.corp.example.com.",
		"prod":       "example.com.",
	},
	ReloadInterval: time.Second * 300,
}

func vs[T any](tb testing.TB, vals []T) *views.Slice[T] {
	tb.Helper()
	s := views.SliceOf[T](vals)
	return &s
}

func ip(tb testing.TB, addr string) netip.Addr {
	tb.Helper()
	return netip.MustParseAddr(addr)
}

func ips(tb testing.TB, addrs ...string) []netip.Addr {
	tb.Helper()
	ret := make([]netip.Addr, len(addrs))
	for i, a := range addrs {
		ret[i] = ip(tb, a)
	}
	return ret
}

var (
	baseOpts = []cmp.Option{
		cmp.AllowUnexported(host{}),
		cmp.Comparer(func(l, r netip.Addr) bool {
			return l.Compare(r) == 0
		}),
	}
	hostsCmpOpt = cmp.Comparer(func(l, r map[string]*host) bool {
		if l == nil {
			l = map[string]*host{}
		}
		if r == nil {
			r = map[string]*host{}
		}
		return cmp.Equal(l, r, baseOpts...)
	})
	cmpOpts = append([]cmp.Option{hostsCmpOpt}, baseOpts...)
)

func TestBuildHosts(t *testing.T) {
	for tn, tc := range map[string]struct {
		config Config
		peers  []*ipnstate.PeerStatus

		want map[string]*host
	}{
		"zero": {},
		"no peers": {
			config: fullConfig,
		},
		"peer without ts dns": {
			config: fullConfig,
			peers: []*ipnstate.PeerStatus{
				{
					TailscaleIPs: []netip.Addr{
						ip(t, "100.101.102.103"),
						ip(t, "fd7a::abcd"),
					},
					Tags: vs[string](t, []string{"foo", "bar"}),
				},
			},
		},
		"peer with no matching tags": {
			config: fullConfig,
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
			want: map[string]*host{
				"foo.corp.example.com.": {
					"foo.magic-dns.ts.net",
					ips(t, "100.101.102.103"),
					ips(t, "fd7a::abcd"),
				},
			},
		},
		"peer with matching tags": {
			config: fullConfig,
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
			want: map[string]*host{
				"foo.corp.example.com.": {
					"foo.magic-dns.ts.net",
					ips(t, "100.101.102.103"),
					ips(t, "fd7a::abcd"),
				},
				"foo.example.com.": {
					"foo.magic-dns.ts.net",
					ips(t, "100.101.102.103"),
					ips(t, "fd7a::abcd"),
				},
				"foo.den.corp.example.com.": {
					"foo.magic-dns.ts.net",
					ips(t, "100.101.102.103"),
					ips(t, "fd7a::abcd"),
				},
			},
		},
	} {
		t.Run(tn, func(t *testing.T) {
			got := buildHosts(&tc.config, tc.peers)
			if diff := cmp.Diff(got, tc.want, cmpOpts...); diff != "" {
				t.Errorf("mismatch: (-got,+want):\n%v", diff)
			}
		})
	}
}
