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

func main() {
	flagLogLevel := flag.String("loglevel", "info", fmt.Sprintf("Log level. One of %v", getLogLevels()))
	flagTapRegex := flag.String("regex", "tap.*_0", "regex to match interfaces.")
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
			ifName := link.Attrs().Name
			tapState := link.Attrs().OperState

			if !e.Qualifies(ifName) {
				ll.WithFields(ll.Fields{"Interface": ifName}).
					Debugf("%s did not qualify, skipping...", ifName)
				continue
			}

			ll.WithFields(ll.Fields{"Interface": ifName}).Tracef(
				"Netlink fired: %v, admin: %v, OperState: %v, Rx/Tx: %v/%v",
				ifName,
				link.Attrs().Flags&net.FlagUp,
				tapState,
				link.Attrs().Statistics.RxPackets,
				link.Attrs().Statistics.TxPackets,
			)

			tapExists := e.Exists(link.Attrs().Index)

			if !tapExists && linkReady(link.Attrs()) {
				e.Add(link.Attrs().Index)
			} else if tapExists && !linkReady(link.Attrs()) {
				e.Close(link.Attrs().Index)
			} else {
				ll.Tracef("%s Exists: %v, OperState: %s ... nothing to do?", ifName, tapExists, tapState)
			}
		}
	}
}
