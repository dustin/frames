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
}

type queueResult struct {
	conn net.Conn
	err  error
}

type frameClient struct {
	c         net.Conn
	channels  map[uint16]*clientChannel
	egress    chan *FramePacket
	connqueue chan chan queueResult
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
		_, err := io.ReadFull(fc.c, hdr)
		if err != nil {
			log.Printf("Error reading pkt header from %v: %v",
				fc.c.RemoteAddr(), err)
			return
		}
		pkt := PacketFromHeader(hdr)
		_, err = io.ReadFull(fc.c, pkt.Data)
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
		e, ok := <-fc.egress
		if !ok {
			log.Printf("egress closed, breaking")
			return
		}
		_, err := fc.c.Write(e.Bytes())
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
		make(chan chan queueResult, 16),
	}

	go fc.readResponses()
	go fc.writeRequests()

	return fc
}

func (f *frameClient) Close() error {
	for _, fc := range f.channels {
		fc.terminate()
	}

	c := f.connqueue
	f.connqueue = nil
	if c != nil {
		close(c)
	}

	e := f.egress
	f.egress = nil
	if e != nil {
		close(e)
	}

	return f.c.Close()
}

func (f *frameClient) Dial() (net.Conn, error) {
	pkt := &FramePacket{Cmd: FrameOpen}

	ch := make(chan queueResult)

	select {
	case f.connqueue <- ch:
	default:
		return nil, errors.New("closed client")
	}

	select {
	case f.egress <- pkt:
	default:
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
	default:
		return 0, errors.New("Write on closed channel")
	}
	return len(b), nil
}

func (f *clientChannel) Close() error {
	f.fc.egress <- &FramePacket{
		Cmd:     FrameClose,
		Channel: f.channel,
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
