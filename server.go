package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
)

type Server struct {
	*log.Logger
	*Configuration
}

func (s *Server) RunRedirect() {
	redirect := func (w http.ResponseWriter, req *http.Request) {
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

func (s *Server) Run() (err error) {
	if s.Configuration.EnableRedirect {
		s.RunRedirect()
	}

	l, err := net.Listen("tcp", s.Configuration.Port)
	if err != nil {
		return err
	}
	s.Printf("Serving connections on %v", l.Addr())

	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Print("Error accepting connection: ", err)
		}

		fmt.Print("closing secure connection")

		conn.Close()
	}

	return
}
