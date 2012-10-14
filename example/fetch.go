package main

import (
	"io"
	"log"
	"net/url"
	"os"

	"github.com/dustin/frames/http"
)

func main() {
	u, err := url.Parse(os.Args[1])
	if err != nil {
		log.Printf("Error parsing url: %v", err)
	}
	c, err := framesweb.NewFramesClient("tcp", u.Host)
	if err != nil {
		log.Fatalf("Error getting frames conn: %v", err)
	}

	res, err := c.Get(u.String())
	if err != nil {
		log.Fatalf("Error getting: %v", err)
	}
	if res.StatusCode != 200 {
		log.Fatalf("HTTP error:  %v", res.Status)
	}
	n, err := io.Copy(os.Stdout, res.Body)
	if err != nil {
		log.Fatalf("Error getting body: %v", err)
	}
	log.Printf("Copied %v bytes", n)
	res.Body.Close()
}
