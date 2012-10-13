package frames

import (
	"log"
	"net"
)

func ExampleServer() {
	tcpListener, err := net.Listen("tcp", ":8675")
	if err != nil {
		log.Fatalf("Error setting up tcp listener: %v", err)
	}

	l, err := ListenerListener(tcpListener)
	if err != nil {
		log.Fatalf("Error setting up the listener listener: %v", err)
	}

	for {
		c, err := l.Accept()
		if err != nil {
			log.Fatalf("Error accepting: %v", err)
		}
		go func(c net.Conn) {
			// do stuff with c
		}(c)
	}
}
