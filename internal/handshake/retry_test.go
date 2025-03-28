package handshake

import (
	"github.com/For-ACGN/quic-go/internal/protocol"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Retry Integrity Check", func() {
	It("calculates retry integrity tags", func() {
		fooTag := GetRetryIntegrityTag([]byte("foo"), protocol.ConnectionID{1, 2, 3, 4})
		barTag := GetRetryIntegrityTag([]byte("bar"), protocol.ConnectionID{1, 2, 3, 4})
		Expect(fooTag).ToNot(BeNil())
		Expect(barTag).ToNot(BeNil())
		Expect(*fooTag).ToNot(Equal(*barTag))
	})

	It("includes the original connection ID in the tag calculation", func() {
		t1 := GetRetryIntegrityTag([]byte("foobar"), protocol.ConnectionID{1, 2, 3, 4})
		t2 := GetRetryIntegrityTag([]byte("foobar"), protocol.ConnectionID{4, 3, 2, 1})
		Expect(*t1).ToNot(Equal(*t2))
	})

	It("uses the test vector from the draft", func() {
		connID := protocol.ConnectionID(splitHexString("0x8394c8f03e515708"))
		data := splitHexString("ffff00001d0008f067a5502a4262b574 6f6b656ed16926d81f6f9ca2953a8aa4 575e1e49")
		Expect(GetRetryIntegrityTag(data[:len(data)-16], connID)[:]).To(Equal(data[len(data)-16:]))
	})
})
