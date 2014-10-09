package frames

import (
	"reflect"
	"testing"
)

func TestPktEncoding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pkt FramePacket
		exp []byte
	}{
		{FramePacket{Cmd: FrameOpen},
			[]byte{0, 0, 0, 0, 0, 0}},
		{FramePacket{Cmd: FrameClose, Channel: 923},
			[]byte{0, 0, 3, 0x9b, 1, 0}},
		{FramePacket{Cmd: FrameOpen, Status: FrameError, Channel: 13},
			[]byte{0, 0, 0, 13, 0, 1}},
		{FramePacket{Cmd: FrameData, Channel: 11, Data: []byte("hi")},
			[]byte{0, 2, 0, 11, 2, 0, 'h', 'i'}},
	}

	for _, test := range tests {
		got := test.pkt.Bytes()
		if !reflect.DeepEqual(got, test.exp) {
			t.Errorf("Error encoding %v\nExpected:\n%#v\nGot:\n%#v",
				test.pkt, test.exp, got)
		}
		t.Logf("Packet: %v", test.pkt)
	}
}

func TestErrorStringing(t *testing.T) {
	e := frameError{Status: FrameError, Data: []byte("broken")}
	got := e.Error()
	want := `status=Error, data=broken`
	if got != want {
		t.Errorf("Wanted %v, got %v", want, got)
	}

	e = frameError{Status: 11, Data: []byte("broken and unknown")}
	got = e.Error()
	want = `status={FrameStatus 0xb}, data=broken and unknown`
	if got != want {
		t.Errorf("Wanted %v, got %v", want, got)
	}
}

func TestBadFrameString(t *testing.T) {
	c := FrameCmd(42)
	got := c.String()
	want := "{FrameCommand 0x2a}"
	if want != got {
		t.Errorf("Wanted %q, got %q", want, got)
	}
}

func benchEncoding(b *testing.B, size int) {
	pkt := FramePacket{
		Cmd:     FrameData,
		Channel: 8184,
		Data:    make([]byte, size),
	}

	b.SetBytes(int64(len(pkt.Bytes())))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pkt.Bytes()
	}
}

func BenchmarkEncoding0(b *testing.B) {
	benchEncoding(b, 0)
}

func BenchmarkEncoding8(b *testing.B) {
	benchEncoding(b, 8)
}

func BenchmarkEncoding16(b *testing.B) {
	benchEncoding(b, 16)
}

func BenchmarkEncoding64(b *testing.B) {
	benchEncoding(b, 64)
}

func BenchmarkEncoding256(b *testing.B) {
	benchEncoding(b, 256)
}

func BenchmarkEncoding1024(b *testing.B) {
	benchEncoding(b, 1024)
}

func BenchmarkEncoding8192(b *testing.B) {
	benchEncoding(b, 8192)
}
