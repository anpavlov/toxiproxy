package toxiproxy

import (
	"errors"
	"io"
	"sync"

	"github.com/rs/zerolog"
	tomb "gopkg.in/tomb.v1"
)

type ProxyConfig struct {
	Name     string `json:"name"`
	Listen   string `json:"listen"`
	Upstream string `json:"upstream"`
	Enabled  bool   `json:"enabled"`
}

// Public interface common to TCP and UDP proxies
type Proxy interface {
	Name() string
	Listen() string
	Upstream() string
	Enabled() bool
	Toxics() *ToxicCollection
	Logger() *zerolog.Logger
	Config() ProxyConfig

	Start() error
	Stop()
	Update(config ProxyConfig) error
	RemoveConnection(name string)
}

// Private interface common to TCP and UDP proxies
type proxyInternal interface {
	Proxy
	toggle(enable bool)
	setTomb(tomb *tomb.Tomb)
	getTomb() *tomb.Tomb
	server()
	startedCh() chan error
	getConnections() *ConnectionList
}

type ConnectionList struct {
	list map[string]io.Closer
	lock sync.Mutex
}

func (c *ConnectionList) Lock() {
	c.lock.Lock()
}

func (c *ConnectionList) Unlock() {
	c.lock.Unlock()
}

var ErrProxyAlreadyStarted = errors.New("Proxy already started")

type proxyBase struct {
	sync.Mutex

	name     string
	listen   string
	upstream string
	enabled  bool

	started chan error

	tomb        *tomb.Tomb
	connections ConnectionList
	toxics      *ToxicCollection
	apiServer   *ApiServer
	logger      *zerolog.Logger
}

func (proxy *proxyBase) Name() string {
	return proxy.name
}

func (proxy *proxyBase) Listen() string {
	return proxy.listen
}

func (proxy *proxyBase) Upstream() string {
	return proxy.upstream
}

func (proxy *proxyBase) Enabled() bool {
	return proxy.enabled
}

func (proxy *proxyBase) Logger() *zerolog.Logger {
	return proxy.logger
}

func (proxy *proxyBase) Toxics() *ToxicCollection {
	return proxy.toxics
}

func (proxy *proxyBase) getTomb() *tomb.Tomb {
	return proxy.tomb
}

func (proxy *proxyBase) setTomb(tomb *tomb.Tomb) {
	proxy.tomb = tomb
}

func (proxy *proxyBase) getConnections() *ConnectionList {
	return &proxy.connections
}

func (proxy *proxyBase) startedCh() chan error {
	return proxy.started
}

func (proxy *proxyBase) toggle(enable bool) {
	proxy.enabled = enable
}

func (proxy *proxyBase) Config() ProxyConfig {
	return ProxyConfig{
		Enabled:  proxy.Enabled(),
		Name:     proxy.Name(),
		Listen:   proxy.Listen(),
		Upstream: proxy.Upstream(),
	}
}

func (base *proxyBase) Update(input ProxyConfig, proxy proxyInternal) error {
	base.Lock()
	defer base.Unlock()

	if input.Listen != base.listen || input.Upstream != base.upstream {
		stop(proxy)
		base.listen = input.Listen
		base.upstream = input.Upstream
	}

	if input.Enabled != base.enabled {
		if input.Enabled {
			return start(proxy)
		}
		stop(proxy)
	}
	return nil
}

func (proxy *proxyBase) RemoveConnection(name string) {
	proxy.connections.Lock()
	defer proxy.connections.Unlock()
	delete(proxy.connections.list, name)
}

// Starts a proxy, assumes the lock has already been taken.
func start(proxy proxyInternal) error {
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
func stop(proxy proxyInternal) {
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
