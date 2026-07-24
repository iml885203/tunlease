package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
)

func main() {
	listen := flag.String("listen", ":8081", "listen address")
	label := flag.String("label", "app", "response label")
	flag.Parse()
	log.Fatal(http.ListenAndServe(*listen, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tunlease-Upstream", *label)
		_, _ = fmt.Fprintf(w, "%s:%s", *label, r.URL.Path)
	})))
}
