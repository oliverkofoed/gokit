package main

import (
	cryptorand "crypto/rand"
	"crypto/tls"
	"fmt"
	"net"

	"github.com/oliverkofoed/gokit/rpckit"
)

func main() {
	server := rpckit.NewTestServer()

	// Create the main listener.
	mainL, err := net.Listen("tcp", ":7000")
	if err != nil {
		panic(err)
	}

	// ssl listener
	mainSSL, err := net.Listen("tcp", ":7123")
	if err != nil {
		panic(err)
	}
	up := "../../../../../../"
	certificate, err := tls.LoadX509KeyPair(up+"certs/domain.crt", up+"certs/domain.key")
	if err != nil {
		panic(err)
	}

	mainSSL = tls.NewListener(mainSSL, &tls.Config{
		Certificates: []tls.Certificate{certificate},
		Rand:         cryptorand.Reader,
	})

	fmt.Println("listening")
	go server.Serve(mainSSL)
	server.Serve(mainL)
}
