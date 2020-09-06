package main

import (
	"log"
	"net"

	"github.com/fregie/mpx"
	"github.com/fregie/mpx/dialer"
	"github.com/shadowsocks/go-shadowsocks2/core"
)

const (
	ssMethod   = "aes-256-gcm"
	ssPassword = "q92H92qreL1MAu9u"
)

type mpxConnecter struct {
	*mpx.ConnPool
}

func (m *mpxConnecter) Connect() (net.Conn, error) { return m.ConnPool.Connect(nil) }
func (m *mpxConnecter) ServerHost() string         { return "" }

func main() {
	ciph, err := core.PickCipher(ssMethod, []byte{}, ssPassword)
	if err != nil {
		log.Fatal(err)
	}
	dialer := &dialer.TCPDialer{RemoteAddr: "47.52.197.88:5512"}
	cp := mpx.NewConnPool()
	cp.StartWithDialer(dialer, 5)
	client := &Client{}
	connecter := &mpxConnecter{ConnPool: cp}

	client.StartsocksConnLocal("127.0.0.1:1080", connecter, ciph.StreamConn)
}
