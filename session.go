package quic

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"sync"
	"time"

	"github.com/For-ACGN/quic-go/internal/ackhandler"
	"github.com/For-ACGN/quic-go/internal/flowcontrol"
	"github.com/For-ACGN/quic-go/internal/handshake"
	"github.com/For-ACGN/quic-go/internal/logutils"
	"github.com/For-ACGN/quic-go/internal/protocol"
	"github.com/For-ACGN/quic-go/internal/qerr"
	"github.com/For-ACGN/quic-go/internal/utils"
	"github.com/For-ACGN/quic-go/internal/wire"
	"github.com/For-ACGN/quic-go/logging"
)

type unpacker interface {
	Unpack(hdr *wire.Header, rcvTime time.Time, data []byte) (*unpackedPacket, error)
}

type streamGetter interface {
	GetOrOpenReceiveStream(protocol.StreamID) (receiveStreamI, error)
	GetOrOpenSendStream(protocol.StreamID) (sendStreamI, error)
}

type streamManager interface {
	GetOrOpenSendStream(protocol.StreamID) (sendStreamI, error)
	GetOrOpenReceiveStream(protocol.StreamID) (receiveStreamI, error)
	OpenStream() (Stream, error)
	OpenUniStream() (SendStream, error)
	OpenStreamSync(context.Context) (Stream, error)
	OpenUniStreamSync(context.Context) (SendStream, error)
	AcceptStream(context.Context) (Stream, error)
	AcceptUniStream(context.Context) (ReceiveStream, error)
	DeleteStream(protocol.StreamID) error
	UpdateLimits(*wire.TransportParameters)
	HandleMaxStreamsFrame(*wire.MaxStreamsFrame) error
	CloseWithError(error)
}

type cryptoStreamHandler interface {
	RunHandshake()
	ChangeConnectionID(protocol.ConnectionID)
	SetLargest1RTTAcked(protocol.PacketNumber) error
	SetHandshakeConfirmed()
	GetSessionTicket() ([]byte, error)
	io.Closer
	ConnectionState() handshake.ConnectionState
}

type receivedPacket struct {
	buffer *packetBuffer

	remoteAddr net.Addr
	rcvTime    time.Time
	data       []byte

	ecn protocol.ECN
}

func (p *receivedPacket) Size() protocol.ByteCount { return protocol.ByteCount(len(p.data)) }

func (p *receivedPacket) Clone() *receivedPacket {
	return &receivedPacket{
		remoteAddr: p.remoteAddr,
		rcvTime:    p.rcvTime,
		data:       p.data,
		buffer:     p.buffer,
		ecn:        p.ecn,
	}
}

type sessionRunner interface {
	Add(protocol.ConnectionID, packetHandler) bool
	GetStatelessResetToken(protocol.ConnectionID) protocol.StatelessResetToken
	Retire(protocol.ConnectionID)
	Remove(protocol.ConnectionID)
	ReplaceWithClosed(protocol.ConnectionID, packetHandler)
	AddResetToken(protocol.StatelessResetToken, packetHandler)
	RemoveResetToken(protocol.StatelessResetToken)
}

type handshakeRunner struct {
	onReceivedParams    func(*wire.TransportParameters)
	onError             func(error)
	dropKeys            func(protocol.EncryptionLevel)
	onHandshakeComplete func()
}

func (r *handshakeRunner) OnReceivedParams(tp *wire.TransportParameters) { r.onReceivedParams(tp) }
func (r *handshakeRunner) OnError(e error)                               { r.onError(e) }
func (r *handshakeRunner) DropKeys(el protocol.EncryptionLevel)          { r.dropKeys(el) }
func (r *handshakeRunner) OnHandshakeComplete()                          { r.onHandshakeComplete() }

type closeError struct {
	err       error
	remote    bool
	immediate bool
}

type errCloseForRecreating struct {
	nextPacketNumber protocol.PacketNumber
	nextVersion      protocol.VersionNumber
}

func (errCloseForRecreating) Error() string {
	return "closing session in order to recreate it"
}

func (errCloseForRecreating) Is(target error) bool {
	_, ok := target.(errCloseForRecreating)
	return ok
}

// A Session is a QUIC session
type session struct {
	// Destination connection ID used during the handshake.
	// Used to check source connection ID on incoming packets.
	handshakeDestConnID protocol.ConnectionID
	// Set for the client. Destination connection ID used on the first Initial sent.
	origDestConnID protocol.ConnectionID
	retrySrcConnID *protocol.ConnectionID // only set for the client (and if a Retry was performed)

	srcConnIDLen int

	perspective protocol.Perspective
	version     protocol.VersionNumber
	config      *Config

	conn      sendConn
	sendQueue *sendQueue

	streamsMap      streamManager
	connIDManager   *connIDManager
	connIDGenerator *connIDGenerator

	rttStats *utils.RTTStats

	cryptoStreamManager   *cryptoStreamManager
	sentPacketHandler     ackhandler.SentPacketHandler
	receivedPacketHandler ackhandler.ReceivedPacketHandler
	retransmissionQueue   *retransmissionQueue
	framer                framer
	windowUpdateQueue     *windowUpdateQueue
	connFlowController    flowcontrol.ConnectionFlowController
	tokenStoreKey         string                    // only set for the client
	tokenGenerator        *handshake.TokenGenerator // only set for the server

	unpacker    unpacker
	frameParser wire.FrameParser
	packer      packer

	oneRTTStream        cryptoStream // only set for the server
	cryptoStreamHandler cryptoStreamHandler

	receivedPackets  chan *receivedPacket
	sendingScheduled chan struct{}

	closeOnce sync.Once
	// closeChan is used to notify the run loop that it should terminate
	closeChan chan closeError

	ctx                context.Context
	ctxCancel          context.CancelFunc
	handshakeCtx       context.Context
	handshakeCtxCancel context.CancelFunc

	undecryptablePackets []*receivedPacket

	clientHelloWritten    <-chan *wire.TransportParameters
	earlySessionReadyChan chan struct{}
	handshakeCompleteChan chan struct{} // is closed when the handshake completes
	handshakeComplete     bool
	handshakeConfirmed    bool

	receivedRetry       bool
	versionNegotiated   bool
	receivedFirstPacket bool

	idleTimeout         time.Duration
	sessionCreationTime time.Time
	// The idle timeout is set based on the max of the time we received the last packet...
	lastPacketReceivedTime time.Time
	// ... and the time we sent a new ack-eliciting packet after receiving a packet.
	firstAckElicitingPacketAfterIdleSentTime time.Time
	// pacingDeadline is the time when the next packet should be sent
	pacingDeadline time.Time

	peerParams *wire.TransportParameters

	timer *utils.Timer
	// keepAlivePingSent stores whether a keep alive PING is in flight.
	// It is reset as soon as we receive a packet from the peer.
	keepAlivePingSent bool
	keepAliveInterval time.Duration

	datagramQueue *datagramQueue

	logID  string
	tracer logging.ConnectionTracer
	logger utils.Logger
}

var (
	_ Session      = &session{}
	_ EarlySession = &session{}
	_ streamSender = &session{}
)

