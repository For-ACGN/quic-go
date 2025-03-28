package congestion

import (
	"time"

	"github.com/For-ACGN/quic-go/internal/protocol"
)

type CongestionEvent interface {
	OnCongestionEvent(priorInFlight protocol.ByteCount, eventTime time.Time, ackedPackets, lostPackets []*protocol.Packet)
}

// A SendAlgorithm performs congestion control
type SendAlgorithm interface {
	TimeUntilSend(bytesInFlight protocol.ByteCount) time.Time
	HasPacingBudget() bool
	OnPacketSent(sentTime time.Time, bytesInFlight protocol.ByteCount, packetNumber protocol.PacketNumber, bytes protocol.ByteCount, isRetransmittable bool)
	CanSend(bytesInFlight protocol.ByteCount) bool
	MaybeExitSlowStart()
	OnPacketAcked(number protocol.PacketNumber, ackedBytes protocol.ByteCount, priorInFlight protocol.ByteCount, eventTime time.Time)
	OnPacketLost(number protocol.PacketNumber, lostBytes protocol.ByteCount, priorInFlight protocol.ByteCount)
	OnRetransmissionTimeout(packetsRetransmitted bool)
}

// A SendAlgorithmWithDebugInfos is a SendAlgorithm that exposes some debug infos
type SendAlgorithmWithDebugInfos interface {
	SendAlgorithm
	InSlowStart() bool
	InRecovery() bool
	GetCongestionWindow() protocol.ByteCount
}
