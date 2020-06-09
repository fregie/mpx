package main

import (
	"io"
	"log"
	"net"
	"os"
	"time"

	"net/http"
	_ "net/http/pprof"

	"github.com/fregie/mpx"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	go http.ListenAndServe("0.0.0.0:8081", nil)
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
			buffer := make([]byte, 65535)
			file, err := os.OpenFile("./test-file", os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
			if err != nil {
				log.Print(err)
				return
			}
			defer file.Close()
			start := time.Now()
			size := 0
			for {
				n, err := tunn.Read(buffer)
				if err != nil {
					if err == io.EOF {
						log.Printf("%d mbps", size*8/1024/int(time.Now().Sub(start).Milliseconds()))
						return
					}
					log.Printf("read: %s", err)
					return
				}
				size += n
			}
		}()
	}
}
