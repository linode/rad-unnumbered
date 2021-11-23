package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/mdlayher/ndp"
	ll "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

// sending the actual RA
func doRA(ctx context.Context, c *ndp.Conn, addr net.HardwareAddr, prefix net.IP) error {
	// Trigger an RA whenever an RS is received.
	rs := make(chan struct{})

	// We are now a "router".
	if err := c.JoinGroup(net.IPv6linklocalallrouters); err != nil {
		return fmt.Errorf("failed to join multicast group: %v", err)
	}

	eg, ctxx := errgroup.WithContext(ctx)
	eg.Go(func() error { return sendLoop(ctxx, c, rs, addr, prefix) })
	eg.Go(func() error { return receiveLoop(ctxx, c, rs) })

	return eg.Wait()
}

func sendLoop(ctx context.Context, c *ndp.Conn, rs <-chan struct{}, addr net.HardwareAddr, prefix net.IP) error {
	m := &ndp.RouterAdvertisement{
		CurrentHopLimit:           64,
		RouterSelectionPreference: ndp.Medium,
		RouterLifetime:            *flagLifeTime,
		Options: []ndp.Option{
			&ndp.PrefixInformation{
				PrefixLength:                   64,
				AutonomousAddressConfiguration: true,
				ValidLifetime:                  3 * *flagLifeTime,
				PreferredLifetime:              *flagLifeTime,
				Prefix:                         prefix,
			},
			&ndp.LinkLayerAddress{
				Direction: ndp.Source,
				Addr:      addr,
			},
			ndp.NewMTU(1500),
		},
	}

	// Send messages until cancelation or error.
	for {
		select {
		case <-ctx.Done():
			return nil
		// Trigger RA at regular intervals or on demand.
		case <-time.After(*flagInterval):
		case <-rs:
		}

		//ll.Debugf("sending RA")
		if err := c.WriteTo(m, nil, net.IPv6linklocalallnodes); err != nil {
			return fmt.Errorf("failed to send router advertisement: %v", err)
		}
	}
}

// check for RS to come in
func receiveLoop(ctx context.Context, c *ndp.Conn, rs chan<- struct{}) error {
	count := 0
	for {
		select {
		case <-ctx.Done():
			ll.Infof("done serving after %v RS", count)
			return nil
		default:
		}

		msg, from, err := receiveRS(c)
		switch err {
		case errRetry:
			continue
		case nil:
			count++
			ll.Debugf("received %v from %v", msg.Type(), from)
			rs <- struct{}{}
		default:
			return err
		}
	}
}

// if a RS hit, read it
func receiveRS(c *ndp.Conn) (ndp.Message, net.IP, error) {
	if err := c.SetReadDeadline(time.Now().Add(1 * time.Second)); err != nil {
		return nil, nil, fmt.Errorf("failed to set deadline: %v", err)
	}

	msg, _, from, err := c.ReadFrom()
	if err == nil {
		if msg.Type() == 133 {
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
