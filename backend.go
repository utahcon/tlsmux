package main

const (
	defaultConnectTimeout = 10000 // milliseconds
)

type Backend struct {
	Address        string `yaml:"address"`
	ConnectTimeout int    `yaml:"timeout"`
}
