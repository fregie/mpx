package dialer

import (
	"fmt"
	"net"
	"sync"
)

type TCPDialer struct {
	RemoteAddr string
}

func (d *TCPDialer) Dial() (net.Conn, error) {
	return net.Dial("tcp", d.RemoteAddr)
}

type TCPmultiDialer struct {
	RemoteAddrs []string
	offset      int
	dialLock    sync.Mutex
}

func (d *TCPmultiDialer) Dial() (net.Conn, error) {
	if len(d.RemoteAddrs) == 0 {
		return nil, fmt.Errorf("no remote address")
	}
	d.dialLock.Lock()
	defer d.dialLock.Unlock()
	conn, err := net.Dial("tcp", d.RemoteAddrs[d.offset%len(d.RemoteAddrs)])
	d.offset++
	return conn, err
}
