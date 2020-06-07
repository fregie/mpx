package mpx

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"
)

var (
	MaxCachedNum = 1024
)

type Dialer interface {
	Dial() (net.Conn, error)
}

type Conn struct {
	net.Conn
	ID int
}

type TunnInfo struct {
	*Tunnel
	Ack            uint32
	unInputed      sync.Map // key: seq, value: *packet
	unInputedCount int
	maxCachedNum   int
}

func (t *TunnInfo) receiveData(p *Packet) {
	t.input(p.Data)
	t.Ack += p.Length
}

func (t *TunnInfo) cache(packet *Packet) (needRST bool) {
	_, loaded := t.unInputed.LoadOrStore(packet.Seq, packet)
	if loaded {
		return true
	}
	t.unInputedCount++
	if t.unInputedCount >= t.maxCachedNum {
		return true
	}
	return false
}

func (t *TunnInfo) removeCached(seq uint32) {
	t.unInputed.Delete(seq)
	t.unInputedCount--
}

func (t *TunnInfo) update() {
	for {
		p, ok := t.unInputed.Load(t.Ack)
		if ok && p != nil {
			packet := p.(*Packet)
			t.removeCached(t.Ack)
			t.receiveData(packet)
		} else {
			break
		}
	}
}

type ConnPool struct {
	connMap  sync.Map // key: int , value: net.conn
	IDs      []int
	idMutex  sync.Mutex
	tunnMap  sync.Map // key: int , value: Tunnel
	sendCh   chan []byte
	closeCh  chan uint32
	acceptCh chan *Tunnel
	dialer   Dialer
}

func NewConnPool() *ConnPool {
	p := &ConnPool{
		IDs:      make([]int, 0),
		sendCh:   make(chan []byte),
		closeCh:  make(chan uint32),
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
	for v, ok := p.tunnMap.Load(tunnID); ok && v != nil; {
		tunnID = rand.Intn(65535)
	}
	packet := &Packet{
		Type:   Connect,
		TunnID: uint32(tunnID),
		Seq:    0,
		Length: uint32(len(data)),
		Data:   data,
	}
	p.sendCh <- packet.Pack()
	tunn := NewTunnel(packet.TunnID, &TunnelWriter{
		TunnID:  packet.TunnID,
		Seq:     packet.Length,
		sendCh:  p.sendCh,
		closeCh: p.closeCh,
	})
	p.tunnMap.Store(packet.TunnID, &TunnInfo{Tunnel: tunn, maxCachedNum: MaxCachedNum})
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
	p.idMutex.Lock()
	defer p.idMutex.Unlock()
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
			p.tunnMap.Store(newTunn.ID, &TunnInfo{Tunnel: newTunn, Ack: uint32(len(packet.Data)), maxCachedNum: MaxCachedNum})
			p.acceptCh <- newTunn
			newTunn.input(packet.Data)
		case Disconnect:
			tunninf, ok := p.tunnMap.Load(packet.TunnID)
			if !ok || tunninf == nil {
				log.Printf("tunnel[%d] to disconnect is not exist", packet.TunnID)
				continue
			}
			tunn := tunninf.(*TunnInfo)
			tunn.RemoteClose()
			p.tunnMap.Delete(tunn.ID)
		case Data:
			tunninf, ok := p.tunnMap.Load(packet.TunnID)
			if !ok || tunninf == nil {
				_, err := p.Send(NewRSTPacket(packet.TunnID, nil).Pack())
				if err != nil {
					log.Printf("send packet failed: %s", err)
				}
				continue
			}
			tunn := tunninf.(*TunnInfo)
			if packet.Seq == tunn.Ack {
				tunn.receiveData(packet)
				if tunn.unInputedCount > 0 {
					tunn.update()
				}
				continue
			}
			needRST := tunn.cache(packet)
			if needRST {
				_, err := p.Send(NewRSTPacket(packet.TunnID, nil).Pack())
				if err != nil {
					log.Printf("send packet failed: %s", err)
				}
				continue
			}
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

func (p *ConnPool) StartWithDialer(dialer Dialer, connNum int) (stop func()) {
	done := make(chan bool)
	wg := sync.WaitGroup{}
	for i := 0; i < connNum; i++ {
		wg.Add(1)
		go func() {
			conn, err := dialer.Dial()
			if err != nil {
				log.Printf("Dail failed: %s", err)
			}
			err = p.AddConn(conn)
			if err != nil {
				log.Printf("AddConn failed: %s", err)
			}
		}()
	}
	wg.Wait()

	ticker := time.NewTicker(time.Second)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				toAdd := connNum - len(p.IDs)
				for i := 0; i < toAdd; i++ {
					conn, err := dialer.Dial()
					if err != nil {
						log.Printf("Dail failed: %s", err)
					}
					err = p.AddConn(conn)
					if err != nil {
						log.Printf("AddConn failed: %s", err)
					}
				}
			}
		}
	}()
	return func() { done <- true }
}

func (p *ConnPool) Writer() {
	for {
		select {
		case toSend := <-p.sendCh:
			_, err := p.Send(toSend)
			if err != nil {
				log.Printf("send failed: %s", err)
			}
		case tunnID := <-p.closeCh:
			p.tunnMap.Delete(tunnID)
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
	TunnID  uint32
	Seq     uint32
	sendCh  chan []byte
	closeCh chan uint32
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
	t.closeCh <- t.TunnID
	return nil
}
