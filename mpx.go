package mpx

import (
	"encoding/binary"
	"fmt"
	"io"

	netstack "github.com/google/netstack/tcpip/header"
)

type Type uint8

func (t *Type) Uint8() uint8 { return uint8(*t) }
func ParseType(t uint8) Type { return Type(t) }

const (
	Connect Type = iota
	Disconnect
	Data
	RST
)

const (
	HeaderSize = 18 //byte
)

type Packet struct {
	Type     Type
	Unused1  uint8
	Unused2  uint16
	TunnID   uint32
	Seq      uint32
	Length   uint32
	Checksum uint16 //RFC 1071
	Data     []byte
}

func (p *Packet) Pack() []byte {
	packed := make([]byte, HeaderSize+len(p.Data))
	packed[0] = p.Type.Uint8()
	binary.BigEndian.PutUint32(packed[4:], p.TunnID)
	binary.BigEndian.PutUint32(packed[8:], p.Seq)
	binary.BigEndian.PutUint32(packed[12:], p.Length)
	cksm := netstack.Checksum(packed[:16], 0)
	binary.BigEndian.PutUint16(packed[16:], cksm)
	copy(packed[HeaderSize:], p.Data)
	return packed
}

func PacketFromReader(r io.Reader) (*Packet, error) {
	headerBuf := make([]byte, HeaderSize)
	_, err := io.ReadFull(r, headerBuf)
	if err != nil {
		return nil, err
	}
	p := &Packet{
		Type:     ParseType(headerBuf[0]),
		TunnID:   binary.BigEndian.Uint32(headerBuf[4:]),
		Seq:      binary.BigEndian.Uint32(headerBuf[8:]),
		Length:   binary.BigEndian.Uint32(headerBuf[12:]),
		Checksum: binary.BigEndian.Uint16(headerBuf[16:]),
	}
	if p.Checksum != netstack.Checksum(headerBuf[:16], 0) {
		return p, fmt.Errorf("Checksum error")
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

func NewRSTPacket(tunnID uint32, data []byte) *Packet {
	if data == nil {
		data = make([]byte, 0)
	}
	return &Packet{
		Type:   RST,
		TunnID: tunnID,
		Length: uint32(len(data)),
		Data:   data,
	}
}
