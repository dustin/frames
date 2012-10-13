package frames

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"time"
)

// Error returned when we've run out of channels.
var ChannelsExhausted = errors.New("channels exhausted")

func (f *frameConnection) connHandler() {
	defer f.c.Close()
	go f.readLoop()
	f.writeLoop()
}

type newconn struct {
	c net.Conn
	e error
}

type frameConnection struct {
	c        net.Conn
	channels map[uint16]*frameChannel
	newConns chan newconn
	egress   chan *FramePacket
	lastChid uint16
}

func (f *frameConnection) nextId() (uint16, error) {
	f.lastChid++
	for i := 0; i < 0xffff; i++ {
		if _, taken := f.channels[f.lastChid]; !taken {
			return f.lastChid, nil
		}
		f.lastChid++
	}
	return 0, ChannelsExhausted
}

func (f *frameConnection) Accept() (net.Conn, error) {
	c, ok := <-f.newConns
	if !ok {
		return nil, io.EOF
	}
	return c.c, c.e
}

func (f *frameConnection) Close() error {
	for _, c := range f.channels {
		c.Close()
	}
	close(f.egress)
	close(f.newConns)
	return nil
}

func (f *frameConnection) Addr() net.Addr {
	return f.c.LocalAddr()
}

func (f *frameConnection) openChannel(pkt *FramePacket) {
	chid, err := f.nextId()
	response := &FramePacket{
		Cmd:     pkt.Cmd,
		Status:  FrameSuccess,
		Channel: chid,
	}
	nc := newconn{}
	if err == nil {
		f.channels[chid] = &frameChannel{
			conn:     f,
			channel:  chid,
			incoming: make(chan []byte, 1024),
			current:  nil,
		}
		nc.c = f.channels[chid]
	} else {
		response.Status = FrameError
		nc.e = err
	}
	f.egress <- response
	f.newConns <- nc
}

func (f *frameConnection) closeChannel(pkt *FramePacket) {
	ch := f.channels[pkt.Channel]
	if ch == nil {
		log.Printf("Closing a closed channel: %v", ch)
		return
	}
	ch.Close()
	delete(f.channels, pkt.Channel)
}

func (f *frameConnection) gotData(pkt *FramePacket) {
	ch := f.channels[pkt.Channel]
	if ch == nil {
		log.Panicf("Write to nonexistent channel: %v", pkt.Channel)
	}
	ch.incoming <- pkt.Data
}

func (f *frameConnection) readLoop() {
	defer f.c.Close()
	for {
		hdr := make([]byte, minPktLen)
		_, err := io.ReadFull(f.c, hdr)
		if err != nil {
			if err != io.EOF {
				log.Printf("Channel read error: %v", err)
			}
			f.Close()
			return
		}
		pkt := PacketFromHeader(hdr)
		_, err = io.ReadFull(f.c, pkt.Data)
		if err != nil {
			log.Printf("Channel read error: %v", err)
			f.Close()
			return
		}

		switch pkt.Cmd {
		case FrameOpen:
			f.openChannel(&pkt)
		case FrameClose:
			f.closeChannel(&pkt)
		case FrameData:
			f.gotData(&pkt)
		default:
			panic("unhandled msg")
		}
	}
}

func (f *frameConnection) writeLoop() {
	for {
		e, ok := <-f.egress
		if !ok {
			return
		}
		_, err := f.c.Write(e.Bytes())
		if err != nil {
			log.Printf("Error writing: %v", err)
			// Close the underlying writer and let
			// read clean up.
			f.c.Close()
		}
	}
}

// Listen for channeled connections across connections from the given
// listener.
func Listen(underlying net.Conn) (net.Listener, error) {
	fc := frameConnection{
		c:        underlying,
		channels: map[uint16]*frameChannel{},
		newConns: make(chan newconn),
		egress:   make(chan *FramePacket, 4096),
	}
	go fc.connHandler()
	return &fc, nil
}

type frameChannel struct {
	conn     *frameConnection
	channel  uint16
	incoming chan []byte
	current  []byte
}

func (f *frameChannel) Read(b []byte) (n int, err error) {
	if f.incoming == nil {
		return 0, io.EOF
	}
	read := 0
	for len(b) > 0 && f.incoming != nil {
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

func (f *frameChannel) Write(b []byte) (n int, err error) {
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
	f.conn.egress <- pkt
	return len(b), nil
}

func (f *frameChannel) Close() error {
	if f == nil || f.incoming == nil {
		return nil
	}
	close(f.incoming)
	f.incoming = nil
	return nil
}

func (f *frameChannel) LocalAddr() net.Addr {
	return frameAddr{f.conn.c.LocalAddr(), f.channel}
}

func (f *frameChannel) RemoteAddr() net.Addr {
	return frameAddr{f.conn.c.RemoteAddr(), f.channel}
}

func (f *frameChannel) SetDeadline(t time.Time) error {
	return errors.New("Not Implemented")
}

func (f *frameChannel) SetReadDeadline(t time.Time) error {
	return errors.New("Not Implemented")
}

func (f *frameChannel) SetWriteDeadline(t time.Time) error {
	return errors.New("Not Implemented")
}

func (f *frameChannel) String() string {
	return fmt.Sprintf("FrameChannel{%v -> %v #%v}",
		f.conn.c.LocalAddr(), f.conn.c.RemoteAddr(), f.channel)
}

type listenerListener struct {
	ch         chan net.Conn
	underlying net.Listener
	err        error
}

func (ll *listenerListener) Addr() net.Addr {
	return ll.underlying.Addr()
}

func (ll *listenerListener) Close() error {
	return ll.underlying.Close()
}

func (ll *listenerListener) Accept() (net.Conn, error) {
	c := <-ll.ch
	return c, ll.err
}

func (ll *listenerListener) listenListen(c net.Conn) {
	defer c.Close()

	l, err := Listen(c)
	if err != nil {
		log.Fatalf("Error listening on a channel: %v", err)
	}
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		ll.ch <- c
	}
}

func (ll *listenerListener) listen(l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			ll.err = err
			if ll.ch != nil {
				close(ll.ch)
				ll.ch = nil
				return
			}
		}
		go ll.listenListen(c)
	}
}

// Get a listener that listens on a listener and returns framed
// connections opened from connections opened by the inner listener.
func ListenerListener(l net.Listener) (net.Listener, error) {
	ll := &listenerListener{make(chan net.Conn), l, nil}

	go ll.listen(l)

	return ll, nil
}
