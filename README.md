# mpx
<p align="center">
<a href="https://github.com/fregie/mpx/actions?query=workflow%3ABuild"><img src="https://github.com/fregie/mpx/workflows/Build/badge.svg" alt="Build Status"></a>
<a href="https://goreportcard.com/report/github.com/fregie/mpx"><img src="https://goreportcard.com/badge/github.com/fregie/mpx" alt="go report"></a>
<a href="https://pkg.go.dev/github.com/fregie/mpx"><img src="https://img.shields.io/badge/go.dev-reference-007d9c?logo=go&logoColor=white" alt="go.dev"></a>
<a href="https://opensource.org/licenses/GPL-3-Clause"><img src="https://img.shields.io/badge/license-GPL3-orange.svg" alt="Licenses"></a>
<a href="https://github.com/fregie"><img src="https://img.shields.io/badge/fregie-weapon-blue" alt="Licenses"></a>
</p>

  
通用连接多路复用协议

## 原理

![mpx](./MPX.png)

mpx是一个基于标准库 `net` 中interface同时也实现了 `net` 库中部分interface的连接多路复用库。  

### 输入

mpx接受任何实现了 `net.Conn` 接口的连接作为输入。  
可以直接调用 `AddConn` 方法将Conn输入，也可以调用 `ServeWithListener` 输入一个 `net.Listener` ，调用 `StartWithDialer` 输入一个 `dailer` (mpx库中的一个interface)来使用mpx

### 输出

mpx提供给调用者一个名为 `ConnPool` 的struct。  
该struct实现了 `net.Listener` 供服务端调用，同时提供一个 `dial` 方法供客户端建立连接(返回一个 `net.Conn` )。

## 快速上手
### 安装
```shell
go get github.com/fregie/mpx
```
### 服务端(TCP)
```golang
import (
  "github.com/fregie/mpx"
  "net"
)

func main(){
  lis, _ := net.Listen("tcp", "0.0.0.0:5512")
  // Skip exception handling here
  cp := mpx.NewConnPool()
  go cp.ServeWithListener(lis)
  for {
    conn, _ := cp.Accept()
    // Skip exception handling here
    go func(){
      defer conn.Close()
      // Do something with conn(net.Conn)
    }
  }
}
```

### 客户端(TCP)
```golang
import (
  "github.com/fregie/mpx"
  "net"
)

type TCPDialer struct {
  ServerAddr string
}
func (t *TCPDialer) Dial() (net.Conn, error) {
	return net.Dial("tcp", t.ServerAddr)
}

func main(){
  cp := mpx.NewConnPool()
  cp.StartWithDialer(&TCPDialer{ServerAddr: "ip:port"}, 5)
  conn, _ := cp.Dial(nil)
  // Skip exception handling here
  defer conn.Close()
  // Do something with conn(net.Conn)
  conn.Write([]byte("something"))
}

```
