package wire

import (
	"bytes"

	"github.com/For-ACGN/quic-go/internal/protocol"
	"github.com/For-ACGN/quic-go/quicvarint"
)

// A StopSendingFrame is a STOP_SENDING frame
type StopSendingFrame struct {
	StreamID  protocol.StreamID
	ErrorCode protocol.ApplicationErrorCode
}

// parseStopSendingFrame parses a STOP_SENDING frame
func parseStopSendingFrame(r *bytes.Reader, _ protocol.VersionNumber) (*StopSendingFrame, error) {
	if _, err := r.ReadByte(); err != nil {
		return nil, err
	}

	streamID, err := quicvarint.Read(r)
	if err != nil {
		return nil, err
	}
	errorCode, err := quicvarint.Read(r)
	if err != nil {
		return nil, err
	}

	return &StopSendingFrame{
		StreamID:  protocol.StreamID(streamID),
		ErrorCode: protocol.ApplicationErrorCode(errorCode),
	}, nil
}

// Length of a written frame
func (f *StopSendingFrame) Length(_ protocol.VersionNumber) protocol.ByteCount {
	return 1 + quicvarint.Len(uint64(f.StreamID)) + quicvarint.Len(uint64(f.ErrorCode))
}

func (f *StopSendingFrame) Write(b *bytes.Buffer, _ protocol.VersionNumber) error {
	b.WriteByte(0x5)
	quicvarint.Write(b, uint64(f.StreamID))
	quicvarint.Write(b, uint64(f.ErrorCode))
	return nil
}