var newSession = func(
	conn sendConn,
	runner sessionRunner,
	origDestConnID protocol.ConnectionID,
	retrySrcConnID *protocol.ConnectionID,
	clientDestConnID protocol.ConnectionID,
	destConnID protocol.ConnectionID,
	srcConnID protocol.ConnectionID,
	statelessResetToken protocol.StatelessResetToken,
	conf *Config,
	tlsConf *tls.Config,
	tokenGenerator *handshake.TokenGenerator,
	enable0RTT bool,
	tracer logging.ConnectionTracer,
	logger utils.Logger,
	v protocol.VersionNumber,
) quicSession {
	s := &session{
		conn:                  conn,
		config:                conf,
		handshakeDestConnID:   destConnID,
		srcConnIDLen:          srcConnID.Len(),
		tokenGenerator:        tokenGenerator,
		oneRTTStream:          newCryptoStream(),
		perspective:           protocol.PerspectiveServer,
		handshakeCompleteChan: make(chan struct{}),
		tracer:                tracer,
		logger:                logger,
		version:               v,
	}
	if origDestConnID != nil {
		s.logID = origDestConnID.String()
	} else {
		s.logID = destConnID.String()
	}
	s.connIDManager = newConnIDManager(
		destConnID,
		func(token protocol.StatelessResetToken) { runner.AddResetToken(token, s) },
		runner.RemoveResetToken,
		s.queueControlFrame,
	)
	s.connIDGenerator = newConnIDGenerator(
		srcConnID,
		clientDestConnID,
		func(connID protocol.ConnectionID) { runner.Add(connID, s) },
		runner.GetStatelessResetToken,
		runner.Remove,
		runner.Retire,
		runner.ReplaceWithClosed,
		s.queueControlFrame,
		s.version,
	)
	s.preSetup()
	s.sentPacketHandler, s.receivedPacketHandler = ackhandler.NewAckHandler(
		0,
		s.rttStats,
		s.perspective,
		s.tracer,
		s.logger,
		s.version,
	)
	initialStream := newCryptoStream()
	handshakeStream := newCryptoStream()
	params := &wire.TransportParameters{
		InitialMaxStreamDataBidiLocal:   protocol.InitialMaxStreamData,
		InitialMaxStreamDataBidiRemote:  protocol.InitialMaxStreamData,
		InitialMaxStreamDataUni:         protocol.InitialMaxStreamData,
		InitialMaxData:                  protocol.InitialMaxData,
		MaxIdleTimeout:                  s.config.MaxIdleTimeout,
		MaxBidiStreamNum:                protocol.StreamNum(s.config.MaxIncomingStreams),
		MaxUniStreamNum:                 protocol.StreamNum(s.config.MaxIncomingUniStreams),
		MaxAckDelay:                     protocol.MaxAckDelayInclGranularity,
		AckDelayExponent:                protocol.AckDelayExponent,
		DisableActiveMigration:          true,
		StatelessResetToken:             &statelessResetToken,
		OriginalDestinationConnectionID: origDestConnID,
		ActiveConnectionIDLimit:         protocol.MaxActiveConnectionIDs,
		InitialSourceConnectionID:       srcConnID,
		RetrySourceConnectionID:         retrySrcConnID,
	}
	if s.config.EnableDatagrams {
		params.MaxDatagramFrameSize = protocol.MaxDatagramFrameSize
	}
	if s.tracer != nil {
		s.tracer.SentTransportParameters(params)
	}
	cs := handshake.NewCryptoSetupServer(
		initialStream,
		handshakeStream,
		clientDestConnID,
		conn.LocalAddr(),
		conn.RemoteAddr(),
		params,
		&handshakeRunner{
			onReceivedParams: s.processTransportParameters,
			onError:          s.closeLocal,
			dropKeys:         s.dropEncryptionLevel,
			onHandshakeComplete: func() {
				runner.Retire(clientDestConnID)
				close(s.handshakeCompleteChan)
			},
		},
		tlsConf,
		enable0RTT,
		s.rttStats,
		tracer,
		logger,
		s.version,
	)
	s.cryptoStreamHandler = cs
	s.packer = newPacketPacker(
		srcConnID,
		s.connIDManager.Get,
		initialStream,
		handshakeStream,
		s.sentPacketHandler,
		s.retransmissionQueue,
		s.RemoteAddr(),
		cs,
		s.framer,
		s.receivedPacketHandler,
		s.datagramQueue,
		s.perspective,
		s.version,
	)
	s.unpacker = newPacketUnpacker(cs, s.version)
	s.cryptoStreamManager = newCryptoStreamManager(cs, initialStream, handshakeStream, s.oneRTTStream)
	return s
}

// declare this as a variable, such that we can it mock it in the tests
var newClientSession = func(
	conn sendConn,
	runner sessionRunner,
	destConnID protocol.ConnectionID,
	srcConnID protocol.ConnectionID,
	conf *Config,
	tlsConf *tls.Config,
	initialPacketNumber protocol.PacketNumber,
	enable0RTT bool,
	hasNegotiatedVersion bool,
	tracer logging.ConnectionTracer,
	logger utils.Logger,
	v protocol.VersionNumber,
) quicSession {
	s := &session{
		conn:                  conn,
		config:                conf,
		origDestConnID:        destConnID,
		handshakeDestConnID:   destConnID,
		srcConnIDLen:          srcConnID.Len(),
		perspective:           protocol.PerspectiveClient,
		handshakeCompleteChan: make(chan struct{}),
		logID:                 destConnID.String(),
		logger:                logger,
		tracer:                tracer,
		versionNegotiated:     hasNegotiatedVersion,
		version:               v,
	}
	s.connIDManager = newConnIDManager(
		destConnID,
		func(token protocol.StatelessResetToken) { runner.AddResetToken(token, s) },
		runner.RemoveResetToken,
		s.queueControlFrame,
	)
	s.connIDGenerator = newConnIDGenerator(
		srcConnID,
		nil,
		func(connID protocol.ConnectionID) { runner.Add(connID, s) },
		runner.GetStatelessResetToken,
		runner.Remove,
		runner.Retire,
		runner.ReplaceWithClosed,
		s.queueControlFrame,
		s.version,
	)
	s.preSetup()
	s.sentPacketHandler, s.receivedPacketHandler = ackhandler.NewAckHandler(
		initialPacketNumber,
		s.rttStats,
		s.perspective,
		s.tracer,
		s.logger,
		s.version,
	)
	initialStream := newCryptoStream()
	handshakeStream := newCryptoStream()
	params := &wire.TransportParameters{
		InitialMaxStreamDataBidiRemote: protocol.InitialMaxStreamData,
		InitialMaxStreamDataBidiLocal:  protocol.InitialMaxStreamData,
		InitialMaxStreamDataUni:        protocol.InitialMaxStreamData,
		InitialMaxData:                 protocol.InitialMaxData,
		MaxIdleTimeout:                 s.config.MaxIdleTimeout,
		MaxBidiStreamNum:               protocol.StreamNum(s.config.MaxIncomingStreams),
		MaxUniStreamNum:                protocol.StreamNum(s.config.MaxIncomingUniStreams),
		MaxAckDelay:                    protocol.MaxAckDelayInclGranularity,
		AckDelayExponent:               protocol.AckDelayExponent,
		DisableActiveMigration:         true,
		ActiveConnectionIDLimit:        protocol.MaxActiveConnectionIDs,
		InitialSourceConnectionID:      srcConnID,
	}
	if s.config.EnableDatagrams {
		params.MaxDatagramFrameSize = protocol.MaxDatagramFrameSize
	}
	if s.tracer != nil {
		s.tracer.SentTransportParameters(params)
	}
	cs, clientHelloWritten := handshake.NewCryptoSetupClient(
		initialStream,
		handshakeStream,
		destConnID,
		conn.LocalAddr(),
		conn.RemoteAddr(),
		params,
		&handshakeRunner{
			onReceivedParams:    s.processTransportParameters,
			onError:             s.closeLocal,
			dropKeys:            s.dropEncryptionLevel,
			onHandshakeComplete: func() { close(s.handshakeCompleteChan) },
		},
		tlsConf,
		enable0RTT,
		s.rttStats,
		tracer,
		logger,
		s.version,
	)
	s.clientHelloWritten = clientHelloWritten
	s.cryptoStreamHandler = cs
	s.cryptoStreamManager = newCryptoStreamManager(cs, initialStream, handshakeStream, newCryptoStream())
	s.unpacker = newPacketUnpacker(cs, s.version)
	s.packer = newPacketPacker(
		srcConnID,
		s.connIDManager.Get,
		initialStream,
		handshakeStream,
		s.sentPacketHandler,
		s.retransmissionQueue,
		s.RemoteAddr(),
		cs,
		s.framer,
		s.receivedPacketHandler,
		s.datagramQueue,
		s.perspective,
		s.version,
	)
	if len(tlsConf.ServerName) > 0 {
		s.tokenStoreKey = tlsConf.ServerName
	} else {
		s.tokenStoreKey = conn.RemoteAddr().String()
	}
	if s.config.TokenStore != nil {
		if token := s.config.TokenStore.Pop(s.tokenStoreKey); token != nil {
			s.packer.SetToken(token.data)
		}
	}
	return s
}

