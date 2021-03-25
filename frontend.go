package main

import (
	"crypto/tls"
)

type Frontend struct {
	Backends  []Backend
	Strategy  string
	TLSCert   string
	TLSKey    string
	Default   bool
	strategy  BackendStrategy
	tlsConfig *tls.Config
	mux       *Muxer
}