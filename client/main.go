package main

import (
	"flag"
	"log"
	"net"
	"strconv"
	"strings"

	"github.com/fregie/mpx"
	"github.com/fregie/mpx/dialer"
	"github.com/shadowsocks/go-shadowsocks2/core"
)

const (
	ssMethod   = "aes-256-gcm"
	ssPassword = "q92H92qreL1MAu9u"
)

var (
	remoteAddr = flag.String("s", "0.0.0.0", "")
	coNum      = flag.Int("p", 2, "")
)

type mpxConnecter struct {
	*mpx.ConnPool
}

func (m *mpxConnecter) Connect() (net.Conn, error) { return m.ConnPool.Connect(nil) }
func (m *mpxConnecter) ServerHost() string         { return "" }

func main() {
	flag.Parse()
	ciph, err := core.PickCipher(ssMethod, []byte{}, ssPassword)
	if err != nil {
		log.Fatal(err)
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
	client := &Client{}
	connecter := &mpxConnecter{ConnPool: cp}

	client.StartsocksConnLocal("127.0.0.1:1080", connecter, ciph.StreamConn)
}