func (s *session) preSetup() {
	s.sendQueue = newSendQueue(s.conn)
	s.retransmissionQueue = newRetransmissionQueue(s.version)
	s.frameParser = wire.NewFrameParser(s.config.EnableDatagrams, s.version)
	s.rttStats = &utils.RTTStats{}
	s.connFlowController = flowcontrol.NewConnectionFlowController(
		protocol.InitialMaxData,
		protocol.ByteCount(s.config.MaxReceiveConnectionFlowControlWindow),
		s.onHasConnectionWindowUpdate,
		s.rttStats,
		s.logger,
	)
	s.earlySessionReadyChan = make(chan struct{})
	s.streamsMap = newStreamsMap(
		s,
		s.newFlowController,
		uint64(s.config.MaxIncomingStreams),
		uint64(s.config.MaxIncomingUniStreams),
		s.perspective,
		s.version,
	)
	s.framer = newFramer(s.streamsMap, s.version)
	s.receivedPackets = make(chan *receivedPacket, protocol.MaxSessionUnprocessedPackets)
	s.closeChan = make(chan closeError, 1)
	s.sendingScheduled = make(chan struct{}, 1)
	s.ctx, s.ctxCancel = context.WithCancel(context.Background())
	s.handshakeCtx, s.handshakeCtxCancel = context.WithCancel(context.Background())

	now := time.Now()
	s.lastPacketReceivedTime = now
	s.sessionCreationTime = now

	s.windowUpdateQueue = newWindowUpdateQueue(s.streamsMap, s.connFlowController, s.framer.QueueControlFrame)
	if s.config.EnableDatagrams {
		s.datagramQueue = newDatagramQueue(s.scheduleSending, s.logger)
	}
}

// run the session main loop
func (s *session) run() error {
	defer s.ctxCancel()

	s.timer = utils.NewTimer()

	go s.cryptoStreamHandler.RunHandshake()
	go func() {
		if err := s.sendQueue.Run(); err != nil {
			s.destroyImpl(err)
		}
	}()

	if s.perspective == protocol.PerspectiveClient {
		select {
		case zeroRTTParams := <-s.clientHelloWritten:
			s.scheduleSending()
			if zeroRTTParams != nil {
				s.restoreTransportParameters(zeroRTTParams)
				close(s.earlySessionReadyChan)
			}
		case closeErr := <-s.closeChan:
			// put the close error back into the channel, so that the run loop can receive it
			s.closeChan <- closeErr
		}
	}

	var closeErr closeError

runLoop:
	for {
		// Close immediately if requested
		select {
		case closeErr = <-s.closeChan:
			break runLoop
		case <-s.handshakeCompleteChan:
			s.handleHandshakeComplete()
		default:
		}

		s.maybeResetTimer()

		select {
		case closeErr = <-s.closeChan:
			break runLoop
		case <-s.timer.Chan():
			s.timer.SetRead()
			// We do all the interesting stuff after the switch statement, so
			// nothing to see here.
		case <-s.sendingScheduled:
			// We do all the interesting stuff after the switch statement, so
			// nothing to see here.
		case p := <-s.receivedPackets:
			// Only reset the timers if this packet was actually processed.
			// This avoids modifying any state when handling undecryptable packets,
			// which could be injected by an attacker.
			if wasProcessed := s.handlePacketImpl(p); !wasProcessed {
				continue
			}
			// Don't set timers and send packets if the packet made us close the session.
			select {
			case closeErr = <-s.closeChan:
				break runLoop
			default:
			}
		case <-s.handshakeCompleteChan:
			s.handleHandshakeComplete()
		}

		now := time.Now()
		if timeout := s.sentPacketHandler.GetLossDetectionTimeout(); !timeout.IsZero() && timeout.Before(now) {
			// This could cause packets to be retransmitted.
			// Check it before trying to send packets.
			if err := s.sentPacketHandler.OnLossDetectionTimeout(); err != nil {
				s.closeLocal(err)
			}
		}

		if keepAliveTime := s.nextKeepAliveTime(); !keepAliveTime.IsZero() && !now.Before(keepAliveTime) {
			// send a PING frame since there is no activity in the session
			s.logger.Debugf("Sending a keep-alive PING to keep the connection alive.")
			s.framer.QueueControlFrame(&wire.PingFrame{})
			s.keepAlivePingSent = true
		} else if !s.handshakeComplete && now.Sub(s.sessionCreationTime) >= s.config.handshakeTimeout() {
			if s.tracer != nil {
				s.tracer.ClosedConnection(logging.NewTimeoutCloseReason(logging.TimeoutReasonHandshake))
			}
			s.destroyImpl(qerr.NewTimeoutError("Handshake did not complete in time"))
			continue
		} else {
			idleTimeoutStartTime := s.idleTimeoutStartTime()
			if (!s.handshakeComplete && now.Sub(idleTimeoutStartTime) >= s.config.HandshakeIdleTimeout) ||
				(s.handshakeComplete && now.Sub(idleTimeoutStartTime) >= s.idleTimeout) {
				if s.tracer != nil {
					s.tracer.ClosedConnection(logging.NewTimeoutCloseReason(logging.TimeoutReasonIdle))
				}
				s.destroyImpl(qerr.NewTimeoutError("No recent network activity"))
				continue
			}
		}

		if err := s.sendPackets(); err != nil {
			s.closeLocal(err)
		}
	}

	s.handleCloseError(closeErr)
	if !errors.Is(closeErr.err, errCloseForRecreating{}) && s.tracer != nil {
		s.tracer.Close()
	}
	s.logger.Infof("Connection %s closed.", s.logID)
	s.cryptoStreamHandler.Close()
	s.sendQueue.Close()
	s.timer.Stop()
	return closeErr.err
}

// blocks until the early session can be used
func (s *session) earlySessionReady() <-chan struct{} {
	return s.earlySessionReadyChan
}

func (s *session) HandshakeComplete() context.Context {
	return s.handshakeCtx
}

func (s *session) Context() context.Context {
	return s.ctx
}

func (s *session) supportsDatagrams() bool {
	return s.peerParams.MaxDatagramFrameSize != protocol.InvalidByteCount
}

func (s *session) ConnectionState() ConnectionState {
	return ConnectionState{
		TLS:               s.cryptoStreamHandler.ConnectionState(),
		SupportsDatagrams: s.supportsDatagrams(),
	}
}

// Time when the next keep-alive packet should be sent.
// It returns a zero time if no keep-alive should be sent.
func (s *session) nextKeepAliveTime() time.Time {
	if !s.config.KeepAlive || s.keepAlivePingSent || !s.firstAckElicitingPacketAfterIdleSentTime.IsZero() {
		return time.Time{}
	}
	return s.lastPacketReceivedTime.Add(s.keepAliveInterval / 2)
}

func (s *session) maybeResetTimer() {
	var deadline time.Time
	if !s.handshakeComplete {
		deadline = s.sessionCreationTime.Add(utils.MinDuration(s.config.handshakeTimeout(), s.config.HandshakeIdleTimeout))
	} else {
		if keepAliveTime := s.nextKeepAliveTime(); !keepAliveTime.IsZero() {
			deadline = keepAliveTime
		} else {
			deadline = s.idleTimeoutStartTime().Add(s.idleTimeout)
		}
	}

	if ackAlarm := s.receivedPacketHandler.GetAlarmTimeout(); !ackAlarm.IsZero() {
		deadline = utils.MinTime(deadline, ackAlarm)
	}
	if lossTime := s.sentPacketHandler.GetLossDetectionTimeout(); !lossTime.IsZero() {
		deadline = utils.MinTime(deadline, lossTime)
	}
	if !s.pacingDeadline.IsZero() {
		deadline = utils.MinTime(deadline, s.pacingDeadline)
	}

	s.timer.Reset(deadline)
}

func (s *session) idleTimeoutStartTime() time.Time {
	return utils.MaxTime(s.lastPacketReceivedTime, s.firstAckElicitingPacketAfterIdleSentTime)
}

func (s *session) handleHandshakeComplete() {
	s.handshakeComplete = true
	s.handshakeCompleteChan = nil // prevent this case from ever being selected again
	s.handshakeCtxCancel()
	// Once the handshake completes, we have derived 1-RTT keys.
	// There's no point in queueing undecryptable packets for later decryption any more.
	s.undecryptablePackets = nil

	s.connIDManager.SetHandshakeComplete()
	s.connIDGenerator.SetHandshakeComplete()

	if s.perspective == protocol.PerspectiveServer {
		s.handshakeConfirmed = true
		s.sentPacketHandler.SetHandshakeConfirmed()
		ticket, err := s.cryptoStreamHandler.GetSessionTicket()
		if err != nil {
			s.closeLocal(err)
		}
		if ticket != nil {
			s.oneRTTStream.Write(ticket)
			for s.oneRTTStream.HasData() {
				s.queueControlFrame(s.oneRTTStream.PopCryptoFrame(protocol.MaxPostHandshakeCryptoFrameSize))
			}
		}
		token, err := s.tokenGenerator.NewToken(s.conn.RemoteAddr())
		if err != nil {
			s.closeLocal(err)
		}
		s.queueControlFrame(&wire.NewTokenFrame{Token: token})
		s.cryptoStreamHandler.SetHandshakeConfirmed()
		s.queueControlFrame(&wire.HandshakeDoneFrame{})
	}
}

