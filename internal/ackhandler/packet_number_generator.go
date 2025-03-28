package ackhandler

import (
	"crypto/rand"
	"encoding/binary"
	mrand "math/rand"

	"github.com/For-ACGN/quic-go/internal/protocol"
	"github.com/For-ACGN/quic-go/internal/utils"
)

type packetNumberGenerator interface {
	Peek() protocol.PacketNumber
	Pop() protocol.PacketNumber
}

type sequentialPacketNumberGenerator struct {
	next protocol.PacketNumber
}

var _ packetNumberGenerator = &sequentialPacketNumberGenerator{}

func newSequentialPacketNumberGenerator(initial protocol.PacketNumber) packetNumberGenerator {
	return &sequentialPacketNumberGenerator{next: initial}
}

func (p *sequentialPacketNumberGenerator) Peek() protocol.PacketNumber {
	return p.next
}

func (p *sequentialPacketNumberGenerator) Pop() protocol.PacketNumber {
	next := p.next
	p.next++
	return next
}

// The skippingPacketNumberGenerator generates the packet number for the next packet
// it randomly skips a packet number every averagePeriod packets (on average).
// It is guaranteed to never skip two consecutive packet numbers.
type skippingPacketNumberGenerator struct {
	rand      *mrand.Rand
	period    protocol.PacketNumber
	maxPeriod protocol.PacketNumber

	next       protocol.PacketNumber
	nextToSkip protocol.PacketNumber
}

var _ packetNumberGenerator = &skippingPacketNumberGenerator{}

func newSkippingPacketNumberGenerator(initial, initialPeriod, maxPeriod protocol.PacketNumber) packetNumberGenerator {
	b := make([]byte, 8)
	rand.Read(b) // it's not the end of the world if we don't get perfect random here
	g := &skippingPacketNumberGenerator{
		rand:      mrand.New(mrand.NewSource(int64(binary.LittleEndian.Uint64(b)))),
		next:      initial,
		period:    initialPeriod,
		maxPeriod: maxPeriod,
	}
	g.generateNewSkip()
	return g
}

func (p *skippingPacketNumberGenerator) Peek() protocol.PacketNumber {
	return p.next
}

func (p *skippingPacketNumberGenerator) Pop() protocol.PacketNumber {
	next := p.next
	p.next++ // generate a new packet number for the next packet
	if p.next == p.nextToSkip {
		p.next++
		p.generateNewSkip()
	}
	return next
}

func (p *skippingPacketNumberGenerator) generateNewSkip() {
	// make sure that there are never two consecutive packet numbers that are skipped
	p.nextToSkip = p.next + 2 + protocol.PacketNumber(p.rand.Int63n(int64(2*p.period)))
	p.period = utils.MinPacketNumber(2*p.period, p.maxPeriod)
}
