package framesweb

import (
	"bufio"
	"errors"
	"net"
	"net/http"

	"github.com/dustin/frames"
)

// A RoundTripper over frames.
type FramesRoundTripper struct {
	Dialer frames.ChannelDialer
}

func (f *FramesRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	c, err := f.Dialer.Dial()
	if err != nil {
		return nil, err
	}
	defer c.Close()

	err = req.Write(c)
	if err != nil {
		return nil, err
	}

	b := bufio.NewReader(c)
	return http.ReadResponse(b, req)
}

// Get an HTTP client that maintains a persistent frames connection.
func NewFramesClient(n, addr string) (*http.Client, error) {
	c, err := net.Dial(n, addr)
	if err != nil {
		return nil, err
	}

	frt := &FramesRoundTripper{
		Dialer: frames.NewClient(c),
	}

	hc := &http.Client{
		Transport: frt,
	}

	return hc, nil
}

// Close the frames client.
func CloseFramesClient(hc *http.Client) error {
	if frt, ok := hc.Transport.(*FramesRoundTripper); ok {
		return frt.Dialer.Close()
	}
	return errors.New("Not a frames client")
}
