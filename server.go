package main

import (
	"crypto/tls"
	"github.com/inconshreveable/go-vhost"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	muxTimeout = 10 * time.Second
)

type Server struct {
	*log.Logger
	*Configuration
	wait sync.WaitGroup

	mux   *TLSMuxer
	ready chan int
}

func (s *Server) Redirect() {
	redirect := func(w http.ResponseWriter, req *http.Request) {
		target := "https://" + req.Host + req.URL.Path
		if len(req.URL.RawQuery) > 0 {
			target += "?" + req.URL.RawQuery
		}
		log.Printf("redirect to: %s", target)
		http.Redirect(w, req, target,
			// see comments below and consider the codes 308, 302, or 301
			http.StatusTemporaryRedirect)
	}
	go http.ListenAndServe(":80", http.HandlerFunc(redirect))
	s.Println("Serving connections on [::]:80")
}

func (s *Server) frontend(name string, front *Frontend, l net.Listener) {
	defer s.wait.Done()

	front.strategy = &RoundRobin{backends: front.Backends}

	s.Printf("Handling connections for %v", name)
	for {
		conn, err := l.Accept()
		if err != nil {
			s.Printf("Failed to accept new connection for '%v': %v", conn.RemoteAddr(), err)
			if e, ok := err.(net.Error); ok {
				if e.Temporary() {
					continue
				}
			}
			return
		}
		s.Printf("Accepted new connection for %v from %v", name, conn.RemoteAddr())
		go s.proxy(conn, front)
	}
}

func (s *Server) proxy(conn net.Conn, front *Frontend) (err error) {
	if front.tlsConfig != nil {
		conn = tls.Server(conn, front.tlsConfig)
	}

	backend := front.strategy.NextBackend()

	upConn, err := net.DialTimeout("tcp", backend.Address, time.Duration(backend.ConnectTimeout)*time.Millisecond)
	if err != nil {
		s.Printf("Failed to dial backend connection %v %v: %v", backend.Protocol, backend.Address, err)
		conn.Close()
		return
	}

	s.Printf("Initiated new connection to backend: %v %v", upConn.LocalAddr(), upConn.RemoteAddr())

	s.joinConnections(conn, upConn)
	return
}

func (s *Server) joinConnections(c1 net.Conn, c2 net.Conn) {
	var wg sync.WaitGroup
	halfJoin := func(dst net.Conn, src net.Conn) {
		defer wg.Done()
		defer dst.Close()
		defer src.Close()
		n, err := io.Copy(dst, src)
		s.Printf("Copy from %v to %v failed after %d bytes with error %v", src.RemoteAddr(), dst.RemoteAddr(), n, err)
	}

	s.Printf("Joining connections: %v %v", c1.RemoteAddr(), c2.RemoteAddr())
	wg.Add(2)
	go halfJoin(c1, c2)
	go halfJoin(c2, c1)
	wg.Wait()
}

func (s *Server) Run() (err error) {
	if s.Configuration.Redirect {
		s.Redirect()
	}

	l, err := net.Listen(s.Configuration.Protocol, s.Configuration.Port)
	if err != nil {
		return err
	}
	s.Printf("Serving connections on %v", l.Addr())

	s.mux, err = NewTLSMuxer(l, muxTimeout)
	if err != nil {
		return err
	}

	s.wait.Add(len(s.Frontends))

	for name, front := range s.Frontends {
		fl, err := s.mux.Listen(name)
		if err != nil {
			return err
		}
		go s.frontend(name, front, fl)
	}

	go func() {
		for {
			conn, err := s.mux.NextError()

			if conn == nil {
				s.Printf("failed to mux next connection, error: %v", err)
				if _, ok := err.(vhost.Closed); ok {
					return
				} else {
					continue
				}
			} else {
				if _, ok := err.(vhost.NotFound); ok && s.defaultFrontend != nil {
					go s.proxy(conn, s.defaultFrontend)
				} else {
					s.Printf("failed to mux connection from %v, error: %v", conn.RemoteAddr(), err)
				}
			}
		}
	}()

	if s.ready != nil {
		close(s.ready)
	}

	s.wait.Wait()

	return nil
}
