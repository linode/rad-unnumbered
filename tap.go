package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/mdlayher/ndp"
	ll "github.com/sirupsen/logrus"
	"golang.org/x/net/ipv6"
)

// Tap is the interface object
type Tap struct {
	c      *ndp.Conn
	Ifi    *net.Interface
	ctx    context.Context
	Close  context.CancelFunc
	Prefix net.IP
	rs     chan struct{}
}

// NewTap finds, verifies and gets all aparms for a new Tap and returns the object
func NewTap(idx int) (*Tap, error) {

	ifi, err := net.InterfaceByIndex(idx)
	if err != nil {
		return nil, fmt.Errorf("unable to get interface: %v", err)
	}

	hostRoutes, subnets, err := getHostRoutesIpv6(ifi.Index)
	if err != nil {
		return nil, fmt.Errorf("failed getting routes for if %v: %v", ifi.Name, err)
	}

	ll.WithFields(ll.Fields{"Interface": ifi.Name}).Debugf("host routes found on %v: %v", ifi.Name, hostRoutes)
	ll.WithFields(ll.Fields{"Interface": ifi.Name}).Debugf("subnet routes found on %v: %v", ifi.Name, subnets)

	if hostRoutes == nil && subnets == nil {
		return nil, fmt.Errorf(
			"neither host nor subnet routes to this tap. this may be a private vlan interface, ignoring comletely",
		)
	}

	var prefixChosen net.IP
	if hostRoutes == nil {
		ll.WithFields(ll.Fields{"Interface": ifi.Name}).
			Warnf("%s has no host routes, only advertising RA without prefix for SLAAC", ifi.Name)
	} else {
		// setting a /64 prefix since thats what I need for the SLAAC advertisements
		prefixMask := net.CIDRMask(64, 128)
		// just picking the first in the available list (and setting bits 65-128 to 0)
		prefixChosen = hostRoutes[0].IP.Mask(prefixMask)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Tap{
		ctx:    ctx,
		Close:  cancel,
		Ifi:    ifi,
		Prefix: prefixChosen,
		rs:     make(chan struct{}),
	}, nil
}

// Listen starts listening for RouterSolicits on this tap and sends periodic RAs
func (t Tap) Listen() error {
	var c *ndp.Conn
	var ip net.IP
	var err error

	// need this hacky loop since there are occasions where the OS seems to lock the tap for about 15sec (or sometimes longer)
	// on innitial creation. causing the dialer to fail.
	// this loop checks the context for cancellation but otherwise continues to re-try
	counter := 0
	for {
		c, ip, err = ndp.Listen(t.Ifi, ndp.LinkLocal)
		if err != nil {
			counter++
			ll.WithFields(ll.Fields{"Interface": t.Ifi.Name}).
				Warnf("unable to dial linklocal: %s, retrying in 1s... %d", err, counter)
			// Was the context canceled already?
			select {
			case <-t.ctx.Done():
				return context.Canceled
				//fmt.Errorf("got stopped by %v while still dialing %v", t.ctx.Err(), err)
			default:
			}
			time.Sleep(1 * time.Second)
		} else {
			ll.WithFields(ll.Fields{"Interface": t.Ifi.Name}).Debugf("successfully dialed linklocal: %v", t.Ifi.Name)
			break
		}
	}
	defer c.Close()

	// filter incoming ICMPs to be limited to RouterSolicits
	f := &ipv6.ICMPFilter{}
	f.SetAll(true)
	f.Accept(ipv6.ICMPTypeRouterSolicitation)
	if err := c.SetICMPFilter(f); err != nil {
		return fmt.Errorf("failed to apply ICMP type filter: %v", err)
	}

	// We are a "router", lets join the MC group
	if err := c.JoinGroup(net.IPv6linklocalallrouters); err != nil {
		return fmt.Errorf("failed to join multicast group: %v", err)
	}

	ll.WithFields(ll.Fields{"Interface": t.Ifi.Name}).
		Debugf("handling interface: %s, mac: %s, src ip: %s", t.Ifi.Name, t.Ifi.HardwareAddr, ip)

	return t.doRA(c)
}
