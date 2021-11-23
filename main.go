package main

import (
	"context"
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
	flagLogLevel = flag.String("loglevel", "info", fmt.Sprintf("Log level. One of %v", getLogLevels()))
	flagTapRegex = flag.String("regex", "tap.*_0", "regex to match interfaces.")
	flagLifeTime = flag.Duration("lifetime", (30 * time.Minute), "Lifetime.")
	flagInterval = flag.Duration("interval", (10 * time.Minute), "Frequency of *un*solicitated RAs.")
	errRetry     = errors.New("retry")
	taps         = make(map[string]tapRA)
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

type tapRA struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func main() {
	flag.Parse()

	fn, ok := logLevels[*flagLogLevel]
	if !ok {
		ll.Fatalf("Invalid log level '%s'. Valid log levels are %v", *flagLogLevel, getLogLevels())
	}
	fn()

	ll.SetFormatter(&ll.TextFormatter{
		FullTimestamp: true,
		PadLevelText:  true,
	})

	ll.Infoln("starting up...")
	ll.Infof("Setting log level to '%s'", ll.GetLevel())

	regex, err := regexp.Compile(*flagTapRegex)
	if err != nil {
		ll.Fatalf("unable to parse interface regex: %s", "test")
	}

	ll.Infof("Handling Interfaces matching '%s'", regex.String())
	ll.Infof("Sending RAs valid for %v every %v", *flagLifeTime, *flagInterval)

	if flagLifeTime.Seconds() < 3*(flagInterval.Seconds()) {
		ll.Warnf(
			"WARN: lifetime (%v) should be at least 3*interval (%v), I hope you know what you're doing...",
			*flagLifeTime,
			*flagInterval,
		)
	}

	linksFeed := make(chan netlink.LinkUpdate)
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

		if !(regex.Match([]byte(ifName))) {
			ll.Debugf("%s did not match configured regex, skipping...", ifName)
			continue
		}

		if link.Attrs().OperState == 6 && link.Attrs().Flags&net.FlagUp == net.FlagUp {
			ll.Infof("adding existing link: %v", ifName)
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

			ll.Debugf("Link: %v, admin: %v, state: %v", ifName, link.Attrs().Flags&net.FlagUp, link.Attrs().OperState)
			ll.Tracef("Stats: %v", *link.Attrs().Statistics)

			if !(regex.Match([]byte(ifName))) {
				ll.Debugf("%s did not match configured regex, skipping...", ifName)
				continue
			}

			tapExists := e.Check(link.Attrs().Index)

			if !tapExists && link.Attrs().OperState == 6 && link.Attrs().Statistics.TxPackets > 0 {
				ll.Infof("adding new link: %v", ifName)
				e.Add(link.Attrs().Index)
			} else if tapExists && link.Attrs().OperState != 6 {
				ll.Infof("removing link: %v", ifName)
				e.Close(link.Attrs().Index)
			} else {
				ll.Debugf("netlink fired for %s, but nothing to do?", ifName)
			}
		}
	}
}
