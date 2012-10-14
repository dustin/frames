package framesweb

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"

	"github.com/dustin/frames"
)

// A RoundTripper over frames.
type FramesRoundTripper struct {
	Dialer frames.ChannelDialer
	err    error
}

type channelBodyCloser struct {
	rc io.ReadCloser
	c  io.Closer
}

func (c *channelBodyCloser) Read(b []byte) (int, error) {
	return c.rc.Read(b)
}

func (c *channelBodyCloser) Close() error {
	c.rc.Close()
	return c.c.Close()
}

func (f *FramesRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if f.err != nil {
		return nil, f.err
	}

	c, err := f.Dialer.Dial()
	if err != nil {
		f.err = err
		return nil, err
	}

	err = req.Write(c)
	if err != nil {
		f.err = err
		c.Close()
		return nil, err
	}

	b := bufio.NewReader(c)
	res, err := http.ReadResponse(b, req)
	if err == nil {
		res.Body = &channelBodyCloser{res.Body, c}
	} else {
		f.err = err
		c.Close()
	}
	return res, err
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
