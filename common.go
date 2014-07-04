package frames

import "io"

func channelRead(b []byte, current []byte, incoming chan []byte,
	close1, close2 chan bool) (int, []byte, error) {

	read := 0
	for len(b) > 0 {
		if current == nil || len(current) == 0 {
			var ok bool
			if read == 0 {
				select {
				case current, ok = <-incoming:
				case <-close1:
				case <-close2:
				}
			} else {
				select {
				case current, ok = <-incoming:
				case <-close1:
				case <-close2:
				default:
					return read, current, nil
				}
			}
			if !ok {
				return read, current, io.EOF
			}
		}
		copied := copy(b, current)
		read += copied
		current = current[copied:]
		b = b[copied:]
	}
	return read, current, nil
}

func channelWrite(b []byte, channel uint16, egress chan *FramePacket,
	close1, close2 chan bool) (int, error) {

	written := 0
	for len(b) > 0 {
		todo := b

		if len(todo) > maxWriteLen {
			todo = b[0:maxWriteLen]
		}
		b = b[len(todo):]

		bc := make([]byte, len(todo))
		copy(bc, todo)
		pkt := &FramePacket{
			Cmd:     FrameData,
			Channel: channel,
			Data:    bc,
			rch:     make(chan error, 1),
		}

		select {
		case egress <- pkt:
		case <-close1:
			return written, errClosedWriteCh
		case <-close2:
			return written, errClosedConn
		}

		// Flush it
		// TODO:  do all the writes and then harvest errors asynchronously
		select {
		case err := <-pkt.rch:
			if err != nil {
				return written, err
			}
			written += len(todo)
		case <-close1:
			return written, errClosedWriteCh
		case <-close2:
			return written, errClosedConn
		}
	}
	return written, nil
}
