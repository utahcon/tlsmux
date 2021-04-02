package main

import (
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"gopkg.in/yaml.v2"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"sync"
)

const (
	initVhostBufSize = 1024 // allocate 1 KB up front to try to avoid resizing
)

type loadTLSConfigFn func(certPath, keyPath string) (*tls.Config, error)

type sharedConn struct {
	sync.Mutex
	net.Conn
	vhostBuf *bytes.Buffer
}

func newSharedConn(conn net.Conn) (*sharedConn, io.Reader) {
	c := &sharedConn{
		Conn:     conn,
		vhostBuf: bytes.NewBuffer(make([]byte, 0, initVhostBufSize)),
	}

	return c, io.TeeReader(conn, c.vhostBuf)
}

type Options struct {
	configPath string
}

type Configuration struct {
	Redirect        bool                 `yaml:"redirect"`
	Protocol        string               `yaml:"protocol"`
	Port            string               `yaml:"port"`
	Frontends       map[string]*Frontend `yaml:"frontends"`
	defaultFrontend *Frontend
}

func parseOpts() (*Options, error) {

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [-config <config file>]\n\n", os.Args[0])
	}

	options := &Options{}

	flag.StringVar(&options.configPath, "config", "/etc/tlsmux", "tlsmux configuration path; default: /etc/tlsmux")
	flag.Parse()

	return options, nil
}

func parseConfiguration(configBuf []byte, loadTLS loadTLSConfigFn) (config *Configuration, err error) {
	config = new(Configuration)
	if err = yaml.Unmarshal(configBuf, &config); err != nil {
		err = fmt.Errorf("error parsing configuration file: %v", err)
		return
	}

	if config.Protocol == "" {
		fmt.Println("No protocol specified, falling back to default: tcp")
		config.Protocol = "tcp"
	}

	if config.Port == "" {
		fmt.Println("No port specified, falling back to default :443")
		config.Port = ":443"
	}

	if len(config.Frontends) == 0 {
		err = fmt.Errorf("you must specify at least one frontend")
		return
	}

	for name, front := range config.Frontends {
		if len(front.Backends) == 0 {
			err = fmt.Errorf("you must specify at least one backend for frontend '%v'", name)
			return
		}

		if front.Default {
			if config.defaultFrontend != nil {
				err = fmt.Errorf("only one frontend may be the default")
				return
			}
			config.defaultFrontend = front
		}

		for _, back := range front.Backends {
			if back.ConnectTimeout == 0 {
				back.ConnectTimeout = defaultConnectTimeout
			}

			if back.Protocol == "" {
				back.Protocol = "tcp"
			}

			if back.Address == "" {
				err = fmt.Errorf("you must specify an address for each backend on frontend '%v'", name)
				return
			}
		}

		if front.TLSCert != "" || front.TLSKey != "" {
			if front.tlsConfig, err = loadTLS(front.TLSCert, front.TLSKey); err != nil {
				err = fmt.Errorf("failed to load TLS configuration for frontend '%v': %v", name, err)
				return
			}
		}
	}

	return
}

func loadTLSConfig(certPath, keyPath string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
	}, nil
}

func main() {

	options, err := parseOpts()
	if err != nil {
		log.Fatalln("error reading options: ", err)
		os.Exit(1)
	}

	configBuf, err := ioutil.ReadFile(options.configPath)
	if err != nil {
		log.Fatalf("Failed to read configuration file %s: %v\n", options.configPath, err)
	}

	config, err := parseConfiguration(configBuf, loadTLSConfig)
	if err != nil {
		log.Fatalln(err)
		os.Exit(1)
	}

	s := &Server{
		Configuration: config,
		Logger:        log.New(os.Stdout, "tlsmux ", log.LstdFlags|log.Lshortfile),
	}

	err = s.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start tlsmux: %v", err)
		os.Exit(1)
	}
}
