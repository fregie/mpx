package dialer

import "net"

type TCPDialer struct {
	RemoteAddr string
}

func (d *TCPDialer) Dial() (net.Conn, error) {
	return net.Dial("tcp", d.RemoteAddr)
}
