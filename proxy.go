package toxiproxy

import (
	"net"
	"sync"

	"github.com/rs/zerolog"
	tomb "gopkg.in/tomb.v1"

	"github.com/Shopify/toxiproxy/v2/stream"
)

// ProxyTCP represents the proxy in its entirety with all its links. The main
// responsibility of ProxyTCP is to accept new client and create Links between the
// client and upstream.
//
// Client <-> toxiproxy <-> Upstream.
type ProxyTCP struct {
	sync.Mutex

	name     string
	listen   string
	upstream string
	enabled  bool

	listener net.Listener
	started  chan error

	tomb        *tomb.Tomb
	connections ConnectionList
	toxics      *ToxicCollection
	apiServer   *ApiServer
	logger      *zerolog.Logger
}

func (proxy *ProxyTCP) Name() string {
	return proxy.name
}

func (proxy *ProxyTCP) Listen() string {
	return proxy.listen
}

func (proxy *ProxyTCP) Upstream() string {
	return proxy.upstream
}

func (proxy *ProxyTCP) Enabled() bool {
	return proxy.enabled
}

func (proxy *ProxyTCP) Logger() *zerolog.Logger {
	return proxy.logger
}

func (proxy *ProxyTCP) Toxics() *ToxicCollection {
	return proxy.toxics
}

func (proxy *ProxyTCP) getTomb() *tomb.Tomb {
	return proxy.tomb
}

func (proxy *ProxyTCP) setTomb(tomb *tomb.Tomb) {
	proxy.tomb = tomb
}

func (proxy *ProxyTCP) getConnections() *ConnectionList {
	return &proxy.connections
}

func (proxy *ProxyTCP) startedCh() chan error {
	return proxy.started
}

func (proxy *ProxyTCP) toggle(enable bool) {
	proxy.enabled = enable
}

func (proxy *ProxyTCP) Config() ProxyConfig {
	return ProxyConfig{
		Enabled:  proxy.Enabled(),
		Name:     proxy.Name(),
		Listen:   proxy.Listen(),
		Upstream: proxy.Upstream(),
	}
}

func NewProxy(server *ApiServer, name, listen, upstream string) *ProxyTCP {
	l := server.Logger.
		With().
		Str("name", name).
		Str("listen", listen).
		Str("upstream", upstream).
		Logger()

	proxy := &ProxyTCP{
		name:        name,
		listen:      listen,
		upstream:    upstream,
		started:     make(chan error),
		connections: ConnectionList{list: make(map[string]net.Conn)},
		apiServer:   server,
		logger:      &l,
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
	proxy.Lock()
	defer proxy.Unlock()

	if input.Listen != proxy.listen || input.Upstream != proxy.upstream {
		stop(proxy)
		proxy.listen = input.Listen
		proxy.upstream = input.Upstream
	}

	if input.Enabled != proxy.enabled {
		if input.Enabled {
			return start(proxy)
		}
		stop(proxy)
	}
	return nil
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

func (proxy *ProxyTCP) RemoveConnection(name string) {
	proxy.connections.Lock()
	defer proxy.connections.Unlock()
	delete(proxy.connections.list, name)
}

// Starts a proxy, assumes the lock has already been taken.
func start(proxy proxy) error {
	if proxy.Enabled() {
		return ErrProxyAlreadyStarted
	}

	proxy.setTomb(&tomb.Tomb{}) // Reset tomb, from previous starts/stops
	go proxy.server()
	err := <-proxy.startedCh()
	// Only enable the proxy if it successfully started
	proxy.toggle(err == nil)
	return err
}

// Stops a proxy, assumes the lock has already been taken.
func stop(proxy proxy) {
	if !proxy.Enabled() {
		return
	}
	proxy.toggle(false)

	proxy.getTomb().Killf("Shutting down from stop()")
	proxy.getTomb().Wait() // Wait until we stop accepting new connections

	proxy.getConnections().Lock()
	defer proxy.getConnections().Unlock()
	for _, conn := range proxy.getConnections().list {
		conn.Close()
	}

	proxy.Logger().
		Info().
		Msg("Terminated proxy")
}
