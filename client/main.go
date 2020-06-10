package main

import (
	"log"
	"net"

	"github.com/fregie/mpx"
	"github.com/fregie/mpx/dialer"
	"github.com/shadowsocks/go-shadowsocks2/core"

	"net/http"
	_ "net/http/pprof"
)

type mpxConnecter struct {
	*mpx.ConnPool
}

func (m *mpxConnecter) Connect() (net.Conn, error) { return m.ConnPool.Connect(nil) }
func (m *mpxConnecter) ServerHost() string         { return "" }

func main() {
	go http.ListenAndServe("0.0.0.0:8083", nil)

	ciph, err := core.PickCipher("AES-256-GCM", []byte{}, "789632145")
	if err != nil {
		log.Fatal(err)
	}

	dialer := &dialer.TCPDialer{RemoteAddr: "45.77.142.97:5512"}
	// dialer := &WSConnecter{ServerAddr: "line-test2.transocks.com.cn:80", URL: "/proxy"}
	cp := mpx.NewConnPool()
	cp.StartWithDialer(dialer, 5)
	client := &Client{}
	// connecter := &mpxConnecter{ConnPool: cp}
	connecter := &TCPConnecter{ServerAddr: "45.77.142.97:5512"}
	// connecter := &WSConnecter{ServerAddr: "line-test2.transocks.com.cn:80", URL: "/proxy"}

	client.StartsocksConnLocal("127.0.0.1:1080", connecter, ciph.StreamConn)
}