func (s *session) handlePacketImpl(rp *receivedPacket) bool {
	if wire.IsVersionNegotiationPacket(rp.data) {
		s.handleVersionNegotiationPacket(rp)
		return false
	}

	var counter uint8
	var lastConnID protocol.ConnectionID
	var processed bool
	data := rp.data
	p := rp
	s.sentPacketHandler.ReceivedBytes(protocol.ByteCount(len(data)))
	for len(data) > 0 {
		if counter > 0 {
			p = p.Clone()
			p.data = data
		}

		hdr, packetData, rest, err := wire.ParsePacket(p.data, s.srcConnIDLen)
		if err != nil {
			if s.tracer != nil {
				dropReason := logging.PacketDropHeaderParseError
				if err == wire.ErrUnsupportedVersion {
					dropReason = logging.PacketDropUnsupportedVersion
				}
				s.tracer.DroppedPacket(logging.PacketTypeNotDetermined, protocol.ByteCount(len(data)), dropReason)
			}
			s.logger.Debugf("error parsing packet: %s", err)
			break
		}

		if hdr.IsLongHeader && hdr.Version != s.version {
			if s.tracer != nil {
				s.tracer.DroppedPacket(logging.PacketTypeFromHeader(hdr), protocol.ByteCount(len(data)), logging.PacketDropUnexpectedVersion)
			}
			s.logger.Debugf("Dropping packet with version %x. Expected %x.", hdr.Version, s.version)
			break
		}

		if counter > 0 && !hdr.DestConnectionID.Equal(lastConnID) {
			if s.tracer != nil {
				s.tracer.DroppedPacket(logging.PacketTypeFromHeader(hdr), protocol.ByteCount(len(data)), logging.PacketDropUnknownConnectionID)
			}
			s.logger.Debugf("coalesced packet has different destination connection ID: %s, expected %s", hdr.DestConnectionID, lastConnID)
			break
		}
		lastConnID = hdr.DestConnectionID

		if counter > 0 {
			p.buffer.Split()
		}
		counter++

		// only log if this actually a coalesced packet
		if s.logger.Debug() && (counter > 1 || len(rest) > 0) {
			s.logger.Debugf("Parsed a coalesced packet. Part %d: %d bytes. Remaining: %d bytes.", counter, len(packetData), len(rest))
		}
		p.data = packetData
		if wasProcessed := s.handleSinglePacket(p, hdr); wasProcessed {
			processed = true
		}
		data = rest
	}
	p.buffer.MaybeRelease()
	return processed
}

func (s *session) handleSinglePacket(p *receivedPacket, hdr *wire.Header) bool /* was the packet successfully processed */ {
	var wasQueued bool

	defer func() {
		// Put back the packet buffer if the packet wasn't queued for later decryption.
		if !wasQueued {
			p.buffer.Decrement()
		}
	}()

	if hdr.Type == protocol.PacketTypeRetry {
		return s.handleRetryPacket(hdr, p.data)
	}

	// The server can change the source connection ID with the first Handshake packet.
	// After this, all packets with a different source connection have to be ignored.
	if s.receivedFirstPacket && hdr.IsLongHeader && hdr.Type == protocol.PacketTypeInitial && !hdr.SrcConnectionID.Equal(s.handshakeDestConnID) {
		if s.tracer != nil {
			s.tracer.DroppedPacket(logging.PacketTypeInitial, p.Size(), logging.PacketDropUnknownConnectionID)
		}
		s.logger.Debugf("Dropping Initial packet (%d bytes) with unexpected source connection ID: %s (expected %s)", p.Size(), hdr.SrcConnectionID, s.handshakeDestConnID)
		return false
	}
	// drop 0-RTT packets, if we are a client
	if s.perspective == protocol.PerspectiveClient && hdr.Type == protocol.PacketType0RTT {
		if s.tracer != nil {
			s.tracer.DroppedPacket(logging.PacketType0RTT, p.Size(), logging.PacketDropKeyUnavailable)
		}
		return false
	}

	packet, err := s.unpacker.Unpack(hdr, p.rcvTime, p.data)
	if err != nil {
		switch err {
		case handshake.ErrKeysDropped:
			if s.tracer != nil {
				s.tracer.DroppedPacket(logging.PacketTypeFromHeader(hdr), p.Size(), logging.PacketDropKeyUnavailable)
			}
			s.logger.Debugf("Dropping %s packet (%d bytes) because we already dropped the keys.", hdr.PacketType(), p.Size())
		case handshake.ErrKeysNotYetAvailable:
			// Sealer for this encryption level not yet available.
			// Try again later.
			wasQueued = true
			s.tryQueueingUndecryptablePacket(p, hdr)
		case wire.ErrInvalidReservedBits:
			s.closeLocal(qerr.NewError(qerr.ProtocolViolation, err.Error()))
		case handshake.ErrDecryptionFailed:
			// This might be a packet injected by an attacker. Drop it.
			if s.tracer != nil {
				s.tracer.DroppedPacket(logging.PacketTypeFromHeader(hdr), p.Size(), logging.PacketDropPayloadDecryptError)
			}
			s.logger.Debugf("Dropping %s packet (%d bytes) that could not be unpacked. Error: %s", hdr.PacketType(), p.Size(), err)
		default:
			var headerErr *headerParseError
			if errors.As(err, &headerErr) {
				// This might be a packet injected by an attacker. Drop it.
				if s.tracer != nil {
					s.tracer.DroppedPacket(logging.PacketTypeFromHeader(hdr), p.Size(), logging.PacketDropHeaderParseError)
				}
				s.logger.Debugf("Dropping %s packet (%d bytes) for which we couldn't unpack the header. Error: %s", hdr.PacketType(), p.Size(), err)
			} else {
				// This is an error returned by the AEAD (other than ErrDecryptionFailed).
				// For example, a PROTOCOL_VIOLATION due to key updates.
				s.closeLocal(err)
			}
		}
		return false
	}

	if s.logger.Debug() {
		s.logger.Debugf("<- Reading packet %d (%d bytes) for connection %s, %s", packet.packetNumber, p.Size(), hdr.DestConnectionID, packet.encryptionLevel)
		packet.hdr.Log(s.logger)
	}

	if s.receivedPacketHandler.IsPotentiallyDuplicate(packet.packetNumber, packet.encryptionLevel) {
		s.logger.Debugf("Dropping (potentially) duplicate packet.")
		if s.tracer != nil {
			s.tracer.DroppedPacket(logging.PacketTypeFromHeader(hdr), p.Size(), logging.PacketDropDuplicate)
		}
		return false
	}

	if err := s.handleUnpackedPacket(packet, p.ecn, p.rcvTime, p.Size()); err != nil {
		s.closeLocal(err)
		return false
	}
	return true
}

