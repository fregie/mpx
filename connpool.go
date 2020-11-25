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
	"runtime"
	"sync"
	"syscall"
	"time"
)

var (
	EClosed = errors.New("closed")
)

var (
	MaxCachedNum = 65535
	// debug        = log.New(ioutil.Discard, "[MPX debug] ", log.Ldate|log.Ltime|log.Lshortfile)
	debug = log.New(ioutil.Discard, "[MPX Debug] ", log.Ldate|log.Ltime|log.Lshortfile)
)

func Verbose(enable bool) {
	if enable {
		debug.SetOutput(os.Stdout)
	} else {
		debug.SetOutput(ioutil.Discard)
	}
}

// Dialer 用于建立net.Conn连接
type Dialer interface {
	Dial() (net.Conn, error)
}

type Conn struct {
	net.Conn
	ID int
}

type tunnInfo struct {
	*Tunnel
	Ack            uint32
	unInputed      sync.Map // key: seq, value: *packet
	unInputedCount int
	maxCachedNum   int
	closeAt        uint32
}

func (t *tunnInfo) receiveData(p *mpxPacket) {
	debug.Printf("[%d] input seq[%d]", t.ID, p.Seq)
	t.input(p.Data)
	t.Ack += p.Length
}

func (t *tunnInfo) cache(packet *mpxPacket) (needRST bool) {
	_, loaded := t.unInputed.LoadOrStore(packet.Seq, packet)
	if loaded {
		debug.Printf("RST cause chong fu")
		return true
	}
	// Debug.Printf("[%d] cache seq[%d]", t.ID, packet.Seq)
	t.unInputedCount++
	if t.unInputedCount >= t.maxCachedNum {
		debug.Printf("RST cause max cache")
		return true
	}
	return false
}

func (t *tunnInfo) removeCached(seq uint32) {
	// Debug.Printf("[%d] remove cache seq[%d]", t.ID, seq)
	t.unInputed.Delete(seq)
	t.unInputedCount--
}

