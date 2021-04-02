package main

const (
	defaultConnectTimeout = 10000 // milliseconds
)

type Backend struct {
	Protocol       string `yaml:"protocol"`
	Address        string `yaml:"addr"`
	ConnectTimeout int    `yaml:"timeout"`
}
