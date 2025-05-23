package wire

import (
	"bytes"

	"github.com/For-ACGN/quic-go/internal/protocol"
	"github.com/For-ACGN/quic-go/quicvarint"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("RESET_STREAM frame", func() {
	Context("when parsing", func() {
		It("accepts sample frame", func() {
			data := []byte{0x4}
			data = append(data, encodeVarInt(0xdeadbeef)...)  // stream ID
			data = append(data, encodeVarInt(0x1337)...)      // error code
			data = append(data, encodeVarInt(0x987654321)...) // byte offset
			b := bytes.NewReader(data)
			frame, err := parseResetStreamFrame(b, versionIETFFrames)
			Expect(err).ToNot(HaveOccurred())
			Expect(frame.StreamID).To(Equal(protocol.StreamID(0xdeadbeef)))
			Expect(frame.FinalSize).To(Equal(protocol.ByteCount(0x987654321)))
			Expect(frame.ErrorCode).To(Equal(protocol.ApplicationErrorCode(0x1337)))
		})

		It("errors on EOFs", func() {
			data := []byte{0x4}
			data = append(data, encodeVarInt(0xdeadbeef)...)  // stream ID
			data = append(data, encodeVarInt(0x1337)...)      // error code
			data = append(data, encodeVarInt(0x987654321)...) // byte offset
			_, err := parseResetStreamFrame(bytes.NewReader(data), versionIETFFrames)
			Expect(err).NotTo(HaveOccurred())
			for i := range data {
				_, err := parseResetStreamFrame(bytes.NewReader(data[0:i]), versionIETFFrames)
				Expect(err).To(HaveOccurred())
			}
		})
	})

	Context("when writing", func() {
		It("writes a sample frame", func() {
			frame := ResetStreamFrame{
				StreamID:  0x1337,
				FinalSize: 0x11223344decafbad,
				ErrorCode: 0xcafe,
			}
			b := &bytes.Buffer{}
			err := frame.Write(b, versionIETFFrames)
			Expect(err).ToNot(HaveOccurred())
			expected := []byte{0x4}
			expected = append(expected, encodeVarInt(0x1337)...)
			expected = append(expected, encodeVarInt(0xcafe)...)
			expected = append(expected, encodeVarInt(0x11223344decafbad)...)
			Expect(b.Bytes()).To(Equal(expected))
		})

		It("has the correct min length", func() {
			rst := ResetStreamFrame{
				StreamID:  0x1337,
				FinalSize: 0x1234567,
				ErrorCode: 0xde,
			}
			expectedLen := 1 + quicvarint.Len(0x1337) + quicvarint.Len(0x1234567) + 2
			Expect(rst.Length(versionIETFFrames)).To(Equal(expectedLen))
		})
	})
})
