package mpx

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
)

var (
	EClosed = errors.New("closed")
)

var (
	MaxCachedNum = 65535
	// Debug        = log.New(ioutil.Discard, "[MPX Debug] ", log.Ldate|log.Ltime|log.Lshortfile)
	Debug = log.New(ioutil.Discard, "[MPX Debug] ", log.Ldate|log.Ltime|log.Lshortfile)
)

func Verbose(enable bool) {
	if enable {
		Debug.SetOutput(os.Stdout)
	} else {
		Debug.SetOutput(ioutil.Discard)
	}
}

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
	closeAt        uint32
}

func (t *TunnInfo) receiveData(p *Packet) {
	Debug.Printf("[%d] input seq[%d]", t.ID, p.Seq)
	t.input(p.Data)
	t.Ack += p.Length
}

func (t *TunnInfo) cache(packet *Packet) (needRST bool) {
	_, loaded := t.unInputed.LoadOrStore(packet.Seq, packet)
	if loaded {
		Debug.Printf("RST cause chong fu")
		return true
	}
	// Debug.Printf("[%d] cache seq[%d]", t.ID, packet.Seq)
	t.unInputedCount++
	if t.unInputedCount >= t.maxCachedNum {
		Debug.Printf("RST cause max cache")
		return true
	}
	return false
}

func (t *TunnInfo) removeCached(seq uint32) {
	// Debug.Printf("[%d] remove cache seq[%d]", t.ID, seq)
	t.unInputed.Delete(seq)
	t.unInputedCount--
}

func (t *TunnInfo) update() (needDelete bool) {
	for {
		p, ok := t.unInputed.Load(t.Ack)
		if ok && p != nil {
			// Debug.Printf("[%d] load cahce seq[%d]", t.ID, t.Ack)
			packet := p.(*Packet)
			t.removeCached(t.Ack)
			t.receiveData(packet)
			if t.closeAt != 0 && t.Ack == t.closeAt {
				Debug.Printf("Close at %d", t.Tunnel.ID)
				t.RemoteClose()
				return true
			}
		} else {
			break
		}
	}
	return false
}

type side int

const (
	server side = iota
	client
)

type ConnPool struct {
	side       side
	connMap    sync.Map // key: int , value: net.conn
	localAddr  net.Addr
	remoteAddr net.Addr
	IDs        []int
	idMutex    sync.Mutex
	tunnMap    sync.Map // key: int , value: Tunnel
	sendCh     chan []byte
	recvCh     chan *Packet
	acceptCh   chan *Tunnel
	running    bool
	ctx        context.Context
	ctxCancel  context.CancelFunc
	dialer     Dialer
}

func NewConnPool() *ConnPool {
	ctx, cancel := context.WithCancel(context.Background())
	p := &ConnPool{
		IDs:       make([]int, 0),
		sendCh:    make(chan []byte),
		recvCh:    make(chan *Packet),
		ctx:       ctx,
		ctxCancel: cancel,
		acceptCh:  make(chan *Tunnel),
	}
	return p
}

func (p *ConnPool) Accept() (*Tunnel, error) {
	select {
	case tunn := <-p.acceptCh:
		return tunn, nil
	case <-p.ctx.Done():
		return nil, EClosed
	}
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
	tunn := NewTunnel(packet.TunnID, p.localAddr, p.remoteAddr, &TunnelWriter{
		TunnID: packet.TunnID,
		Seq:    packet.Length,
		sendCh: p.sendCh,
	})
	p.tunnMap.Store(packet.TunnID, &TunnInfo{Tunnel: tunn, maxCachedNum: MaxCachedNum})
	return tunn, nil
}

