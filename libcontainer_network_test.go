// +build linux

package hsup

import (
	"net"
	"testing"
)

func TestAddressesInsideSmallSubnet(t *testing.T) {
	_, subnet, err := net.ParseCIDR("192.168.0.0/30")
	if err != nil {
		t.Fatal(err)
	}
	n, err := newSmallSubnet(subnet)
	if err != nil {
		t.Fatal(err)
	}
	checkIPNet(t, n.Gateway(), &net.IPNet{
		IP:   net.IPv4(192, 168, 0, 1).To4(),
		Mask: net.CIDRMask(30, 32),
	})
	checkIPNet(t, n.Host(), &net.IPNet{
		IP:   net.IPv4(192, 168, 0, 2).To4(),
		Mask: net.CIDRMask(30, 32),
	})
	checkIPNet(t, n.Broadcast(), &net.IPNet{
		IP:   net.IPv4(192, 168, 0, 3).To4(),
		Mask: net.CIDRMask(30, 32),
	})
}

func TestAcceptsOnlySlash30Masks(t *testing.T) {
	_, sn24, err := net.ParseCIDR("192.168.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	_, sn16, err := net.ParseCIDR("192.168.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newSmallSubnet(sn24); err != ErrInvalidIPMask {
		t.Fatalf("/24 should not be accepted")
	}
	if _, err := newSmallSubnet(sn16); err != ErrInvalidIPMask {
		t.Fatalf("/16 should not be accepted")
	}
}
