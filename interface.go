package main

type BackendStrategy interface {
	NextBackend() Backend
}
