package dialer

import (
	"container/list"
	"fmt"
	"net"
	"sync"
)

type TCPDialer struct {
	RemoteAddr string
}

func (d *TCPDialer) Dial() (net.Conn, uint32, error) {
	conn, err := net.Dial("tcp", d.RemoteAddr)
	return conn, 1, err
}

type ServerWithWeight struct {
	Addr   string
	Weight uint32
}

type TCPmultiDialer struct {
	RemoteAddrs []ServerWithWeight
	dialLock    sync.Mutex
	connList    *list.List
}

type TCPmultiConn struct {
	net.Conn
	weight     uint32
	remoteAddr string
	isClosed   bool
}

func (c *TCPmultiConn) Close() error {
	c.isClosed = true
	return c.Conn.Close()
}

func NewTCPmultiDialer(remoteAddrs []ServerWithWeight) *TCPmultiDialer {
	return &TCPmultiDialer{
		RemoteAddrs: remoteAddrs,
		connList:    list.New(),
	}
}

func (d *TCPmultiDialer) Dial() (net.Conn, uint32, error) {
	if len(d.RemoteAddrs) == 0 {
		return nil, 0, fmt.Errorf("no remote address")
	}
	d.dialLock.Lock()
	defer d.dialLock.Unlock()
	weightMap := make(map[string]uint32)
	var totalWeight, totalActualWeight uint32
	for _, ra := range d.RemoteAddrs {
		weightMap[ra.Addr] = 0
		totalWeight += ra.Weight
	}
	toDelete := make([]*list.Element, 0)
	for conn := d.connList.Front(); conn != nil; conn = conn.Next() {
		c := conn.Value.(*TCPmultiConn)
		if c.isClosed {
			toDelete = append(toDelete, conn)
			continue
		}
		weightMap[c.remoteAddr] += c.weight
		totalActualWeight += c.weight
	}
	for _, conn := range toDelete {
		d.connList.Remove(conn)
	}

	var addr string
	var weight uint32
	if d.connList.Len() == 0 {
		addr = d.RemoteAddrs[0].Addr
		weight = d.RemoteAddrs[0].Weight
	} else {
		for _, ra := range d.RemoteAddrs {
			if float32(weightMap[ra.Addr])/float32(totalActualWeight) <= float32(ra.Weight)/float32(totalWeight) {
				addr = ra.Addr
				weight = ra.Weight
				break
			}
		}
	}
	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, 0, err
	}
	conn, err := net.DialTCP("tcp", nil, tcpAddr)
	if err != nil {
		return nil, 0, err
	}
	conn.SetKeepAlive(true)
	conn.SetNoDelay(true)
	mc := &TCPmultiConn{
		Conn:       conn,
		weight:     weight,
		remoteAddr: addr,
	}
	d.connList.PushBack(mc)

	return mc, weight, err
}
