package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"regexp"
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

	setlog, ok := logLevels[*flagLogLevel]
	if !ok {
		ll.Fatalf("Invalid log level '%s'. Valid log levels are %v", *flagLogLevel, getLogLevels())
	}
	setlog()

	regex, err := regexp.Compile(*flagTapRegex)
	if err != nil {
		ll.Fatalf("unable to parse interface regex: %s", "test")
	}

	ll.Infoln("starting up...")
	ll.Infof("Loglevel '%s'", ll.GetLevel())
	ll.Infof("Handling Interfaces matching '%s'", regex.String())
	ll.Infof("Sending RAs valid for %v every %v", *flagLifeTime, *flagInterval)

	if flagLifeTime.Seconds() < 3*(flagInterval.Seconds()) {
		ll.Warnf(
			"WARN: lifetime (%v) should be at least 3*interval (%v), I hope you know what you're doing...",
			*flagLifeTime,
			*flagInterval,
		)
	}

	linksFeed := make(chan netlink.LinkUpdate, 10)
	linksDone := make(chan struct{})

	err = netlink.LinkSubscribe(linksFeed, linksDone)
	if err != nil {
		ll.Fatalf("unable to open netlink feed: %v", err)
	}

	// get existing list of links, in case we startup when vms are already active
	t, err := netlink.LinkList()
	if err != nil {
		ll.Fatalf("unable to get current list of links: %v", err)
	}

	e := NewEngine()

	// when starting up making sure any already existing interfaces are being handled and started
	for _, link := range t {

		ifName := link.Attrs().Name
		tapState := link.Attrs().OperState

		if !(regex.Match([]byte(ifName))) {
			ll.WithFields(ll.Fields{"Interface": ifName}).
				Debugf("%s did not match configured regex, skipping...", ifName)
			continue
		}

		if tapState == 6 && link.Attrs().Flags&net.FlagUp == net.FlagUp {
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

			ll.Tracef("Link: %v, admin: %v, state: %v", ifName, link.Attrs().Flags&net.FlagUp, tapState)
			ll.Tracef("Stats: %v", *link.Attrs().Statistics)

			if !(regex.Match([]byte(ifName))) {
				ll.Debugf("%s did not match configured regex, skipping...", ifName)
				continue
			}

			tapExists := e.Check(link.Attrs().Index)

			if !tapExists && tapState == 6 && link.Attrs().Statistics.TxPackets > 0 {
				e.Add(link.Attrs().Index)
			} else if tapExists && tapState != 6 {
				e.Close(link.Attrs().Index)
			} else {
				ll.Tracef("netlink fired for %s, Operstate: %s, but nothing to do?", ifName, tapState)
			}
		}
	}
}
