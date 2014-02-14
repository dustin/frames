package frames

import (
	"encoding/binary"
	"fmt"
)

// FrameCmd is the type of command on a frames stream.
type FrameCmd uint8

const (
	// FrameOpen is a command to open a channel on a connection.
	FrameOpen = FrameCmd(iota)
	// FrameClose is a command to close a channel on a connection.
	FrameClose
	// FrameData is a command indicating the packet contains data.
	FrameData
)

// FrameStatus represents a command status.
type FrameStatus uint8

const (
	// FrameSuccess is the status indicating a successful command.
	FrameSuccess = FrameStatus(iota)
	// FrameError is the status indicating a failed command.
	FrameError
)

const minPktLen = 6

// Could do a full 16-bits, but a smaller value makes it easy to tell
// when we've gone out of bounds.
const maxWriteLen = 32768

// A FramePacket is a packet sent or received over a connection.
type FramePacket struct {
	// The command
	Cmd FrameCmd
	// Status int
	Status FrameStatus
	// Channel over which the command should be sent.
	Channel uint16
	// Extra data for the command.
	Data []byte
}

// Header:
// 2 bytes data length,
// 2 bytes channel,
// 1 byte command
// 1 bytes status
// [<length> bytes of data]

// Bytes converts this packet to its network representation.
func (fp FramePacket) Bytes() []byte {
	dlen := uint16(0)
	if fp.Data != nil {
		dlen = uint16(len(fp.Data))
	}
	rv := make([]byte, dlen+minPktLen)
	binary.BigEndian.PutUint16(rv, dlen)
	offset := 2
	binary.BigEndian.PutUint16(rv[offset:], fp.Channel)
	offset += 2
	rv[offset] = byte(fp.Cmd)
	offset++
	rv[offset] = byte(fp.Status)
	offset++
	if dlen > 0 {
		copy(rv[offset:], fp.Data)
	}
	return rv
}

func (fp FramePacket) String() string {
	return fmt.Sprintf("{FramePacket cmd=%v, status=%v, channel=%d, datalen=%d}",
		fp.Cmd, fp.Status, fp.Channel, len(fp.Data))
}

type frameError FramePacket

func (f frameError) Error() string {
	return fmt.Sprintf("status=%v, data=%s", f.Status, f.Data)
}

// PacketFromHeader constructs a packet from the given header.
func PacketFromHeader(hdr []byte) FramePacket {
	if len(hdr) < minPktLen {
		panic("Too short")
	}
	dlen := binary.BigEndian.Uint16(hdr)
	if dlen > maxWriteLen {
		panic("data length exceeds max data len")
	}
	return FramePacket{
		Cmd:     FrameCmd(hdr[4]),
		Channel: binary.BigEndian.Uint16(hdr[2:]),
		Data:    make([]byte, dlen),
	}
}

func (c FrameCmd) String() string {
	switch c {
	case FrameOpen:
		return "FrameOpen"
	case FrameClose:
		return "FrameClose"
	case FrameData:
		return "FrameData"
	}
	return fmt.Sprintf("{FrameCommand 0x%x}", int(c))
}

func (c FrameStatus) String() string {
	switch c {
	case FrameSuccess:
		return "Success"
	case FrameError:
		return "Error"
	}
	return fmt.Sprintf("{FrameStatus 0x%x}", int(c))
}
