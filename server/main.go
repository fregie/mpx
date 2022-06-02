package main

import (
	"io"
	"log"
	"net"
	"time"

	"github.com/shadowsocks/go-shadowsocks2/core"
	"github.com/shadowsocks/go-shadowsocks2/socks"

	"github.com/fregie/mpx"
)

const (
	ssMethod   = "aes-256-gcm"
	ssPassword = "q92H92qreL1MAu9u"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	ciph, err := core.PickCipher(ssMethod, []byte{}, ssPassword)
	if err != nil {
		log.Fatal(err)
	}
	mpx.Verbose(true)
	lis, err := net.Listen("tcp", "0.0.0.0:5512")
	if err != nil {
		log.Fatal(err)
	}
	cp := mpx.NewConnPool()
	go cp.ServeWithListener(lis)
	log.Printf("Start at %s", lis.Addr().String())

	for {
		tunn, err := cp.Accept()
		if err != nil {
			log.Print(err)
			continue
		}
		if tunn == nil {
			continue
		}
		go func() {
			defer tunn.Close()
			conn := ciph.StreamConn(tunn)
			tgt, err := socks.ReadAddr(conn)
			if err != nil {
				log.Printf("failed to get target address: %v", err)
				return
			}
			var rcc net.Conn
			log.Printf("dial to %s", tgt.String())
			rcc, err = net.Dial("tcp4", tgt.String())
			if err != nil || rcc == nil {
				log.Printf("failed to connect to target[%s]: %v", tgt.String(), err)
				return
			}
			rc := rcc.(*net.TCPConn)
			defer rc.Close()
			rc.SetKeepAlive(true)
			_, _, err = relay(conn, rcc)
			if err != nil {
				log.Print(err)
				return
			}
		}()
	}
}

func relay(left, right net.Conn) (int64, int64, error) {
	type res struct {
		N   int64
		Err error
	}
	ch := make(chan res)

	go func() {
		var n int64
		var err error
		n, err = io.Copy(right, left)
		right.SetDeadline(time.Now()) // wake up the other goroutine blocking on right
		left.SetDeadline(time.Now())  // wake up the other goroutine blocking on left
		ch <- res{n, err}
	}()
	var n int64
	var err error
	n, err = io.Copy(left, right)
	right.SetDeadline(time.Now()) // wake up the other goroutine blocking on right
	left.SetDeadline(time.Now())  // wake up the other goroutine blocking on left
	rs := <-ch

	if err == nil {
		err = rs.Err
	}
	return n, rs.N, err
}
