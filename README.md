# rad-unnumbered

### what is rad-unnumbered
rad-unnumbered is a very light weight ipv6 RA server that dynamically detects and handles l3 unnumbered tap interfaces on a hypervisor for ipv6 forwarding


### how does it work
- it finds tap interfaces dynamically through netlink push msg as they are created/destroyed
- it matches tap interface name by regex, to handle only matching interfaces (tap.*_0), can be configured through command line
- if tap matches regex AND has at least one route pointing to it, it will send RAs advertising a default route on that interface
- if tap matches regex AND also has a host route (aka /128) pointing there, it will pick the first host route in the list and advertise that as a /64 prefix so clients can auto configure themselfs with a slaac IP.


### NOTE:
- rad-unnumbered *assumes* the host route found is actually matching the SLAAC ip for the VMs assigned mac address - but rad-unnumbered has no knowledge of this mac to verify


### usage:
```
./rad-unnumbered --help
```
