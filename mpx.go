package mpx

import (
	"encoding/binary"
	"fmt"
	"io"

	netstack "github.com/google/netstack/tcpip/header"
)

type packetType uint8

func (t *packetType) Uint8() uint8 { return uint8(*t) }
func ParseType(t uint8) packetType { return packetType(t) }

const (
	Connect packetType = iota
	Disconnect
	Data
	RST
	Heartbeat
	SetWeight
	ACK
)

const (
	headerSize = 18 //byte
)

type mpxPacket struct {
	Type     packetType
	Unused1  uint8
	Unused2  uint16
	TunnID   uint32
	Seq      uint32
	Length   uint32
	Checksum uint16 //RFC 1071
	Data     []byte
}

func (p *mpxPacket) Pack() []byte {
	packed := make([]byte, headerSize+len(p.Data))
	packed[0] = p.Type.Uint8()
	binary.BigEndian.PutUint32(packed[4:], p.TunnID)
	binary.BigEndian.PutUint32(packed[8:], p.Seq)
	binary.BigEndian.PutUint32(packed[12:], p.Length)
	cksm := netstack.Checksum(packed[:16], 0)
	binary.BigEndian.PutUint16(packed[16:], cksm)
	copy(packed[headerSize:], p.Data)
	return packed
}

func (p *mpxPacket) PacketID() uint64 {
	return uint64(p.TunnID)<<32 | uint64(p.Seq)
}

func PacketFromReader(r io.Reader) (*mpxPacket, error) {
	headerBuf := make([]byte, headerSize)
	_, err := io.ReadFull(r, headerBuf)

	if err != nil {
		return nil, err
	}
	p := &mpxPacket{
		Type:     ParseType(headerBuf[0]),
		TunnID:   binary.BigEndian.Uint32(headerBuf[4:]),
		Seq:      binary.BigEndian.Uint32(headerBuf[8:]),
		Length:   binary.BigEndian.Uint32(headerBuf[12:]),
		Checksum: binary.BigEndian.Uint16(headerBuf[16:]),
	}
	if p.Checksum != netstack.Checksum(headerBuf[:16], 0) {
		return p, fmt.Errorf("checksum error")
	}
	p.Data = make([]byte, p.Length)
	if p.Length > 0 {
		_, err := io.ReadFull(r, p.Data)
		if err != nil {
			return nil, err
		}
	}
	return p, nil
}

func NewRSTPacket(tunnID uint32, data []byte) *mpxPacket {
	if data == nil {
		data = make([]byte, 0)
	}
	return &mpxPacket{
		Type:   RST,
		TunnID: tunnID,
		Length: uint32(len(data)),
		Data:   data,
	}
}

func NewHeartbeatPacket() *mpxPacket {
	return &mpxPacket{
		Type:   Heartbeat,
		TunnID: 0,
		Length: 0,
		Data:   []byte{},
	}
}

func NewSetWeightPacket(weight uint32) *mpxPacket {
	p := &mpxPacket{
		Type:   SetWeight,
		TunnID: 0,
		Length: 4,
		Data:   make([]byte, 4),
	}
	binary.BigEndian.PutUint32(p.Data, weight)
	return p
}

func NewAckPacket(tunnelID, seq, length uint32) *mpxPacket {
	p := &mpxPacket{
		Type:   ACK,
		TunnID: tunnelID,
		Seq:    seq,
		Length: 4,
		Data:   make([]byte, 4),
	}
	binary.BigEndian.PutUint32(p.Data, length)
	return p
}
