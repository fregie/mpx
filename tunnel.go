package mpx

import (
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const (
	defaultBufferSize = 65535
)

type State int

const (
	Connected State = iota
	Closed
)

type Tunnel struct {
	ID         uint32
	localAddr  net.Addr
	remoteAddr net.Addr
	reciver    chan []byte
	closeCh    chan bool
	leftover   []byte
	state      State
	readMutex  sync.Mutex
	writer     io.WriteCloser
}

func NewTunnel(id uint32, writer io.WriteCloser) *Tunnel {
	return &Tunnel{
		ID:       id,
		leftover: make([]byte, 0),
		reciver:  make(chan []byte),
		closeCh:  make(chan bool),
		writer:   writer,
		state:    Connected,
	}
}

func (t *Tunnel) input(data []byte) {
	// newBuffer := make([]byte, len(data))
	// copy(newBuffer, data)
	t.reciver <- data
}

func (t *Tunnel) Read(buf []byte) (int, error) {
	t.readMutex.Lock()
	defer t.readMutex.Unlock()
	if buf == nil {
		return 0, errors.New("buf is nil")
	}
	if len(t.leftover) == 0 {
		select {
		case new := <-t.reciver:
			n := copy(buf, new)
			t.leftover = new[n:]
			return n, nil
		case close := <-t.closeCh:
			if close {
				log.Printf("Closed")
				return 0, io.EOF
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
	return t.writer.Write(b)
}

func (t *Tunnel) RemoteClose() {
	t.state = Closed
	t.closeCh <- true
}

// Close closes the connection.
// Any blocked Read or Write operations will be unblocked and return errors.
func (t *Tunnel) Close() error {
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
	return nil
}

// SetReadDeadline sets the deadline for future Read calls
// and any currently-blocked Read call.
// A zero value for t means Read will not time out.
func (t *Tunnel) SetReadDeadline(ti time.Time) error {
	return nil
}

// SetWriteDeadline sets the deadline for future Write calls
// and any currently-blocked Write call.
// Even if write times out, it may return n > 0, indicating that
// some of the data was successfully written.
// A zero value for t means Write will not time out.
func (t *Tunnel) SetWriteDeadline(ti time.Time) error {
	return nil
}
