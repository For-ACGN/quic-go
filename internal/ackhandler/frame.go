package ackhandler

import "github.com/For-ACGN/quic-go/internal/wire"

type Frame struct {
	wire.Frame // nil if the frame has already been acknowledged in another packet
	OnLost     func(wire.Frame)
	OnAcked    func(wire.Frame)
}
