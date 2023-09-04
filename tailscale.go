// Package corednstailscale provides a coredns plugin for serving records about
// tailnet hosts in one or more private zones.
package corednstailscale

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
	"tailscale.com/ipn/ipnstate"
)

// Config describes a mapping of Tailscale ACL tags to domain names on which to
// answer about hosts.
type Config struct {
	DefaultZone    string
	Zones          map[string]string
	ReloadInterval time.Duration
}

// localClientish describes the subset of the Tailscale LocalClient used in this
// package.
type localClientish interface {
	Status(context.Context) (*ipnstate.Status, error)
}

type host struct {
	cname   string
	a, aaaa []netip.Addr
}

type Tailscale struct {
	client localClientish

	sync.RWMutex // protects access to hosts
	// hosts maps synthetic hostnames to tailnet peer details.
	hosts map[string]*host

	// Configuration for the Tailscale plugin instance.
	Configuration Config

	// Next handler in the chain.
	Next plugin.Handler
}

func peerDNSHostname(pdns string) string {
	splits := strings.SplitN(pdns, ".", 1)
	if len(splits) != 2 {
		return ""
	}
	return splits[0]
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

func buildSyntheticHosts(config *Config, peers []*ipnstate.PeerStatus) map[string]*host {
	hosts := make(map[string]*host)
	for _, peer := range peers {
		pdns := peer.DNSName
		if pdns == "" {
			continue
		}
		phn := peerDNSHostname(pdns)
		if phn == "" {
			continue
		}
		v4, v6 := bucketAddrs(peer.TailscaleIPs)
		host := &host{
			cname: pdns,
			a:     v4,
			aaaa:  v6,
		}

		// Insert default zone record
		hosts[fmt.Sprintf("%s.%s.", phn, config.DefaultZone)] = host

		// Insert any additional records based on tags
		for _, tag := range peer.Tags.AsSlice() {
			if zone := config.Zones[tag]; zone != "" {
				hosts[fmt.Sprintf("%s.%s.", phn, zone)] = host
			}
		}
	}
	return hosts
}

func (ts *Tailscale) update() {
	ctx := context.TODO()
	status, err := ts.client.Status(ctx)
	if err != nil {
		// TODO(christian): error log or metric or something.
		return
	}

	var peers []*ipnstate.PeerStatus
	for _, peer := range status.Peer {
		peers = append(peers, peer)
	}
	hosts := buildSyntheticHosts(&ts.Configuration, peers)

	ts.Lock()
	defer ts.Unlock()
	ts.hosts = hosts
}

// name of this plugin as coredns will refer to it.
const name = "tailscale"

func (*Tailscale) Name() string {
	return name
}

func (ts *Tailscale) Ready() bool {
	// TODO: implement something smarter.
	return true
}

func (ts *Tailscale) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {

	state := request.Request{W: w, Req: r}
	if state.QClass() != dns.ClassINET {
		return plugin.NextOrFailure(ts.Name(), ts.Next, ctx, w, r)
	}

	// var canAnswer []*dns.Question
	// for i := range r.Question {
	// 	q := &r.Question[i]
	// 	if q.Qclass != dns.ClassINET || !canAnswerForType[q.Qtype] {
	// 		continue
	// 	}
	// 	cname, v4, v6 := ts.sh.Get(q.Name)
	// 	switch q.Qtype {
	// 	case dns.TypeCNAME:
	// 		if cname != "" {

	// 		}
	// 	}

	// 	canAnswer = append(canAnswer, q)
	// }
	// if len(canAnswer) == 0 {
	// 	return plugin.NextOrFailure(ts.Name(), ts.Next, ctx, w, r)
	// }

	// reply := &dns.Msg{}
	// reply.SetReply(r)
	// hdr := dns.RR_Header{
	// 	Name:   state.QName(),
	// 	Rrtype: dns.TypeTXT,
	// 	Class:  dns.ClassCHAOS,
	// 	Ttl:    0,
	// }

	return plugin.NextOrFailure(ts.Name(), ts.Next, ctx, w, r)
}
