package toxiproxy

import (
	"bufio"
	"fmt"
	"io"
	"net"

	tomb "gopkg.in/tomb.v1"

	"github.com/Shopify/toxiproxy/v2/stream"
	"github.com/rs/zerolog"
)

// ProxyUDP represents the proxy in its entirety with all its links. The main
// responsibility of ProxyUDP is to accept new client and create Links between the
// client and upstream.
//
// Client <-> toxiproxy <-> Upstream.
type ProxyUDP struct {
	proxyBase

	listener *net.UDPConn
}

const UDPBufferSize = 64 * 1024

func NewProxyUdp(server *ApiServer, name, listen, upstream string) Proxy {
	l := zerolog.Nop()
	if server != nil {
		l = server.Logger.
			With().
			Str("name", name).
			Str("listen", listen).
			Str("upstream", upstream).
			Str("protocol", "udp").
			Logger()
	}

	proxy := &ProxyUDP{
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

func (proxy *ProxyUDP) Start() error {
	proxy.Lock()
	defer proxy.Unlock()

	return start(proxy)
}

func (proxy *ProxyUDP) Update(input ProxyConfig) error {
	return proxy.proxyBase.Update(input, proxy)
}

func (proxy *ProxyUDP) Stop() {
	proxy.Lock()
	defer proxy.Unlock()

	stop(proxy)
}

func (proxy *ProxyUDP) startListening() error {
	var err error
	listenAddr, err := net.ResolveUDPAddr("udp", proxy.listen)
	if err != nil {
		proxy.started <- err
		return err
	}
	proxy.listener, err = net.ListenUDP("udp", listenAddr)
	if err != nil {
		proxy.started <- err
		return err
	}
	proxy.listen = proxy.listener.LocalAddr().String()
	proxy.started <- nil

	proxy.logger.
		Info().
		Msg("Started proxy")

	return nil
}

func (proxy *ProxyUDP) close() {
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
func (proxy *ProxyUDP) freeBlocker(acceptTomb *tomb.Tomb) {
	<-proxy.tomb.Dying()

	// Notify ln.Accept() that the shutdown was safe
	acceptTomb.Killf("Shutting down from stop()")

	proxy.close()

	// Wait for the accept loop to finish processing
	acceptTomb.Wait()
	proxy.tomb.Done()
}

// server runs the ProxyUdp server, accepting new clients and creating Links to
// connect them to upstreams.
func (proxy *ProxyUDP) server() {
	err := proxy.startListening()
	if err != nil {
		return
	}

	acceptTomb := &tomb.Tomb{}
	defer acceptTomb.Done()

	// This channel is to kill the blocking Accept() call below by closing the
	// net.Listener.
	go proxy.freeBlocker(acceptTomb)

	buffer := make([]byte, UDPBufferSize)

	for {
		msglen, remoteAddr, err := proxy.listener.ReadFromUDP(buffer)
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
					Str("protocol", "udp").
					Str("remote", remoteAddr.String()).
					Msg("Error while accepting client")
			}
			return
		}

		name := remoteAddr.String()
		if v, ok := proxy.connections.list[name+"upstream"]; ok {
			if writer, ok := v.(io.Writer); ok {
				writer.Write(buffer[:msglen])
			} else {
				panic("downstream writer ")
			}
			continue
		}

		proxy.logger.
			Info().
			Str("protocol", "udp").
			Str("client", remoteAddr.String()).
			Msg("Accepted client")

		upstreamAddr, err := net.ResolveUDPAddr("udp", proxy.upstream)
		if err != nil {
			proxy.logger.
				Err(err).
				Str("protocol", "udp").
				Str("client", remoteAddr.String()).
				Msg("Unable to resolve upstream address")
			continue
		}

		upstream, err := net.DialUDP("udp", nil, upstreamAddr)
		if err != nil {
			proxy.logger.
				Err(err).
				Str("protocol", "udp").
				Str("client", remoteAddr.String()).
				Msg("Unable to open connection to upstream")
			continue
		}

		// It is not possible to pass downstream or upstream UDPConn as Reader or Writer,
		// so we use two Pipes for client downstream "connection" in two directions
		clientUpPipeReader, clientUpPipeWriter := makeBufferedPipe(UDPBufferSize)
		clientDownPipeReader, clientDownPipeWriter := makeBufferedPipe(UDPBufferSize)

		// Buffered read writer is used to ensure whole UDP packet always fits
		// in read write buffers, futher calls to io.Copy in Links will use bufferedUpstream
		// buffers, so UDP packet will always be written to upstream with one Write call
		bufferedUpstreamReader := bufio.NewReaderSize(upstream, UDPBufferSize)
		// It is impossible to use bufio.Writer here, see makeBufferedPipe(int)
		bufferedUpstreamWriter := NewBufferedWriter(upstream, UDPBufferSize)

		go proxy.clientWrite(remoteAddr, clientDownPipeReader)

		proxy.connections.Lock()
		proxy.connections.list[name+"upstream"] = upstream
		proxy.connections.list[name+"downstream"] = clientUpPipeWriter
		proxy.connections.Unlock()

		// Links will never be closed themselves, because UDP has no living "connection"
		// the only way is to close unused links after some time of inactivity
		// TODO make some timeout for unused connections
		proxy.toxics.StartLink(proxy.apiServer, name+"upstream", clientUpPipeReader, bufferedUpstreamWriter, stream.Upstream)
		proxy.toxics.StartLink(proxy.apiServer, name+"downstream", bufferedUpstreamReader, clientDownPipeWriter, stream.Downstream)

		clientUpPipeWriter.Write(buffer[:msglen])
	}
}

func (proxy *ProxyUDP) clientWrite(clientAddr *net.UDPAddr, reader io.Reader) {
	for {
		buffer := make([]byte, UDPBufferSize)
		msglen, err := reader.Read(buffer)
		if err == io.EOF {
			return
		}
		if err != nil {
			panic(err)
		}
		_, err = proxy.listener.WriteToUDP(buffer[:msglen], clientAddr)
		if err != nil {
			panic(err)
		}
	}
}

func makeBufferedPipe(size int) (io.Reader, io.WriteCloser) {
	reader, writer := io.Pipe()
	bufferedWriter := NewBufferedWriter(writer, size)
	// It is impossible to use bufio.Writer here to make use of ReadFrom,
	// beacuse bufio.Writer writes data to the wrapped io.Writer only
	// when its internal buffer is full
	bufferedReader := bufio.NewReaderSize(reader, size)
	return bufferedReader, bufferedWriter
}

func NewBufferedWriter(w io.WriteCloser, size int) io.WriteCloser {
	return &WriteBufferCloser{
		wr:   w,
		size: size,
	}
}

// Implements ReaderFrom interface with custom sized buffer,
// but writes data after each Read call, opposed to bufio.Writer,
// which waits till buffer is full
type WriteBufferCloser struct {
	wr   io.WriteCloser
	size int
}

func (wb *WriteBufferCloser) Write(p []byte) (int, error) {
	return wb.wr.Write(p)
}

func (wb *WriteBufferCloser) ReadFrom(r io.Reader) (n int64, err error) {
	buffer := make([]byte, wb.size)
	for {
		nr, err := r.Read(buffer)
		if err != nil {
			break
		}
		if nr == 0 {
			continue
		}
		nw, err := wb.wr.Write(buffer[:nr])
		n += int64(nw)
		if err != nil {
			return n, err
		}
		if nw != nr {
			return n, fmt.Errorf("Failed to write all read buffer")
		}
	}
	if err == io.EOF {
		err = nil
	}
	return n, err
}

func (wb *WriteBufferCloser) Close() error {
	return wb.wr.Close()
}
