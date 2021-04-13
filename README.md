# my-ndpd

### what is my-ndpd
my-ndpd is a very light weight ipv6 "route-advertisment" and "route-solicitation" servivce designed for unnumbered l3 tap interfaces

### how does it work
- it finds tap interfaces dynamically through netlink push msg as they are created/destroyed
- if matched tap name by regex, to handle only matching interfaces (tap.*_0), can be configured through command line
- if tap matches it will advertise default routes on that interface
- if tap matches my-ndpd will look out the routes pointing to that interface, picking the first one (subject to change) and using that as /64 for slaac announcements


### usage:
```
Usage of ./my-ndpd:
  -interval duration
        Frequency of *un*solicitated RAs. (default 10m0s)
  -lifetime duration
        Lifetime. (default 30m0s)
  -loglevel string
        Log level. One of [info warning error fatal none trace debug] (default "info")
  -regex string
        regex to match interfaces. (default "tap.*_0")

```
