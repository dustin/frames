// Package frames is a simple multiplexing protocol.
package frames

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"time"
)

// ErrChannelsExhausted is returned when we've run out of channels.
var ErrChannelsExhausted = errors.New("channels exhausted")

type newconn struct {
	c net.Conn
	e error
}

type frameConnection struct {
	c           net.Conn
	channels    map[uint16]*frameChannel
	newConns    chan newconn
	egress      chan *FramePacket
	closeMarker chan bool
	lastChid    uint16
}

func (f *frameConnection) nextID() (uint16, error) {
	f.lastChid++
	for i := 0; i < 0xffff; i++ {
		if _, taken := f.channels[f.lastChid]; !taken {
			return f.lastChid, nil
		}
		f.lastChid++
	}
	return 0, ErrChannelsExhausted
}

func (f *frameConnection) Accept() (net.Conn, error) {
	select {
	case c, ok := <-f.newConns:
		if !ok {
			return nil, io.EOF
		}
		return c.c, c.e
	case <-f.closeMarker:
		return nil, io.EOF
	}
}

func (f *frameConnection) Close() error {
	select {
	case <-f.closeMarker:
		return nil // already closed
	default:
	}

	for _, c := range f.channels {
		c.Close()
	}
	close(f.closeMarker)
	return f.c.Close()
}

func (f *frameConnection) Addr() net.Addr {
	return f.c.LocalAddr()
}

func (f *frameConnection) openChannel(pkt *FramePacket) {
	chid, err := f.nextID()
	response := &FramePacket{
		Cmd:     pkt.Cmd,
		Status:  FrameSuccess,
		Channel: chid,
		rch:     make(chan error, 1),
	}
	nc := newconn{}
	if err == nil {
		f.channels[chid] = &frameChannel{
			conn:        f,
			channel:     chid,
			incoming:    make(chan []byte),
			current:     nil,
			closeMarker: make(chan bool),
		}
		nc.c = f.channels[chid]
	} else {
		response.Status = FrameError
		nc.e = err
	}
	select {
	case f.egress <- response:
	case <-f.closeMarker:
		nc.c = nil
		nc.e = errors.New("connection closed")
	}
	select {
	case f.newConns <- nc:
	case <-f.closeMarker:
	}
}

func (f *frameConnection) closeChannel(pkt *FramePacket) {
	ch := f.channels[pkt.Channel]
	if ch == nil {
		log.Printf("Closing a closed channel: %v", pkt)
		return
	}
	ch.Close()
	delete(f.channels, pkt.Channel)
}

func (f *frameConnection) gotData(pkt *FramePacket) {
	ch := f.channels[pkt.Channel]
	if ch == nil {
		log.Printf("Write to nonexistent channel on %v %v",
			f.c.RemoteAddr(), pkt)
		return
	}
	select {
	case ch.incoming <- pkt.Data:
	case <-ch.closeMarker:
	}
}

