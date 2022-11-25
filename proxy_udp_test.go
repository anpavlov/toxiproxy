package toxiproxy_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"net"
	"testing"

	"github.com/Shopify/toxiproxy/v2"
)

func TestUDPProxySimpleMessage(t *testing.T) {
	WithUDPProxy(t, func(conn *net.UDPConn, proxy toxiproxy.Proxy) {
		msg := []byte("hello world")

		_, err := conn.Write(msg)
		if err != nil {
			t.Error("Failed writing to UDP proxy", err)
		}
		// conn.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if !bytes.Equal(buf[:n], msg) {
			t.Errorf("Proxy didn't response same bytes from client; err: %v, request: %s, response: %s", err, msg, buf[:n])
		}
	})
}

func TestUDPProxyBigMessage(t *testing.T) {
	WithUDPProxy(t, func(conn *net.UDPConn, proxy toxiproxy.Proxy) {
		msg := make([]byte, 63*1024)
		rand.Read(msg)

		_, err := conn.Write(msg)
		if err != nil {
			t.Fatal("Failed writing to UDP proxy", err)
		}
		// conn.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, len(msg))
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatal("Unexpected error on read from UDP proxy", err)
		}
		if len(msg) != n {
			t.Fatalf("Proxy didn't response same bytes num from clien; request: %d, response: %d", len(msg), n)
		}
		if !bytes.Equal(buf[:n], msg) {
			t.Fatalf("Proxy didn't response same bytes from client, request: %s, response: %s", hex.EncodeToString(msg), hex.EncodeToString(buf[:n]))
		}
	})
}
