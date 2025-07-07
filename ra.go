package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/mdlayher/ndp"
	ll "github.com/sirupsen/logrus"
	"golang.org/x/net/ipv6"
	"golang.org/x/sync/errgroup"
)

// sending the actual RA
func (t Tap) doRA(c *ndp.Conn) error {
	eg, ctxx := errgroup.WithContext(t.ctx)
	eg.Go(func() error { return t.sendLoop(ctxx, c) })
	eg.Go(func() error { return t.receiveLoop(ctxx, c) })

	return eg.Wait()
}

// tiggers RouterAdvertisements every Interval duration or when a RouterSolicit was received on the interface
func (t Tap) sendLoop(ctx context.Context, c *ndp.Conn) error {
	var options = []ndp.Option{
		&ndp.LinkLayerAddress{
			Direction: ndp.Source,
			Addr:      t.Ifi.HardwareAddr,
		},
		ndp.NewMTU(uint32(t.Ifi.MTU)),
	}

	if t.Prefix != nil {
		options = append(options, &ndp.PrefixInformation{
			PrefixLength:                   64,
			AutonomousAddressConfiguration: true,
			ValidLifetime:                  3 * *flagLifeTime,
			PreferredLifetime:              *flagLifeTime,
			Prefix:                         t.Prefix,
		})
	}

	m := &ndp.RouterAdvertisement{
		CurrentHopLimit:           64,
		RouterSelectionPreference: ndp.Medium,
		RouterLifetime:            *flagLifeTime,
		Options:                   options,
	}

	// Send messages until cancelation or error.
	count := 0
	for {
		ll.WithFields(ll.Fields{"Interface": t.Ifi.Name}).Debugf("%s sent RA prefix %s", t.Ifi.Name, t.Prefix)
		count++
		if err := c.WriteTo(m, nil, net.IPv6linklocalallnodes); err != nil {
			return fmt.Errorf("failed to send router advertisement: %v", err)
		}

		select {
		case <-ctx.Done():
			ll.WithFields(ll.Fields{"Interface": t.Ifi.Name}).
				Debugf("%s sender closed, sent: %d advertisements", t.Ifi.Name, count)
			return ctx.Err()
		// Trigger RA at regular intervals or on demand.
		case <-time.After(*flagInterval):
		case <-t.rs:
		}
	}
}

// receiveLoop endlessly checks for RouterSolicits while also checking if Context has been cancelled
func (t Tap) receiveLoop(ctx context.Context, c *ndp.Conn) error {
	count := 0
	for {
		select {
		case <-ctx.Done():
			ll.WithFields(ll.Fields{"Interface": t.Ifi.Name}).
				Debugf("%s listener closed, received: %d solicits", t.Ifi.Name, count)
			return ctx.Err()
		default:
		}

		_, from, err := receiveRS(c)
		switch err {
		case errRetry:
			continue
		case nil:
			count++
			ll.WithFields(ll.Fields{"Interface": t.Ifi.Name}).Debugf("%s received RS from %s", t.Ifi.Name, from)
			t.rs <- struct{}{}
		default:
			return err
		}
	}
}

// receiveRS reads RouterSolicitsts but tries to keep it brief
func receiveRS(c *ndp.Conn) (ndp.Message, net.IP, error) {
	if err := c.SetReadDeadline(time.Now().Add(1 * time.Second)); err != nil {
		return nil, nil, fmt.Errorf("failed to set deadline: %v", err)
	}

	msg, _, from, err := c.ReadFrom()
	if err == nil {
		ll.Tracef("received %d...", msg.Type())
		if msg.Type() != ipv6.ICMPTypeRouterSolicitation {
			// Read a message, but it isn't a router solicit.  Keep trying.
			return nil, nil, errRetry
		}

		// Got a Solicit
		return msg, from, nil
	}

	// Was the error caused by a read timeout, and should the loop continue?
	if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
		return nil, nil, errRetry
	}

	return nil, nil, fmt.Errorf("failed to read message: %v", err)
}
