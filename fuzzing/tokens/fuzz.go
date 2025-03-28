package tokens

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"time"

	"github.com/For-ACGN/quic-go/internal/handshake"
	"github.com/For-ACGN/quic-go/internal/protocol"
)

func Fuzz(data []byte) int {
	if len(data) < 8 {
		return -1
	}
	seed := binary.BigEndian.Uint64(data[:8])
	data = data[8:]
	tg, err := handshake.NewTokenGenerator(rand.New(rand.NewSource(int64(seed))))
	if err != nil {
		panic(err)
	}
	if len(data) < 1 {
		return -1
	}
	s := data[0] % 3
	data = data[1:]
	switch s {
	case 0:
		tg.DecodeToken(data)
		return 1
	case 1:
		return newToken(tg, data)
	case 2:
		return newRetryToken(tg, data)
	}
	return -1
}

func newToken(tg *handshake.TokenGenerator, data []byte) int {
	if len(data) < 1 {
		return -1
	}
	usesUDPAddr := data[0]%2 == 0
	data = data[1:]
	if len(data) != 18 {
		return -1
	}
	var addr net.Addr
	if usesUDPAddr {
		addr = &net.UDPAddr{
			Port: int(binary.BigEndian.Uint16(data[:2])),
			IP:   net.IP(data[2:]),
		}
	} else {
		addr = &net.TCPAddr{
			Port: int(binary.BigEndian.Uint16(data[:2])),
			IP:   net.IP(data[2:]),
		}
	}
	start := time.Now()
	encrypted, err := tg.NewToken(addr)
	if err != nil {
		panic(err)
	}
	token, err := tg.DecodeToken(encrypted)
	if err != nil {
		panic(err)
	}
	if token.IsRetryToken {
		panic("didn't encode a Retry token")
	}
	if token.SentTime.Before(start) || token.SentTime.After(time.Now()) {
		panic("incorrect send time")
	}
	if token.OriginalDestConnectionID != nil || token.RetrySrcConnectionID != nil {
		panic("didn't expect connection IDs")
	}
	checkAddr(token.RemoteAddr, addr)
	return 1
}

func newRetryToken(tg *handshake.TokenGenerator, data []byte) int {
	if len(data) < 2 {
		return -1
	}
	origDestConnIDLen := int(data[0] % 21)
	retrySrcConnIDLen := int(data[1] % 21)
	data = data[2:]
	if len(data) < origDestConnIDLen {
		return -1
	}
	origDestConnID := protocol.ConnectionID(data[:origDestConnIDLen])
	data = data[origDestConnIDLen:]
	if len(data) < retrySrcConnIDLen {
		return -1
	}
	retrySrcConnID := protocol.ConnectionID(data[:retrySrcConnIDLen])
	data = data[retrySrcConnIDLen:]

	if len(data) < 1 {
		return -1
	}
	usesUDPAddr := data[0]%2 == 0
	data = data[1:]
	if len(data) != 18 {
		return -1
	}
	start := time.Now()
	var addr net.Addr
	if usesUDPAddr {
		addr = &net.UDPAddr{
			Port: int(binary.BigEndian.Uint16(data[:2])),
			IP:   net.IP(data[2:]),
		}
	} else {
		addr = &net.TCPAddr{
			Port: int(binary.BigEndian.Uint16(data[:2])),
			IP:   net.IP(data[2:]),
		}
	}
	encrypted, err := tg.NewRetryToken(addr, origDestConnID, retrySrcConnID)
	if err != nil {
		panic(err)
	}
	token, err := tg.DecodeToken(encrypted)
	if err != nil {
		panic(err)
	}
	if !token.IsRetryToken {
		panic("expected a Retry token")
	}
	if token.SentTime.Before(start) || token.SentTime.After(time.Now()) {
		panic("incorrect send time")
	}
	if !token.OriginalDestConnectionID.Equal(origDestConnID) {
		panic("orig dest conn ID doesn't match")
	}
	if !token.RetrySrcConnectionID.Equal(retrySrcConnID) {
		panic("retry src conn ID doesn't match")
	}
	checkAddr(token.RemoteAddr, addr)
	return 1
}

func checkAddr(tokenAddr string, addr net.Addr) {
	if udpAddr, ok := addr.(*net.UDPAddr); ok {
		// For UDP addresses, we encode only the IP (not the port).
		if ip := udpAddr.IP.String(); tokenAddr != ip {
			fmt.Printf("%s vs %s", tokenAddr, ip)
			panic("wrong remote address for a net.UDPAddr")
		}
		return
	}

	if tokenAddr != addr.String() {
		fmt.Printf("%s vs %s", tokenAddr, addr.String())
		panic("wrong remote address")
	}
}
