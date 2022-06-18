package mpx

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	defaultTunnelBufferSize = 4096
)

type state int

const (
	Connected state = iota
	Closed
)

type Tunnel struct {
	ID          uint32
	localAddr   net.Addr
	remoteAddr  net.Addr
	reciver     chan []byte
	readCtx     context.Context
	readCancel  context.CancelFunc
	writeCtx    context.Context
	writeCancel context.CancelFunc
	leftover    []byte
	state       state
	writer      *tunnelWriter
	lastSeen    int64
}

func newTunnel(id uint32, la, ra net.Addr, writer *tunnelWriter) *Tunnel {
	readctx, readcancel := context.WithCancel(context.Background())
	writectx, writecancel := context.WithCancel(context.Background())
	return &Tunnel{
		ID:          id,
		leftover:    make([]byte, 0),
		reciver:     make(chan []byte, defaultTunnelBufferSize),
		readCtx:     readctx,
		readCancel:  readcancel,
		writeCtx:    writectx,
		writeCancel: writecancel,
		writer:      writer,
		state:       Connected,
		localAddr:   la,
		remoteAddr:  ra,
	}
}

func (t *Tunnel) input(data []byte) {
	if t.state == Closed {
		return
	}
	if len(t.reciver) == cap(t.reciver) {
		debug.Printf("[%d]recv channel full", t.ID)
		t.Close()
		return
	}
	t.reciver <- data

}

func (t *Tunnel) Read(buf []byte) (int, error) {
	defer atomic.StoreInt64(&t.lastSeen, time.Now().UnixNano())
	if t.state == Closed && len(t.leftover) == 0 && len(t.reciver) == 0 {
		debug.Printf("[%d]EOF", t.ID)
		return 0, io.EOF
	}
	if buf == nil {
		return 0, errors.New("buf is nil")
	}
	// ctx, cancel := context.WithTimeout(t.readCtx, 10*time.Second)
	ctx, cancel := context.WithCancel(t.readCtx)
	defer cancel()
	if len(t.leftover) == 0 {
		select {
		case new := <-t.reciver:
			n := copy(buf, new)
			t.leftover = new[n:]
			return n, nil
		case <-ctx.Done():
			if t.state == Closed {
				debug.Printf("[%d]EOF", t.ID)
				return 0, io.EOF
			} else {
				return 0, syscall.ETIMEDOUT
			}
		}

	}

	n := copy(buf, t.leftover)
	t.leftover = t.leftover[n:]

	return n, nil
}

// Write writes data to the connection.
// Write can be made to time out and return an Error with Timeout() == true
// after a fixed time limit; see SetDeadline and SetWriteDeadline.
func (t *Tunnel) Write(b []byte) (n int, err error) {
	defer atomic.StoreInt64(&t.lastSeen, time.Now().UnixNano())
	if t.state == Closed {
		return 0, errors.New("closed")
	}
	return t.writer.Write(t.writeCtx, b)
}

func (t *Tunnel) RemoteClose() {
	t.readCancel()
	t.writeCancel()
	t.state = Closed
}

// Close closes the connection.
// Any blocked Read or Write operations will be unblocked and return errors.
func (t *Tunnel) Close() error {
	t.readCancel()
	t.writeCancel()
	if t.state != Closed {
		t.state = Closed
		return t.writer.Close()
	}
	return nil
}

// LocalAddr returns the local network address.
func (t *Tunnel) LocalAddr() net.Addr {
	return t.localAddr
}

// RemoteAddr returns the remote network address.
func (t *Tunnel) RemoteAddr() net.Addr {
	return t.remoteAddr
}

// SetDeadline sets the read and write deadlines associated
// with the connection. It is equivalent to calling both
// SetReadDeadline and SetWriteDeadline.
//
// A deadline is an absolute time after which I/O operations
// fail with a timeout (see type Error) instead of
// blocking. The deadline applies to all future and pending
// I/O, not just the immediately following call to Read or
// Write. After a deadline has been exceeded, the connection
// can be refreshed by setting a deadline in the future.
//
// An idle timeout can be implemented by repeatedly extending
// the deadline after successful Read or Write calls.
//
// A zero value for t means I/O operations will not time out.
//
// Note that if a TCP connection has keep-alive turned on,
// which is the default unless overridden by Dialer.KeepAlive
// or ListenConfig.KeepAlive, then a keep-alive failure may
// also return a timeout error. On Unix systems a keep-alive
// failure on I/O can be detected using
// errors.Is(err, syscall.ETIMEDOUT).
func (t *Tunnel) SetDeadline(ti time.Time) error {
	// log.Printf("set deadline")
	err := t.SetReadDeadline(ti)
	if err != nil {
		return err
	}
	err = t.SetWriteDeadline(ti)
	if err != nil {
		return err
	}
	return nil
}

// SetReadDeadline sets the deadline for future Read calls
// and any currently-blocked Read call.
// A zero value for t means Read will not time out.
func (t *Tunnel) SetReadDeadline(ti time.Time) error {
	now := time.Now()
	if !ti.After(now) {
		t.readCancel()
	} else {
		time.AfterFunc(ti.Sub(now), func() {
			t.readCancel()
		})
	}
	return nil
}

// SetWriteDeadline sets the deadline for future Write calls
// and any currently-blocked Write call.
// Even if write times out, it may return n > 0, indicating that
// some of the data was successfully written.
// A zero value for t means Write will not time out.
func (t *Tunnel) SetWriteDeadline(ti time.Time) error {
	now := time.Now()
	if !ti.After(now) {
		t.writeCancel()
	} else {
		time.AfterFunc(ti.Sub(now), func() {
			t.writeCancel()
		})
	}
	return nil
}

func (t *Tunnel) LastSeen() time.Time {
	return time.Unix(0, atomic.LoadInt64(&t.lastSeen))
}
