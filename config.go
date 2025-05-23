package quic

import (
	"errors"
	"time"

	"github.com/For-ACGN/quic-go/internal/utils"

	"github.com/For-ACGN/quic-go/internal/protocol"
)

// Clone clones a Config
func (c *Config) Clone() *Config {
	copy := *c
	return &copy
}

func (c *Config) handshakeTimeout() time.Duration {
	return utils.MaxDuration(protocol.DefaultHandshakeTimeout, 2*c.HandshakeIdleTimeout)
}

func validateConfig(config *Config) error {
	if config == nil {
		return nil
	}
	if config.MaxIncomingStreams > 1<<60 {
		return errors.New("invalid value for Config.MaxIncomingStreams")
	}
	if config.MaxIncomingUniStreams > 1<<60 {
		return errors.New("invalid value for Config.MaxIncomingUniStreams")
	}
	return nil
}

// populateServerConfig populates fields in the quic.Config with their default values, if none are set
// it may be called with nil
func populateServerConfig(config *Config) *Config {
	config = populateConfig(config)
	if config.ConnectionIDLength == 0 {
		config.ConnectionIDLength = protocol.DefaultConnectionIDLength
	}
	if config.AcceptToken == nil {
		config.AcceptToken = defaultAcceptToken
	}
	return config
}

// populateClientConfig populates fields in the quic.Config with their default values, if none are set
// it may be called with nil
func populateClientConfig(config *Config, createdPacketConn bool) *Config {
	config = populateConfig(config)
	if config.ConnectionIDLength == 0 && !createdPacketConn {
		config.ConnectionIDLength = protocol.DefaultConnectionIDLength
	}
	return config
}

func populateConfig(config *Config) *Config {
	if config == nil {
		config = &Config{}
	}
	versions := config.Versions
	if len(versions) == 0 {
		versions = protocol.SupportedVersions
	}
	handshakeIdleTimeout := protocol.DefaultHandshakeIdleTimeout
	if config.HandshakeIdleTimeout != 0 {
		handshakeIdleTimeout = config.HandshakeIdleTimeout
	}
	idleTimeout := protocol.DefaultIdleTimeout
	if config.MaxIdleTimeout != 0 {
		idleTimeout = config.MaxIdleTimeout
	}
	maxReceiveStreamFlowControlWindow := config.MaxReceiveStreamFlowControlWindow
	if maxReceiveStreamFlowControlWindow == 0 {
		maxReceiveStreamFlowControlWindow = protocol.DefaultMaxReceiveStreamFlowControlWindow
	}
	maxReceiveConnectionFlowControlWindow := config.MaxReceiveConnectionFlowControlWindow
	if maxReceiveConnectionFlowControlWindow == 0 {
		maxReceiveConnectionFlowControlWindow = protocol.DefaultMaxReceiveConnectionFlowControlWindow
	}
	maxIncomingStreams := config.MaxIncomingStreams
	if maxIncomingStreams == 0 {
		maxIncomingStreams = protocol.DefaultMaxIncomingStreams
	} else if maxIncomingStreams < 0 {
		maxIncomingStreams = 0
	}
	maxIncomingUniStreams := config.MaxIncomingUniStreams
	if maxIncomingUniStreams == 0 {
		maxIncomingUniStreams = protocol.DefaultMaxIncomingUniStreams
	} else if maxIncomingUniStreams < 0 {
		maxIncomingUniStreams = 0
	}

	return &Config{
		Versions:                              versions,
		HandshakeIdleTimeout:                  handshakeIdleTimeout,
		MaxIdleTimeout:                        idleTimeout,
		AcceptToken:                           config.AcceptToken,
		KeepAlive:                             config.KeepAlive,
		MaxReceiveStreamFlowControlWindow:     maxReceiveStreamFlowControlWindow,
		MaxReceiveConnectionFlowControlWindow: maxReceiveConnectionFlowControlWindow,
		MaxIncomingStreams:                    maxIncomingStreams,
		MaxIncomingUniStreams:                 maxIncomingUniStreams,
		ConnectionIDLength:                    config.ConnectionIDLength,
		StatelessResetKey:                     config.StatelessResetKey,
		TokenStore:                            config.TokenStore,
		EnableDatagrams:                       config.EnableDatagrams,
		Tracer:                                config.Tracer,
	}
}
