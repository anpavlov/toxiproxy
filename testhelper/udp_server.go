package testhelper

import (
	"net"
	"testing"
)

func NewUDPServer() (*UDPServer, error) {
	result := &UDPServer{
		addr: "localhost:0",
		stop: make(chan struct{}),
	}
	err := result.Run()
	if err != nil {
		return nil, err
	}
	return result, nil
}

type UDPServer struct {
	addr   string
	server *net.UDPConn
	stop   chan struct{}
}

func (server *UDPServer) Run() (err error) {
	listernAddr, err := net.ResolveUDPAddr("udp", server.addr)
	if err != nil {
		return
	}
	server.server, err = net.ListenUDP("udp", listernAddr)
	if err != nil {
		return
	}
	server.addr = server.server.LocalAddr().String()
	return
}

func (server *UDPServer) handle_connection() error {
	buffer := make([]byte, 64*1024)
	for {
		n, addr, err := server.server.ReadFromUDP(buffer)
		if err != nil {
			select {
			case <-server.stop:
				return nil
			default:
			}
			return err
		}

		_, err = server.server.WriteToUDP(buffer[:n], addr)
		if err != nil {
			return err
		}
	}
}

func (server *UDPServer) Close() (err error) {
	close(server.stop)
	return server.server.Close()
}

func WithUDPServer(t *testing.T, block func(string)) {
	server, err := NewUDPServer()
	if err != nil {
		t.Fatal("Failed to create UDP server", err)
	}
	go func(t *testing.T, server *UDPServer) {
		err := server.handle_connection()
		if err != nil {
			t.Error("Failed to handle connection", err)
		}
	}(t, server)
	block(server.addr)
	server.Close()
}
