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
	}
	opening <- queueResult{fc.channels[pkt.Channel], nil}
}

func (fc *frameClient) handleClosed(pkt *FramePacket) {
	log.Panicf("Closing channel %v (unhandled)", pkt.Channel)
}

func (fc *frameClient) handleData(pkt *FramePacket) {
	ch := fc.channels[pkt.Channel]
	if ch == nil {
		log.Panicf("Data on closed channel: %v", pkt)
	}
	ch.incoming <- pkt.Data
}

func (fc *frameClient) readResponses() {
	defer fc.Close()
	for {
		hdr := make([]byte, minPktLen)
		_, err := io.ReadFull(fc.c, hdr)
		if err != nil {
			log.Printf("Error reading pkt header: %v", err)
			return
		}
		pkt := PacketFromHeader(hdr)
		_, err = io.ReadFull(fc.c, pkt.Data)
		if err != nil {
			log.Printf("Error reading pkt body: %v", err)
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
		e := <-fc.egress
		_, err := fc.c.Write(e.Bytes())
		if err != nil {
			panic(err)
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
	return f.c.Close()
}

func (f *frameClient) Dial() (net.Conn, error) {
	pkt := &FramePacket{Cmd: FrameOpen}

	ch := make(chan queueResult)

	f.connqueue <- ch
	f.egress <- pkt

	qr := <-ch
	return qr.conn, qr.err
}

type clientChannel struct {
	fc       *frameClient
	channel  uint16
	incoming chan []byte
	current  []byte
}

func (f *clientChannel) Read(b []byte) (n int, err error) {
	if f.incoming == nil {
		return 0, io.EOF
	}
	read := 0
	for len(b) > 0 {
		if f.current == nil || len(f.current) == 0 {
			if read == 0 {
				f.current = <-f.incoming
			} else {
				var ok bool
				select {
				case f.current, ok = <-f.incoming:
					if !ok {
						return read, io.EOF
					}
				default:
					return read, nil
				}
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
	if f.incoming == nil {
		return 0, io.EOF
	}

	bc := make([]byte, len(b))
	copy(bc, b)
	pkt := &FramePacket{
		Cmd:     FrameData,
		Channel: f.channel,
		Data:    bc,
	}
	f.fc.egress <- pkt
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
	close(f.incoming)
	f.incoming = nil
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
	return fmt.Sprintf("ClientChannel{%v -> %v #%v}",
		f.fc.c.LocalAddr(), f.fc.c.RemoteAddr(), f.channel)
}
