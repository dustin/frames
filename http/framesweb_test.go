package framesweb

import (
	"io"
	"log"
	"os"
)

func ExampleClient() {
	// Get a frames client with a persistent TCP connection.
	hc, err := NewFramesClient("tcp", "localhost:8423")
	if err != nil {
		log.Fatalf("Error creating frames client: %v", err)
	}

	// ----------------------------------------------------------------
	// Normal HTTP stuff
	res, err := hc.Get("http://whatever/something/")
	if err != nil {
		log.Fatalf("Error getting: %v", err)
	}

	_, err = io.Copy(os.Stdout, res.Body)
	if err != nil {
		log.Fatalf("Error copying stuff: %v", err)
	}
	res.Body.Close()
	// End normal http stuff
	// ----------------------------------------------------------------

	// Close the frames client to close the underlying connection.
	err = CloseFramesClient(hc)
	if err != nil {
		log.Fatalf("Error closing frames client: %v", err)
	}
}
