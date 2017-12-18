package rpckit

import (
	"net"
)

type Server struct {
	onConnection ConnectionHandler
	onMessage    MessageHandler
	onDisconnect DisconnectedHandler
}

func NewServer(onConnection ConnectionHandler, onMessage MessageHandler, onDisconnect DisconnectedHandler) *Server {
	return &Server{
		onConnection: onConnection,
		onMessage:    onMessage,
		onDisconnect: onDisconnect,
	}
}

func (s *Server) ListenAndServe(network, address string) error {
	l, err := net.Listen(network, address)
	if err != nil {
		return err
	}
	return s.Serve(l)
}

func (s *Server) Serve(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}

		connection := connected(conn, s.onMessage, s.onDisconnect)
		if s.onConnection != nil {
			s.onConnection(connection)
		}
	}
}
