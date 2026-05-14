package main

import (
	"flag"
	"log"

	"speedrss/internal/backup"
	"speedrss/internal/server"
)

func main() {
	restorePath := flag.String("restore", "", "restore a SpeedRSS data backup zip before starting")
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	if *restorePath != "" {
		if err := backup.Restore(*restorePath, "data"); err != nil {
			log.Fatal(err)
		}
		log.Printf("restored %s into data", *restorePath)
		return
	}

	app, err := server.New("data/speedrss.db")
	if err != nil {
		log.Fatal(err)
	}
	defer app.Close()

	log.Printf("SpeedRSS running at http://localhost%s", *addr)
	log.Fatal(app.ListenAndServe(*addr))
}
