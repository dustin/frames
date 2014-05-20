package frames

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"time"
)

// ChannelDialer is the client interface from which one builds
// connections.
type ChannelDialer interface {
	io.Closer
	Dial() (net.Conn, error)
	GetInfo() Info
}

// Info provides basic state of a client.
type Info struct {
	BytesRead    uint64 `json:"read"`
	BytesWritten uint64 `json:"written"`
	ChannelsOpen int    `json:"channels"`
}

var (
	errClosedConn    = errors.New("closed connection")
	errClosedReadCh  = errors.New("read on closed channel")
	errClosedWriteCh = errors.New("write on closed channel")
	errNotImpl       = errors.New("not implemented")
)

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
	}

	if pkt.Status != FrameSuccess {
		err := frameError(*pkt)
		select {
		case opening <- queueResult{err: err}:
		case <-fc.closeMarker:
		}
		return
	}

	fc.channels[pkt.Channel] = &clientChannel{
		fc,
		pkt.Channel,
		make(chan []byte),
		nil,
		make(chan bool),
	}
	select {
	case opening <- queueResult{fc.channels[pkt.Channel], nil}:
	case <-fc.closeMarker:
	}
}

func (fc *frameClient) handleClosed(pkt *FramePacket) {
	log.Panicf("Closing channel on %v %v (unhandled)",
		fc.c.LocalAddr(), pkt.Channel)
}

func (fc *frameClient) handleData(pkt *FramePacket) {
	ch := fc.channels[pkt.Channel]
	if ch == nil {
		log.Printf("Data on non-existent channel on %v %v: %v",
			fc.c.LocalAddr(), ch, pkt)
		return
	}

	select {
	case ch.incoming <- pkt.Data:
	case <-ch.closeMarker:
		log.Printf("Data on closed channel on %v: %v: %v",
			fc.c.LocalAddr(), ch, pkt)
	}
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
			return
		}
		written, err := fc.c.Write(e.Bytes())
		e.rch <- err
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

// NewClient converts a socket into a channel dialer.
func NewClient(c net.Conn) ChannelDialer {
	fc := &frameClient{
		c,
		map[uint16]*clientChannel{},
		make(chan *FramePacket, 16),
		make(chan bool),
		make(chan chan queueResult, 16),
		Info{},
	}

	go fc.readResponses()
	go fc.writeRequests()

	return fc
}

func (fc *frameClient) Close() error {
	select {
	case <-fc.closeMarker:
		return nil // already closed
	default:
	}

	for _, c := range fc.channels {
		c.terminate()
	}

	close(fc.closeMarker)
	return fc.c.Close()
}

func (fc *frameClient) Dial() (net.Conn, error) {
	pkt := &FramePacket{Cmd: FrameOpen, rch: make(chan error, 1)}

	ch := make(chan queueResult)

	select {
	case fc.connqueue <- ch:
	case <-fc.closeMarker:
		return nil, errClosedConn
	}

	select {
	case fc.egress <- pkt:
	case <-fc.closeMarker:
		return nil, errClosedConn
	}

	select {
	case qr := <-ch:
		return qr.conn, qr.err
	case <-fc.closeMarker:
		return nil, io.EOF
	}
}

type clientChannel struct {
	fc          *frameClient
	channel     uint16
	incoming    chan []byte
	current     []byte
	closeMarker chan bool
}

func (f *clientChannel) isClosed() bool {
	select {
	case <-f.closeMarker:
		return true
	default:
	}
	return false
}

func (f *clientChannel) Read(b []byte) (n int, err error) {
	if f.isClosed() {
		return 0, errClosedReadCh
	}
	read := 0
	for len(b) > 0 {
		if f.current == nil || len(f.current) == 0 {
			var ok bool
			if read == 0 {
				select {
				case f.current, ok = <-f.incoming:
				case <-f.closeMarker:
				case <-f.fc.closeMarker:
				}
			} else {
				select {
				case f.current, ok = <-f.incoming:
				case <-f.closeMarker:
				case <-f.fc.closeMarker:
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
	if len(b) > maxWriteLen {
		b = b[0:maxWriteLen]
	}

	bc := make([]byte, len(b))
	copy(bc, b)
	pkt := &FramePacket{
		Cmd:     FrameData,
		Channel: f.channel,
		Data:    bc,
		rch:     make(chan error, 1),
	}

	select {
	case f.fc.egress <- pkt:
	case <-f.closeMarker:
		return 0, errClosedWriteCh
	case <-f.fc.closeMarker:
		return 0, errClosedConn
	}
	return len(b), <-pkt.rch
}

func (f *clientChannel) Close() error {
	select {
	case <-f.fc.closeMarker:
		// Socket's closed, we're done
	case <-f.closeMarker:
		// This channel is already closed.
	case f.fc.egress <- &FramePacket{
		Cmd:     FrameClose,
		Channel: f.channel,
		rch:     make(chan error, 1),
	}:
		// Send intent to close.
	}
	f.terminate()
	return nil
}

func (f *clientChannel) terminate() {
	if !f.isClosed() {
		close(f.closeMarker)
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
	return errNotImpl
}

func (f *clientChannel) SetReadDeadline(t time.Time) error {
	return errNotImpl
}

func (f *clientChannel) SetWriteDeadline(t time.Time) error {
	return errNotImpl
}

func (f *clientChannel) String() string {
	info := ""
	if f.isClosed() {
		info = " CLOSED"
	}
	return fmt.Sprintf("ClientChannel{%v -> %v #%v%v}",
		f.fc.c.LocalAddr(), f.fc.c.RemoteAddr(), f.channel, info)
}
