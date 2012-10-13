package frames

import (
	"encoding/binary"
	"fmt"
)

// Type of command.
type FrameCmd uint8

const (
	FrameOpen = FrameCmd(iota)
	FrameClose
	FrameData
)

// Command Status
type FrameStatus uint8

const (
	FrameSuccess = FrameStatus(iota)
	FrameError
)

const minPktLen = 6

// A packet sent or received over a connection.
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
// 2 bytes seq,
// 1 byte command, data

// Convert this packet to its network representation.
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

// Construct a packet from the given header.
func PacketFromHeader(hdr []byte) FramePacket {
	if len(hdr) < minPktLen {
		panic("Too short")
	}
	return FramePacket{
		Cmd:     FrameCmd(hdr[4]),
		Channel: binary.BigEndian.Uint16(hdr[2:]),
		Data:    make([]byte, binary.BigEndian.Uint16(hdr)),
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