func (s *session) handleRetryPacket(hdr *wire.Header, data []byte) bool /* was this a valid Retry */ {
	(&wire.ExtendedHeader{Header: *hdr}).Log(s.logger)
	if s.perspective == protocol.PerspectiveServer {
		if s.tracer != nil {
			s.tracer.DroppedPacket(logging.PacketTypeRetry, protocol.ByteCount(len(data)), logging.PacketDropUnexpectedPacket)
		}
		s.logger.Debugf("Ignoring Retry.")
		return false
	}
	if s.receivedFirstPacket {
		if s.tracer != nil {
			s.tracer.DroppedPacket(logging.PacketTypeRetry, protocol.ByteCount(len(data)), logging.PacketDropUnexpectedPacket)
		}
		s.logger.Debugf("Ignoring Retry, since we already received a packet.")
		return false
	}
	destConnID := s.connIDManager.Get()
	if hdr.SrcConnectionID.Equal(destConnID) {
		if s.tracer != nil {
			s.tracer.DroppedPacket(logging.PacketTypeRetry, protocol.ByteCount(len(data)), logging.PacketDropUnexpectedPacket)
		}
		s.logger.Debugf("Ignoring Retry, since the server didn't change the Source Connection ID.")
		return false
	}
	// If a token is already set, this means that we already received a Retry from the server.
	// Ignore this Retry packet.
	if s.receivedRetry {
		s.logger.Debugf("Ignoring Retry, since a Retry was already received.")
		return false
	}

	tag := handshake.GetRetryIntegrityTag(data[:len(data)-16], destConnID)
	if !bytes.Equal(data[len(data)-16:], tag[:]) {
		if s.tracer != nil {
			s.tracer.DroppedPacket(logging.PacketTypeRetry, protocol.ByteCount(len(data)), logging.PacketDropPayloadDecryptError)
		}
		s.logger.Debugf("Ignoring spoofed Retry. Integrity Tag doesn't match.")
		return false
	}

	if s.logger.Debug() {
		s.logger.Debugf("<- Received Retry:")
		(&wire.ExtendedHeader{Header: *hdr}).Log(s.logger)
		s.logger.Debugf("Switching destination connection ID to: %s", hdr.SrcConnectionID)
	}
	if s.tracer != nil {
		s.tracer.ReceivedRetry(hdr)
	}
	newDestConnID := hdr.SrcConnectionID
	s.receivedRetry = true
	if err := s.sentPacketHandler.ResetForRetry(); err != nil {
		s.closeLocal(err)
		return false
	}
	s.handshakeDestConnID = newDestConnID
	s.retrySrcConnID = &newDestConnID
	s.cryptoStreamHandler.ChangeConnectionID(newDestConnID)
	s.packer.SetToken(hdr.Token)
	s.connIDManager.ChangeInitialConnID(newDestConnID)
	s.scheduleSending()
	return true
}

func (s *session) handleVersionNegotiationPacket(p *receivedPacket) {
	if s.perspective == protocol.PerspectiveServer || // servers never receive version negotiation packets
		s.receivedFirstPacket || s.versionNegotiated { // ignore delayed / duplicated version negotiation packets
		if s.tracer != nil {
			s.tracer.DroppedPacket(logging.PacketTypeVersionNegotiation, p.Size(), logging.PacketDropUnexpectedPacket)
		}
		return
	}

	hdr, supportedVersions, err := wire.ParseVersionNegotiationPacket(bytes.NewReader(p.data))
	if err != nil {
		if s.tracer != nil {
			s.tracer.DroppedPacket(logging.PacketTypeVersionNegotiation, p.Size(), logging.PacketDropHeaderParseError)
		}
		s.logger.Debugf("Error parsing Version Negotiation packet: %s", err)
		return
	}

	for _, v := range supportedVersions {
		if v == s.version {
			if s.tracer != nil {
				s.tracer.DroppedPacket(logging.PacketTypeVersionNegotiation, p.Size(), logging.PacketDropUnexpectedVersion)
			}
			// The Version Negotiation packet contains the version that we offered.
			// This might be a packet sent by an attacker, or it was corrupted.
			return
		}
	}

	s.logger.Infof("Received a Version Negotiation packet. Supported Versions: %s", supportedVersions)
	if s.tracer != nil {
		s.tracer.ReceivedVersionNegotiationPacket(hdr, supportedVersions)
	}
	newVersion, ok := protocol.ChooseSupportedVersion(s.config.Versions, supportedVersions)
	if !ok {
		//nolint:stylecheck
		s.destroyImpl(fmt.Errorf("No compatible QUIC version found. We support %s, server offered %s.", s.config.Versions, supportedVersions))
		s.logger.Infof("No compatible QUIC version found.")
		return
	}

	s.logger.Infof("Switching to QUIC version %s.", newVersion)
	nextPN, _ := s.sentPacketHandler.PeekPacketNumber(protocol.EncryptionInitial)
	s.destroyImpl(&errCloseForRecreating{
		nextPacketNumber: nextPN,
		nextVersion:      newVersion,
	})
}

func (s *session) handleUnpackedPacket(
	packet *unpackedPacket,
	ecn protocol.ECN,
	rcvTime time.Time,
	packetSize protocol.ByteCount, // only for logging
) error {
	if len(packet.data) == 0 {
		return qerr.NewError(qerr.ProtocolViolation, "empty packet")
	}

	if !s.receivedFirstPacket {
		s.receivedFirstPacket = true
		// The server can change the source connection ID with the first Handshake packet.
		if s.perspective == protocol.PerspectiveClient && packet.hdr.IsLongHeader && !packet.hdr.SrcConnectionID.Equal(s.handshakeDestConnID) {
			cid := packet.hdr.SrcConnectionID
			s.logger.Debugf("Received first packet. Switching destination connection ID to: %s", cid)
			s.handshakeDestConnID = cid
			s.connIDManager.ChangeInitialConnID(cid)
		}
		// We create the session as soon as we receive the first packet from the client.
		// We do that before authenticating the packet.
		// That means that if the source connection ID was corrupted,
		// we might have create a session with an incorrect source connection ID.
		// Once we authenticate the first packet, we need to update it.
		if s.perspective == protocol.PerspectiveServer {
			if !packet.hdr.SrcConnectionID.Equal(s.handshakeDestConnID) {
				s.handshakeDestConnID = packet.hdr.SrcConnectionID
				s.connIDManager.ChangeInitialConnID(packet.hdr.SrcConnectionID)
			}
			if s.tracer != nil {
				s.tracer.StartedConnection(
					s.conn.LocalAddr(),
					s.conn.RemoteAddr(),
					s.version,
					packet.hdr.SrcConnectionID,
					packet.hdr.DestConnectionID,
				)
			}
		}
	}

	s.lastPacketReceivedTime = rcvTime
	s.firstAckElicitingPacketAfterIdleSentTime = time.Time{}
	s.keepAlivePingSent = false

	// Only used for tracing.
	// If we're not tracing, this slice will always remain empty.
	var frames []wire.Frame
	r := bytes.NewReader(packet.data)
	var isAckEliciting bool
	for {
		frame, err := s.frameParser.ParseNext(r, packet.encryptionLevel)
		if err != nil {
			return err
		}
		if frame == nil {
			break
		}
		if ackhandler.IsFrameAckEliciting(frame) {
			isAckEliciting = true
		}
		// Only process frames now if we're not logging.
		// If we're logging, we need to make sure that the packet_received event is logged first.
		if s.tracer == nil {
			if err := s.handleFrame(frame, packet.encryptionLevel, packet.hdr.DestConnectionID); err != nil {
				return err
			}
		} else {
			frames = append(frames, frame)
		}
	}

	if s.tracer != nil {
		fs := make([]logging.Frame, len(frames))
		for i, frame := range frames {
			fs[i] = logutils.ConvertFrame(frame)
		}
		s.tracer.ReceivedPacket(packet.hdr, packetSize, fs)
		for _, frame := range frames {
			if err := s.handleFrame(frame, packet.encryptionLevel, packet.hdr.DestConnectionID); err != nil {
				return err
			}
		}
	}

	return s.receivedPacketHandler.ReceivedPacket(packet.packetNumber, ecn, packet.encryptionLevel, rcvTime, isAckEliciting)
}

func (s *session) handleFrame(f wire.Frame, encLevel protocol.EncryptionLevel, destConnID protocol.ConnectionID) error {
	var err error
	wire.LogFrame(s.logger, f, false)
	switch frame := f.(type) {
	case *wire.CryptoFrame:
		err = s.handleCryptoFrame(frame, encLevel)
	case *wire.StreamFrame:
		err = s.handleStreamFrame(frame)
	case *wire.AckFrame:
		err = s.handleAckFrame(frame, encLevel)
	case *wire.ConnectionCloseFrame:
		s.handleConnectionCloseFrame(frame)
	case *wire.ResetStreamFrame:
		err = s.handleResetStreamFrame(frame)
	case *wire.MaxDataFrame:
		s.handleMaxDataFrame(frame)
	case *wire.MaxStreamDataFrame:
		err = s.handleMaxStreamDataFrame(frame)
	case *wire.MaxStreamsFrame:
		err = s.handleMaxStreamsFrame(frame)
	case *wire.DataBlockedFrame:
	case *wire.StreamDataBlockedFrame:
	case *wire.StreamsBlockedFrame:
	case *wire.StopSendingFrame:
		err = s.handleStopSendingFrame(frame)
	case *wire.PingFrame:
	case *wire.PathChallengeFrame:
		s.handlePathChallengeFrame(frame)
	case *wire.PathResponseFrame:
		// since we don't send PATH_CHALLENGEs, we don't expect PATH_RESPONSEs
		err = errors.New("unexpected PATH_RESPONSE frame")
	case *wire.NewTokenFrame:
		err = s.handleNewTokenFrame(frame)
	case *wire.NewConnectionIDFrame:
		err = s.handleNewConnectionIDFrame(frame)
	case *wire.RetireConnectionIDFrame:
		err = s.handleRetireConnectionIDFrame(frame, destConnID)
	case *wire.HandshakeDoneFrame:
		err = s.handleHandshakeDoneFrame()
	case *wire.DatagramFrame:
		err = s.handleDatagramFrame(frame)
	default:
		err = fmt.Errorf("unexpected frame type: %s", reflect.ValueOf(&frame).Elem().Type().Name())
	}
	return err
}

