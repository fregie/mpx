package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shadowsocks/go-shadowsocks2/socks"
)

type Client struct {
	TCPListener net.Listener
	TCPRuning   bool
	closeTCP    bool
}

type Connecter interface {
	Connect() (net.Conn, error)
	ServerHost() string
}

type shadowUpgrade func(net.Conn) net.Conn

// Create a SOCKS server listening on addr and proxy to server.
func (c *Client) StartsocksConnLocal(addr string, connecter Connecter, shadow shadowUpgrade) error {
	log.Printf("SOCKS proxy %s <-> %s", addr, connecter.ServerHost())
	var err error
	c.TCPListener, err = net.Listen("tcp", addr)
	if err != nil {
		log.Printf("failed to listen on %s: %v", addr, err)
		return err
	}
	c.TCPRuning = true
	defer func() { c.TCPRuning = false }()

	for {
		lc, err := c.TCPListener.Accept()
		if err != nil {
			log.Printf("failed to accept: %s", err)
			if c.closeTCP {
				log.Printf("tcp ss stoped")
				break
			}
			continue
		}
		lc.(*net.TCPConn).SetKeepAlive(true)
		go c.handleConn(lc, connecter, shadow)
	}
	return nil
}

func (c *Client) StopsocksConnLocal() error {
	log.Printf("stopping tcp ss")
	if !c.TCPRuning {
		log.Printf("TCP is not running")
		return errors.New("Not running")
	}
	c.closeTCP = true
	err := c.TCPListener.Close()
	if err != nil {
		log.Printf("close tcp listener failed: %s", err)
		return err
	}
	return nil
}

func (c *Client) handleConn(lc net.Conn, connecter Connecter, shadow shadowUpgrade) {
	defer lc.Close()
	tgt, err := socks.Handshake(lc)
	if err != nil {
		// UDP: keep the connection until disconnect then free the UDP socket
		if err == socks.InfoUDPAssociate {
			buf := []byte{}
			// block here
			for {
				_, err := lc.Read(buf)
				if err, ok := err.(net.Error); ok && err.Timeout() {
					continue
				}
				log.Printf("UDP Associate End.")
				return
			}
		}
		log.Printf("failed to get target address: %v", err)
		return
	}
	rc, err := connecter.Connect()
	if err != nil {
		log.Printf("Connect to %s failed: %s", connecter.ServerHost(), err)
		return
	}
	defer rc.Close()
	rc = shadow(rc)
	if _, err = rc.Write(tgt); err != nil {
		log.Printf("failed to send target address: %v", err)
		return
	}

	log.Printf("proxy %s <-> %s <-> %s", lc.RemoteAddr(), connecter.ServerHost(), tgt)
	_, _, err = relay(rc, lc)
	if err != nil {
		if err, ok := err.(net.Error); ok && err.Timeout() {
			return // ignore i/o timeout
		}
		log.Printf("relay error: %v", err)
	}
}

// relay copies between left and right bidirectionally. Returns number of
// bytes copied from right to left, from left to right, and any error occurred.
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

type TCPConnecter struct {
	ServerAddr string
}

func (tc *TCPConnecter) Connect() (net.Conn, error) {
	return net.Dial("tcp", tc.ServerAddr)
}

func (tc *TCPConnecter) ServerHost() string {
	return tc.ServerAddr
}

type WSConnecter struct {
	ServerAddr string
	URL        string
	Username   string
	dailer     *websocket.Dialer
}

func (ws *WSConnecter) Connect() (net.Conn, error) {
	if ws.dailer == nil {
		ws.dailer = websocket.DefaultDialer
	}
	u := url.URL{Scheme: "ws", Host: ws.ServerAddr, Path: ws.URL}
	fmt.Printf("dial to %s\n", u.String())
	header := http.Header{
		"Shadowsocks-Username": []string{ws.Username},
	}
	wc, _, err := ws.dailer.Dial(u.String(), header)
	if err != nil {
		log.Printf("websocket dail failed: %s", err)
		return nil, err
	}
	return wc.UnderlyingConn(), nil
}

func (ws *WSConnecter) Dial() (net.Conn, error) {
	return ws.Connect()
}

func (ws *WSConnecter) ServerHost() string {
	return ws.ServerAddr
}
