package mpx

import (
	"errors"
	"io"
	"log"
	"net"
	"sync"
)

type Conn struct {
	net.Conn
	ID int
}

type ConnPool struct {
	connMap sync.Map // key: int , value: net.conn
	IDs     []int
	tunnMap sync.Map // key: int , value: Tunnel
}

func (p *ConnPool) AddConn(conn net.Conn) error {
	if conn == nil {
		return errors.New("Conn is nil")
	}
	p.connMap.Store(len(p.IDs)+1, conn)
	p.updateIDs()

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
	buf := make([]byte, 1024*10)
	defer conn.Close()
	for {
		n, err := conn.Read(buf)
		if err != nil {
			if err == io.EOF {
				break
			}
			log.Printf("read err:%s", err)
		}

	}
}
