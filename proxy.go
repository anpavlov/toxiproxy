package toxiproxy

import (
	"io"
	"net"

	tomb "gopkg.in/tomb.v1"

	"github.com/Shopify/toxiproxy/v2/stream"
)

// ProxyTCP represents the proxy in its entirety with all its links. The main
// responsibility of ProxyTCP is to accept new client and create Links between the
// client and upstream.
//
// Client <-> toxiproxy <-> Upstream.
type ProxyTCP struct {
	proxyBase

	listener net.Listener
}

func NewProxyTCP(server *ApiServer, name, listen, upstream string) Proxy {
	l := server.Logger.
		With().
		Str("name", name).
		Str("listen", listen).
		Str("upstream", upstream).
		Logger()

	proxy := &ProxyTCP{
		proxyBase: proxyBase{
			name:        name,
			listen:      listen,
			upstream:    upstream,
			started:     make(chan error),
			connections: ConnectionList{list: make(map[string]io.Closer)},
			apiServer:   server,
			logger:      &l,
		},
	}
	proxy.toxics = NewToxicCollection(proxy)
	return proxy
}

func (proxy *ProxyTCP) Start() error {
	proxy.Lock()
	defer proxy.Unlock()

	return start(proxy)
}

func (proxy *ProxyTCP) Update(input ProxyConfig) error {
	return proxy.proxyBase.Update(input, proxy)
}

func (proxy *ProxyTCP) Stop() {
	proxy.Lock()
	defer proxy.Unlock()

	stop(proxy)
}

func (proxy *ProxyTCP) startListening() error {
	var err error
	proxy.listener, err = net.Listen("tcp", proxy.listen)
	if err != nil {
		proxy.started <- err
		return err
	}
	proxy.listen = proxy.listener.Addr().String()
	proxy.started <- nil

	proxy.Logger().
		Info().
		Msg("Started proxy")

	return nil
}

func (proxy *ProxyTCP) close() {
	// Unblock proxy.listener.Accept()
	err := proxy.listener.Close()
	if err != nil {
		proxy.logger.
			Warn().
			Err(err).
			Msg("Attempted to close an already closed proxy server")
	}
}

// This channel is to kill the blocking Accept() call below by closing the
// net.Listener.
func (proxy *ProxyTCP) freeBlocker(acceptTomb *tomb.Tomb) {
	<-proxy.tomb.Dying()

	// Notify ln.Accept() that the shutdown was safe
	acceptTomb.Killf("Shutting down from stop()")

	proxy.close()

	// Wait for the accept loop to finish processing
	acceptTomb.Wait()
	proxy.tomb.Done()
}

// server runs the Proxy server, accepting new clients and creating Links to
// connect them to upstreams.
func (proxy *ProxyTCP) server() {
	err := proxy.startListening()
	if err != nil {
		return
	}

	acceptTomb := &tomb.Tomb{}
	defer acceptTomb.Done()

	// This channel is to kill the blocking Accept() call below by closing the
	// net.Listener.
	go proxy.freeBlocker(acceptTomb)

	for {
		client, err := proxy.listener.Accept()
		if err != nil {
			// This is to confirm we're being shut down in a legit way. Unfortunately,
			// Go doesn't export the error when it's closed from Close() so we have to
			// sync up with a channel here.
			//
			// See http://zhen.org/blog/graceful-shutdown-of-go-net-dot-listeners/
			select {
			case <-acceptTomb.Dying():
			default:
				proxy.logger.
					Warn().
					Err(err).
					Msg("Error while accepting client")
			}
			return
		}

		proxy.logger.
			Info().
			Str("client", client.RemoteAddr().String()).
			Msg("Accepted client")

		upstream, err := net.Dial("tcp", proxy.upstream)
		if err != nil {
			proxy.logger.
				Err(err).
				Str("client", client.RemoteAddr().String()).
				Msg("Unable to open connection to upstream")
			client.Close()
			continue
		}

		name := client.RemoteAddr().String()
		proxy.connections.Lock()
		proxy.connections.list[name+"upstream"] = upstream
		proxy.connections.list[name+"downstream"] = client
		proxy.connections.Unlock()
		proxy.toxics.StartLink(proxy.apiServer, name+"upstream", client, upstream, stream.Upstream)
		proxy.toxics.StartLink(proxy.apiServer, name+"downstream", upstream, client, stream.Downstream)
	}
}
