package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"time"

	ll "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

var (
	flagLifeTime = flag.Duration("lifetime", (30 * time.Minute), "Lifetime (prefix valid time will be 3x lifetime).")
	flagInterval = flag.Duration("interval", (5 * time.Minute), "Frequency of *un*solicitated RAs.")
	errRetry     = errors.New("retry")
	exclude      IPNets
)

var logLevels = map[string]func(){
	"none":    func() { ll.SetOutput(ioutil.Discard) },
	"trace":   func() { ll.SetLevel(ll.TraceLevel) },
	"debug":   func() { ll.SetLevel(ll.DebugLevel) },
	"info":    func() { ll.SetLevel(ll.InfoLevel) },
	"warning": func() { ll.SetLevel(ll.WarnLevel) },
	"error":   func() { ll.SetLevel(ll.ErrorLevel) },
	"fatal":   func() { ll.SetLevel(ll.FatalLevel) },
}

func getLogLevels() []string {
	var levels []string
	for k := range logLevels {
		levels = append(levels, k)
	}
	return levels
}

type IPNets []net.IPNet

func (i *IPNets) String() string {
	var s string
	for _, ip := range *i {
		j, _ := ip.Mask.Size()
		s = s + " " + fmt.Sprintf("%v/%v", ip.IP, j)
	}
	return s
}

func (i *IPNets) Set(value string) error {
	ip, snet, err := net.ParseCIDR(value)
	if err != nil {
		return fmt.Errorf("invalid subnet: %v", value)
	}
	if ip.To4() != nil {
		return fmt.Errorf("not a ipv6 subnet: %v", value)
	}
	*i = append(*i, *snet)
	return nil
}

func main() {
	flagLogLevel := flag.String("loglevel", "info", fmt.Sprintf("Log level. One of %v", getLogLevels()))
	flagTapRegex := flag.String("regex", "tap.*_0", "regex to match interfaces.")
	flag.Var(&exclude, "exclude", "subnet to be excluded from slaac advertisments")
	flag.Parse()

	ll.SetFormatter(&ll.TextFormatter{
		FullTimestamp: true,
		PadLevelText:  true,
	})

	setlvl, ok := logLevels[*flagLogLevel]
	if !ok {
		ll.Fatalf("Invalid log level '%s'. Valid log levels are %v", *flagLogLevel, getLogLevels())
	}
	setlvl()

	ll.Infoln("starting up...")
	ll.Infof("Loglevel '%s'", ll.GetLevel())
	ll.Infof("Sending RAs valid for %v every %v on interfaces matching %s", *flagLifeTime, *flagInterval, *flagTapRegex)
	ll.Infof("Excluding %s from RAs", exclude)

	if flagLifeTime.Seconds() < 3*(flagInterval.Seconds()) {
		ll.Warnf(
			"WARN: lifetime (%v) should be at least 3*interval (%v), I hope you know what you're doing...",
			*flagLifeTime,
			*flagInterval,
		)
	}

	linksFeed := make(chan netlink.LinkUpdate, 10)
	linksDone := make(chan struct{})

	// lets hook into the netlink channel for push notifications from the kernel
	err := netlink.LinkSubscribe(linksFeed, linksDone)
	if err != nil {
		ll.Fatalf("unable to open netlink feed: %v", err)
	}

	// get existing list of links, in case we startup when vms are already active
	t, err := netlink.LinkList()
	if err != nil {
		ll.Fatalf("unable to get current list of links: %v", err)
	}

	e, err := NewEngine(*flagTapRegex)
	if err != nil {
		ll.Fatalf("unable to get started: %v", err)
	}

	// when starting up making sure any already existing interfaces are being handled and started
	for _, link := range t {

		ifName := link.Attrs().Name

		if !e.Qualifies(ifName) {
			ll.WithFields(ll.Fields{"Interface": ifName}).
				Debugf("%s did not qualify, skipping...", ifName)
			continue
		}

		if linkReady(link.Attrs()) {
			e.Add(link.Attrs().Index)
		}
	}

	// as we go on, detect any NIC changes from netlink and act accordingly
	for {
		select {
		case <-linksDone:
			ll.Fatalln("netlink feed ended")
		case link := <-linksFeed:
			linkAttrs := link.Attrs()
			if linkAttrs == nil {
				ll.Tracef("Skipping unkown link: %s", link.Type())
				continue
			}

			ifName := linkAttrs.Name
			tapState := linkAttrs.OperState

			if !e.Qualifies(ifName) {
				ll.WithFields(ll.Fields{"Interface": ifName}).
					Debugf("%s did not qualify, skipping...", ifName)
				continue
			}

			if linkAttrs.Statistics == nil {
				ll.WithFields(ll.Fields{"Interface": ifName}).Infof(
					"Netlink fired: %v, admin: %v, OperState: %v",
					ifName,
					linkAttrs.Flags&net.FlagUp,
					tapState,
				)
			}
			ll.WithFields(ll.Fields{"Interface": ifName}).Tracef(
				"Netlink fired: %v, admin: %v, OperState: %v",
				ifName,
				linkAttrs.Flags&net.FlagUp,
				tapState,
			)

			tapExists := e.Exists(linkAttrs.Index)

			if !tapExists && linkReady(linkAttrs) {
				e.Add(linkAttrs.Index)
			} else if tapExists && !linkReady(linkAttrs) {
				e.Close(linkAttrs.Index)
			} else {
				ll.Tracef("%s Exists: %v, OperState: %s ... nothing to do?", ifName, tapExists, tapState)
			}
		}
	}
}
