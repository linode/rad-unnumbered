package main

import (
	"context"
	"fmt"
	"github.com/mdlayher/ndp"
	ll "github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"net"
	"time"
)

// sending the actual RA
func doRA(ctx context.Context, c *ndp.Conn, addr net.HardwareAddr, prefix net.IP) error {

	m := &ndp.RouterAdvertisement{
		CurrentHopLimit:           64,
		RouterSelectionPreference: ndp.Medium,
		RouterLifetime:            *flagLifeTime,
		Options: []ndp.Option{
			&ndp.PrefixInformation{
				PrefixLength:                   64,
				AutonomousAddressConfiguration: true,
				ValidLifetime:                  2 * *flagLifeTime,
				PreferredLifetime:              *flagLifeTime,
				Prefix:                         prefix,
			},
			&ndp.LinkLayerAddress{
				Direction: ndp.Source,
				Addr:      addr,
			},
		},
	}

	// Expect any router solicitation message.
	check := func(m ndp.Message) bool {
		_, ok := m.(*ndp.RouterSolicitation)
		return ok
	}

	// Trigger an RA whenever an RS is received.
	rsC := make(chan struct{})
	recv := func(msg ndp.Message, from net.IP) {
		ll.Debugf("received %v from %v", msg.Type(), from)
		rsC <- struct{}{}
	}

	// We are now a "router".
	if err := c.JoinGroup(net.IPv6linklocalallrouters); err != nil {
		return fmt.Errorf("failed to join multicast group: %v", err)
	}

	var eg errgroup.Group
	eg.Go(func() error {
		// Send messages until cancelation or error.
		for {
			//ll.Debugf("sending RA")
			if err := c.WriteTo(m, nil, net.IPv6linklocalallnodes); err != nil {
				return fmt.Errorf("failed to send router advertisement: %v", err)
			}

			select {
			case <-ctx.Done():
				return nil
			// Trigger RA at regular intervals or on demand.
			case <-time.After(*flagInterval):
			case <-rsC:
			}
		}
	})

	if err := receiveLoop(ctx, c, check, recv); err != nil {
		return fmt.Errorf("failed to receive router solicitations: %v", err)
	}

	return eg.Wait()
}

// check for RS to come in
func receiveLoop(ctx context.Context, c *ndp.Conn, check func(m ndp.Message) bool, recv func(msg ndp.Message, from net.IP)) error {
	var count int
	for {
		msg, from, err := receive(ctx, c, check)
		switch err {
		case context.Canceled:
			ll.Debugf("received %d message(s)", count)
			return nil
		case errRetry:
			continue
		case nil:
			count++
			recv(msg, from)
		default:
			return err
		}
	}
}

// if a RS hit, read it
func receive(ctx context.Context, c *ndp.Conn, check func(m ndp.Message) bool) (ndp.Message, net.IP, error) {
	if err := c.SetReadDeadline(time.Now().Add(1 * time.Second)); err != nil {
		return nil, nil, fmt.Errorf("failed to set deadline: %v", err)
	}

	msg, _, from, err := c.ReadFrom()
	if err == nil {
		if check != nil && !check(msg) {
			// Read a message, but it isn't the one we want.  Keep trying.
			return nil, nil, errRetry
		}

		// Got a message that passed the check, if check was not nil.
		return msg, from, nil
	}

	// Was the context canceled already?
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	default:
	}

	// Was the error caused by a read timeout, and should the loop continue?
	if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
		return nil, nil, errRetry
	}

	return nil, nil, fmt.Errorf("failed to read message: %v", err)
}
