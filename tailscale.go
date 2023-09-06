// Package corednstailscale provides a coredns plugin for serving records about
// tailnet hosts in one or more private zones.
package corednstailscale

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	"tailscale.com/ipn/ipnstate"
)

type record struct {
	name   string
	v4, v6 []netip.Addr
}

// Config describes a mapping of Tailscale ACL tags to DNS zones on which to
// answer about hosts.
type Config struct {
	// DefaultZone in which all peers should appear.
	DefaultZone string

	// Zones maps Tailscale ACL tags to additional zones in which tagged peers
	// should appear in addition to the DefaultZone.
	Zones map[string]string

	// ReloadInterval at which polling for changes to peers should occur. Also
	// used as the TTL for responses.
	ReloadInterval time.Duration
}

func answer(req *dns.Msg) *dns.Msg {
	ans := &dns.Msg{}
	ans.SetReply(req)
	ans.Authoritative = true
	ans.RecursionAvailable = false
	ans.Compress = true
	return ans
}

func bucketAddrs(addrs []netip.Addr) (v4, v6 []netip.Addr) {
	for i := range addrs {
		if !addrs[i].IsValid() {
			// Skip invalid addresses
			continue
		}
		if addrs[i].Is4() {
			v4 = append(v4, addrs[i])
			continue
		}
		if addrs[i].Is6() {
			v6 = append(v6, addrs[i])
			continue
		}
	}
	return
}

func buildRecords(config *Config, peers []*ipnstate.PeerStatus) map[string]*record {
	records := make(map[string]*record)
	for _, peer := range peers {
		if peer.DNSName == "" {
			continue
		}

		tsdns := dns.CanonicalName(peer.DNSName)
		phn := peerDNSHostname(tsdns)
		if phn == "" {
			continue
		}

		v4, v6 := bucketAddrs(peer.TailscaleIPs)
		host := &record{
			name: tsdns,
			v4:   v4,
			v6:   v6,
		}

		records[dns.CanonicalName(fmt.Sprintf("%s.%s", phn, config.DefaultZone))] = host
		if peer.Tags == nil {
			continue
		}
		for _, tag := range peer.Tags.AsSlice() {
			if zone := config.Zones[tag]; zone != "" {
				records[dns.CanonicalName(fmt.Sprintf("%s.%s", phn, zone))] = host
			}
		}
	}
	return records
}

func peerDNSHostname(pdns string) string {
	splits := strings.SplitN(pdns, ".", 2)
	if len(splits) != 2 {
		return ""
	}
	return splits[0]
}

// clientish describes the subset of the Tailscale LocalClient used in this
// package.
type clientish interface {
	Status(context.Context) (*ipnstate.Status, error)
}

// Tailscale plugin for coredns, which serves records for peer hosts in
// custom DNS zones based on ACL tags.
type Tailscale struct {
	Config

	// Next handler in the chain.
	Next plugin.Handler

	client clientish
	done   chan any

	sync.RWMutex // protects the following.
	hosts        map[string]*record
}

func (ts *Tailscale) answerA(qn string, hr *record) []dns.RR {
	ans := make([]dns.RR, len(hr.v4))
	for i, addr := range hr.v4 {
		ans[i] = &dns.A{
			Hdr: dns.RR_Header{
				Name:   qn,
				Rrtype: dns.TypeA,
				Class:  dns.ClassINET,
				Ttl:    uint32(ts.ReloadInterval.Seconds()),
			},
			A: net.IP(addr.AsSlice()),
		}
	}
	return ans
}

func (ts *Tailscale) answerAAAA(qn string, hr *record) []dns.RR {
	ans := make([]dns.RR, len(hr.v6))
	for i, addr := range hr.v6 {
		ans[i] = &dns.AAAA{
			Hdr: dns.RR_Header{
				Name:   qn,
				Rrtype: dns.TypeAAAA,
				Class:  dns.ClassINET,
				Ttl:    uint32(ts.ReloadInterval.Seconds()),
			},
			AAAA: net.IP(addr.AsSlice()),
		}
	}
	return ans
}

func (ts *Tailscale) poll(t *time.Ticker) {
	log.Debug("Polling started")
	defer log.Debug("Polling stoped")
	for {
		select {
		case <-t.C:
			ts.reload()
		case <-ts.done:
			t.Stop()
			return
		}
	}
}

func (ts *Tailscale) reload() {
	log.Debug("Beginning peer record reload")
	defer log.Debug("Peer record reload complete")
	status, err := ts.client.Status(context.Background())
	if err != nil {
		log.Errorf("Failed fetching status from Tailscale Local API: %v", err)
		return
	}

	var i int
	peers := make([]*ipnstate.PeerStatus, len(status.Peer))
	for _, peer := range status.Peer {
		peers[i] = peer
		i++
	}
	if status.Self != nil {
		peers = append(peers, status.Self)
	}
	hosts := buildRecords(&ts.Config, peers)
	log.Infof("Build records for %d custom hosts", len(hosts))

	ts.Lock()
	defer ts.Unlock()
	ts.hosts = hosts
}