func (f *frameConnection) readLoop() {
	defer f.Close()
	for {
		hdr := make([]byte, minPktLen)
		_, err := io.ReadFull(f.c, hdr)
		if err != nil {
			if err != io.EOF {
				log.Printf("Channel header read error: %v", err)
			}
			return
		}
		pkt := PacketFromHeader(hdr)
		_, err = io.ReadFull(f.c, pkt.Data)
		if err != nil {
			log.Printf("Channel data read error: %v", err)
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
	// Only close the underlying connection on return.  The read
	// loop does the rest of the cleanup.
	defer f.c.Close()
	for {
		var e *FramePacket
		select {
		case e = <-f.egress:
		case <-f.closeMarker:
			return
		}
		_, err := f.c.Write(e.Bytes())
		e.rch <- err
		if err != nil {
			log.Printf("Error writing to %v: %v",
				f.c.RemoteAddr(), err)
			return
		}
	}
}

// Listen for channeled connections across connections from the given
// listener.
func Listen(underlying net.Conn) (net.Listener, error) {
	fc := frameConnection{
		c:           underlying,
		channels:    map[uint16]*frameChannel{},
		newConns:    make(chan newconn),
		egress:      make(chan *FramePacket),
		closeMarker: make(chan bool),
	}
	go fc.readLoop()
	go fc.writeLoop()
	return &fc, nil
}

type frameChannel struct {
	conn        *frameConnection
	channel     uint16
	incoming    chan []byte
	current     []byte
	closeMarker chan bool
}

func (f *frameChannel) Read(b []byte) (n int, err error) {
	if f.isClosed() {
		return 0, errors.New("read on a closed channel")
	}
	read := 0
	for len(b) > 0 {
		if f.current == nil || len(f.current) == 0 {
			var ok bool
			if read == 0 {
				select {
				case f.current, ok = <-f.incoming:
				case <-f.closeMarker:
				case <-f.conn.closeMarker:
				}
			} else {
				select {
				case f.current, ok = <-f.incoming:
				case <-f.closeMarker:
				case <-f.conn.closeMarker:
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

func (f *frameChannel) Write(b []byte) (n int, err error) {
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
	case f.conn.egress <- pkt:
	case <-f.conn.closeMarker:
		return 0, errors.New("write on closed channel")
	}
	return len(b), <-pkt.rch
}

func (f *frameChannel) isClosed() bool {
	select {
	case <-f.closeMarker:
		return true
	default:
	}
	return false
}

func (f *frameChannel) Close() error {
	if f == nil || f.isClosed() {
		return nil
	}

	close(f.closeMarker)

	return nil
}

func (f *frameChannel) LocalAddr() net.Addr {
	return frameAddr{f.conn.c.LocalAddr(), f.channel}
}

func (f *frameChannel) RemoteAddr() net.Addr {
	return frameAddr{f.conn.c.RemoteAddr(), f.channel}
}

func (f *frameChannel) SetDeadline(t time.Time) error {
	return errors.New("not Implemented")
}

func (f *frameChannel) SetReadDeadline(t time.Time) error {
	return errors.New("not Implemented")
}

func (f *frameChannel) SetWriteDeadline(t time.Time) error {
	return errors.New("not Implemented")
}

func (f *frameChannel) String() string {
	return fmt.Sprintf("FrameChannel{%v -> %v #%v}",
		f.conn.c.LocalAddr(), f.conn.c.RemoteAddr(), f.channel)
}

type listenerListener struct {
	ch          chan net.Conn
	underlying  net.Listener
	closeMarker chan bool
	err         error
}

func (ll *listenerListener) Addr() net.Addr {
	return ll.underlying.Addr()
}

func (ll *listenerListener) closed() bool {
	select {
	case <-ll.closeMarker:
		return true
	default:
	}
	return false
}

func (ll *listenerListener) Close() error {
	if !ll.closed() {
		close(ll.closeMarker)
	}
	return ll.underlying.Close()
}

func (ll *listenerListener) Accept() (net.Conn, error) {
	select {
	case c := <-ll.ch:
		return c, ll.err
	case <-ll.closeMarker:
		return nil, io.EOF
	}
}

func (ll *listenerListener) listenListen(c net.Conn) error {
	defer c.Close()

	l, err := Listen(c)
	if err != nil {
		return err
	}
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		select {
		case ll.ch <- c:
		case <-ll.closeMarker:
			c.Close()
			return io.EOF
		}

	}
}

func (ll *listenerListener) listen(l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			ll.Close()
			ll.err = err
			return
		}
		go ll.listenListen(c)
	}
}

// ListenerListener is a listener that listens on a net.Listener and
// returns framed connections opened from connections opened by the
// underlying Listener.
func ListenerListener(l net.Listener) (net.Listener, error) {
	ll := &listenerListener{
		make(chan net.Conn),
		l,
		make(chan bool),
		nil}

	go ll.listen(l)

	return ll, nil
}
