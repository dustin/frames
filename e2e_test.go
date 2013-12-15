package frames

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testService struct {
	addr     string
	l        net.Listener
	channels int32
	msgs     int32
}

func runTestServer(t *testing.T) *testService {
	t.Parallel()
	ta, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Error resolving test server addr: %v", err)
	}
	l, err := net.ListenTCP("tcp", ta)
	if err != nil {
		t.Fatalf("Error listening: %v", err)
	}

	ll, err := ListenerListener(l)
	if err != nil {
		t.Fatalf("Error listen listening: %v", err)
	}

	t.Logf("Listening on %v", l.Addr())

	rv := testService{addr: l.Addr().String(), l: l}

	workIt := func(c net.Conn) {
		atomic.AddInt32(&rv.channels, 1)
		b := bufio.NewReader(c)
		for {
			l, err := b.ReadString('\n')
			switch err {
			case nil:
				atomic.AddInt32(&rv.msgs, 1)
			case io.EOF:
				return
			default:
				t.Errorf("Error reading: %v", err)
				return
			}
			fmt.Fprintf(c, "Ack your %v", l)
		}

	}

	go func() {
		defer ll.Close()
		for {
			c, err := ll.Accept()
			if err != nil {
				return
			}
			go workIt(c)
		}
	}()

	return &rv
}

func TestEndToEnd(t *testing.T) {
	defer time.AfterFunc(time.Second*5, func() {
		panic("Taking too long")
	}).Stop()
	tc := runTestServer(t)
	defer tc.l.Close()

	c, err := net.Dial("tcp", tc.addr)
	if err != nil {
		t.Fatalf("Error connecting to my server: %v", err)
	}

	fc := NewClient(c)

	wg := sync.WaitGroup{}

	worker := func(n int, fc ChannelDialer) {
		defer wg.Done()

		c, err := fc.Dial()
		if err != nil {
			t.Fatalf("Error dialing channel: %v", err)
		}
		defer c.Close()

		t.Logf("Got %v", c)

		b := bufio.NewReader(c)
		for i := 0; i < 5; i++ {
			fmt.Fprintf(c, "%d: Hello #%d\n", n, i)
			l, err := b.ReadString('\n')
			if err != nil {
				t.Fatalf("Error reading %d: %v", n, err)
			}
			t.Logf("Read %v", l)
		}
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go worker(i, fc)
	}

	wg.Wait()
	err = fc.Close()
	if err != nil {
		t.Errorf("Expected no error closing, got %v", err)
	}

	if tc.channels != 5 {
		t.Fatalf("Expected 5 channels, only saw %v", tc.channels)
	}
	if tc.msgs != 25 {
		t.Fatalf("Expected 25 messages, only saw %v", tc.msgs)
	}
}

func TestChannelExhaustion(t *testing.T) {
	t.Parallel()
	fc := frameConnection{
		channels: map[uint16]*frameChannel{},
		egress:   make(chan *FramePacket),
		newConns: make(chan newconn),
	}

	errs := int32(0)
	pkterrs := int32(0)

	wg := sync.WaitGroup{}
	wg.Add(2)

	go func() {
		defer wg.Done()
		for r := range fc.egress {
			if r.Status != FrameSuccess {
				t.Logf("Packet error: %v", r.Status)
				atomic.AddInt32(&pkterrs, 1)
			}
		}
	}()

	go func() {
		defer wg.Done()
		for nc := range fc.newConns {
			if nc.e != nil {
				t.Logf("conn err: %v", nc.e)
				atomic.AddInt32(&errs, 1)
			}
		}
	}()

	p := &FramePacket{Cmd: FrameOpen}
	for i := 0; i <= 0xffff+2; i++ {
		fc.openChannel(p)
	}

	close(fc.egress)
	close(fc.newConns)
	wg.Wait()

	if errs != 2 {
		t.Fatalf("Expected two errors, got %v", errs)
	}
	if pkterrs != 2 {
		t.Fatalf("Expected two packet errors, got %v", pkterrs)
	}
}
