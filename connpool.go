package mpx

import (
	"container/list"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	ErrClosed = errors.New("closed")
)

var (
	MaxCachedNum             = 65535
	defaultTunnelTimeout     = time.Second * 10
	defaultPacketsBufferSize = 4096
	defaultRetransmitCount   = 1000
	defaultRetransmitTimeout = 500 * time.Millisecond
	debug                    = log.New(ioutil.Discard, "[MPX Debug] ", log.Ldate|log.Ltime|log.Lshortfile)
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
	Dial() (net.Conn, uint32, error)
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

type packetWithtime struct {
	*mpxPacket
	time time.Time
}

func (t *tunnInfo) receiveData(p *mpxPacket) {
	debug.Printf("[%d] input seq[%d]", t.ID, p.Seq)
	t.input(p.Data)
	t.Ack += p.Length
}

func (t *tunnInfo) cache(packet *mpxPacket) (needRST bool) {
	_, loaded := t.unInputed.LoadOrStore(packet.Seq, packet)
	if loaded {
		return false
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

type connWithWeight struct {
	net.Conn
	weight uint32
	tx     uint64
}

func (c *connWithWeight) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	atomic.AddUint64(&c.tx, uint64(n))
	return n, err
}

// ConnPool 是使用mpx主要用到的结构体
// 接受任何实现了 net.Conn 接口的连接作为输入
// 可以直接调用 AddConn 方法将Conn输入，也可以调用 ServeWithListener 输入一个 net.Listener ，调用 StartWithDialer 输入一个 dailer (mpx库中的一个interface)来使用mpx
type ConnPool struct {
	side              side
	connMap           sync.Map // key: int , value: connWithWeight
	localAddr         net.Addr
	remoteAddr        net.Addr
	tunnMap           sync.Map // key: int , value: Tunnel
	chanCloseOnce     sync.Once
	sendCh            chan *mpxPacket
	recvCh            chan *mpxPacket
	acceptCh          chan *Tunnel
	maxBufferSize     int
	retransmitCount   int
	retransmitTimeout time.Duration
	bufferLock        sync.Mutex
	packetsBuffer     *list.List
	packetMap         map[uint64]*list.Element
	ctx               context.Context
	ctxCancel         context.CancelFunc
}

// NewConnPool 创建一个新的 *ConnPool
func NewConnPool() *ConnPool {
	ctx, cancel := context.WithCancel(context.Background())
	p := &ConnPool{
		sendCh:            make(chan *mpxPacket),
		recvCh:            make(chan *mpxPacket),
		ctx:               ctx,
		ctxCancel:         cancel,
		acceptCh:          make(chan *Tunnel),
		maxBufferSize:     defaultPacketsBufferSize,
		packetsBuffer:     list.New(),
		packetMap:         make(map[uint64]*list.Element),
		retransmitCount:   defaultRetransmitCount,
		retransmitTimeout: defaultRetransmitTimeout,
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
		return nil, ErrClosed
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
	defer func() {
		if v := recover(); v != nil {
			debug.Println("capture a panic:", v)
		}
	}()
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
	p.sendCh <- packet
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
	return p.AddConnWithWeight(conn, 1)
}

func (p *ConnPool) AddConnWithWeight(conn net.Conn, weight uint32) error {
	if conn == nil {
		return errors.New("Conn is nil")
	}
	connID := rand.Intn(65535)
	for v, ok := p.connMap.Load(connID); ok && v != nil; {
		connID = rand.Intn(65535)
	}
	cw := &connWithWeight{
		Conn:   conn,
		weight: weight,
	}
	p.connMap.Range(func(key, value interface{}) bool {
		atomic.StoreUint64(&value.(*connWithWeight).tx, 0)
		return true
	})
	p.connMap.Store(connID, cw)
	p.localAddr = conn.LocalAddr()
	p.remoteAddr = conn.RemoteAddr()
	go p.handleConn(cw, connID)
	debug.Printf("Add connection [%d]", connID)

	return nil
}

func (p *ConnPool) ConnCount() int {
	count := 0
	p.connMap.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}

func (p *ConnPool) choseConnByWeight() *connWithWeight {
	totalWeight := uint32(0)
	totalTx := uint64(0)
	var conn *connWithWeight
	p.connMap.Range(func(key, value interface{}) bool {
		cv := value.(*connWithWeight)
		totalWeight += atomic.LoadUint32(&cv.weight)
		totalTx += atomic.LoadUint64(&cv.tx)
		return true
	})
	if totalWeight == 0 {
		return nil
	}
	p.connMap.Range(func(key, value interface{}) bool {
		cv := value.(*connWithWeight)
		if conn == nil {
			conn = cv
		}
		if totalTx <= 0 || float32(atomic.LoadUint64(&cv.tx))/float32(totalTx) <= float32(atomic.LoadUint32(&cv.weight))/float32(totalWeight) {
			conn = cv
			return false
		}
		return true
	})
	return conn
}

func (p *ConnPool) handleConn(conn *connWithWeight, id int) {
	defer func() {
		if v := recover(); v != nil {
			debug.Println("capture a panic:", v)
		}
		conn.Close()
		p.connMap.Delete(id)
		debug.Printf("[%s]Totally send %d bytes", conn.RemoteAddr().String(), atomic.LoadUint64(&conn.tx))
	}()
	connCtx, cancel := context.WithCancel(p.ctx)
	defer cancel()
	go func() {
		<-connCtx.Done()
		conn.Close()
	}()
	lastHeartBeat := time.Now()
	go func() {
		defer conn.Close()
		ticker := time.NewTicker(1 * time.Second)
		for range ticker.C {
			_, err := conn.Write(NewHeartbeatPacket().Pack())
			if err != nil {
				debug.Printf("Write err:%s", err)
				break
			}
			_, err = conn.Write(NewSetWeightPacket(atomic.LoadUint32(&conn.weight)).Pack())
			if err != nil {
				debug.Printf("Write err:%s", err)
				break
			}
		}
	}()
	enableHeartbeat := false
	go func() {
		checkDuration := 5 * time.Second
		timer := time.NewTimer(checkDuration)
		for range timer.C {
			if enableHeartbeat && time.Now().After(lastHeartBeat.Add(checkDuration)) {
				conn.Close()
				break
			}
			timer.Reset(checkDuration)
		}
	}()
	for {
		packet, err := PacketFromReader(conn)
		if err != nil {
			debug.Printf("read err:%s", err)
			break
		}
		switch packet.Type {
		case Heartbeat:
			lastHeartBeat = time.Now()
			// log.Printf("Heartbeat")
			enableHeartbeat = true
			continue
		case SetWeight:
			weight := binary.BigEndian.Uint32(packet.Data)
			atomic.StoreUint32(&conn.weight, weight)
			continue
		}
		p.recvCh <- packet
	}
}

func (p *ConnPool) retransmiter() {
	ticker := time.NewTicker(1 * time.Second)
	for {
		select {
		case <-ticker.C:
			p.bufferLock.Lock()
			now := time.Now()
			toSend := make([]*list.Element, 0)
			for ele := p.packetsBuffer.Front(); ele != nil; ele = ele.Next() {
				pwt := ele.Value.(*packetWithtime)
				if now.After(pwt.time.Add(p.retransmitTimeout)) {
					toSend = append(toSend, ele)
				}
			}
			for _, ele := range toSend {
				p.packetsBuffer.MoveToBack(ele)
			}
			p.bufferLock.Unlock()
			for _, ele := range toSend {
				p.sendCh <- ele.Value.(*packetWithtime).mpxPacket
			}
		case <-p.ctx.Done():
			return
		}
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
			switch packet.Type {
			case Data:
				fallthrough
			case Connect:
				ackPkt := NewAckPacket(packet.TunnID, packet.Seq, packet.Length)
				p.sendCh <- ackPkt
			case ACK:
				toReSend := make([]*list.Element, 0)
				p.bufferLock.Lock()
				ackEle, ok := p.packetMap[packet.PacketID()]
				if ok {
					count := 0
					for ele := ackEle.Prev(); ele != nil; ele = ele.Prev() {
						count++
						if count <= p.retransmitCount {
							continue
						}
						toReSend = append(toReSend, ele)
					}
					p.packetsBuffer.Remove(ackEle)
					delete(p.packetMap, packet.PacketID())
					for _, ele := range toReSend {
						p.packetsBuffer.MoveToBack(ele)
					}
				}
				p.bufferLock.Unlock()
				for _, ele := range toReSend {
					p.sendCh <- ele.Value.(*packetWithtime).mpxPacket
				}
				continue
			}
			// 处理tunnel
			var tunn *tunnInfo
			tunnel, ok := p.tunnMap.Load(packet.TunnID)
			if !ok || tunnel == nil {
				if p.side == client {
					debug.Printf("[%d] drop deq[%d]", packet.TunnID, packet.Seq)
					p.sendCh <- NewRSTPacket(packet.TunnID, nil)
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
			case RST:
				debug.Printf("[%d] RST", packet.TunnID)
				tunn.Tunnel.Close()
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
				// debug.Printf("[%d] receive: seq[%d]", packet.TunnID, packet.Seq)
				if packet.Seq < tunn.Ack {
					goto CONTINUE
				}
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
					p.sendCh <- NewRSTPacket(packet.TunnID, nil)
					tunn.Tunnel.Close()
					p.tunnMap.Delete(tunn.ID)
					goto CONTINUE
				}
			}
		CONTINUE:
		}
	}
}

func (p *ConnPool) timeoutTunnelCleaner(interval time.Duration) {
	ticker := time.NewTicker(interval)
	for range ticker.C {
		p.tunnMap.Range(func(key, value interface{}) bool {
			tunnel := value.(*tunnInfo)
			if time.Now().After(tunnel.LastSeen().Add(defaultTunnelTimeout)) {
				tunnel.Close()
				p.tunnMap.Delete(tunnel.ID)
			}
			return true
		})
	}
}

// Serve 启用服务，适用于使用 AddConn 方法输入连接的情况下启用服务
// 如果已经调用 ServeWithListener 或 StartWithDialer，请勿调用该方法
func (p *ConnPool) Serve() error {
	p.side = server
	go p.writer()
	go p.timeoutTunnelCleaner(10 * time.Second)
	go p.retransmiter()
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
	go p.writer()
	go p.receiver()
	go p.retransmiter()
	go p.timeoutTunnelCleaner(10 * time.Second)
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
// 注意：请勿和 Serve 同时调用
// 注意：err != nil 代表建立第一个连接失败，但是仍然会启动服务，若想停止服务请调用Close()
func (p *ConnPool) StartWithDialer(dialer Dialer, connNum int) (err error) {
	p.side = client
	go p.writer()
	go p.receiver()
	go p.retransmiter()
	go p.timeoutTunnelCleaner(10 * time.Second)

	conn, weight, err := dialer.Dial()
	if err != nil {
		log.Printf("Dail failed: %s", err)
	} else {
		err = p.AddConnWithWeight(conn, weight)
		if err != nil {
			log.Printf("AddConn failed: %s", err)
		}
	}

	go func() {
		ticker := time.NewTicker(time.Second)
		for {
			select {
			case <-p.ctx.Done():
				return
			case <-ticker.C:
				toAdd := connNum - p.ConnCount()
				for i := 0; i < toAdd; i++ {
					conn, weight, e := dialer.Dial()
					if e != nil {
						log.Printf("Dail failed: %s", e)
						continue
					}
					e = p.AddConnWithWeight(conn, weight)
					if e != nil {
						log.Printf("AddConn failed: %s", e)
						continue
					}
				}
			}
		}
	}()
	return
}

// Close closes the listener.
// Any blocked Accept operations will be unblocked and return errors.
func (p *ConnPool) Close() error {
	p.tunnMap.Range(func(k, v interface{}) bool {
		v.(*tunnInfo).Close()
		p.tunnMap.Delete(k)
		return true
	})
	p.ctxCancel()
	p.chanCloseOnce.Do(func() {
		close(p.sendCh)
		close(p.recvCh)
	})
	return nil
}

// Addr returns the listener's network address.
func (p *ConnPool) Addr() net.Addr {
	var addr net.Addr
	p.connMap.Range(func(k, v interface{}) bool {
		addr = v.(*connWithWeight).LocalAddr()
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
		case packet := <-p.sendCh:
			if packet.Type == Connect || packet.Type == Data {
				p.bufferLock.Lock()
				_, exist := p.packetMap[packet.PacketID()]
				if !exist {
					ele := p.packetsBuffer.PushBack(&packetWithtime{mpxPacket: packet, time: time.Now()})
					if p.packetsBuffer.Len() > p.maxBufferSize {
						toDel := p.packetsBuffer.Front()
						p.packetsBuffer.Remove(toDel)
						delete(p.packetMap, toDel.Value.(*mpxPacket).PacketID())
					}
					p.packetMap[packet.PacketID()] = ele
				}
				p.bufferLock.Unlock()
			}
			_, err := p.send(packet.Pack())
			if err != nil {
				debug.Printf("send failed: %s", err)
			}
		case <-p.ctx.Done():
			for range p.sendCh {
			}
			return
		}
	}
}

func (p *ConnPool) send(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, errors.New("buffer is nil or empty")
	}
	conn := p.choseConnByWeight()
	if conn == nil {
		return 0, fmt.Errorf("connection not found")
	}
	n, err := conn.Write(buf)
	if err != nil {
		conn.Close()
	}
	return n, err
}

type tunnelWriter struct {
	TunnID uint32
	Seq    uint32
	sendCh chan *mpxPacket
	closed bool
}

func (tw *tunnelWriter) Write(ctx context.Context, data []byte) (n int, err error) {
	defer func() {
		if v := recover(); v != nil {
			debug.Println("capture a panic:", v)
			n = 0
			err = errors.New("panic")
		}
	}()
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
	case tw.sendCh <- packet:
	case <-ctx.Done():
		return 0, syscall.ETIMEDOUT
	}

	tw.Seq = tw.Seq + packet.Length
	return len(data), nil
}

func (tw *tunnelWriter) Close() (err error) {
	defer func() {
		if v := recover(); v != nil {
			debug.Println("capture a panic:", v)
			err = errors.New("panic")
		}
	}()
	debug.Printf("[%d]send close[%d]", tw.TunnID, tw.Seq)
	packet := &mpxPacket{
		Type:   Disconnect,
		TunnID: tw.TunnID,
		Seq:    tw.Seq,
		Length: 0,
		Data:   make([]byte, 0),
	}
	tw.sendCh <- packet
	tw.closed = true
	return nil
}

func init() {
	rand.Seed(time.Now().Unix())
}
