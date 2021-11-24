package main

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/mdlayher/ndp"
	ll "github.com/sirupsen/logrus"
	"golang.org/x/net/ipv6"
)

// Engine is the main object collecting all running taps
type Engine struct {
	tap  map[int]Tap
	lock sync.RWMutex
}

// NewEngine just setups up a empty new engine
func NewEngine() *Engine {
	return &Engine{
		tap:  make(map[int]Tap),
		lock: sync.RWMutex{},
	}
}

// Add adds a new Interface to be handled by the engine
func (e *Engine) Add(ifIdx int) {
	t, err := NewTap(ifIdx)
	if err != nil {
		ll.WithFields(ll.Fields{"InterfaceID": ifIdx}).Errorf("failed adding ifIndex %d: %s", ifIdx, err)
		return
	}

	ll.WithFields(ll.Fields{"Interface": t.Ifi.Name}).Infof("adding %s with prefix %s", t.Ifi.Name, t.Prefix)

	e.lock.Lock()
	//assigning a copy to the map so I don't have to deal with concurrency
	e.tap[ifIdx] = *t
	e.lock.Unlock()

	go func() {
		if err := t.Listen(); err != nil {
			// Context cancel means a signal was sent, so no need to log an error.
			if err == context.Canceled {
				ll.WithFields(ll.Fields{"Interface": t.Ifi.Name}).Infof("%s closed", t.Ifi.Name)
			} else {
				ll.WithFields(ll.Fields{"Interface": t.Ifi.Name}).Errorf("%s failed with %s", t.Ifi.Name, err)
			}
			e.lock.Lock()
			delete(e.tap, ifIdx)
			e.lock.Unlock()
		}
	}()
}

// Get returns a lookedup Tap interface thread safe
func (e *Engine) Get(ifIdx int) Tap {
	e.lock.RLock()
	defer e.lock.RUnlock()
	return e.tap[ifIdx]
}

// Check verifies (thread safe) if tap  is already handled or not
func (e *Engine) Check(ifIdx int) bool {
	e.lock.RLock()
	_, exists := e.tap[ifIdx]
	e.lock.RUnlock()
	return exists
}

// Close stops handling a Tap interfaces and drops it from the map - thread safe
func (e *Engine) Close(ifIdx int) {
	e.lock.RLock()
	tap := e.tap[ifIdx]
	e.lock.RUnlock()
	ifName := tap.Ifi.Name
	ll.WithFields(ll.Fields{"Interface": ifName}).Infof("removing %s", ifName)
	tap.Close()
}

// Tap is the interface object
type Tap struct {
	c       *ndp.Conn
	Ifi     *net.Interface
	ctx     context.Context
	Close   context.CancelFunc
	Prefix  net.IP
	IPs     []*net.IPNet
	Subnets []*net.IPNet
	rs      chan struct{}
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
		ctx:     ctx,
		Close:   cancel,
		Ifi:     ifi,
		Prefix:  prefixChosen,
		IPs:     hostRoutes,
		Subnets: subnets,
		rs:      make(chan struct{}),
	}, nil
}

// Listen starts listening for RS on this tap and sends periodic RAs
func (t Tap) Listen() error {
	var c *ndp.Conn
	var ip net.IP
	var err error

	// need this hacky loop since there are occasions where the OS seems to lock the tap for about 15sec (or sometimes longer)
	// on innitial creation. causing the dialer to fail.
	// this loop checks the context for cancellation but otherwise continues to re-try
	for {
		c, ip, err = ndp.Listen(t.Ifi, ndp.LinkLocal)
		if err != nil {
			ll.WithFields(ll.Fields{"Interface": t.Ifi.Name}).Warnf("unable to dial linklocal: %s, retrying...", err)
			time.Sleep(1 * time.Second)
			// Was the context canceled already?
			select {
			case <-t.ctx.Done():
				return context.Canceled
				//fmt.Errorf("got stopped by %v while still dialing %v", t.ctx.Err(), err)
			default:
			}
		} else {
			ll.WithFields(ll.Fields{"Interface": t.Ifi.Name}).Debugf("successfully dialed linklocal: %v", t.Ifi.Name)
			break
		}
	}
	defer c.Close()

	f := &ipv6.ICMPFilter{}
	f.SetAll(true)
	f.Accept(ipv6.ICMPTypeRouterSolicitation)
	if err := c.SetICMPFilter(f); err != nil {
		return fmt.Errorf("failed to apply ICMP type filter: %v", err)
	}

	// We are now a "router".
	if err := c.JoinGroup(net.IPv6linklocalallrouters); err != nil {
		return fmt.Errorf("failed to join multicast group: %v", err)
	}

	ll.WithFields(ll.Fields{"Interface": t.Ifi.Name}).
		Debugf("handling interface: %s, mac: %s, src ip: %s", t.Ifi.Name, t.Ifi.HardwareAddr, ip)

	return t.doRA(c)
}
