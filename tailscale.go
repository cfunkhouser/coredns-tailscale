// Package corednstailscale provides a coredns plugin for serving records about
// tailnet hosts in one or more private zones.
package corednstailscale

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"net"
	"net/netip"
	"sort"
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

func (r *record) String() string {
	if r == nil {
		return "<nil>"
	}
	return fmt.Sprintf("A: %v AAAA: %v CNAME: %v", r.v4, r.v6, r.name)
}

type records map[string]*record

func (r records) String() string {
	if len(r) == 0 {
		return "records: [ ]"
	}
	rs := make([]string, len(r))
	var i int
	for qn, r := range r {
		rs[i] = fmt.Sprintf("%s => %s", qn, r)
		i++
	}
	sort.Strings(rs)
	return "records: [\n" + strings.Join(rs, "\n") + "\n]"
}

func answer(req *dns.Msg) *dns.Msg {
	ans := &dns.Msg{}
	ans.SetReply(req)
	ans.Authoritative = true
	ans.RecursionAvailable = false
	ans.Compress = true
	return ans
}

func assemblePeer(config *Config, peer *ipnstate.PeerStatus, r records) *record {
	if peer == nil || peer.DNSName == "" {
		// Peer is nil, or does not have a DNSName. Either case will make serving
		// CNAMEs problematic. Better to skip adding it to the hosts map, so we
		// don't serve anything about it (or worse).
		return nil
	}

	tsdns := dns.CanonicalName(peer.DNSName)
	phn := peerDNSHostname(tsdns)
	if phn == "" {
		// Could not extract the host name from the peer's DNS name. Log it, and
		// then skip it, as well.
		log.Warningf("Failed to extract a hostname from peer %q", tsdns)
		return nil
	}

	host := &record{name: tsdns}
	host.v4, host.v6 = bucketAddrs(peer.TailscaleIPs)

	// Assemble the default zone record.
	r[dns.CanonicalName(fmt.Sprintf("%s.%s", phn, config.DefaultZone))] = host

	// Assemble any additional zone records based on tags.
	if peer.Tags == nil {
		log.Debugf("Peer %s has no Tags", tsdns)
		return host
	}
	for _, tag := range peer.Tags.AsSlice() {
		tag = strings.TrimPrefix(tag, "tag:")
		if zone := config.Zones[tag]; zone != "" {
			r[dns.CanonicalName(fmt.Sprintf("%s.%s", phn, zone))] = host
		}
	}
	return host
}

