package main

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

var (
	normalize = strings.ToLower
	isClosed = func(err error) bool {
		netErr, ok := err.(net.Error)
		if ok {
			return netErr.Temporary()
		}
		return false
	}
)

type NotFound struct {
	error
}

type BadRequest struct {
	error
}

type Closed struct {
	error
}

type Conn interface {
	net.Conn
	Host() string
	Free()
}

type (
	muxFunc func(net.Conn) (Conn, error)

	muxError struct {
		err  error
		conn net.Conn
	}
)

type Listener struct {
	name   string
	mux    *Muxer
	accept chan Conn
}

func (l *Listener) Accept() (net.Conn, error){
	conn, ok := <- l.accept
	if !ok {
		return nil, fmt.Errorf("listener closed")
	}
	return conn, nil
}

func (l *Listener) Close() error {
	l.mux.del(l.name)
	close(l.accept)
	return nil
}

func (l *Listener) Addr() net.Addr {
	return l.mux.listener.Addr()
}

func (l *Listener) Name() string {
	return l.name
}

type Muxer struct {
	listener   net.Listener
	muxTimeout time.Duration
	hostFunc   muxFunc
	muxErrors  chan muxError
	registry   map[string]*Listener
	sync.RWMutex
}

type TLSMuxer struct {
	*Muxer
}

func (m *TLSMuxer) Listen(name string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(name)
	if err != nil {
		host = name
	}
	return m.Muxer.Listen(host)
}

func NewTLSMuxer(listener net.Listener, muxTimeout time.Duration) (*TLSMuxer, error){
	fn := func(c net.Conn) (Conn, error) { return TLS(c) }
	mux, err := NewMuxer(listener, fn, muxTimeout)
	return &TLSMuxer{mux}, err
}

func NewMuxer(listener net.Listener, hostFunc muxFunc, muxTimeout time.Duration) (*Muxer, error) {
	mux := &Muxer{
		listener:   listener,
		muxTimeout: muxTimeout,
		hostFunc:   hostFunc,
		muxErrors:  make(chan muxError),
		registry:   make(map[string]*Listener),
	}

	go mux.run()
	return mux, nil
}

func (m *Muxer) NextError() (net.Conn, error){
	muxError := <- m.muxErrors
	return muxError.conn, muxError.err
}

func (m *Muxer) sendError(conn net.Conn, err error){
	m.muxErrors <- muxError{conn: conn, err: err}
}

func (m *Muxer) set(name string, l *Listener) error {
	m.Lock()
	defer m.Unlock()
	if _, exists := m.registry[name]; exists {
		return fmt.Errorf("name %s is already bound", name)
	}
	m.registry[name] = l
	return nil
}

func (m *Muxer) get(name string) (l *Listener, ok bool){
	m.RLock()
	defer m.RUnlock()
	l, ok = m.registry[name]
	if !ok {
		parts := strings.Split(name, ".")
		for i := 0; i < len(parts)-1; i++ {
			parts[i] = "*"
			name = strings.Join(parts[i:], ".")
			l, ok = m.registry[name]
			if ok {
				break
			}
		}
	}
	return
}

func (m *Muxer) del(name string) {
	m.Lock()
	defer m.Unlock()
	delete(m.registry, name)
}

func (m *Muxer) handle(conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			m.sendError(conn, fmt.Errorf("NameMux.handle failed with err %v", r))
		}
	}()

	if err := conn.SetDeadline(time.Now().Add(m.muxTimeout)); err != nil {
		m.sendError(conn, fmt.Errorf("failed to set deadline: %v", err))
		return
	}

	vconn, err := m.hostFunc(conn)
	if err != nil {
		m.sendError(conn, BadRequest{fmt.Errorf("failed to extra vhost name: %v", err)})
		return
	}

	host := normalize(vconn.Host())

	l, ok := m.get(host)
	if !ok  {
		m.sendError(vconn, NotFound{fmt.Errorf("host not found: %v", host)})
		return
	}

	if err = vconn.SetDeadline(time.Time{}); err != nil {
		m.sendError(vconn, fmt.Errorf("failed unset connection deadline: %v", err))
		return
	}

	l.accept <- vconn
}

func (m *Muxer) Listen(name string) (net.Listener, error){
	name = normalize(name)

	vhost := &Listener{
		name: name,
		mux: m,
		accept: make(chan Conn),
	}

	if err := m.set(name, vhost); err != nil {
		return nil, err
	}

	return vhost, nil
}

func (m *Muxer) run() {
	for {
		conn, err := m.listener.Accept()
		if err != nil {
			if isClosed(err) {
				m.sendError(nil, Closed{err})
				return
			} else {
				m.sendError(nil, err)
				continue
			}
		}
		go m.handle(conn)
	}
}
