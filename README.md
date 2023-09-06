# CoreDNS Tailscale

`coredns-tailscale` is a [CoreDNS](https://coredns.io/) plugin which enables
custom DNS for your tailnet hosts. It requires access to the Tailscale Local API
and serves DNS records for each peer in a custom DNS zone. Additionally, more
zones can be added based on Tailscale ACL tags applied to peer hosts.

## Custom DNS Zone for Tailscale

To illustrate, consider the following `Corefile`:

```Corefile
.:1053 {
        tailscale corp.example.com.
        forward . 100.100.100.100
        log
        errors
}
```

This configuration will cause `coredns` to answer queries for any host among
your peers in the zone `corp.example.com.`. Queries for any host which is _not_
in the specified zone will be forwarded to the Tailscale DNS server at
`100.100.100.100`.

```
$ dig -p 1053 sshfe2.$MAGICDNS.ts.net @127.0.0.1 +short
100.254.7.31
$ dig -p 1053 sshfe2.corp.example.com @127.0.0.1 +short
100.254.7.31
```

The behavior above is the same for `A` and `AAAA` queries. `CNAME` queries will
return the Magic DNS host name.

### Even more custom DNS zones!!1

In addition to the top-level zone which applies to all hosts on the Tailnet,
more zones can be added based on ACL tags applied to hosts. Consider:

```Corefile
.:1053 {
        tailscale corp.example.com. {
          tag campus-den den.corp.example.com.
          tag prod example.com.
        }
        forward . 100.100.100.100
        log
        errors
}
```

Now, any hosts to which the tag `campus-den` is applied will _also_ be queriable
under the `den.corp.example.com.` zone. Similarly, any host to which the tag
`prod` is applied will be queriable under the `example.com.` zone. The
additional zones needn't be subdomains of the top-level domain. This plugin
will assert itself as authoratative over any zone you configure. This is your
DNS; if you want to own yourself, feel free.


## Full Configuration Example

A full example looks like:

```Corefile
tailscale corp.example.com. {
  refresh 300s
  tag campus-den den.corp.example.com.
  tag prod example.com.
}
```

The `refresh` option may only be specified once. It determins how frequently the
Tailscale Local API is polled for peers and tags. You may speciy as many `tag`s
as you would like.


## Deployment

The only constraint for deployment is that the host must have a Tailscale Local
API.