func assemble(config *Config, self *ipnstate.PeerStatus, peers []*ipnstate.PeerStatus) records {
	if config.DefaultZone == "" {
		// If no default zone is configured, nothing will work anyway. This
		// should not have been permitted by the config parser.
		log.Error("No default zone specified; it is likely that invalid data will be served!")
		return nil
	}
	r := make(records)
	for _, peer := range peers {
		_ = assemblePeer(config, peer, r)
	}
	// Insert all records for self as a peer so that queries for the NS from
	// other hosts will succeed.
	sr := assemblePeer(config, self, r)
	if sr == nil {
		log.Errorf("Assembled Self record is nil; it is likely that invalid data will be served!")
		return r
	}

	// Generate ns hosts for each zone covered, and set to self. This is used in
	// serving SOA.
	for zone := range config.fastZoneLookup {
		r[dns.CanonicalName(fmt.Sprintf("ns.%s", zone))] = sr
	}
	return r
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

func peerDNSHostname(pdns string) string {
	splits := strings.SplitN(pdns, ".", 2)
	if len(splits) != 2 {
		return ""
	}
	return splits[0]
}

func serial(when time.Time) uint32 {
	h := fnv.New32()
	d := make([]byte, 8)
	binary.PutVarint(d, when.UTC().Unix())
	h.Write(d)
	return h.Sum32()
}

func zoneFromQN(qn string) string {
	splits := strings.SplitN(qn, ".", 2)
	if len(splits) != 2 {
		return ""
	}
	return dns.CanonicalName(splits[1])
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
	hosts        records
	serial       uint32 // 32-bit FNV hash of the time of last reload.
}

func (ts *Tailscale) A(hr *record) []dns.RR {
	ans := make([]dns.RR, len(hr.v4))
	for i, addr := range hr.v4 {
		ans[i] = &dns.A{
			Hdr: dns.RR_Header{
				Name:   hr.name,
				Rrtype: dns.TypeA,
				Class:  dns.ClassINET,
				Ttl:    uint32(ts.ReloadInterval.Seconds()),
			},
			A: net.IP(addr.AsSlice()),
		}
	}
	return ans
}

func (ts *Tailscale) AAAA(hr *record) []dns.RR {
	ans := make([]dns.RR, len(hr.v6))
	for i, addr := range hr.v6 {
		ans[i] = &dns.AAAA{
			Hdr: dns.RR_Header{
				Name:   hr.name,
				Rrtype: dns.TypeAAAA,
				Class:  dns.ClassINET,
				Ttl:    uint32(ts.ReloadInterval.Seconds()),
			},
			AAAA: net.IP(addr.AsSlice()),
		}
	}
	return ans
}

func (ts *Tailscale) authority(zone string, serial uint32) *dns.SOA {
	ri := uint32(ts.ReloadInterval.Seconds())
	return &dns.SOA{
		Hdr: dns.RR_Header{
			Name:   zone,
			Rrtype: dns.TypeSOA,
			Class:  dns.ClassINET,
			Ttl:    ri,
		},
		Ns:      fmt.Sprintf("ns.%s", zone),
		Mbox:    fmt.Sprintf("root.ns.%s", zone), // TODO: Stop lying.
		Serial:  serial,
		Refresh: ri,
		Retry:   (ri / 2),
		Expire:  (ri * 2),
		Minttl:  (ri / 2),
	}
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
	log.Debug("Beginning assembly of records for Tailnet peers")
	defer log.Debug("Assembly of records for Tailnet peers complete")
	sn := serial(time.Now())
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
	hosts := assemble(&ts.Config, status.Self, peers)
	log.Infof("Assembled %d custom DNS entries for Tailnet peers", len(hosts))
	log.Debugf("Assembled records with serial %d:\n%s", sn, hosts)

	ts.Lock()
	defer ts.Unlock()
	ts.hosts = hosts
	ts.serial = sn
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
	ans.Answer = append(ans.Answer, ts.A(hr)...)
	ans.Answer = append(ans.Answer, ts.AAAA(hr)...)
	if err := w.WriteMsg(ans); err != nil {
		return dns.RcodeServerFailure, err
	}
	return dns.RcodeSuccess, nil
}

func (ts *Tailscale) serveNoData(ctx context.Context, w dns.ResponseWriter, req *dns.Msg, qn string, zone bool, serial uint32) (int, error) {
	ans := answer(req)
	if !zone {
		qn = zoneFromQN(qn)
	}
	ans.Ns = append(ans.Ns, ts.authority(qn, serial))
	if err := w.WriteMsg(ans); err != nil {
		return dns.RcodeServerFailure, err
	}
	return dns.RcodeSuccess, nil
}

func (ts *Tailscale) serveNXDOMAIN(ctx context.Context, w dns.ResponseWriter, req *dns.Msg, qn string, zone bool, serial uint32) (int, error) {
	ans := answer(req)
	if !zone {
		qn = zoneFromQN(qn)
	}
	ans.Ns = append(ans.Ns, ts.authority(qn, serial))
	ans.Rcode = dns.RcodeNameError
	if err := w.WriteMsg(ans); err != nil {
		return dns.RcodeServerFailure, err
	}
	return dns.RcodeNameError, nil
}

func (ts *Tailscale) serveSOA(ctx context.Context, w dns.ResponseWriter, req *dns.Msg, qn string, serial uint32) (int, error) {
	ans := answer(req)
	ans.Answer = append(ans.Answer, ts.authority(qn, serial))
	if err := w.WriteMsg(ans); err != nil {
		return dns.RcodeServerFailure, err
	}
	return dns.RcodeSuccess, nil
}

func (ts *Tailscale) serveNS(ctx context.Context, w dns.ResponseWriter, req *dns.Msg, qn string) (int, error) {
	ans := answer(req)
	ans.Answer = append(ans.Answer,
		&dns.NS{
			Hdr: dns.RR_Header{
				Name:   qn,
				Rrtype: dns.TypeNS,
				Class:  dns.ClassINET,
				Ttl:    uint32(ts.ReloadInterval.Seconds()),
			},
			Ns: fmt.Sprintf("ns.%s", qn),
		})
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
	return ts.hosts != nil && ts.serial > 0
}

// lookup a record by name. Returns the record if any, and the serial for which
// the lookup result is valid. Acquires a read lock.
func (ts *Tailscale) lookup(qn string) (*record, uint32) {
	ts.RLock()
	defer ts.RUnlock()
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("recovered from panic while looking up %q: %v", qn, r)
		}
	}()
	return ts.hosts[qn], ts.serial
}

// ServeDNS queries about Tailscale peers with custom domains. Satisfies the
// coredns handler interface.
func (ts *Tailscale) ServeDNS(ctx context.Context, w dns.ResponseWriter, req *dns.Msg) (int, error) {
	if ts == nil || !ts.Ready() {
		return plugin.NextOrFailure(ts.Name(), ts.Next, ctx, w, req)
	}

	state := request.Request{W: w, Req: req}
	if qc := state.QClass(); qc != dns.ClassINET && qc != dns.ClassANY {
		return plugin.NextOrFailure(ts.Name(), ts.Next, ctx, w, req)
	}
	qn, qt := state.QName(), state.QType()

	// If the zone is not covered by this plugin, hand the request off to the
	// CoreDNS chain before wasting lock cycles doing a lookup.
	if !(ts.fastZoneLookup[qn] || ts.fastZoneLookup[zoneFromQN(qn)]) {
		return plugin.NextOrFailure(ts.Name(), ts.Next, ctx, w, req)
	}

	hr, serial := ts.lookup(qn) // Do the actual lookup; takes read lock.

	// If the qname is the name of a zone handled by this plugin, don't bother
	// inspecting the returned host record; it will always be nil. We respond
	// anyway for the record types which make sense in this case.
	if ts.fastZoneLookup[qn] {
		switch qt {
		case dns.TypeNS:
			return ts.serveNS(ctx, w, req, qn)
		case dns.TypeSOA:
			return ts.serveSOA(ctx, w, req, qn, serial)
		default:
			return ts.serveNoData(ctx, w, req, qn, true, serial)
		}
	}

	// If the qname was not a zone and no peer host record was found, return
	// NXDOMAIN.
	if hr == nil {
		return ts.serveNXDOMAIN(ctx, w, req, qn, false, serial)
	}

	// Serve the response for supported record types, or respond with the No
	// Data condition to indicate that the requested record, but that there is
	// no record of the requested type.
	switch qt {
	case dns.TypeA, dns.TypeAAAA, dns.TypeANY, dns.TypeCNAME:
		return ts.serveCNAME(ctx, w, req, qn, hr)
	default:
		return ts.serveNoData(ctx, w, req, qn, false, serial)
	}
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