// handlePacket is called by the server with a new packet
func (s *session) handlePacket(p *receivedPacket) {
	// Discard packets once the amount of queued packets is larger than
	// the channel size, protocol.MaxSessionUnprocessedPackets
	select {
	case s.receivedPackets <- p:
	default:
		if s.tracer != nil {
			s.tracer.DroppedPacket(logging.PacketTypeNotDetermined, p.Size(), logging.PacketDropDOSPrevention)
		}
	}
}

func (s *session) handleConnectionCloseFrame(frame *wire.ConnectionCloseFrame) {
	var e error
	if frame.IsApplicationError {
		e = qerr.NewApplicationError(frame.ErrorCode, frame.ReasonPhrase)
	} else {
		e = qerr.NewError(frame.ErrorCode, frame.ReasonPhrase)
	}
	s.closeRemote(e)
}

func (s *session) handleCryptoFrame(frame *wire.CryptoFrame, encLevel protocol.EncryptionLevel) error {
	encLevelChanged, err := s.cryptoStreamManager.HandleCryptoFrame(frame, encLevel)
	if err != nil {
		return err
	}
	if encLevelChanged {
		s.tryDecryptingQueuedPackets()
	}
	return nil
}

func (s *session) handleStreamFrame(frame *wire.StreamFrame) error {
	str, err := s.streamsMap.GetOrOpenReceiveStream(frame.StreamID)
	if err != nil {
		return err
	}
	if str == nil {
		// Stream is closed and already garbage collected
		// ignore this StreamFrame
		return nil
	}
	return str.handleStreamFrame(frame)
}

func (s *session) handleMaxDataFrame(frame *wire.MaxDataFrame) {
	s.connFlowController.UpdateSendWindow(frame.MaximumData)
}

func (s *session) handleMaxStreamDataFrame(frame *wire.MaxStreamDataFrame) error {
	str, err := s.streamsMap.GetOrOpenSendStream(frame.StreamID)
	if err != nil {
		return err
	}
	if str == nil {
		// stream is closed and already garbage collected
		return nil
	}
	str.handleMaxStreamDataFrame(frame)
	return nil
}

func (s *session) handleMaxStreamsFrame(frame *wire.MaxStreamsFrame) error {
	return s.streamsMap.HandleMaxStreamsFrame(frame)
}

func (s *session) handleResetStreamFrame(frame *wire.ResetStreamFrame) error {
	str, err := s.streamsMap.GetOrOpenReceiveStream(frame.StreamID)
	if err != nil {
		return err
	}
	if str == nil {
		// stream is closed and already garbage collected
		return nil
	}
	return str.handleResetStreamFrame(frame)
}

func (s *session) handleStopSendingFrame(frame *wire.StopSendingFrame) error {
	str, err := s.streamsMap.GetOrOpenSendStream(frame.StreamID)
	if err != nil {
		return err
	}
	if str == nil {
		// stream is closed and already garbage collected
		return nil
	}
	str.handleStopSendingFrame(frame)
	return nil
}

func (s *session) handlePathChallengeFrame(frame *wire.PathChallengeFrame) {
	s.queueControlFrame(&wire.PathResponseFrame{Data: frame.Data})
}

func (s *session) handleNewTokenFrame(frame *wire.NewTokenFrame) error {
	if s.perspective == protocol.PerspectiveServer {
		return qerr.NewError(qerr.ProtocolViolation, "Received NEW_TOKEN frame from the client.")
	}
	if s.config.TokenStore != nil {
		s.config.TokenStore.Put(s.tokenStoreKey, &ClientToken{data: frame.Token})
	}
	return nil
}

func (s *session) handleNewConnectionIDFrame(f *wire.NewConnectionIDFrame) error {
	return s.connIDManager.Add(f)
}

func (s *session) handleRetireConnectionIDFrame(f *wire.RetireConnectionIDFrame, destConnID protocol.ConnectionID) error {
	return s.connIDGenerator.Retire(f.SequenceNumber, destConnID)
}

func (s *session) handleHandshakeDoneFrame() error {
	if s.perspective == protocol.PerspectiveServer {
		return qerr.NewError(qerr.ProtocolViolation, "received a HANDSHAKE_DONE frame")
	}
	s.handshakeConfirmed = true
	s.sentPacketHandler.SetHandshakeConfirmed()
	s.cryptoStreamHandler.SetHandshakeConfirmed()
	return nil
}

func (s *session) handleAckFrame(frame *wire.AckFrame, encLevel protocol.EncryptionLevel) error {
	if err := s.sentPacketHandler.ReceivedAck(frame, encLevel, s.lastPacketReceivedTime); err != nil {
		return err
	}
	if encLevel != protocol.Encryption1RTT {
		return nil
	}
	return s.cryptoStreamHandler.SetLargest1RTTAcked(frame.LargestAcked())
}

func (s *session) handleDatagramFrame(f *wire.DatagramFrame) error {
	if f.Length(s.version) > protocol.MaxDatagramFrameSize {
		return qerr.NewError(qerr.ProtocolViolation, "DATAGRAM frame too large")
	}
	s.datagramQueue.HandleDatagramFrame(f)
	return nil
}

// closeLocal closes the session and send a CONNECTION_CLOSE containing the error
func (s *session) closeLocal(e error) {
	s.closeOnce.Do(func() {
		if e == nil {
			s.logger.Infof("Closing session.")
		} else {
			s.logger.Errorf("Closing session with error: %s", e)
		}
		s.closeChan <- closeError{err: e, immediate: false, remote: false}
	})
}

// destroy closes the session without sending the error on the wire
func (s *session) destroy(e error) {
	s.destroyImpl(e)
	<-s.ctx.Done()
}

func (s *session) destroyImpl(e error) {
	s.closeOnce.Do(func() {
		if nerr, ok := e.(net.Error); ok && nerr.Timeout() {
			s.logger.Errorf("Destroying session: %s", e)
		} else {
			s.logger.Errorf("Destroying session with error: %s", e)
		}
		s.closeChan <- closeError{err: e, immediate: true, remote: false}
	})
}

func (s *session) closeRemote(e error) {
	s.closeOnce.Do(func() {
		s.logger.Errorf("Peer closed session with error: %s", e)
		s.closeChan <- closeError{err: e, immediate: true, remote: true}
	})
}

// Close the connection. It sends a NO_ERROR application error.
// It waits until the run loop has stopped before returning
func (s *session) shutdown() {
	s.closeLocal(nil)
	<-s.ctx.Done()
}

func (s *session) CloseWithError(code protocol.ApplicationErrorCode, desc string) error {
	s.closeLocal(qerr.NewApplicationError(qerr.ErrorCode(code), desc))
	<-s.ctx.Done()
	return nil
}

