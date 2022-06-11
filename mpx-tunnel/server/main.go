package main

import (
	"flag"
	"io"
	"log"
	"net"
	"time"

	"github.com/fregie/mpx"
)

var (
	ListenAddr = flag.String("l", "0.0.0.0:5512", "listen address")
	targetAddr = flag.String("target", "", "target address")
	verbose    = flag.Bool("v", false, "verbose")
)

func main() {
	flag.Parse()
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	mpx.Verbose(*verbose)
	target, err := net.ResolveTCPAddr("tcp", *targetAddr)
	if err != err {
		log.Fatal(err)
	}
	lis, err := net.Listen("tcp", *ListenAddr)
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
			log.Printf("dial to %s", *targetAddr)
			rc, err := net.DialTCP("tcp", nil, target)
			if err != nil || rc == nil {
				log.Printf("failed to connect to target[%s]: %v", *targetAddr, err)
				return
			}
			rc.SetKeepAlive(true)
			_, _, err = relay(tunn, rc)
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