func (ts *Tailscale) serveA(ctx context.Context, w dns.ResponseWriter, req *dns.Msg, qn string, hr *record) (int, error) {
	ans := answer(req)
	ans.Answer = append(ans.Answer, ts.answerA(qn, hr)...)
	if err := w.WriteMsg(ans); err != nil {
		return dns.RcodeServerFailure, err
	}
	return dns.RcodeSuccess, nil
}

func (ts *Tailscale) serveAAAA(ctx context.Context, w dns.ResponseWriter, req *dns.Msg, qn string, hr *record) (int, error) {
	ans := answer(req)
	ans.Answer = append(ans.Answer, ts.answerAAAA(qn, hr)...)
	if err := w.WriteMsg(ans); err != nil {
		return dns.RcodeServerFailure, err
	}
	return dns.RcodeSuccess, nil
}

func (ts *Tailscale) serveCNAME(ctx context.Context, w dns.ResponseWriter, req *dns.Msg, qn string, hr *record) (int, error) {
	ans := answer(req)
	ans.Answer = append(ans.Answer,
		&dns.CNAME{
			Hdr: dns.RR_Header{
				Name:   qn,
				Rrtype: dns.TypeCNAME,
				Class:  dns.ClassINET,
				Ttl:    uint32(ts.ReloadInterval.Seconds()),
			},
			Target: hr.name,
		})
	ans.Answer = append(ans.Answer, ts.answerA(qn, hr)...)
	ans.Answer = append(ans.Answer, ts.answerAAAA(qn, hr)...)
	if err := w.WriteMsg(ans); err != nil {
		return dns.RcodeServerFailure, err
	}
	return dns.RcodeSuccess, nil
}

// Name of this plugin.
func (*Tailscale) Name() string {
	return name
}

// Ready returns true when the plugin is ready to serve.
func (ts *Tailscale) Ready() bool {
	ts.RLock()
	defer ts.RUnlock()

	// Ready when the hosts have been populated at least once.
	return ts.hosts != nil
}

// ServeDNS queries about Tailscale peers with custom domains. Satisfies the
// coredns handler interface.
func (ts *Tailscale) ServeDNS(ctx context.Context, w dns.ResponseWriter, req *dns.Msg) (int, error) {
	if ts == nil || !ts.Ready() {
		return plugin.NextOrFailure(ts.Name(), ts.Next, ctx, w, req)
	}

	state := request.Request{W: w, Req: req}
	if qc := state.QClass(); qc != dns.ClassINET && qc != dns.ClassANY {
		log.Debugf("Skipping query of unsupported class: %v", qc)
		return plugin.NextOrFailure(ts.Name(), ts.Next, ctx, w, req)
	}

	qt := state.QType()
	// Fail before taking the lock if the requested type is unsupported.
	switch qt {
	case dns.TypeA, dns.TypeAAAA, dns.TypeANY, dns.TypeCNAME:
		break
	default:
		log.Debugf("Skipping query of unsupported type: %v", qt)
		return plugin.NextOrFailure(ts.Name(), ts.Next, ctx, w, req)
	}

	qn := state.QName()
	hr := func(ts *Tailscale, qn string) *record {
		ts.RLock()
		defer ts.RUnlock()
		defer func() {
			if r := recover(); r != nil {
				log.Errorf("recovered from panic while looking up peer host: %v", r)
			}
		}()
		return ts.hosts[qn]
	}(ts, qn)
	if hr == nil {
		log.Debugf("No matches for %q", qn)
		return plugin.NextOrFailure(ts.Name(), ts.Next, ctx, w, req)
	}

	switch qt {
	case dns.TypeA:
		if len(hr.v4) == 0 {
			break
		}
		return ts.serveA(ctx, w, req, qn, hr)
	case dns.TypeAAAA:
		if len(hr.v6) == 0 {
			break
		}
		return ts.serveAAAA(ctx, w, req, qn, hr)
	case dns.TypeANY, dns.TypeCNAME:
		return ts.serveCNAME(ctx, w, req, qn, hr)
	}

	// Should never reach this point.
	return plugin.NextOrFailure(ts.Name(), ts.Next, ctx, w, req)
}

// Shutdown the Tailscale plugin.
func (ts *Tailscale) Shutdown() {
	log.Debug("Shutting down")
	ts.Lock()
	defer ts.Unlock()
	ts.hosts = nil
	ts.done <- true
}

// Startup the Tailscale plugin. The handler will not be usable until this is
// called for the first time.
func (ts *Tailscale) Startup() {
	log.Debug("Starting up")
	if ts.done == nil {
		ts.done = make(chan any)
	}
	if ts.ReloadInterval == 0 {
		ts.ReloadInterval = defaultReloadInterval
	}
	// Always reload on startup.
	ts.reload()
	go ts.poll(time.NewTicker(ts.ReloadInterval))
}
