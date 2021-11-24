package main

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
)

func linkReady(l *netlink.LinkAttrs) bool {
	// on upcoming interfaces I'm just waiting for the TX counter to count up 1
	// not really needed but it just saves more errors and retries later on the socket binding in the tap.Listen call
	// this is due to the fact that the tap needs to give itself a ipv6 LL and do dad etc
	// and doing it this way is actually faster and plays nice with live migrations
	if l.OperState == 6 && l.Flags&net.FlagUp == net.FlagUp && l.Statistics.TxPackets > 0 {
		return true
	}
	return false
}

// getHostRoutesIpv6 finds all routes for a interfaces and returns them broken out in host routes and subnet routes
func getHostRoutesIpv6(ifIdx int) ([]*net.IPNet, []*net.IPNet, error) {
	nlh, err := netlink.NewHandle()
	defer nlh.Delete()
	if err != nil {
		return nil, nil, fmt.Errorf("unable to hook into netlink: %v", err)
	}

	link, err := netlink.LinkByIndex(ifIdx)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to get link info: %v", err)
	}

	ro, err := nlh.RouteList(link, 6)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to get routes: %v", err)
	}

	var hr []*net.IPNet
	var sr []*net.IPNet
	for _, d := range ro {
		m, l := d.Dst.Mask.Size()
		if m == 128 && l == 128 {
			hr = append(hr, d.Dst)
		} else if l == 128 && !d.Dst.IP.IsLinkLocalUnicast() {
			sr = append(sr, d.Dst)
		}
	}
	return hr, sr, nil
}
