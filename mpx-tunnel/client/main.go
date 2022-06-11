package main

import (
	"flag"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/fregie/mpx"
	"github.com/fregie/mpx/dialer"

	"net/http"
	_ "net/http/pprof"
)

var (
	remoteAddr  = flag.String("s", "0.0.0.0", "")
	coNum       = flag.Int("p", 2, "")
	serverAddr  = flag.String("l", "0.0.0.0:5513", "")
	enablePprof = flag.Bool("pprof", false, "")
)

type mpxConnecter struct {
	*mpx.ConnPool
}

func (m *mpxConnecter) Connect() (net.Conn, error) { return m.ConnPool.Connect(nil) }
func (m *mpxConnecter) ServerHost() string         { return "" }

func main() {
	flag.Parse()
	if *enablePprof {
		go http.ListenAndServe("0.0.0.0:6060", nil)
	}
	servers := strings.Split(*remoteAddr, ",")
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
	cp.StartWithDialer(d, *coNum)
	connecter := &mpxConnecter{ConnPool: cp}

	lis, err := net.Listen("tcp", *serverAddr)
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
			rc, err := connecter.Connect()
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
