package main

import (
	"fmt"
	"krisha_kz_bot/pkg/utils"
	"log"
	"net/http"
	"time"
)

const (
	DefaultReadTimout = 5 * time.Second
)

func main() {
	port := utils.ParseEnvOrPanic[string]("PORT")
	addr := fmt.Sprintf(":%s", port)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "ok")
	})

	server := &http.Server{
		Addr:              addr,
		ReadHeaderTimeout: DefaultReadTimout,
	}

	if err := server.ListenAndServe(); err != nil {
		log.Panicf("failed to start web, error %v", err)
	}
}
