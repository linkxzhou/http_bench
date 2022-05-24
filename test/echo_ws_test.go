package test

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"testing"

	"github.com/gorilla/websocket"
)

const (
	NAME3 = "WS"
)

var upgrader = websocket.Upgrader{} // use default options

func TestEchoWS(t *testing.T) {
	listen := "0.0.0.0:18092"
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Print("upgrade:", err)
			return
		}
		defer c.Close()
		for {
			mt, message, err := c.ReadMessage()
			if err != nil {
				log.Println(NAME3+" read:", err)
				break
			}
			if message != nil {
				log.Println("message: ", string(message))
			}
			err = c.WriteMessage(mt, message)
			if err != nil {
				log.Println(NAME3+" write:", err)
				break
			}
		}
	})
	fmt.Fprintf(os.Stdout, NAME3+" Server listen %s\n", listen)
	if err := http.ListenAndServe(listen, mux); err != nil {
		fmt.Fprintf(os.Stderr, NAME3+" ListenAndServe err: %s\n", err.Error())
	}
}
