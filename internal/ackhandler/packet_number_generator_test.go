package ackhandler

import (
	"fmt"

	"github.com/For-ACGN/quic-go/internal/protocol"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Sequential Packet Number Generator", func() {
	It("generates sequential packet numbers", func() {
		const initialPN protocol.PacketNumber = 123
		png := newSequentialPacketNumberGenerator(initialPN)

		for i := initialPN; i < initialPN+1000; i++ {
			Expect(png.Peek()).To(Equal(i))
			Expect(png.Peek()).To(Equal(i))
			Expect(png.Pop()).To(Equal(i))
		}
	})
})

var _ = Describe("Skipping Packet Number Generator", func() {
	const initialPN protocol.PacketNumber = 8
	const initialPeriod protocol.PacketNumber = 25
	const maxPeriod protocol.PacketNumber = 300

	It("can be initialized to return any first packet number", func() {
		png := newSkippingPacketNumberGenerator(12345, initialPeriod, maxPeriod)
		Expect(png.Pop()).To(Equal(protocol.PacketNumber(12345)))
	})

	It("allows peeking", func() {
		png := newSkippingPacketNumberGenerator(initialPN, initialPeriod, maxPeriod).(*skippingPacketNumberGenerator)
		png.nextToSkip = 1000
		Expect(png.Peek()).To(Equal(initialPN))
		Expect(png.Peek()).To(Equal(initialPN))
		Expect(png.Pop()).To(Equal(initialPN))
		Expect(png.Peek()).To(Equal(initialPN + 1))
		Expect(png.Peek()).To(Equal(initialPN + 1))
	})

	It("skips a packet number", func() {
		png := newSkippingPacketNumberGenerator(initialPN, initialPeriod, maxPeriod)
		var last protocol.PacketNumber
		var skipped bool
		for i := 0; i < 1000; i++ {
			num := png.Pop()
			if num > last+1 {
				skipped = true
				break
			}
			last = num
		}
		Expect(skipped).To(BeTrue())
	})

	It("generates a new packet number to skip", func() {
		const rep = 500
		periods := make([][]protocol.PacketNumber, rep)
		expectedPeriods := []protocol.PacketNumber{25, 50, 100, 200, 300, 300, 300}

		for i := 0; i < rep; i++ {
			png := newSkippingPacketNumberGenerator(initialPN, initialPeriod, maxPeriod)
			last := initialPN
			lastSkip := initialPN
			for len(periods[i]) < len(expectedPeriods) {
				next := png.Pop()
				if next > last+1 {
					skipped := next - 1
					Expect(skipped).To(BeNumerically(">", lastSkip+1))
					periods[i] = append(periods[i], skipped-lastSkip-1)
					lastSkip = skipped
				}
				last = next
			}
		}

		for j := 0; j < len(expectedPeriods); j++ {
			var average float64
			for i := 0; i < rep; i++ {
				average += float64(periods[i][j]) / float64(len(periods))
			}
			fmt.Fprintf(GinkgoWriter, "Period %d: %.2f (expected %d)\n", j, average, expectedPeriods[j])
			tolerance := protocol.PacketNumber(5)
			if t := expectedPeriods[j] / 10; t > tolerance {
				tolerance = t
			}
			Expect(average).To(BeNumerically("~", expectedPeriods[j]+1 /* we never skip two packet numbers at the same time */, tolerance))
		}
	})
})
