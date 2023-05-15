package main

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
)

// linkReady will return true when its ok to bind the ndp listener to it.
// it will wait for the TX counter to start incrementing since before thats the case
// there are certain aspects not fulfilled. (i.e. link local may not yet be assinged etc
// it will also help on edge cases where the interface is not yet fully provisioned even though up
func linkReady(l *netlink.LinkAttrs) bool {
	if l.OperState == 6 && l.Flags&net.FlagUp == net.FlagUp {
		if l.Statistics != nil && l.Statistics.TxPackets > 0 {
			return true
		}
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
		match := false
		for _, e := range exclude {
			if e.Contains(d.Dst.IP) {
				match = true
			}
		}
		if match {
			continue
		}

		m, l := d.Dst.Mask.Size()
		if m == 128 && l == 128 {
			hr = append(hr, d.Dst)
		} else if l == 128 && !d.Dst.IP.IsLinkLocalUnicast() {
			sr = append(sr, d.Dst)
		}
	}
	return hr, sr, nil
}
