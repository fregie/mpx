package main

import (
	"io"
	"log"
	"time"

	"github.com/fregie/mpx"
	"github.com/fregie/mpx/dialer"
)

func main() {
	dialer := &dialer.TCPDialer{RemoteAddr: "127.0.0.1:5512"}
	cp := mpx.NewConnPool()
	cp.StartWithDialer(dialer, 5)
	tunn, err := cp.Connect([]byte("Hello mpx !"))
	if err != nil {
		log.Fatal(err)
	}
	tunn.Write([]byte("Hello mpx2 !"))
	tunn.Write([]byte("Hello mpx3 !"))
	tunn.Write([]byte("Hello mpx4 !"))

	tunn2, err := cp.Connect([]byte("Hello2 mpx !"))
	if err != nil {
		log.Fatal(err)
	}
	tunn2.Write([]byte("Hello2 mpx2 !"))
	tunn2.Write([]byte("Hello2 mpx3 !"))
	tunn2.Write([]byte("Hello2 mpx4 !"))

	buffer := make([]byte, 10000)
	for {
		n, err := tunn.Read(buffer)
		if err != nil {
			if err == io.EOF {
				return
			}
			log.Printf("read: %s", err)
			return
		}
		log.Printf("Receive: %s", string(buffer[:n]))
	}

	// tunn.Close()
	time.Sleep(600 * time.Second)
}