func (t *tunnInfo) update() (needDelete bool) {
	for {
		p, ok := t.unInputed.Load(t.Ack)
		if ok && p != nil {
			// Debug.Printf("[%d] load cahce seq[%d]", t.ID, t.Ack)
			packet := p.(*mpxPacket)
			t.removeCached(t.Ack)
			t.receiveData(packet)
			if t.closeAt != 0 && t.Ack == t.closeAt {
				debug.Printf("Close at %d", t.Tunnel.ID)
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

// ConnPool 是使用mpx主要用到的结构体
// 接受任何实现了 net.Conn 接口的连接作为输入
// 可以直接调用 AddConn 方法将Conn输入，也可以调用 ServeWithListener 输入一个 net.Listener ，调用 StartWithDialer 输入一个 dailer (mpx库中的一个interface)来使用mpx
type ConnPool struct {
	side       side
	connMap    sync.Map // key: int , value: net.conn
	localAddr  net.Addr
	remoteAddr net.Addr
	IDs        []int
	idMutex    sync.Mutex
	tunnMap    sync.Map // key: int , value: Tunnel
	sendCh     chan []byte
	recvCh     chan *mpxPacket
	acceptCh   chan *Tunnel
	running    bool
	ctx        context.Context
	ctxCancel  context.CancelFunc
	dialer     Dialer
}

// NewConnPool 创建一个新的 *ConnPool
func NewConnPool() *ConnPool {
	ctx, cancel := context.WithCancel(context.Background())
	p := &ConnPool{
		IDs:       make([]int, 0),
		sendCh:    make(chan []byte),
		recvCh:    make(chan *mpxPacket),
		ctx:       ctx,
		ctxCancel: cancel,
		acceptCh:  make(chan *Tunnel),
	}
	return p
}

// Accept 阻塞直到有新的连接可以返回
// 返回的 *Tunnel 实现了 net.Conn
func (p *ConnPool) Accept() (*Tunnel, error) {
	select {
	case tunn := <-p.acceptCh:
		return tunn, nil
	case <-p.ctx.Done():
		return nil, EClosed
	}
}

// Dial 建立并返回一个新的mpx连接（net.Conn）
// 参数中data为在建立连接的时候携带要传输的数据，可以为nil
func (p *ConnPool) Dial(data []byte) (*Tunnel, error) {
	return p.Connect(data)
}

// Connect 同Dial
func (p *ConnPool) Connect(data []byte) (*Tunnel, error) {
	runtime.GC()
	// ddebug.FreeOSMemory()
	if data == nil {
		data = make([]byte, 0)
	}
	tunnID := rand.Intn(65535)
	for v, ok := p.tunnMap.Load(tunnID); ok && v != nil; {
		tunnID = rand.Intn(65535)
	}
	packet := &mpxPacket{
		Type:   Connect,
		TunnID: uint32(tunnID),
		Seq:    0,
		Length: uint32(len(data)),
		Data:   data,
	}
	p.sendCh <- packet.Pack()
	tunn := newTunnel(packet.TunnID, p.localAddr, p.remoteAddr, &tunnelWriter{
		TunnID: packet.TunnID,
		Seq:    packet.Length,
		sendCh: p.sendCh,
	})
	p.tunnMap.Store(packet.TunnID, &tunnInfo{Tunnel: tunn, maxCachedNum: MaxCachedNum})
	return tunn, nil
}

// AddConn 向ConnPool中添加一个新的net.Conn
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
	debug.Printf("Add connection [%d]", connID)

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
	go func() {
		select {
		case <-connCtx.Done():
			conn.Close()
		}
	}()
	for {
		packet, err := PacketFromReader(conn)
		if err != nil {
			log.Printf("read err:%s", err)
			break
		}
		p.recvCh <- packet
	}
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
			debug.Printf("[%d] receive: seq[%d]", packet.TunnID, packet.Seq)
			var tunn *tunnInfo
			tunnel, ok := p.tunnMap.Load(packet.TunnID)
			if !ok || tunnel == nil {
				if p.side == client {
					debug.Printf("[%d] drop deq[%d]", packet.TunnID, packet.Seq)
					continue
				}
				debug.Printf("New tunn")
				newTunn := newTunnel(packet.TunnID, p.localAddr, p.remoteAddr, &tunnelWriter{
					TunnID: packet.TunnID,
					Seq:    0,
					sendCh: p.sendCh,
				})
				ti := &tunnInfo{Tunnel: newTunn, Ack: 0, maxCachedNum: MaxCachedNum}
				p.tunnMap.Store(newTunn.ID, ti)
				p.acceptCh <- newTunn
				tunn = ti
			} else {
				tunn = tunnel.(*tunnInfo)
			}
			switch packet.Type {
			case Connect:
				if len(packet.Data) > 0 {
					tunn.receiveData(packet)
					if tunn.unInputedCount > 0 {
						needDelete := tunn.update()
						if needDelete {
							debug.Printf("[%d]Delete tunnel", tunn.ID)
							p.tunnMap.Delete(tunn.ID)
						}
					}
				}
			case Disconnect:
				debug.Printf("[%d]receive disconnect", packet.TunnID)
				if packet.Seq == tunn.Ack {
					debug.Printf("Close %d", tunn.ID)
					tunn.RemoteClose()
					debug.Printf("[%d]Delete tunnel", tunn.ID)
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
							debug.Printf("[%d]Delete tunnel", tunn.ID)
							p.tunnMap.Delete(tunn.ID)
						}
					}
					goto CONTINUE
				}
				needRST := tunn.cache(packet)
				if needRST {
					_, err := p.send(NewRSTPacket(packet.TunnID, nil).Pack())
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

// Serve 启用服务，适用于使用 AddConn 方法输入连接的情况下启用服务
// 如果已经调用 ServeWithListener 或 StartWithDialer，请勿调用该方法
func (p *ConnPool) Serve() error {
	p.side = server
	p.running = true
	defer func() { p.running = false }()
	go p.writer()
	p.receiver()
	return nil
}

// ServeWithListener 启用服务，通过 net.Listener 输入连接
// 请勿和 Serve 同时调用
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

// StartWithDialer 启用服务，通过 Dailer 输入连接
// 请勿和 Serve 同时调用
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
		v.(*tunnInfo).Close()
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
			_, err := p.send(toSend)
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

func (p *ConnPool) send(buf []byte) (int, error) {
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

type tunnelWriter struct {
	TunnID uint32
	Seq    uint32
	sendCh chan []byte
	closed bool
}

func (tw *tunnelWriter) Write(ctx context.Context, data []byte) (n int, err error) {
	if tw.closed {
		return 0, errors.New("closed")
	}
	debug.Printf("[%d]Write seq[%d]", tw.TunnID, tw.Seq)
	packet := &mpxPacket{
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

func (tw *tunnelWriter) Close() error {
	debug.Printf("[%d]send close[%d]", tw.TunnID, tw.Seq)
	packet := &mpxPacket{
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
