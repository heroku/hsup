// +build linux

package hsup

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io/ioutil"
	"net"

	"github.com/docker/libnetwork"
	"github.com/docker/libnetwork/driverapi"
	"github.com/docker/libnetwork/iptables"
	"github.com/docker/libnetwork/netutils"
	"github.com/docker/libnetwork/types"
	"github.com/vishvananda/netlink"
)

const (
	routedIFID = 1
	ipvlanIFID = 2
	MTU        = 1500
	TxQueueLen = 0
)

var (
	ErrInvalidIPMask   = errors.New("mask is not a /30")
	ErrInvalidNetwork  = errors.New("Invalid Network")
	ErrInvalidEndpoint = errors.New("Invalid Endpoint")
)

// Routed implements libnetwork's Driver interface,
// offering containers only layer 3 connectivity to the outside world.
type Routed struct {
	networks map[string]*routedNetwork
}

func RegisterRoutedDriver(controller libnetwork.NetworkController) error {
	r := &Routed{networks: make(map[string]*routedNetwork)}
	capability := driverapi.Capability{Scope: driverapi.LocalScope}
	c := controller.(driverapi.DriverCallback)
	if err := c.RegisterDriver(r.Type(), r, capability); err != nil {
		return err
	}
	return controller.ConfigureNetworkDriver(r.Type(), nil)
}

type routedNetwork struct {
	privateSubnet net.IPNet
	endpoints     map[string]*routedEndpoint
}

type routedEndpoint struct {
	address         net.IP
	gateway         net.IP
	hostIFName      string
	containerIFName string
}

func (r *Routed) Config(options map[string]interface{}) error {
	return r.enablePacketForwarding()
}

func (r *Routed) CreateNetwork(nid types.UUID, options map[string]interface{}) error {
	network := &routedNetwork{
		privateSubnet: options["subnet"].(net.IPNet),
		endpoints:     make(map[string]*routedEndpoint),
	}
	r.networks[string(nid)] = network
	return network.natOutboundTraffic()
}

func (r *Routed) DeleteNetwork(nid types.UUID) error {
	delete(r.networks, string(nid))
	// TODO remove NAT rule
	return nil
}

func (r *Routed) CreateEndpoint(
	nid, eid types.UUID,
	epInfo driverapi.EndpointInfo,
	options map[string]interface{},
) error {
	var err error
	epSubnet := options["subnet"].(*SmallSubnet)
	network, ok := r.networks[string(nid)]
	if !ok {
		return ErrInvalidNetwork
	}
	name1, name2, err := createVethPair("veth", TxQueueLen)
	if err != nil {
		return err
	}
	hostIF, err := netlink.LinkByName(name1)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			netlink.LinkDel(hostIF)
		}
	}()
	containerIF, err := netlink.LinkByName(name2)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			netlink.LinkDel(containerIF)
		}
	}()
	if err := netlink.LinkSetMTU(hostIF, MTU); err != nil {
		return err
	}
	if err := netlink.LinkSetMTU(containerIF, MTU); err != nil {
		return err
	}

	if err := netlink.AddrAdd(hostIF, &netlink.Addr{
		IPNet: epSubnet.Gateway(),
	}); err != nil {
		return err
	}

	if err := netlink.LinkSetUp(hostIF); err != nil {
		return err
	}
	network.endpoints[string(eid)] = &routedEndpoint{
		address:         epSubnet.Host().IP,
		gateway:         epSubnet.Gateway().IP,
		hostIFName:      name1,
		containerIFName: name2,
	}
	emptyIPV6 := net.IPNet{}
	return epInfo.AddInterface(
		routedIFID,
		containerIF.Attrs().HardwareAddr,
		*epSubnet.Host(),
		emptyIPV6,
	)
}

