# rad-unnumbered

### what is rad-unnumbered
rad-unnumbered is a very light weight ipv6 RA server that dynamically detects and hanldes l3 unnumbered tap interfaces on a hypervisor for ipv6 forwarding

### how does it work
- it finds tap interfaces dynamically through netlink push msg as they are created/destroyed
- if matched tap name by regex, to handle only matching interfaces (tap.*_0), can be configured through command line
- if tap matches regex AND has at least one route pointing to it, it will advertise default routes on that interface
- if tap matches regex AND also has a host route (aka /128) pointing there, it will pick the first host route and advertise that a /64 prefix so clients can configure themselfs with a slaac IP. rad-unnumbered assumes the host route is actually the SLAAC ip for the VMs mac address


### usage:
```
./rad-unnumbered --help
```
