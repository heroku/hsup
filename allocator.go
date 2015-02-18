package hsup

import (
	"bytes"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

var (
	// 172.16/12
	privateSubnet = net.IPNet{
		IP:   net.IPv4(172, 16, 0, 0).To4(),
		Mask: net.CIDRMask(12, 32),
	}
	// 172.16.0.28/30
	basePrivateIP = net.IPNet{
		IP:   net.IPv4(172, 16, 0, 28).To4(),
		Mask: net.CIDRMask(30, 32),
	}
)

// Allocator is responsible for allocating globally unique (per host) resources.
type Allocator struct {
	uidsDir  string
	portsDir string

	// (maxUID-minUID) should always be smaller than 2 ** 18
	// see privateNetForUID for details
	minUID int
	maxUID int

	minPort int
	maxPort int

	rng *rand.Rand
}

func NewAllocator(workDir string) (*Allocator, error) {
	uids := filepath.Join(workDir, "uids")
	if err := os.MkdirAll(uids, 0755); err != nil {
		return nil, err
	}
	ports := filepath.Join(workDir, "ports")
	if err := os.MkdirAll(ports, 0755); err != nil {
		return nil, err
	}
	// use a seed with some entropy from crypt/rand to initialize a cheaper
	// math/rand rng
	seed, err := crand.Int(crand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		return nil, err
	}
	return &Allocator{
		uidsDir:  uids,
		portsDir: ports,
		// TODO: configurable ranges
		minUID: 3000,
		maxUID: 60000,
		// outside of the usual range used as ephemeral ports (32768-61000)
		minPort: 3000,
		maxPort: 9999,
		rng:     rand.New(rand.NewSource(seed.Int64())),
	}, nil
}

// ReserveUID optimistically locks uid numbers until one is successfully
// allocated. It relies on atomic filesystem operations to guarantee that
// multiple concurrent tasks will never allocate the same uid.
//
// uid numbers allocated by this should be returned to the pool with FreeUID
// when they are not required anymore.
func (a *Allocator) ReserveUID() (int, error) {
	return a.allocate(a.uidsDir, a.minUID, a.maxUID)
}

// ReservePort optimistically locks port numbers until one is successfully
// allocated. It relies on atomic filesystem operations to guarantee that
// multiple concurrent tasks will never allocate the same port.
//
// Ports allocated by this should be returned to the pool with FreePort when
// they are not required anymore.
func (a *Allocator) ReservePort() (int, error) {
	return a.allocate(a.portsDir, a.minPort, a.maxPort)
}

// allocate relies on atomic filesystem operations to guarantee that
// multiple concurrent tasks will never allocate the same numbers using the same
// numbersDir.
func (a *Allocator) allocate(numbersDir string, min, max int) (int, error) {
	var (
		interval   = max - min + 1
		maxRetries = 5 * interval
	)
	// Try random uids in the [min, max] interval until one works.
	// With a good random distribution, a few times the number of possible
	// numbers should be enough attempts to guarantee that all possible
	// numbers will be eventually tried.
	for i := 0; i < maxRetries; i++ {
		n := a.rng.Intn(interval) + a.minUID
		file := filepath.Join(a.uidsDir, strconv.Itoa(n))
		// check if free by optimistically locking this uid
		f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			continue // already allocated by someone else
		}
		if err := f.Close(); err != nil {
			return -1, err
		}
		return n, nil
	}
	return -1, errors.New("no free number available at " + numbersDir)
}

// FreeUID returns the provided UID to the pool to be used by others
func (a *Allocator) FreeUID(uid int) error {
	return os.Remove(filepath.Join(a.uidsDir, strconv.Itoa(uid)))
}

// FreePort returns the provided port number to the pool to be used by others
func (a *Allocator) FreePort(port int) error {
	return os.Remove(filepath.Join(a.portsDir, strconv.Itoa(port)))
}

// privateNetForUID determines which /30 IPv4 network to use for each container,
// relying on the fact that each one has a different, unique UID allocated to
// them.
//
// All /30 subnets are allocated from the 172.16/12 block (RFC1918 - Private
// Address Space), starting at 172.16.0.28/30 to avoid clashes with IPs used by
// AWS (eg.: the internal DNS server is 172.16.0.23 on ec2-classic). This block
// provides at most 2**18 = 262144 subnets of size /30, then (maxUID-minUID)
// must be always smaller than 262144.
func (a *Allocator) privateNetForUID(uid int) (*net.IPNet, error) {
	shift := uint32(uid - a.minUID)
	var asInt uint32
	base := bytes.NewReader(basePrivateIP.IP.To4())
	if err := binary.Read(base, binary.BigEndian, &asInt); err != nil {
		return nil, err
	}

	// pick a /30 block
	asInt >>= 2
	asInt += shift
	asInt <<= 2

	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.BigEndian, &asInt); err != nil {
		return nil, err
	}
	ip := net.IP(buf.Bytes())
	if !privateSubnet.Contains(ip) {
		return nil, fmt.Errorf(
			"the assigned IP %q falls out of the allowed subnet %q",
			ip, privateSubnet,
		)
	}
	return &net.IPNet{
		IP:   ip,
		Mask: basePrivateIP.Mask,
	}, nil
}
