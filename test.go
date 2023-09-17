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
	// cmpOpts are used in multiple test locations for comparing results of
	// various types in this package.
	cmpOpts = []cmp.Option{
		cmp.AllowUnexported(Config{}, record{}),
		cmp.Comparer(func(l, r netip.Addr) bool {
			return l.Compare(r) == 0
		}),
	}

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
		fastZoneLookup: map[string]bool{
			"corp.example.com.":     true,
			"den.corp.example.com.": true,
			"rdu.corp.example.com.": true,
			"example.com.":          true,
		},
	}
)

// fakeLocalClient implements the clientish interface for testing.
type fakeLocalClient struct {
	status ipnstate.Status
	err    error
}

func (c *fakeLocalClient) Status(context.Context) (*ipnstate.Status, error) {
	return &c.status, c.err
}

// recorder implements the ResponseWriter interface for testing.
type recorder struct {
	test.ResponseWriter

	got *dns.Msg
}

func (r *recorder) WriteMsg(m *dns.Msg) error {
	r.got = m
	return nil
}

// rr creates a response record from record text for testing.
func rr(tb testing.TB, s string) dns.RR {
	tb.Helper()
	rr, err := dns.NewRR(s)
	if err != nil {
		tb.Fatal(err)
	}
	return rr
}

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
