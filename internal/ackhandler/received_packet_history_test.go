package ackhandler

import (
	"github.com/For-ACGN/quic-go/internal/protocol"
	"github.com/For-ACGN/quic-go/internal/utils"
	"github.com/For-ACGN/quic-go/internal/wire"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("receivedPacketHistory", func() {
	var hist *receivedPacketHistory

	BeforeEach(func() {
		hist = newReceivedPacketHistory()
	})

	Context("ranges", func() {
		It("adds the first packet", func() {
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ranges.Len()).To(Equal(1))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 4, End: 4}))
		})

		It("doesn't care about duplicate packets", func() {
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ReceivedPacket(4)).To(BeFalse())
			Expect(hist.ranges.Len()).To(Equal(1))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 4, End: 4}))
		})

		It("adds a few consecutive packets", func() {
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ReceivedPacket(5)).To(BeTrue())
			Expect(hist.ReceivedPacket(6)).To(BeTrue())
			Expect(hist.ranges.Len()).To(Equal(1))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 4, End: 6}))
		})

		It("doesn't care about a duplicate packet contained in an existing range", func() {
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ReceivedPacket(5)).To(BeTrue())
			Expect(hist.ReceivedPacket(6)).To(BeTrue())
			Expect(hist.ReceivedPacket(5)).To(BeFalse())
			Expect(hist.ranges.Len()).To(Equal(1))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 4, End: 6}))
		})

		It("extends a range at the front", func() {
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ReceivedPacket(3)).To(BeTrue())
			Expect(hist.ranges.Len()).To(Equal(1))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 3, End: 4}))
		})

		It("creates a new range when a packet is lost", func() {
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ReceivedPacket(6)).To(BeTrue())
			Expect(hist.ranges.Len()).To(Equal(2))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 4, End: 4}))
			Expect(hist.ranges.Back().Value).To(Equal(utils.PacketInterval{Start: 6, End: 6}))
		})

		It("creates a new range in between two ranges", func() {
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ReceivedPacket(10)).To(BeTrue())
			Expect(hist.ranges.Len()).To(Equal(2))
			Expect(hist.ReceivedPacket(7)).To(BeTrue())
			Expect(hist.ranges.Len()).To(Equal(3))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 4, End: 4}))
			Expect(hist.ranges.Front().Next().Value).To(Equal(utils.PacketInterval{Start: 7, End: 7}))
			Expect(hist.ranges.Back().Value).To(Equal(utils.PacketInterval{Start: 10, End: 10}))
		})

		It("creates a new range before an existing range for a belated packet", func() {
			Expect(hist.ReceivedPacket(6)).To(BeTrue())
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ranges.Len()).To(Equal(2))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 4, End: 4}))
			Expect(hist.ranges.Back().Value).To(Equal(utils.PacketInterval{Start: 6, End: 6}))
		})

		It("extends a previous range at the end", func() {
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ReceivedPacket(7)).To(BeTrue())
			Expect(hist.ReceivedPacket(5)).To(BeTrue())
			Expect(hist.ranges.Len()).To(Equal(2))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 4, End: 5}))
			Expect(hist.ranges.Back().Value).To(Equal(utils.PacketInterval{Start: 7, End: 7}))
		})

		It("extends a range at the front", func() {
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ReceivedPacket(7)).To(BeTrue())
			Expect(hist.ReceivedPacket(6)).To(BeTrue())
			Expect(hist.ranges.Len()).To(Equal(2))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 4, End: 4}))
			Expect(hist.ranges.Back().Value).To(Equal(utils.PacketInterval{Start: 6, End: 7}))
		})

		It("closes a range", func() {
			Expect(hist.ReceivedPacket(6)).To(BeTrue())
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ranges.Len()).To(Equal(2))
			Expect(hist.ReceivedPacket(5)).To(BeTrue())
			Expect(hist.ranges.Len()).To(Equal(1))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 4, End: 6}))
		})

		It("closes a range in the middle", func() {
			Expect(hist.ReceivedPacket(1)).To(BeTrue())
			Expect(hist.ReceivedPacket(10)).To(BeTrue())
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ReceivedPacket(6)).To(BeTrue())
			Expect(hist.ranges.Len()).To(Equal(4))
			Expect(hist.ReceivedPacket(5)).To(BeTrue())
			Expect(hist.ranges.Len()).To(Equal(3))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 1, End: 1}))
			Expect(hist.ranges.Front().Next().Value).To(Equal(utils.PacketInterval{Start: 4, End: 6}))
			Expect(hist.ranges.Back().Value).To(Equal(utils.PacketInterval{Start: 10, End: 10}))
		})
	})

	Context("deleting", func() {
		It("does nothing when the history is empty", func() {
			hist.DeleteBelow(5)
			Expect(hist.ranges.Len()).To(BeZero())
		})

		It("deletes a range", func() {
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ReceivedPacket(5)).To(BeTrue())
			Expect(hist.ReceivedPacket(10)).To(BeTrue())
			hist.DeleteBelow(6)
			Expect(hist.ranges.Len()).To(Equal(1))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 10, End: 10}))
		})

		It("deletes multiple ranges", func() {
			Expect(hist.ReceivedPacket(1)).To(BeTrue())
			Expect(hist.ReceivedPacket(5)).To(BeTrue())
			Expect(hist.ReceivedPacket(10)).To(BeTrue())
			hist.DeleteBelow(8)
			Expect(hist.ranges.Len()).To(Equal(1))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 10, End: 10}))
		})

		It("adjusts a range, if packets are delete from an existing range", func() {
			Expect(hist.ReceivedPacket(3)).To(BeTrue())
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ReceivedPacket(5)).To(BeTrue())
			Expect(hist.ReceivedPacket(6)).To(BeTrue())
			Expect(hist.ReceivedPacket(7)).To(BeTrue())
			hist.DeleteBelow(5)
			Expect(hist.ranges.Len()).To(Equal(1))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 5, End: 7}))
		})

		It("adjusts a range, if only one packet remains in the range", func() {
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ReceivedPacket(5)).To(BeTrue())
			Expect(hist.ReceivedPacket(10)).To(BeTrue())
			hist.DeleteBelow(5)
			Expect(hist.ranges.Len()).To(Equal(2))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 5, End: 5}))
			Expect(hist.ranges.Back().Value).To(Equal(utils.PacketInterval{Start: 10, End: 10}))
		})

		It("keeps a one-packet range, if deleting up to the packet directly below", func() {
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			hist.DeleteBelow(4)
			Expect(hist.ranges.Len()).To(Equal(1))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 4, End: 4}))
		})

		It("doesn't add delayed packets below deleted ranges", func() {
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ReceivedPacket(5)).To(BeTrue())
			Expect(hist.ReceivedPacket(6)).To(BeTrue())
			hist.DeleteBelow(5)
			Expect(hist.ranges.Len()).To(Equal(1))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 5, End: 6}))
			Expect(hist.ReceivedPacket(2)).To(BeFalse())
			Expect(hist.ranges.Len()).To(Equal(1))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 5, End: 6}))
		})

		It("doesn't create more than MaxNumAckRanges ranges", func() {
			for i := protocol.PacketNumber(0); i < protocol.MaxNumAckRanges; i++ {
				Expect(hist.ReceivedPacket(2 * i)).To(BeTrue())
			}
			Expect(hist.ranges.Len()).To(Equal(protocol.MaxNumAckRanges))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 0, End: 0}))
			hist.ReceivedPacket(2*protocol.MaxNumAckRanges + 1000)
			// check that the oldest ACK range was deleted
			Expect(hist.ranges.Len()).To(Equal(protocol.MaxNumAckRanges))
			Expect(hist.ranges.Front().Value).To(Equal(utils.PacketInterval{Start: 2, End: 2}))
		})
	})

	Context("ACK range export", func() {
		It("returns nil if there are no ranges", func() {
			Expect(hist.GetAckRanges()).To(BeNil())
		})

		It("gets a single ACK range", func() {
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ReceivedPacket(5)).To(BeTrue())
			ackRanges := hist.GetAckRanges()
			Expect(ackRanges).To(HaveLen(1))
			Expect(ackRanges[0]).To(Equal(wire.AckRange{Smallest: 4, Largest: 5}))
		})

		It("gets multiple ACK ranges", func() {
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ReceivedPacket(5)).To(BeTrue())
			Expect(hist.ReceivedPacket(6)).To(BeTrue())
			Expect(hist.ReceivedPacket(1)).To(BeTrue())
			Expect(hist.ReceivedPacket(11)).To(BeTrue())
			Expect(hist.ReceivedPacket(10)).To(BeTrue())
			Expect(hist.ReceivedPacket(2)).To(BeTrue())
			ackRanges := hist.GetAckRanges()
			Expect(ackRanges).To(HaveLen(3))
			Expect(ackRanges[0]).To(Equal(wire.AckRange{Smallest: 10, Largest: 11}))
			Expect(ackRanges[1]).To(Equal(wire.AckRange{Smallest: 4, Largest: 6}))
			Expect(ackRanges[2]).To(Equal(wire.AckRange{Smallest: 1, Largest: 2}))
		})
	})

	Context("Getting the highest ACK range", func() {
		It("returns the zero value if there are no ranges", func() {
			Expect(hist.GetHighestAckRange()).To(BeZero())
		})

		It("gets a single ACK range", func() {
			Expect(hist.ReceivedPacket(4)).To(BeTrue())
			Expect(hist.ReceivedPacket(5)).To(BeTrue())
			Expect(hist.GetHighestAckRange()).To(Equal(wire.AckRange{Smallest: 4, Largest: 5}))
		})

		It("gets the highest of multiple ACK ranges", func() {
			Expect(hist.ReceivedPacket(3)).To(BeTrue())
			Expect(hist.ReceivedPacket(6)).To(BeTrue())
			Expect(hist.ReceivedPacket(7)).To(BeTrue())
			Expect(hist.GetHighestAckRange()).To(Equal(wire.AckRange{Smallest: 6, Largest: 7}))
		})
	})

	Context("duplicate detection", func() {
		It("doesn't declare the first packet a duplicate", func() {
			Expect(hist.IsPotentiallyDuplicate(5)).To(BeFalse())
		})

		It("detects a duplicate in a range", func() {
			hist.ReceivedPacket(4)
			hist.ReceivedPacket(5)
			hist.ReceivedPacket(6)
			Expect(hist.IsPotentiallyDuplicate(3)).To(BeFalse())
			Expect(hist.IsPotentiallyDuplicate(4)).To(BeTrue())
			Expect(hist.IsPotentiallyDuplicate(5)).To(BeTrue())
			Expect(hist.IsPotentiallyDuplicate(6)).To(BeTrue())
			Expect(hist.IsPotentiallyDuplicate(7)).To(BeFalse())
		})

		It("detects a duplicate in multiple ranges", func() {
			hist.ReceivedPacket(4)
			hist.ReceivedPacket(5)
			hist.ReceivedPacket(8)
			hist.ReceivedPacket(9)
			Expect(hist.IsPotentiallyDuplicate(3)).To(BeFalse())
			Expect(hist.IsPotentiallyDuplicate(4)).To(BeTrue())
			Expect(hist.IsPotentiallyDuplicate(5)).To(BeTrue())
			Expect(hist.IsPotentiallyDuplicate(6)).To(BeFalse())
			Expect(hist.IsPotentiallyDuplicate(7)).To(BeFalse())
			Expect(hist.IsPotentiallyDuplicate(8)).To(BeTrue())
			Expect(hist.IsPotentiallyDuplicate(9)).To(BeTrue())
			Expect(hist.IsPotentiallyDuplicate(10)).To(BeFalse())
		})

		It("says a packet is a potentially duplicate if the ranges were already deleted", func() {
			hist.ReceivedPacket(4)
			hist.ReceivedPacket(5)
			hist.ReceivedPacket(8)
			hist.ReceivedPacket(9)
			hist.ReceivedPacket(11)
			hist.DeleteBelow(8)
			Expect(hist.IsPotentiallyDuplicate(3)).To(BeTrue())
			Expect(hist.IsPotentiallyDuplicate(4)).To(BeTrue())
			Expect(hist.IsPotentiallyDuplicate(5)).To(BeTrue())
			Expect(hist.IsPotentiallyDuplicate(6)).To(BeTrue())
			Expect(hist.IsPotentiallyDuplicate(7)).To(BeTrue())
			Expect(hist.IsPotentiallyDuplicate(8)).To(BeTrue())
			Expect(hist.IsPotentiallyDuplicate(9)).To(BeTrue())
			Expect(hist.IsPotentiallyDuplicate(10)).To(BeFalse())
			Expect(hist.IsPotentiallyDuplicate(11)).To(BeTrue())
			Expect(hist.IsPotentiallyDuplicate(12)).To(BeFalse())
		})
	})
})
