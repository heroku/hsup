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
	ErrNoFreeUID = errors.New("no free UID available")

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
	uidsDir string

	// (maxUID-minUID) should always be smaller than 2 ** 18
	// see privateNetForUID for details
	minUID int
	maxUID int

	rng *rand.Rand
}

func NewAllocator(workDir string) (*Allocator, error) {
	uids := filepath.Join(workDir, "uids")
	if err := os.MkdirAll(uids, 0755); err != nil {
		return nil, err
	}
	// use a seed with some entropy from crypt/rand to initialize a cheaper
	// math/rand rng
	seed, err := crand.Int(crand.Reader, big.NewInt(math.MaxInt64))
	if err != nil {
		return nil, err
	}
	return &Allocator{
		uidsDir: uids,
		// TODO: configurable range
		minUID: 3000,
		maxUID: 60000,
		rng:    rand.New(rand.NewSource(seed.Int64())),
	}, nil
}

// ReserveUID optimistically locks uid and gid pairs until one is successfully
// allocated. It relies on atomic filesystem operations to guarantee that
// multiple concurrent tasks will never allocate the same uid/gid pair.
func (a *Allocator) ReserveUID() (int, int, error) {
	var (
		interval   = a.maxUID - a.minUID + 1
		maxRetries = 5 * interval
	)
	// try random uids in the [minUID, maxUID] interval until one works.
	// With a good random distribution, a few times the number of possible
	// uids should be enough attempts to guarantee that all possible uids
	// will be eventually tried.
	for i := 0; i < maxRetries; i++ {
		uid := a.rng.Intn(interval) + a.minUID
		uidFile := filepath.Join(a.uidsDir, strconv.Itoa(uid))
		// check if free by optimistically locking this uid
		f, err := os.OpenFile(uidFile, os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			continue // already allocated by someone else
		}
		if err := f.Close(); err != nil {
			return 0, 0, err
		}
		return uid, uid, nil
	}
	return 0, 0, ErrNoFreeUID
}

// FreeUID returns the provided UID to the pool to be used by others
func (a *Allocator) FreeUID(uid int) error {
	uidFile := filepath.Join(a.uidsDir, strconv.Itoa(uid))
	return os.Remove(uidFile)
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
