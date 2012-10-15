package frames

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"time"
)

type ChannelDialer interface {
	io.Closer
	Dial() (net.Conn, error)
	GetInfo() Info
}

type Info struct {
	BytesRead    uint64
	BytesWritten uint64
	ChannelsOpen int
}

func (i Info) String() string {
	return fmt.Sprintf("{FrameInfo Wrote: %v, Read: %v, Open: %v}",
		i.BytesWritten, i.BytesRead, i.ChannelsOpen)
}

type queueResult struct {
	conn net.Conn
	err  error
}

type frameClient struct {
	c           net.Conn
	channels    map[uint16]*clientChannel
	egress      chan *FramePacket
	closeMarker chan bool
	connqueue   chan chan queueResult
	info        Info
}

func (fc *frameClient) GetInfo() Info {
	rv := fc.info
	rv.ChannelsOpen = len(fc.channels)
	return rv
}

func (fc *frameClient) handleOpened(pkt *FramePacket) {
	var opening chan queueResult
	select {
	case opening = <-fc.connqueue:
	default:
		log.Panicf("Opening response, but nobody's opening")
		return
	}

	if pkt.Status != FrameSuccess {
		err := frameError(*pkt)
		opening <- queueResult{err: err}
		return
	}

	fc.channels[pkt.Channel] = &clientChannel{
		fc,
		pkt.Channel,
		make(chan []byte, 8192),
		nil,
		false,
	}
	opening <- queueResult{fc.channels[pkt.Channel], nil}
}

func (fc *frameClient) handleClosed(pkt *FramePacket) {
	log.Panicf("Closing channel on %v %v (unhandled)",
		fc.c.LocalAddr(), pkt.Channel)
}

func (fc *frameClient) handleData(pkt *FramePacket) {
	ch := fc.channels[pkt.Channel]
	if ch == nil || ch.closed {
		log.Printf("Data on closed channel on %v %v: %v",
			fc.c.LocalAddr(), ch, pkt)
		return
	}
	ch.incoming <- pkt.Data
}

func (fc *frameClient) readResponses() {
	defer fc.Close()
	for {
		hdr := make([]byte, minPktLen)
		r, err := io.ReadFull(fc.c, hdr)
		fc.info.BytesRead += uint64(r)
		if err != nil {
			log.Printf("Error reading pkt header from %v: %v",
				fc.c.RemoteAddr(), err)
			return
		}
		pkt := PacketFromHeader(hdr)
		r, err = io.ReadFull(fc.c, pkt.Data)
		fc.info.BytesRead += uint64(r)
		if err != nil {
			log.Printf("Error reading pkt body from %v: %v",
				fc.c.RemoteAddr(), err)
			return
		}

		switch pkt.Cmd {
		case FrameOpen:
			fc.handleOpened(&pkt)
		case FrameClose:
			fc.handleClosed(&pkt)
		case FrameData:
			fc.handleData(&pkt)
		default:
			panic("unhandled msg")
		}
	}
}

func (fc *frameClient) writeRequests() {
	for {
		var e *FramePacket
		select {
		case e = <-fc.egress:
		case <-fc.closeMarker:
			log.Printf("closed completing writer")
			return
		}
		written, err := fc.c.Write(e.Bytes())
		fc.info.BytesWritten += uint64(written)
		// Clean up on close
		if e.Cmd == FrameClose {
			delete(fc.channels, e.Channel)
		}
		if err != nil {
			log.Printf("write error: %v", err)
			fc.c.Close()
			return
		}
	}
}

// Convert a socket into a channel dialer.
func NewClient(c net.Conn) ChannelDialer {
	fc := &frameClient{
		c,
		map[uint16]*clientChannel{},
		make(chan *FramePacket, 256),
		make(chan bool),
		make(chan chan queueResult, 16),
		Info{},
	}

	go fc.readResponses()
	go fc.writeRequests()

	return fc
}

func (f *frameClient) Close() error {
	select {
	case <-f.closeMarker:
		return errors.New("already closed")
	default:
	}

	for _, fc := range f.channels {
		fc.terminate()
	}

	close(f.closeMarker)
	return f.c.Close()
}

func (f *frameClient) Dial() (net.Conn, error) {
	pkt := &FramePacket{Cmd: FrameOpen}

	ch := make(chan queueResult)

	select {
	case f.connqueue <- ch:
	case <-f.closeMarker:
		return nil, errors.New("closed client")
	}

	select {
	case f.egress <- pkt:
	case <-f.closeMarker:
		return nil, errors.New("Closed client")
	}

	qr := <-ch
	return qr.conn, qr.err
}

type clientChannel struct {
	fc       *frameClient
	channel  uint16
	incoming chan []byte
	current  []byte
	closed   bool
}

func (f *clientChannel) Read(b []byte) (n int, err error) {
	if f.closed {
		return 0, errors.New("Read on closed channel")
	}
	read := 0
	for len(b) > 0 {
		if f.current == nil || len(f.current) == 0 {
			var ok bool
			if read == 0 {
				f.current, ok = <-f.incoming
			} else {
				select {
				case f.current, ok = <-f.incoming:
				default:
					return read, nil
				}
			}
			if !ok {
				return read, io.EOF
			}
		}
		copied := copy(b, f.current)
		read += copied
		f.current = f.current[copied:]
		b = b[copied:]
	}
	return read, nil
}

func (f *clientChannel) Write(b []byte) (n int, err error) {
	bc := make([]byte, len(b))
	copy(bc, b)
	pkt := &FramePacket{
		Cmd:     FrameData,
		Channel: f.channel,
		Data:    bc,
	}

	select {
	case f.fc.egress <- pkt:
	case <-f.fc.closeMarker:
		return 0, errors.New("Write on closed channel")
	}
	return len(b), nil
}

func (f *clientChannel) Close() error {
	select {
	case f.fc.egress <- &FramePacket{
		Cmd:     FrameClose,
		Channel: f.channel,
	}:
	default:
	}
	f.terminate()
	return nil
}

func (f *clientChannel) terminate() {
	if !f.closed {
		i := f.incoming
		f.incoming = nil
		close(i)
		f.closed = true
	}
}

type frameAddr struct {
	a  net.Addr
	ch uint16
}

func (f frameAddr) Network() string {
	return f.a.String()
}

func (f frameAddr) String() string {
	return fmt.Sprintf("%v#%v", f.Network(), f.ch)
}

func (f *clientChannel) LocalAddr() net.Addr {
	return frameAddr{f.fc.c.LocalAddr(), f.channel}
}

func (f *clientChannel) RemoteAddr() net.Addr {
	return frameAddr{f.fc.c.RemoteAddr(), f.channel}
}

func (f *clientChannel) SetDeadline(t time.Time) error {
	return errors.New("Not Implemented")
}

func (f *clientChannel) SetReadDeadline(t time.Time) error {
	return errors.New("Not Implemented")
}

func (f *clientChannel) SetWriteDeadline(t time.Time) error {
	return errors.New("Not Implemented")
}

func (f *clientChannel) String() string {
	info := ""
	if f.closed {
		info = " CLOSED"
	}
	return fmt.Sprintf("ClientChannel{%v -> %v #%v%v}",
		f.fc.c.LocalAddr(), f.fc.c.RemoteAddr(), f.channel, info)
}