func (r *Routed) DeleteEndpoint(nid, eid types.UUID) error {
	network, ok := r.networks[string(nid)]
	if !ok {
		return ErrInvalidNetwork
	}
	endpoint, ok := network.endpoints[string(eid)]
	if !ok {
		return nil // already gone
	}
	hostIF, err := netlink.LinkByName(endpoint.hostIFName)
	if err != nil {
		return err
	}
	if err := netlink.LinkDel(hostIF); err != nil {
		return err
	}
	containerIF, err := netlink.LinkByName(endpoint.containerIFName)
	if err != nil {
		return nil // already gone
	}
	if err := netlink.LinkDel(containerIF); err != nil {
		return err
	}
	delete(network.endpoints, string(eid))
	return nil
}

func (r *Routed) EndpointOperInfo(nid, eid types.UUID) (map[string]interface{}, error) {
	return make(map[string]interface{}), nil
}

func (r *Routed) Join(
	nid, eid types.UUID,
	sboxKey string,
	jinfo driverapi.JoinInfo,
	options map[string]interface{},
) error {
	network, ok := r.networks[string(nid)]
	if !ok {
		return ErrInvalidNetwork
	}
	endpoint, ok := network.endpoints[string(eid)]
	if !ok {
		return ErrInvalidEndpoint
	}
	for _, n := range jinfo.InterfaceNames() {
		if n.ID() != routedIFID {
			continue // find the container interface
		}
		if err := n.SetNames(endpoint.containerIFName, "eth"); err != nil {
			return err
		}
	}
	return jinfo.SetGateway(endpoint.gateway)
}

func (r *Routed) Leave(nid, eid types.UUID) error {
	return nil // noop
}

func (_ *Routed) Type() string {
	return "routed"
}

func (r *Routed) enablePacketForwarding() error {
	return ioutil.WriteFile(
		"/proc/sys/net/ipv4/ip_forward",
		[]byte{'1', '\n'},
		0644,
	)
}

func (n *routedNetwork) natOutboundTraffic() error {
	masquerade := []string{
		"POSTROUTING", "-t", "nat",
		"-s", n.privateSubnet.String(),
		"-j", "MASQUERADE",
	}
	if _, err := iptables.Raw(
		append([]string{"-C"}, masquerade...)...,
	); err != nil {
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
	host, err := netutils.GenerateIfaceName(prefix, 7)
	if err != nil {
		return "", "", err
	}
	child, err := netutils.GenerateIfaceName(prefix, 7)
	if err != nil {
		return "", "", err
	}
	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: host, TxQLen: txQueueLen},
		PeerName:  child,
	}
	return host, child, netlink.LinkAdd(veth)
}

// IPVlan implements libnetwork's Driver interface,
// creating ipvlan (type=L3) subinterfaces for containers
type IPVlan struct {
	networks map[string]*ipvlanNetwork
}

func RegisterIPVlanDriver(controller libnetwork.NetworkController) error {
	d := &IPVlan{networks: make(map[string]*ipvlanNetwork)}
	capability := driverapi.Capability{Scope: driverapi.LocalScope}
	c := controller.(driverapi.DriverCallback)
	if err := c.RegisterDriver(d.Type(), d, capability); err != nil {
		return err
	}
	return controller.ConfigureNetworkDriver(d.Type(), nil)
}

type ipvlanNetwork struct {
	hostIF    string
	endpoints map[string]*ipvlanEndpoint
}

type ipvlanEndpoint struct {
	containerIFName string
}

func (d *IPVlan) Config(options map[string]interface{}) error {
	return nil // noop
}

func (d *IPVlan) CreateNetwork(nid types.UUID, options map[string]interface{}) error {
	network := &ipvlanNetwork{
		hostIF:    options["hostIF"].(string),
		endpoints: make(map[string]*ipvlanEndpoint),
	}
	d.networks[string(nid)] = network
	_, err := netlink.LinkByName(network.hostIF) // check if exists
	return err
}

func (d *IPVlan) DeleteNetwork(nid types.UUID) error {
	delete(d.networks, string(nid))
	return nil
}

