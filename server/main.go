package main

import (
	"io"
	"log"
	"net"

	"github.com/fregie/mpx"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	lis, err := net.Listen("tcp", "127.0.0.1:5512")
	if err != nil {
		log.Fatal(err)
	}
	cp := mpx.NewConnPool()
	go cp.Serve(lis)
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
				_, err = tunn.Write(buffer[:n])
				if err != nil {
					log.Print(err)
				}
			}
		}()
	}
}