func (s *session) handleCloseError(closeErr closeError) {
	if closeErr.err == nil {
		closeErr.err = qerr.NewApplicationError(0, "")
	}

	var quicErr *qerr.QuicError
	var ok bool
	if quicErr, ok = closeErr.err.(*qerr.QuicError); !ok {
		quicErr = qerr.ToQuicError(closeErr.err)
	}

	s.streamsMap.CloseWithError(quicErr)
	s.connIDManager.Close()
	if s.datagramQueue != nil {
		s.datagramQueue.CloseWithError(quicErr)
	}

	if s.tracer != nil {
		// timeout errors are logged as soon as they occur (to distinguish between handshake and idle timeouts)
		if nerr, ok := closeErr.err.(net.Error); !ok || !nerr.Timeout() {
			var resetErr statelessResetErr
			if errors.As(closeErr.err, &resetErr) {
				s.tracer.ClosedConnection(logging.NewStatelessResetCloseReason(resetErr.token))
			} else if quicErr.IsApplicationError() {
				s.tracer.ClosedConnection(logging.NewApplicationCloseReason(quicErr.ErrorCode, closeErr.remote))
			} else {
				s.tracer.ClosedConnection(logging.NewTransportCloseReason(quicErr.ErrorCode, closeErr.remote))
			}
		}
	}

	// If this is a remote close we're done here
	if closeErr.remote {
		s.connIDGenerator.ReplaceWithClosed(newClosedRemoteSession(s.perspective))
		return
	}
	if closeErr.immediate {
		s.connIDGenerator.RemoveAll()
		return
	}
	connClosePacket, err := s.sendConnectionClose(quicErr)
	if err != nil {
		s.logger.Debugf("Error sending CONNECTION_CLOSE: %s", err)
	}
	cs := newClosedLocalSession(s.conn, connClosePacket, s.perspective, s.logger)
	s.connIDGenerator.ReplaceWithClosed(cs)
}

func (s *session) dropEncryptionLevel(encLevel protocol.EncryptionLevel) {
	s.sentPacketHandler.DropPackets(encLevel)
	s.receivedPacketHandler.DropPackets(encLevel)
	if s.tracer != nil {
		s.tracer.DroppedEncryptionLevel(encLevel)
	}
}

// is called for the client, when restoring transport parameters saved for 0-RTT
func (s *session) restoreTransportParameters(params *wire.TransportParameters) {
	if s.logger.Debug() {
		s.logger.Debugf("Restoring Transport Parameters: %s", params)
	}

	s.peerParams = params
	s.connIDGenerator.SetMaxActiveConnIDs(params.ActiveConnectionIDLimit)
	s.connFlowController.UpdateSendWindow(params.InitialMaxData)
	s.streamsMap.UpdateLimits(params)
}

func (s *session) processTransportParameters(params *wire.TransportParameters) {
	if err := s.processTransportParametersImpl(params); err != nil {
		s.closeLocal(err)
	}
}

func (s *session) processTransportParametersImpl(params *wire.TransportParameters) error {
	if s.logger.Debug() {
		s.logger.Debugf("Processed Transport Parameters: %s", params)
	}
	if s.tracer != nil {
		s.tracer.ReceivedTransportParameters(params)
	}

	// check the initial_source_connection_id
	if !params.InitialSourceConnectionID.Equal(s.handshakeDestConnID) {
		return qerr.NewError(qerr.TransportParameterError, fmt.Sprintf("expected initial_source_connection_id to equal %s, is %s", s.handshakeDestConnID, params.InitialSourceConnectionID))
	}

	if s.perspective == protocol.PerspectiveClient {
		// check the original_destination_connection_id
		if !params.OriginalDestinationConnectionID.Equal(s.origDestConnID) {
			return qerr.NewError(qerr.TransportParameterError, fmt.Sprintf("expected original_destination_connection_id to equal %s, is %s", s.origDestConnID, params.OriginalDestinationConnectionID))
		}
		if s.retrySrcConnID != nil { // a Retry was performed
			if params.RetrySourceConnectionID == nil {
				return qerr.NewError(qerr.TransportParameterError, "missing retry_source_connection_id")
			}
			if !(*params.RetrySourceConnectionID).Equal(*s.retrySrcConnID) {
				return qerr.NewError(qerr.TransportParameterError, fmt.Sprintf("expected retry_source_connection_id to equal %s, is %s", s.retrySrcConnID, *params.RetrySourceConnectionID))
			}
		} else if params.RetrySourceConnectionID != nil {
			return qerr.NewError(qerr.TransportParameterError, "received retry_source_connection_id, although no Retry was performed")
		}
	}

	s.peerParams = params
	// Our local idle timeout will always be > 0.
	s.idleTimeout = utils.MinNonZeroDuration(s.config.MaxIdleTimeout, params.MaxIdleTimeout)
	s.keepAliveInterval = utils.MinDuration(s.idleTimeout/2, protocol.MaxKeepAliveInterval)
	s.streamsMap.UpdateLimits(params)
	s.packer.HandleTransportParameters(params)
	s.frameParser.SetAckDelayExponent(params.AckDelayExponent)
	s.connFlowController.UpdateSendWindow(params.InitialMaxData)
	s.rttStats.SetMaxAckDelay(params.MaxAckDelay)
	s.connIDGenerator.SetMaxActiveConnIDs(params.ActiveConnectionIDLimit)
	if params.StatelessResetToken != nil {
		s.connIDManager.SetStatelessResetToken(*params.StatelessResetToken)
	}
	// We don't support connection migration yet, so we don't have any use for the preferred_address.
	if params.PreferredAddress != nil {
		// Retire the connection ID.
		s.connIDManager.AddFromPreferredAddress(params.PreferredAddress.ConnectionID, params.PreferredAddress.StatelessResetToken)
	}
	// On the server side, the early session is ready as soon as we processed
	// the client's transport parameters.
	if s.perspective == protocol.PerspectiveServer {
		close(s.earlySessionReadyChan)
	}
	return nil
}

func (s *session) sendPackets() error {
	s.pacingDeadline = time.Time{}

	var sentPacket bool // only used in for packets sent in send mode SendAny
	for {
		switch sendMode := s.sentPacketHandler.SendMode(); sendMode {
		case ackhandler.SendNone:
			return nil
		case ackhandler.SendAck:
			// If we already sent packets, and the send mode switches to SendAck,
			// as we've just become congestion limited.
			// There's no need to try to send an ACK at this moment.
			if sentPacket {
				return nil
			}
			// We can at most send a single ACK only packet.
			// There will only be a new ACK after receiving new packets.
			// SendAck is only returned when we're congestion limited, so we don't need to set the pacingt timer.
			return s.maybeSendAckOnlyPacket()
		case ackhandler.SendPTOInitial:
			if err := s.sendProbePacket(protocol.EncryptionInitial); err != nil {
				return err
			}
		case ackhandler.SendPTOHandshake:
			if err := s.sendProbePacket(protocol.EncryptionHandshake); err != nil {
				return err
			}
		case ackhandler.SendPTOAppData:
			if err := s.sendProbePacket(protocol.Encryption1RTT); err != nil {
				return err
			}
		case ackhandler.SendAny:
			if s.handshakeComplete && !s.sentPacketHandler.HasPacingBudget() {
				s.pacingDeadline = s.sentPacketHandler.TimeUntilSend()
				return nil
			}
			sent, err := s.sendPacket()
			if err != nil || !sent {
				return err
			}
			sentPacket = true
		default:
			return fmt.Errorf("BUG: invalid send mode %d", sendMode)
		}
	}
}

func (s *session) maybeSendAckOnlyPacket() error {
	packet, err := s.packer.MaybePackAckPacket(s.handshakeConfirmed)
	if err != nil {
		return err
	}
	if packet == nil {
		return nil
	}
	s.sendPackedPacket(packet)
	return nil
}

func (s *session) sendProbePacket(encLevel protocol.EncryptionLevel) error {
	// Queue probe packets until we actually send out a packet,
	// or until there are no more packets to queue.
	var packet *packedPacket
	for {
		if wasQueued := s.sentPacketHandler.QueueProbePacket(encLevel); !wasQueued {
			break
		}
		var err error
		packet, err = s.packer.MaybePackProbePacket(encLevel)
		if err != nil {
			return err
		}
		if packet != nil {
			break
		}
	}
	if packet == nil {
		//nolint:exhaustive // Cannot send probe packets for 0-RTT.
		switch encLevel {
		case protocol.EncryptionInitial:
			s.retransmissionQueue.AddInitial(&wire.PingFrame{})
		case protocol.EncryptionHandshake:
			s.retransmissionQueue.AddHandshake(&wire.PingFrame{})
		case protocol.Encryption1RTT:
			s.retransmissionQueue.AddAppData(&wire.PingFrame{})
		default:
			panic("unexpected encryption level")
		}
		var err error
		packet, err = s.packer.MaybePackProbePacket(encLevel)
		if err != nil {
			return err
		}
	}
	if packet == nil || packet.packetContents == nil {
		return fmt.Errorf("session BUG: couldn't pack %s probe packet", encLevel)
	}
	s.sendPackedPacket(packet)
	return nil
}

