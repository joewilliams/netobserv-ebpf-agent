package flow

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

// Values according to field 61 in https://www.iana.org/assignments/ipfix/ipfix.xhtml
const (
	DirectionIngress = uint8(0)
	DirectionEgress  = uint8(1)
)
const MacLen = 6

// IPv6Type value as defined in IEEE 802: https://www.iana.org/assignments/ieee-802-numbers/ieee-802-numbers.xhtml
const IPv6Type = 0x86DD

type HumanBytes uint64
type MacAddr [MacLen]uint8
type Direction uint8

// IPAddr encodes v4 and v6 IPs with a fixed length.
// IPv4 addresses are encoded as IPv6 addresses with prefix ::ffff/96
// as described in https://datatracker.ietf.org/doc/html/rfc4038#section-4.2
// (same behavior as Go's net.IP type)
type IPAddr [net.IPv6len]uint8

type DataLink struct {
	SrcMac MacAddr
	DstMac MacAddr
}

type Network struct {
	SrcAddr IPAddr
	DstAddr IPAddr
}

type Transport struct {
	SrcPort  uint16
	DstPort  uint16
	Protocol uint8 `json:"Proto"`
}

// RecordKey identifies a flow
// Must coincide byte by byte with kernel-side flow_id_t (bpf/flow.h)
type RecordKey struct {
	EthProtocol uint16 `json:"Etype"`
	Direction   uint8  `json:"FlowDirection"`
	DataLink
	Network
	Transport
	IFIndex uint32
}

// RecordMetrics provides flows metrics and timing information
// Must coincide byte by byte with kernel-side flow_metrics_t (bpf/flow.h)
type RecordMetrics struct {
	Packets uint32
	Bytes   uint64
	// StartMonoTimeNs and EndMonoTimeNs are the start and end times as system monotonic timestamps
	// in nanoseconds, as output from bpf_ktime_get_ns() (kernel space)
	// and monotime.Now() (user space)
	StartMonoTimeNs uint64
	EndMonoTimeNs   uint64
}

// record structure as parsed from eBPF
// it's important to emphasize that the fields in this structure have to coincide,
// byte by byte, with the flow_record_t structure in the bpf/flow.h file
type RawRecord struct {
	RecordKey
	RecordMetrics
}

// Record contains accumulated metrics from a flow
type Record struct {
	RawRecord
	// TODO: redundant field from RecordMetrics. Reorganize structs
	TimeFlowStart time.Time
	TimeFlowEnd   time.Time
	Interface     string
}

type InterfaceNamer func(ifIndex int) string

func NewRecord(
	key RecordKey,
	metrics RecordMetrics,
	currentTime time.Time,
	monotonicCurrentTime uint64,
	namer InterfaceNamer,
) *Record {
	startDelta := time.Duration(monotonicCurrentTime - metrics.StartMonoTimeNs)
	endDelta := time.Duration(monotonicCurrentTime - metrics.EndMonoTimeNs)
	return &Record{
		RawRecord: RawRecord{
			RecordKey:     key,
			RecordMetrics: metrics,
		},
		Interface:     namer(int(key.IFIndex)),
		TimeFlowStart: currentTime.Add(-startDelta),
		TimeFlowEnd:   currentTime.Add(-endDelta),
	}
}

func (r *RecordMetrics) Accumulate(src *RecordMetrics) {
	// time == 0 if the value has not been yet set
	if r.StartMonoTimeNs == 0 || r.StartMonoTimeNs > src.StartMonoTimeNs {
		r.StartMonoTimeNs = src.StartMonoTimeNs
	}
	if r.EndMonoTimeNs == 0 || r.EndMonoTimeNs < src.EndMonoTimeNs {
		r.EndMonoTimeNs = src.EndMonoTimeNs
	}
	r.Bytes += src.Bytes
	r.Packets += src.Packets
}

// IP returns the net.IP equivalent object
func (ia *IPAddr) IP() net.IP {
	return ia[:]
}

// IntEncodeV4 encodes an IPv4 address as an integer (in network encoding, big endian).
// It assumes that the passed IP is already IPv4. Otherwise it would just encode the
// last 4 bytes of an IPv6 address
func (ia *IPAddr) IntEncodeV4() uint32 {
	return binary.BigEndian.Uint32(ia[net.IPv6len-net.IPv4len : net.IPv6len])
}

func (ia *IPAddr) MarshalJSON() ([]byte, error) {
	return []byte(`"` + ia.IP().String() + `"`), nil
}

func (m *MacAddr) String() string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", m[0], m[1], m[2], m[3], m[4], m[5])
}

func (m *MacAddr) MarshalJSON() ([]byte, error) {
	return []byte("\"" + m.String() + "\""), nil
}

// ReadFrom reads a Record from a binary source, in LittleEndian order
func ReadFrom(reader io.Reader) (*RawRecord, error) {
	var fr RawRecord
	err := binary.Read(reader, binary.LittleEndian, &fr)
	return &fr, err
}