func (p *ConnPool) AddConn(conn net.Conn) error {
	if conn == nil {
		return errors.New("Conn is nil")
	}
	connID := rand.Intn(65535)
	for v, ok := p.connMap.Load(connID); ok && v != nil; {
		connID = rand.Intn(65535)
	}
	p.connMap.Store(connID, conn)
	p.updateIDs()
	p.localAddr = conn.LocalAddr()
	p.remoteAddr = conn.RemoteAddr()
	go p.handleConn(conn, connID)
	log.Printf("Add connection [%d]", connID)

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

func (p *ConnPool) handleConn(conn net.Conn, id int) {
	defer func() {
		conn.Close()
		p.connMap.Delete(id)
		p.updateIDs()
	}()
	connCtx, cancel := context.WithCancel(p.ctx)
	defer cancel()
	for {
		packet, err := PacketFromReader(conn)
		if err != nil {
			log.Printf("read err:%s", err)
			break
		}
		p.recvCh <- packet
	}
	go func() {
		select {
		case <-connCtx.Done():
			conn.Close()
		}
	}()
}

func (p *ConnPool) receiver() {
	for {
		select {
		case <-p.ctx.Done():
			for range p.recvCh { // 防止阻塞导致goroutin泄露
			}
			return
		case packet, ok := <-p.recvCh:
			if !ok {
				return
			}
			Debug.Printf("[%d] receive: seq[%d]", packet.TunnID, packet.Seq)
			var tunn *TunnInfo
			tunnel, ok := p.tunnMap.Load(packet.TunnID)
			if !ok || tunnel == nil {
				if p.side == client {
					Debug.Printf("[%d] drop deq[%d]", packet.TunnID, packet.Seq)
					continue
				}
				Debug.Printf("New tunn")
				newTunn := NewTunnel(packet.TunnID, p.localAddr, p.remoteAddr, &TunnelWriter{
					TunnID: packet.TunnID,
					Seq:    0,
					sendCh: p.sendCh,
				})
				ti := &TunnInfo{Tunnel: newTunn, Ack: 0, maxCachedNum: MaxCachedNum}
				p.tunnMap.Store(newTunn.ID, ti)
				p.acceptCh <- newTunn
				tunn = ti
			} else {
				tunn = tunnel.(*TunnInfo)
			}
			switch packet.Type {
			case Connect:
				if len(packet.Data) > 0 {
					tunn.receiveData(packet)
					if tunn.unInputedCount > 0 {
						needDelete := tunn.update()
						if needDelete {
							Debug.Printf("[%d]Delete tunnel", tunn.ID)
							p.tunnMap.Delete(tunn.ID)
						}
					}
				}
			case Disconnect:
				Debug.Printf("[%d]receive disconnect", packet.TunnID)
				if packet.Seq == tunn.Ack {
					Debug.Printf("Close %d", tunn.ID)
					tunn.RemoteClose()
					Debug.Printf("[%d]Delete tunnel", tunn.ID)
					p.tunnMap.Delete(tunn.ID)
					goto CONTINUE
				}
				tunn.closeAt = packet.Seq
			case Data:
				if packet.Seq == tunn.Ack {
					tunn.receiveData(packet)
					if tunn.unInputedCount > 0 {
						needDelete := tunn.update()
						if needDelete {
							Debug.Printf("[%d]Delete tunnel", tunn.ID)
							p.tunnMap.Delete(tunn.ID)
						}
					}
					goto CONTINUE
				}
				needRST := tunn.cache(packet)
				if needRST {
					_, err := p.Send(NewRSTPacket(packet.TunnID, nil).Pack())
					if err != nil {
						log.Printf("send packet failed: %s", err)
					}
					goto CONTINUE
				}
			}
		CONTINUE:
		}
	}
}

func (p *ConnPool) Serve() error {
	p.side = server
	p.running = true
	defer func() { p.running = false }()
	go p.writer()
	p.receiver()
	return nil
}

func (p *ConnPool) ServeWithListener(lis net.Listener) error {
	if lis == nil {
		return errors.New("listener is nil")
	}
	defer lis.Close()
	p.side = server
	p.running = true
	defer func() { p.running = false }()
	go p.writer()
	go p.receiver()
	connCh := make(chan net.Conn)
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				close(connCh)
				log.Printf("serve accept failed: %s", err)
				break
			}
			connCh <- conn
		}
	}()
	for {
		select {
		case <-p.ctx.Done():
			lis.Close()
			for range connCh {
			}
			return p.ctx.Err()
		case conn := <-connCh:
			p.AddConn(conn)
		}
	}
}

func (p *ConnPool) StartWithDialer(dialer Dialer, connNum int) {
	p.side = client
	go p.writer()
	go p.receiver()
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
			wg.Done()
		}()
	}
	wg.Wait()

	ticker := time.NewTicker(time.Second)
	go func() {
		p.running = true
		defer func() { p.running = false }()
		for {
			select {
			case <-p.ctx.Done():
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
	return
}

// Close closes the listener.
// Any blocked Accept operations will be unblocked and return errors.\
func (p *ConnPool) Close() error {
	if !p.running {
		return errors.New("Not running")
	}
	p.tunnMap.Range(func(k, v interface{}) bool {
		v.(*TunnInfo).Close()
		p.tunnMap.Delete(k)
		return true
	})
	p.ctxCancel()
	return nil
}

// Addr returns the listener's network address.
func (p *ConnPool) Addr() net.Addr {
	var addr net.Addr
	p.connMap.Range(func(k, v interface{}) bool {
		addr = v.(net.Conn).LocalAddr()
		return false
	})
	if addr == nil {
		addr, _ = net.ResolveIPAddr("ip", "0.0.0.0")
	}
	return addr
}

func (p *ConnPool) writer() {
	for {
		select {
		case toSend := <-p.sendCh:
			_, err := p.Send(toSend)
			if err != nil {
				log.Printf("send failed: %s", err)
			}
		case <-p.ctx.Done():
			for range p.sendCh {
			}
			return
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
	closed bool
}

func (tw *TunnelWriter) Write(ctx context.Context, data []byte) (n int, err error) {
	if tw.closed {
		return 0, errors.New("closed")
	}
	Debug.Printf("[%d]Write seq[%d]", tw.TunnID, tw.Seq)
	packet := &Packet{
		Type:   Data,
		TunnID: tw.TunnID,
		Seq:    tw.Seq,
		Length: uint32(len(data)),
		Data:   make([]byte, len(data)),
	}
	if packet.Length > 0 {
		copy(packet.Data, data)
	}
	select {
	case tw.sendCh <- packet.Pack():
	case <-ctx.Done():
		return 0, syscall.ETIMEDOUT
	}

	tw.Seq = tw.Seq + packet.Length
	return len(data), nil
}

func (tw *TunnelWriter) Close() error {
	Debug.Printf("[%d]send close[%d]", tw.TunnID, tw.Seq)
	packet := &Packet{
		Type:   Disconnect,
		TunnID: tw.TunnID,
		Seq:    tw.Seq,
		Length: 0,
		Data:   make([]byte, 0),
	}
	tw.sendCh <- packet.Pack()
	tw.closed = true
	return nil
}

func init() {
	rand.Seed(time.Now().Unix())
}