func (s *session) sendPacket() (bool, error) {
	if isBlocked, offset := s.connFlowController.IsNewlyBlocked(); isBlocked {
		s.framer.QueueControlFrame(&wire.DataBlockedFrame{MaximumData: offset})
	}
	s.windowUpdateQueue.QueueAll()

	if !s.handshakeConfirmed {
		now := time.Now()
		packet, err := s.packer.PackCoalescedPacket()
		if err != nil || packet == nil {
			return false, err
		}
		s.logCoalescedPacket(packet)
		for _, p := range packet.packets {
			if s.firstAckElicitingPacketAfterIdleSentTime.IsZero() && p.IsAckEliciting() {
				s.firstAckElicitingPacketAfterIdleSentTime = now
			}
			s.sentPacketHandler.SentPacket(p.ToAckHandlerPacket(now, s.retransmissionQueue))
		}
		s.connIDManager.SentPacket()
		s.sendQueue.Send(packet.buffer)
		return true, nil
	}
	packet, err := s.packer.PackPacket()
	if err != nil || packet == nil {
		return false, err
	}
	s.sendPackedPacket(packet)
	return true, nil
}

func (s *session) sendPackedPacket(packet *packedPacket) {
	now := time.Now()
	if s.firstAckElicitingPacketAfterIdleSentTime.IsZero() && packet.IsAckEliciting() {
		s.firstAckElicitingPacketAfterIdleSentTime = now
	}
	s.logPacket(packet)
	s.sentPacketHandler.SentPacket(packet.ToAckHandlerPacket(now, s.retransmissionQueue))
	s.connIDManager.SentPacket()
	s.sendQueue.Send(packet.buffer)
}

func (s *session) sendConnectionClose(quicErr *qerr.QuicError) ([]byte, error) {
	packet, err := s.packer.PackConnectionClose(quicErr)
	if err != nil {
		return nil, err
	}
	s.logCoalescedPacket(packet)
	return packet.buffer.Data, s.conn.Write(packet.buffer.Data)
}

func (s *session) logPacketContents(p *packetContents) {
	// tracing
	if s.tracer != nil {
		frames := make([]logging.Frame, 0, len(p.frames))
		for _, f := range p.frames {
			frames = append(frames, logutils.ConvertFrame(f.Frame))
		}
		s.tracer.SentPacket(p.header, p.length, p.ack, frames)
	}

	// quic-go logging
	if !s.logger.Debug() {
		return
	}
	p.header.Log(s.logger)
	if p.ack != nil {
		wire.LogFrame(s.logger, p.ack, true)
	}
	for _, frame := range p.frames {
		wire.LogFrame(s.logger, frame.Frame, true)
	}
}

func (s *session) logCoalescedPacket(packet *coalescedPacket) {
	if s.logger.Debug() {
		if len(packet.packets) > 1 {
			s.logger.Debugf("-> Sending coalesced packet (%d parts, %d bytes) for connection %s", len(packet.packets), packet.buffer.Len(), s.logID)
		} else {
			s.logger.Debugf("-> Sending packet %d (%d bytes) for connection %s, %s", packet.packets[0].header.PacketNumber, packet.buffer.Len(), s.logID, packet.packets[0].EncryptionLevel())
		}
	}
	for _, p := range packet.packets {
		s.logPacketContents(p)
	}
}

func (s *session) logPacket(packet *packedPacket) {
	if s.logger.Debug() {
		s.logger.Debugf("-> Sending packet %d (%d bytes) for connection %s, %s", packet.header.PacketNumber, packet.buffer.Len(), s.logID, packet.EncryptionLevel())
	}
	s.logPacketContents(packet.packetContents)
}

// AcceptStream returns the next stream openend by the peer
func (s *session) AcceptStream(ctx context.Context) (Stream, error) {
	return s.streamsMap.AcceptStream(ctx)
}

func (s *session) AcceptUniStream(ctx context.Context) (ReceiveStream, error) {
	return s.streamsMap.AcceptUniStream(ctx)
}

// OpenStream opens a stream
func (s *session) OpenStream() (Stream, error) {
	return s.streamsMap.OpenStream()
}

func (s *session) OpenStreamSync(ctx context.Context) (Stream, error) {
	return s.streamsMap.OpenStreamSync(ctx)
}

func (s *session) OpenUniStream() (SendStream, error) {
	return s.streamsMap.OpenUniStream()
}

func (s *session) OpenUniStreamSync(ctx context.Context) (SendStream, error) {
	return s.streamsMap.OpenUniStreamSync(ctx)
}

func (s *session) newFlowController(id protocol.StreamID) flowcontrol.StreamFlowController {
	initialSendWindow := s.peerParams.InitialMaxStreamDataUni
	if id.Type() == protocol.StreamTypeBidi {
		if id.InitiatedBy() == s.perspective {
			initialSendWindow = s.peerParams.InitialMaxStreamDataBidiRemote
		} else {
			initialSendWindow = s.peerParams.InitialMaxStreamDataBidiLocal
		}
	}
	return flowcontrol.NewStreamFlowController(
		id,
		s.connFlowController,
		protocol.InitialMaxStreamData,
		protocol.ByteCount(s.config.MaxReceiveStreamFlowControlWindow),
		initialSendWindow,
		s.onHasStreamWindowUpdate,
		s.rttStats,
		s.logger,
	)
}

// scheduleSending signals that we have data for sending
func (s *session) scheduleSending() {
	select {
	case s.sendingScheduled <- struct{}{}:
	default:
	}
}

func (s *session) tryQueueingUndecryptablePacket(p *receivedPacket, hdr *wire.Header) {
	if s.handshakeComplete {
		panic("shouldn't queue undecryptable packets after handshake completion")
	}
	if len(s.undecryptablePackets)+1 > protocol.MaxUndecryptablePackets {
		if s.tracer != nil {
			s.tracer.DroppedPacket(logging.PacketTypeFromHeader(hdr), p.Size(), logging.PacketDropDOSPrevention)
		}
		s.logger.Infof("Dropping undecryptable packet (%d bytes). Undecryptable packet queue full.", p.Size())
		return
	}
	s.logger.Infof("Queueing packet (%d bytes) for later decryption", p.Size())
	if s.tracer != nil {
		s.tracer.BufferedPacket(logging.PacketTypeFromHeader(hdr))
	}
	s.undecryptablePackets = append(s.undecryptablePackets, p)
}

func (s *session) tryDecryptingQueuedPackets() {
	for _, p := range s.undecryptablePackets {
		s.handlePacket(p)
	}
	s.undecryptablePackets = s.undecryptablePackets[:0]
}

func (s *session) queueControlFrame(f wire.Frame) {
	s.framer.QueueControlFrame(f)
	s.scheduleSending()
}

func (s *session) onHasStreamWindowUpdate(id protocol.StreamID) {
	s.windowUpdateQueue.AddStream(id)
	s.scheduleSending()
}

func (s *session) onHasConnectionWindowUpdate() {
	s.windowUpdateQueue.AddConnection()
	s.scheduleSending()
}

func (s *session) onHasStreamData(id protocol.StreamID) {
	s.framer.AddActiveStream(id)
	s.scheduleSending()
}

func (s *session) onStreamCompleted(id protocol.StreamID) {
	if err := s.streamsMap.DeleteStream(id); err != nil {
		s.closeLocal(err)
	}
}

func (s *session) SendMessage(p []byte) error {
	f := &wire.DatagramFrame{DataLenPresent: true}
	if protocol.ByteCount(len(p)) > f.MaxDataLen(s.peerParams.MaxDatagramFrameSize, s.version) {
		return errors.New("message too large")
	}
	f.Data = make([]byte, len(p))
	copy(f.Data, p)
	s.datagramQueue.AddAndWait(f)
	return nil
}

func (s *session) ReceiveMessage() ([]byte, error) {
	return s.datagramQueue.Receive()
}

func (s *session) LocalAddr() net.Addr {
	return s.conn.LocalAddr()
}

func (s *session) RemoteAddr() net.Addr {
	return s.conn.RemoteAddr()
}

func (s *session) getPerspective() protocol.Perspective {
	return s.perspective
}

func (s *session) GetVersion() protocol.VersionNumber {
	return s.version
}
