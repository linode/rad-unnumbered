package main

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/mdlayher/ndp"
	ll "github.com/sirupsen/logrus"
)

type Engine struct {
	tap  map[int]*tap
	lock sync.RWMutex
}

func NewEngine() *Engine {
	return &Engine{
		tap:  make(map[int]*tap),
		lock: sync.RWMutex{},
	}
}

func (e *Engine) Add(ifIdx int) {
	go func() {
		t, err := NewTap(ifIdx)

		e.lock.Lock()
		e.tap[ifIdx] = t
		e.lock.Unlock()

		if err := e.Listen(t); err != nil {
			// Context cancel means a signal was sent, so no need to log an error.
			if err == context.Canceled {
				ll.Infof("%s closed", t.Name)
			} else {
				ll.Errorf("%s failed with %s", t.Name, err)
			}
			e.lock.Lock()
			delete(e.tap, ifIdx)
			e.lock.Unlock()
		}
	}()

}

func (e *Engine) Check(ifIdx int) bool {
	e.lock.RLock()
	_, exists := e.tap[ifIdx]
	e.lock.RUnlock()
	return exists
}

func (e *Engine) Close(ifIdx int) {
	e.tap[ifIdx].Cancel()
	e.lock.Lock()
	delete(e.tap, ifIdx)
	e.lock.Unlock()
}

type tap struct {
	c            *ndp.Conn
	Ifi          net.Interface
	ctx          context.Context
	Cancel       context.CancelFunc
	HostRoutes   []*net.IPNet
	SubnetRoutes []*net.IPNet
}

func NewTap(idx int) (*tap, error) {
	ifi, err := net.InterfaceByIndex(idx)
	if err != nil {
		return nil, fmt.Errorf("unable to get interface: %v", err)
	}

	hostRoutes, subnets, err := getHostRoutesIpv6(ifi.Name)
	if err != nil {
		return nil, fmt.Errorf("failed getting routes for if %v: %v", t.Name, err)
	}

	ll.Debugf("host routes found on %v: %v", ifi.Name, hostRoutes)
	ll.Debugf("subnet routes found on %v: %v", ifi.Name, subnets)

	if hostRoutes == nil && subnets == nil {
		return nil, fmt.Errorf(
			"neither host nor subnet routes to this tap. this may be a private vlan interface, ignoring comletely",
		)
	}

	var prefixChosen net.IP
	if hostRoutes == nil {
		ll.WithFields(ll.Fields{"Interface": t.Name}).
			Warnf("%s has no host routes, only advertising RA without prefix for SLAAC", t.Name)
	} else {
		// setting a  /64 prefix since thats what I need for the SLAAC advertisements
		prefixMask := net.CIDRMask(64, 128)
		// just picking the first in the available list (and setting bits 65-128 to 0)
		prefixChosen = hostRoutes[0].IP.Mask(prefixMask)
	}

	/*
		maybe move this block??
		I need something to immediately cancel it all once ti interface disappears again

	*/
	var c *ndp.Conn
	var ip net.IP

	deadline := time.NewTimer(1 * time.Minute)

	// need this hacky loop since there are occasions where the OS seems to lock the tap for about 15sec (or sometimes longer)
	// on innitial creation. causing the dialer to fail.
	// this loop checks the context for cancellation but otherwise continues to re-try
	for {
		c, ip, err = ndp.Listen(ifi, ndp.LinkLocal)
		if err != nil {
			ll.Warnf("unable to dial linklocal: %v, retrying...", err)
			time.Sleep(1 * time.Second)
			// Was the context canceled already?
			select {
			case <-deadline.C:
				return fmt.Errorf("got stopped by %v while still dialing %v", t.ctx.Err(), err)
			default:
			}
		} else {
			ll.Debugf("successfully dialed linklocal: %v", ifi.Name)
			break
		}
	}
	defer c.Close()

	ll.WithFields(ll.Fields{"Interface": t.Name}).Infof("%s advertising: %v", t.Name, prefixChosen)
	ll.WithFields(ll.Fields{"Interface": t.Name}).
		Debugf("interface: %s, mac: %s, ip: %s", ifi.Name, ifi.HardwareAddr, ip)

	ctx, cancel := context.WithCancel(context.Background())

	return &tap{
		Ifi:          ifi,
		ctx:          ctx,
		Cancel:       cancel,
		HostROutes:   hostRoutes,
		SubnetRoutes: subnets,
	}
}

// trigger RAs based on interval and/or RS
func (t *tap) Listen() error {
	return doRA(t.ctx, c, ifi.HardwareAddr, prefixChosen)
}
