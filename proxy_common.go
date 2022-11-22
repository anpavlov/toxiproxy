package toxiproxy

import (
	"errors"
	"net"
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
type proxy interface {
	Proxy
	toggle(enable bool)
	setTomb(tomb *tomb.Tomb)
	getTomb() *tomb.Tomb
	server()
	// TODO naming?
	startedCh() chan error
	getConnections() *ConnectionList
}

type ConnectionList struct {
	list map[string]net.Conn
	lock sync.Mutex
}

func (c *ConnectionList) Lock() {
	c.lock.Lock()
}

func (c *ConnectionList) Unlock() {
	c.lock.Unlock()
}

var ErrProxyAlreadyStarted = errors.New("Proxy already started")