func (d *IPVlan) CreateEndpoint(
	nid, eid types.UUID,
	epInfo driverapi.EndpointInfo,
	options map[string]interface{},
) error {
	var err error
	address := options["address"].(net.IPNet)
	network, ok := d.networks[string(nid)]
	if !ok {
		return ErrInvalidNetwork
	}
	subIFName, err := netutils.GenerateIfaceName("ipvl", 7)
	if err != nil {
		return err
	}
	hostIF, err := netlink.LinkByName(network.hostIF)
	if err != nil {
		return err
	}
	subIF := &netlink.IPVlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:        subIFName,
			ParentIndex: hostIF.Attrs().Index,
			TxQLen:      TxQueueLen,
		},
		Mode: netlink.IPVLAN_MODE_L3,
	}
	if err := netlink.LinkAdd(subIF); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			netlink.LinkDel(subIF)
		}
	}()
	if err := netlink.LinkSetMTU(subIF, MTU); err != nil {
		return err
	}
	if err := netlink.AddrAdd(subIF, &netlink.Addr{
		IPNet: &address,
	}); err != nil {
		return err
	}
	if err := netlink.LinkSetUp(subIF); err != nil {
		return err
	}
	network.endpoints[string(eid)] = &ipvlanEndpoint{
		containerIFName: subIFName,
	}
	emptyIPV6 := net.IPNet{}
	return epInfo.AddInterface(
		ipvlanIFID,
		subIF.Attrs().HardwareAddr,
		address,
		emptyIPV6,
	)
}

func (d *IPVlan) DeleteEndpoint(nid, eid types.UUID) error {
	network, ok := d.networks[string(nid)]
	if !ok {
		return ErrInvalidNetwork
	}
	endpoint, ok := network.endpoints[string(eid)]
	if !ok {
		return nil // already gone
	}
	containerIF, err := netlink.LinkByName(endpoint.containerIFName)
	if err != nil {
		return nil // already gone
	}
	if err := netlink.LinkDel(containerIF); err != nil {
		return err
	}
	delete(network.endpoints, string(eid))
	return nil
}

func (d *IPVlan) EndpointOperInfo(nid, eid types.UUID) (map[string]interface{}, error) {
	return make(map[string]interface{}), nil
}

func (d *IPVlan) Join(
	nid, eid types.UUID,
	sboxKey string,
	jinfo driverapi.JoinInfo,
	options map[string]interface{},
) error {
	network, ok := d.networks[string(nid)]
	if !ok {
		return ErrInvalidNetwork
	}
	endpoint, ok := network.endpoints[string(eid)]
	if !ok {
		return ErrInvalidEndpoint
	}
	for _, n := range jinfo.InterfaceNames() {
		if n.ID() != ipvlanIFID {
			continue // find the container interface
		}
		if err := n.SetNames(endpoint.containerIFName, "eth"); err != nil {
			return err
		}
		return nil
	}
	return errors.New("Join: ipvlan interface not found")
}

func (d *IPVlan) Leave(nid, eid types.UUID) error {
	return nil // noop
}

func (_ *IPVlan) Type() string {
	return "ipvlan"
}

// smallSubnet encapsulates operations on single host /30 IPv4 networks. They
// contain only 4 ip addresses, and only one of them is usable for hosts:
// 1) network address, 2) gateway ip, 3) host ip, and 4) broadcast ip.
type SmallSubnet struct {
	subnet    *net.IPNet
	gateway   *net.IPNet
	host      *net.IPNet
	broadcast *net.IPNet
}

func NewSmallSubnet(n *net.IPNet) (*SmallSubnet, error) {
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

	return &SmallSubnet{
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
func (sn *SmallSubnet) Gateway() *net.IPNet {
	return sn.gateway
}

// Host returns the only unassigned (free) IP/mask in the subnet
func (sn *SmallSubnet) Host() *net.IPNet {
	return sn.host
}

// Broadcast address and mask of the subnet
func (sn *SmallSubnet) Broadcast() *net.IPNet {
	return sn.broadcast
}
