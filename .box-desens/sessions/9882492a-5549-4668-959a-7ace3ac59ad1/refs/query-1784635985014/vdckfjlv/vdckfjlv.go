package test

import (
	"fmt"
	"net"
	"os"
	"testing"
)

const (
	NAMETCP = "TCP"
)

func TestEchoTCP(t *testing.T) {
	listen := "0.0.0.0:18095"
	if len(os.Args) > 5 {
		listen = os.Args[len(os.Args)-1]
	}

	listener, err := net.Listen("tcp", listen)
	if err != nil {
		fmt.Println(NAMETCP+" listener err: ", err)
		return
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	buffer := make([]byte, 1024)
	for {
		n, err := conn.Read(buffer)
		if err != nil {
			fmt.Println(NAMETCP+" read error: ", err)
			return
		}

		fmt.Println(NAMETCP+" read buffer: ", string(buffer), ", n: ", n)
		message := string(buffer[:n])
		response := fmt.Sprintf(NAMETCP+"recv: %s", message)
		_, err = conn.Write([]byte(response))
		if err != nil {
			fmt.Println(NAMETCP+" send error: ", err)
			return
		}
		fmt.Println(NAMETCP+" send buffer: ", string(message))
	}
}
