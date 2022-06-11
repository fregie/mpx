package main

import (
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/fregie/mpx"
	"github.com/fregie/mpx/dialer"
)

var (
	ListenAddr  = flag.String("listen", "0.0.0.0:5513", "")
	remoteAddr  = flag.String("server", "", "")
	targetAddr  = flag.String("target", "", "target address")
	coNum       = flag.Int("p", 2, "")
	enablePprof = flag.Bool("pprof", false, "")
	verbose     = flag.Bool("v", false, "verbose")
)

func main() {
	flag.Parse()
	if *enablePprof {
		go http.ListenAndServe("0.0.0.0:6060", nil)
	}
	if *verbose {
		mpx.Verbose(true)
	}

	if *remoteAddr != "" { // client
		runClient(*ListenAddr, *remoteAddr, *coNum)
	} else if *targetAddr != "" { // server
		runServer(*ListenAddr, *targetAddr)
	} else {
		log.Fatal("server or target address must be set")
	}
}

func runClient(localAddr, remoteAddr string, concurrentNum int) {
	servers := strings.Split(remoteAddr, ",")
	remoteAddrs := make([]dialer.ServerWithWeight, 0, len(servers))
	for _, server := range servers {
		re := strings.Split(server, "|")
		if len(re) == 2 {
			addr := re[0]
			weight, err := strconv.Atoi(re[1])
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("add server %s with weight %d", addr, weight)
			remoteAddrs = append(remoteAddrs, dialer.ServerWithWeight{
				Addr:   addr,
				Weight: uint32(weight),
			})
		}
	}
	d := dialer.NewTCPmultiDialer(remoteAddrs)
	mpx.Verbose(true)
	cp := mpx.NewConnPool()
	cp.StartWithDialer(d, concurrentNum)
	lis, err := net.Listen("tcp", localAddr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Start at %s", lis.Addr().String())
	for {
		c, err := lis.Accept()
		if err != nil {
			log.Print(err)
			continue
		}
		go func() {
			defer c.Close()
			rc, err := cp.Connect(nil)
			if err != nil {
				log.Printf("failed to connect to target: %v", err)
			}
			_, _, err = relay(c, rc)
			if err != nil {
				log.Printf("relay error: %v", err)
			}
		}()
	}
}

func runServer(localAddr, targetAddr string) {
	target, err := net.ResolveTCPAddr("tcp", targetAddr)
	if err != nil {
		log.Fatal(err)
	}
	lis, err := net.Listen("tcp", localAddr)
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
			log.Printf("dial to %s", targetAddr)
			rc, err := net.DialTCP("tcp", nil, target)
			if err != nil || rc == nil {
				log.Printf("failed to connect to target[%s]: %v", targetAddr, err)
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
		n, err := io.Copy(right, left)
		right.SetDeadline(time.Now()) // wake up the other goroutine blocking on right
		left.SetDeadline(time.Now())  // wake up the other goroutine blocking on left
		ch <- res{n, err}
	}()

	n, err := io.Copy(left, right)
	right.SetDeadline(time.Now()) // wake up the other goroutine blocking on right
	left.SetDeadline(time.Now())  // wake up the other goroutine blocking on left
	rs := <-ch

	if err == nil {
		err = rs.Err
	}
	return n, rs.N, err
}
