package main

import (
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/coremain"

	_ "funkhouse.rs/coredns-tailscale" // include the tailscale plugin.
)

func init() {
	dnsserver.Directives = append(dnsserver.Directives, "tailscale")
}

func main() {
	coremain.Run()
}
