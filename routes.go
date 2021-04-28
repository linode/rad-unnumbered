package main

import (
	"fmt"
	"github.com/vishvananda/netlink"
	"net"
)

// find routes pointing out a specific interface
func getHostRoutesIpv6(ifName string) ([]*net.IPNet, []*net.IPNet, error) {
	nlh, err := netlink.NewHandle()
	defer nlh.Delete()
	if err != nil {
		return nil, nil, fmt.Errorf("unable to hook into netlink: %v", err)
	}

	link, err := netlink.LinkByName(ifName)
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
		} else if l == 128 && !d.Dst.IP.Equal(net.ParseIP("fe80::")) {
			sr = append(sr, d.Dst)
		}
	}
	return hr, sr, nil
}
