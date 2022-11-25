package toxiproxy_test

import (
	"net"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/Shopify/toxiproxy/v2"
	"github.com/Shopify/toxiproxy/v2/collectors"
	"github.com/Shopify/toxiproxy/v2/testhelper"
)

func NewTestProxy(name, upstream string) toxiproxy.Proxy {
	log := zerolog.Nop()
	if flag.Lookup("test.v").DefValue == "true" {
		log = zerolog.New(os.Stdout).With().Caller().Timestamp().Logger()
	}
	srv := toxiproxy.NewServer(
		toxiproxy.NewMetricsContainer(prometheus.NewRegistry()),
		log,
	)
	srv.Metrics.ProxyMetrics = collectors.NewProxyMetricCollectors()
	proxy := toxiproxy.NewProxyTCP(srv, name, "localhost:0", upstream)

	return proxy
}

func NewTestUDPProxy(name, upstream string) toxiproxy.Proxy {
	log := zerolog.Nop()
	if flag.Lookup("test.v").DefValue == "true" {
		log = zerolog.New(os.Stdout).With().Caller().Timestamp().Logger()
	}
	srv := toxiproxy.NewServer(
		toxiproxy.NewMetricsContainer(prometheus.NewRegistry()),
		log,
	)
	srv.Metrics.ProxyMetrics = collectors.NewProxyMetricCollectors()
	proxy := toxiproxy.NewProxyUdp(srv, name, "localhost:0", upstream)

	return proxy
}

func WithTCPProxy(
	t *testing.T,
	f func(proxy net.Conn, response chan []byte, proxyServer toxiproxy.Proxy),
) {
	testhelper.WithTCPServer(t, func(upstream string, response chan []byte) {
		proxy := NewTestProxy("test", upstream)
		proxy.Start()

		conn := AssertProxyUp(t, proxy.Listen(), true)

		f(conn, response, proxy)

		proxy.Stop()
	})
}

func WithUDPProxy(
	t *testing.T,
	f func(proxy *net.UDPConn, proxyServer toxiproxy.Proxy),
) {
	testhelper.WithUDPServer(t, func(upstream string) {
		proxy := NewTestUDPProxy("test", upstream)
		proxy.Start()
		defer proxy.Stop()

		raddr, err := net.ResolveUDPAddr("udp", proxy.Listen())
		if err != nil {
			t.Error("Failed to resolve proxy listen udp addr:", err)
		}

		conn, err := net.DialUDP("udp", nil, raddr)
		if err != nil {
			t.Error("Failed to dial udp proxy:", err)
		}
		defer conn.Close()

		f(conn, proxy)
	})
}

func AssertProxyUp(t *testing.T, addr string, up bool) net.Conn {
	conn, err := net.Dial("tcp", addr)
	if err != nil && up {
		t.Error("Expected proxy to be up:", err)
	} else if err == nil && !up {
		t.Error("Expected proxy to be down")
	}
	return conn
}
