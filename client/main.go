package main

import (
	"log"
	"time"

	"github.com/fregie/mpx"
	"github.com/fregie/mpx/dialer"

	"net/http"
	_ "net/http/pprof"
)

func main() {
	go http.ListenAndServe("0.0.0.0:8083", nil)
	time.Sleep(5 * time.Second)
	dialer := &dialer.TCPDialer{RemoteAddr: "127.0.0.1:5512"}
	cp := mpx.NewConnPool()
	cp.StartWithDialer(dialer, 5)
	tunn, err := cp.Connect(nil)
	if err != nil {
		log.Fatal(err)
	}

	// io.Copy(tunn, file)
	size := 0
	buffer := make([]byte, 1400)
	for {
		if size >= 1024*1024*1024 {
			break
		}
		n, err := tunn.Write(buffer)
		if err != nil {
			log.Print(err)
		}
		size += n
	}

	tunn.Close()
	time.Sleep(60 * time.Second)
}
