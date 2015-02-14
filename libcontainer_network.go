// +build linux

package hsup

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"

	"github.com/docker/libcontainer/netlink"
	"github.com/docker/libcontainer/network"
	"github.com/docker/libcontainer/utils"
)

var (
	ErrInvalidIPMask = errors.New("mask is not a /30")
)

func init() {
	network.AddStrategy("routed", &Routed{})
}

// Routed implements libcontainer's network.NetworkStrategy interface,
// offering containers only layer 3 connectivity to the outside world.
type Routed struct {
	network.Veth
}

// Create sets up a veth pair, setting the config.Gateway address on the master
// (host) side. The veth pair forms a small subnet with a single host and
// gateway.
func (r *Routed) Create(
	config *network.Network, nspid int, state *network.NetworkState,
) error {
	// TODO: ensure that config.Gateway and config.Address are in the same subnet
	if config.VethPrefix == "" {
		return fmt.Errorf("veth prefix is not specified")
	}
	name1, name2, err := createVethPair(config.VethPrefix, config.TxQueueLen)
	if err != nil {
		return err
	}
	_, subnet, err := net.ParseCIDR(config.Address)
	if err != nil {
		return err
	}
	gw := &net.IPNet{
		IP:   net.ParseIP(config.Gateway),
		Mask: subnet.Mask,
	}
	if err := network.SetInterfaceIp(name1, gw.String()); err != nil {
		return err
	}
	if err := network.SetMtu(name1, config.Mtu); err != nil {
		return err
	}
	if err := network.InterfaceUp(name1); err != nil {
		return err
	}
	if err := network.SetInterfaceInNamespacePid(name2, nspid); err != nil {
		return err
	}
	state.VethHost = name1
	state.VethChild = name2
	return nil
}

func (r *Routed) Initialize(
	net *network.Network, state *network.NetworkState,
) error {
	return r.Veth.Initialize(net, state)
}

// createVethPair will automatically generage two random names for
// the veth pair and ensure that they have been created
//
// Copied from libcontainer/network.createVethPair because it is not exported
func createVethPair(prefix string, txQueueLen int) (name1 string, name2 string, err error) {
	for i := 0; i < 10; i++ {
		if name1, err = utils.GenerateRandomName(prefix, 7); err != nil {
			return
		}

		if name2, err = utils.GenerateRandomName(prefix, 7); err != nil {
			return
		}

		if err = network.CreateVethPair(name1, name2, txQueueLen); err != nil {
			if err == netlink.ErrInterfaceExists {
				continue
			}

			return
		}

		break
	}

	return
}

// smallSubnet encapsulates operations on single host /30 IPv4 networks. They
// contain only 4 ip addresses, and only one of them is usable for hosts:
// 1) network address, 2) gateway ip, 3) host ip, and 4) broadcast ip.
type smallSubnet struct {
	subnet    *net.IPNet
	gateway   *net.IPNet
	host      *net.IPNet
	broadcast *net.IPNet
}

func newSmallSubnet(n *net.IPNet) (*smallSubnet, error) {
	ones, bits := n.Mask.Size()
	if bits-ones != 2 {
		return nil, ErrInvalidIPMask
	}

	var asInt uint32
	if err := binary.Read(
		bytes.NewReader(n.IP.To4()),
		binary.BigEndian,
		&asInt,
	); err != nil {
		return nil, err
	}

	var (
		gwAsInt       = asInt + 1
		freeAsInt     = asInt + 2
		brdAsInt      = asInt + 3
		gw, free, brd bytes.Buffer
	)
	if err := binary.Write(&gw, binary.BigEndian, &gwAsInt); err != nil {
		return nil, err
	}
	if err := binary.Write(&free, binary.BigEndian, &freeAsInt); err != nil {
		return nil, err
	}
	if err := binary.Write(&brd, binary.BigEndian, &brdAsInt); err != nil {
		return nil, err
	}

	return &smallSubnet{
		subnet: n,
		gateway: &net.IPNet{
			IP:   net.IP(gw.Bytes()).To4(),
			Mask: n.Mask,
		},
		host: &net.IPNet{
			IP:   net.IP(free.Bytes()).To4(),
			Mask: n.Mask,
		},
		broadcast: &net.IPNet{
			IP:   net.IP(brd.Bytes()).To4(),
			Mask: n.Mask,
		},
	}, nil
}

// Gateway address and mask of the subnet
func (sn *smallSubnet) Gateway() *net.IPNet {
	return sn.gateway
}

// Host returns the only unassigned (free) IP/mask in the subnet
func (sn *smallSubnet) Host() *net.IPNet {
	return sn.host
}

// Broadcast address and mask of the subnet
func (sn *smallSubnet) Broadcast() *net.IPNet {
	return sn.broadcast
}
