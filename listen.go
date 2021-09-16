package main

import (
	"context"
	"fmt"
	"github.com/mdlayher/ndp"
	ll "github.com/sirupsen/logrus"
	"net"
	"time"
)

// starts a RouteSolicitation listener on a tap. we need to respond to a rs right away when a linode comes up or is ready to configure it interface
func addTap(ctx context.Context, ifName string) {
	go func() {
		if err := listen(ctx, ifName); err != nil {
			// Context cancel means a signal was sent, so no need to log an error.
			if err == context.Canceled {
				ll.Infof("%s closed", ifName)
				delete(taps, ifName)
			} else {
				ll.Errorf("%s failed", err)
				delete(taps, ifName)
			}
		}
	}()
}

// trigger RAs based on interval and/or RS
func listen(ctx context.Context, ifName string) error {
	prefix, subnets, err := getHostRoutesIpv6(ifName)
	if err != nil {
		return fmt.Errorf("Failed getting routes for if %v: %v", ifName, err)
	}

	ll.Debugf("host routes found on %v: %v", ifName, prefix)
	ll.Debugf("subnet routes found on %v: %v", ifName, subnets)

	if prefix == nil && subnets == nil {
		ll.Infof("neither host nor subnet routes to this tap. this may be a vlan interface, ignoring comletely")
		return context.Canceled
	}

	var prefixChosen net.IP
	if prefix != nil {
		prefixMask := net.CIDRMask(64, 128)
		prefixChosen = prefix[0].IP.Mask(prefixMask)
	} else {
		ll.Infof("no host routes on this interface, only advertising RA without prefix for SLAAC")
	}
	ll.Infof("Advertising %v: %v", ifName, prefixChosen)

	ifi, err := net.InterfaceByName(ifName)
	if err != nil {
		return fmt.Errorf("unable to find interface: %v", err)
	}

	// need this hacky loop since there are regular occasions where the OS seems to lock the tap for about 15sec on innitial creation.
	// causing the dialer to fail. this loop checks the context for cancellation but otherwise continues to re-try
	var c *ndp.Conn
	var ip net.IP
	for {
		c, ip, err = ndp.Listen(ifi, ndp.LinkLocal)
		if err != nil {
			ll.Warnf("unable to dial linklocal: %v, retrying...", err)
			time.Sleep(1 * time.Second)
			// Was the context canceled already?
			select {
			case <-ctx.Done():
				return fmt.Errorf("got stopped by %v while still dialing %v", ctx.Err(), err)
			default:
			}
		} else {
			ll.Debugf("successfully dialed linklocal: %v", ifi.Name)
			break
		}
	}
	defer c.Close()

	ll.Debugf("interface: %s, mac: %s, ip: %s", ifi.Name, ifi.HardwareAddr, ip)

	return doRA(ctx, c, ifi.HardwareAddr, prefixChosen)
}
