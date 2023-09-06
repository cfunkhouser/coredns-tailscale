package corednstailscale

import (
	"time"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	corelog "github.com/coredns/coredns/plugin/pkg/log"
	"tailscale.com/client/tailscale"
)

// name of this plugin as coredns will refer to it.
const name = "tailscale"

var log = corelog.NewWithPlugin(name)

func init() {
	plugin.Register(name, setup)
}

// setup the coredns tailscale plugin.
func setup(c *caddy.Controller) error {
	ts := Tailscale{
		client: &tailscale.LocalClient{}, // zero value is usable.
	}
	if err := parse(c, &ts.Config); err != nil {
		return plugin.Error(name, err)
	}

	// Configure the Tailscale plugin to start polling the local API for updates
	// when the server starts...
	c.OnStartup(func() error {
		ts.Startup()
		return nil
	})

	// ... and to stop polling when the server shuts down.
	c.OnShutdown(func() error {
		ts.Shutdown()
		return nil
	})

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		ts.Next = next
		return &ts
	})
	return nil
}

var defaultReloadInterval = time.Minute * 5

func parse(c *caddy.Controller, config *Config) error {
	if !c.Next() {
		return c.ArgErr()
	}
	// First token should be the name of this plugin. Check it for sanity.
	if v := c.Val(); v != name {
		return c.Errf("unexpected option %q; expected %q", v, name)
	}

	// Second is the default zone name.
	c.Next()
	dz := c.Val()
	if dz == "{" {
		return c.Err("default zone is required")
	}
	config.DefaultZone = c.Val()

	// Parse the optional settings.
	for c.NextBlock() {
		if err := parseBlock(c, config); err != nil {
			return err
		}
	}

	if config.ReloadInterval == 0 {
		config.ReloadInterval = defaultReloadInterval
	}
	return nil
}

func parseBlock(c *caddy.Controller, config *Config) error {
	switch tok := c.Val(); tok {
	case "reload":
		if !c.NextArg() {
			return c.ArgErr()
		}
		if config.ReloadInterval != 0 {
			return c.Err("reload already specified")
		}
		reload, err := time.ParseDuration(c.Val())
		if err != nil {
			return c.Errf("invalid reload interval: %v", err)
		}
		config.ReloadInterval = reload

	case "tag":
		if !c.NextArg() {
			return c.ArgErr()
		}
		tag := c.Val()
		if !c.NextArg() {
			return c.ArgErr()
		}
		if config.Zones == nil {
			config.Zones = make(map[string]string)
		}
		if prev, has := config.Zones[tag]; has {
			return c.Errf("tag %q already configured; previous value was %q", tag, prev)
		}
		config.Zones[tag] = c.Val()

	default:
		return c.Errf("unknown option %q", tok)
	}
	return nil
}
