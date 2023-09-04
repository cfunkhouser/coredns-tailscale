package corednstailscale

import (
	"time"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
)

func init() {
	// Register as a plugin with coredns.
	plugin.Register(name, setup)
}

// setup the coredns tailscale plugin.
func setup(c *caddy.Controller) error {
	config, err := parse(c)
	if err != nil {
		return plugin.Error(name, err)
	}
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return &Tailscale{Configuration: config, Next: next}
	})
	return nil
}

func parse(c *caddy.Controller) (config Config, err error) {
	if !c.Next() {
		err = c.ArgErr()
		return
	}
	// First token should be the name of this plugin. Check it for sanity.
	if v := c.Val(); v != name {
		err = c.Errf("unexpected option %q; expected %q", v, name)
		return
	}

	// Second is the default zone name.
	c.Next()
	dz := c.Val()
	if dz == "{" {
		err = c.Err("default zone is required")
		return
	}
	config.DefaultZone = c.Val()

	// Parse the optional settings.
	for c.NextBlock() {
		if err = parseBlock(c, &config); err != nil {
			break
		}
	}
	return
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
