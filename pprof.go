package main

import (
	"log"
	"net/http"
	_ "net/http/pprof"
)

func enablePPROF(addr string) {
	go func() {
		log.Printf("[pprof] listening at http://%s/debug/pprof/", addr)
		_ = http.ListenAndServe(addr, nil)
	}()
}
