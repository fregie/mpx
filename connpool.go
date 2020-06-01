package mpx

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
)

type Conn struct {
	net.Conn
	ID int
}

type TunnInfo struct {
	*Tunnel
	Ack uint32
}

type ConnPool struct {
	connMap  sync.Map // key: int , value: net.conn
	IDs      []int
	tunnMap  sync.Map // key: int , value: Tunnel
	sendCh   chan []byte
	acceptCh chan *Tunnel
}

func NewConnPool() *ConnPool {
	p := &ConnPool{
		IDs:      make([]int, 0),
		sendCh:   make(chan []byte),
		acceptCh: make(chan *Tunnel),
	}
	return p
}

func (p *ConnPool) Accept() (*Tunnel, error) {
	tunn := <-p.acceptCh
	return tunn, nil
}

func (p *ConnPool) Connect(data []byte) (*Tunnel, error) {
	if data == nil {
		data = make([]byte, 0)
	}
	tunnID := rand.Intn(65535)
	packet := &Packet{
		Type:   Connect,
		TunnID: uint32(tunnID),
		Seq:    0,
		Length: uint32(len(data)),
		Data:   data,
	}
	p.sendCh <- packet.Pack()
	tunn := NewTunnel(packet.TunnID, &TunnelWriter{
		TunnID: packet.TunnID,
		Seq:    packet.Length,
		sendCh: p.sendCh,
	})
	p.tunnMap.Store(packet.TunnID, &TunnInfo{Tunnel: tunn, Ack: uint32(len(packet.Data))})
	return tunn, nil
}

func (p *ConnPool) AddConn(conn net.Conn) error {
	if conn == nil {
		return errors.New("Conn is nil")
	}
	p.connMap.Store(len(p.IDs)+1, conn)
	p.updateIDs()
	go p.handleConn(conn)

	return nil
}

func (p *ConnPool) updateIDs() {
	p.IDs = p.IDs[:0]
	p.connMap.Range(func(k, v interface{}) bool {
		p.IDs = append(p.IDs, k.(int))
		return true
	})
}

func (p *ConnPool) handleConn(conn net.Conn) {
	defer conn.Close()
	for {
		packet, err := PacketFromReader(conn)
		if err != nil {
			log.Printf("read err:%s", err)
			break
		}
		switch packet.Type {
		case Connect:
			tunn, ok := p.tunnMap.Load(packet.TunnID)
			if ok && tunn != nil {
				_, err := p.Send(NewRSTPacket(packet.TunnID, nil).Pack())
				if err != nil {
					log.Printf("send packet failed: %s", err)
				}
				continue
			}
			newTunn := NewTunnel(packet.TunnID, &TunnelWriter{
				TunnID: packet.TunnID,
				Seq:    0,
				sendCh: p.sendCh,
			})
			p.tunnMap.Store(packet.TunnID, &TunnInfo{Tunnel: newTunn, Ack: uint32(len(packet.Data))})
			p.acceptCh <- newTunn
			newTunn.input(packet.Data)

		case Disconnect:

		case Data:

		}
	}
}

func (p *ConnPool) Serve(lis net.Listener) error {
	if lis == nil {
		return errors.New("listener is nil")
	}
	defer lis.Close()
	go p.Writer()
	for {
		conn, err := lis.Accept()
		if err != nil {
			log.Printf("serve accept failed: %s", err)
		}
		p.AddConn(conn)
	}
}

func (p *ConnPool) Writer() {
	for toSend := range p.sendCh {
		_, err := p.Send(toSend)
		if err != nil {
			log.Printf("send failed: %s", err)
		}
	}
}

func (p *ConnPool) Send(buf []byte) (int, error) {
	if buf == nil || len(buf) == 0 {
		return 0, errors.New("buffer is nil or empty")
	}
	if len(p.IDs) == 0 {
		return 0, errors.New("No available connection")
	}
	connID := p.IDs[rand.Intn(len(p.IDs))]
	conn, ok := p.connMap.Load(connID)
	if !ok || conn == nil {
		return 0, fmt.Errorf("Connection [%d] not found", connID)
	}
	return conn.(net.Conn).Write(buf)
}

type TunnelWriter struct {
	TunnID uint32
	Seq    uint32
	sendCh chan []byte
}

func (t TunnelWriter) Write(data []byte) (n int, err error) {
	packet := &Packet{
		Type:   Data,
		TunnID: t.TunnID,
		Seq:    t.Seq,
		Length: uint32(len(data)),
		Data:   make([]byte, len(data)),
	}
	t.sendCh <- packet.Pack()
	t.Seq += packet.Length
	return len(data), nil
}

func (t TunnelWriter) Close() error {
	packet := &Packet{
		Type:   Disconnect,
		TunnID: t.TunnID,
		Seq:    t.Seq,
		Length: 0,
		Data:   make([]byte, 0),
	}
	t.sendCh <- packet.Pack()
	return nil
}
