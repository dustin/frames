package framesweb

import (
	"bufio"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/dustin/frames"
)

// A RoundTripper over frames.
type FramesRoundTripper struct {
	Dialer  frames.ChannelDialer
	Timeout time.Duration
	Logger  *log.Logger
	err     error
}

type channelBodyCloser struct {
	rc    io.ReadCloser
	c     io.Closer
	frt   *FramesRoundTripper
	req   *http.Request
	start time.Time
	t     *time.Timer
}

func (c *channelBodyCloser) Read(b []byte) (int, error) {
	return c.rc.Read(b)
}

func (c *channelBodyCloser) Close() error {
	if !c.t.Stop() {
		c.frt.Logger.Printf("framesweb: body of %v %v was closed after %v",
			c.req.Method, c.req.URL, time.Since(c.start))
	}
	c.rc.Close()
	return c.c.Close()
}

func (f *FramesRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if f.err != nil {
		return nil, f.err
	}

	start := time.Now()
	sendT := time.AfterFunc(f.Timeout, func() {
		f.Logger.Printf("framesweb: %v request for %v is taking longer than %v",
			req.Method, req.URL, f.Timeout)
	})

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

	if !sendT.Stop() {
		f.Logger.Printf("framesweb: completed %v request for %v in %v",
			req.Method, req.URL, time.Since(start))
	}

	start = time.Now()
	endT := time.AfterFunc(f.Timeout, func() {
		f.Logger.Printf("framesweb: response for %v %v is taking longer than %v",
			req.Method, req.URL, f.Timeout)
	})

	b := bufio.NewReader(c)
	res, err := http.ReadResponse(b, req)
	if err == nil {
		res.Body = &channelBodyCloser{
			res.Body,
			c,
			f,
			req,
			start,
			endT}
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
		Dialer:  frames.NewClient(c),
		Timeout: time.Hour,
		Logger:  log.New(os.Stdout, "", log.LstdFlags),
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
