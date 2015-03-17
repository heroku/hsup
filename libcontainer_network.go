// +build linux

package hsup

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"strconv"

	"github.com/docker/docker/pkg/iptables"
	"github.com/docker/libcontainer/network"
)

var (
	ErrInvalidIPMask = errors.New("mask is not a /30")
)

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
	if config.VethPrefix == "" {
		return fmt.Errorf("veth prefix is not specified")
	}
	if err := r.enablePacketForwarding(); err != nil {
		return err
	}
	if err := r.natOutboundTraffic(); err != nil {
		return err
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

func (r *Routed) enablePacketForwarding() error {
	return ioutil.WriteFile(
		"/proc/sys/net/ipv4/ip_forward",
		[]byte{'1', '\n'},
		0644,
	)
}

func (r *Routed) natOutboundTraffic() error {
	masquerade := []string{
		"POSTROUTING", "-t", "nat",
		"-s", privateSubnet.String(),
		"-j", "MASQUERADE",
	}
	if !iptables.Exists(masquerade...) {
		incl := append([]string{"-I"}, masquerade...)
		if output, err := iptables.Raw(incl...); err != nil {
			return err
		} else if len(output) > 0 {
			return &iptables.ChainError{
				Chain:  "POSTROUTING",
				Output: output,
			}
		}
	}
	return nil
}

func createVethPair(prefix string, txQueueLen int) (string, string, error) {
	host := prefix
	child := prefix + "-child"
	if err := network.CreateVethPair(host, child, txQueueLen); err != nil {
		return "", "", err
	}
	return host, child, nil
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

type portMapper interface {
	Create() error
	Destroy() error
}

type noopPortMapper struct{}

func (m *noopPortMapper) Create() error {
	return nil
}

func (m *noopPortMapper) Destroy() error {
	return nil
}

// iptablesPortMapper manages iptables rules. Each container must use a different
// chainId.
type iptablesPortMapper struct {
	chainId int
	port    int
	subnet  *smallSubnet
}

// Create adds port mapping rules using iptables.
func (m *iptablesPortMapper) Create() error {
	chain := fmt.Sprintf("dnat-%d", m.chainId)
	if out, err := iptables.Raw("-t", "nat", "-N", chain); err != nil {
		return err
	} else if len(out) > 0 {
		return &iptables.ChainError{
			Chain:  chain,
			Output: out,
		}
	}

	// abspath driver currently hardcodes $PORT to 5000 inside containers
	// forward host:$port -> container:5000
	if out, err := iptables.Raw(
		"-t", "nat", "-A", chain, "-p", "tcp",
		"--dport", strconv.Itoa(m.port), "-j", "DNAT",
		"--to-destination", fmt.Sprintf("%s:5000", m.subnet.Host().IP),
	); err != nil {
		return err
	} else if len(out) > 0 {
		return &iptables.ChainError{
			Chain:  chain,
			Output: out,
		}
	}

	// links from PREROUTING (remote) and OUTPUT (local connections)
	if out, err := iptables.Raw(
		"-t", "nat", "-A", "PREROUTING",
		"-m", "addrtype", "--dst-type", "LOCAL",
		"-j", chain,
	); err != nil {
		return err
	} else if len(out) > 0 {
		return &iptables.ChainError{
			Chain:  "PREROUTING",
			Output: out,
		}
	}
	if out, err := iptables.Raw(
		"-t", "nat", "-A", "OUTPUT",
		"-m", "addrtype", "--dst-type", "LOCAL",
		"!", "--dst", "127.0.0.0/8",
		"-j", chain,
	); err != nil {
		return err
	} else if len(out) > 0 {
		return &iptables.ChainError{
			Chain:  "OUTPUT",
			Output: out,
		}
	}

	return nil
}

// Destroy cleans up rules previously created by Create.
func (m *iptablesPortMapper) Destroy() error {
	chain := fmt.Sprintf("dnat-%d", m.chainId)
	if out, err := iptables.Raw(
		"-t", "nat", "-D", "OUTPUT",
		"-m", "addrtype", "--dst-type", "LOCAL",
		"!", "--dst", "127.0.0.0/8",
		"-j", chain,
	); err != nil {
		return err
	} else if len(out) > 0 {
		return &iptables.ChainError{
			Chain:  "PREROUTING",
			Output: out,
		}
	}

	if out, err := iptables.Raw(
		"-t", "nat", "-D", "PREROUTING",
		"-m", "addrtype", "--dst-type", "LOCAL",
		"-j", chain,
	); err != nil {
		return err
	} else if len(out) > 0 {
		return &iptables.ChainError{
			Chain:  "PREROUTING",
			Output: out,
		}
	}

	if out, err := iptables.Raw("-t", "nat", "-F", chain); err != nil {
		return err
	} else if len(out) > 0 {
		return &iptables.ChainError{
			Chain:  chain,
			Output: out,
		}
	}
	if out, err := iptables.Raw("-t", "nat", "-X", chain); err != nil {
		return err
	} else if len(out) > 0 {
		return &iptables.ChainError{
			Chain:  chain,
			Output: out,
		}
	}

	return nil
}
