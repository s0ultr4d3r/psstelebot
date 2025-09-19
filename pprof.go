package main

import (
	"log"
	"net/http"
	_ "net/http/pprof"
)

func enablePPROF(addr string) {
	go func() {
		log.Printf("pprof: http://%s/debug/pprof/", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Printf("pprof error: %v", err)
		}
	}()
}
