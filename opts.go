package ws

import (
	"errors"
	"time"

	"github.com/gg2001/backoff"
	"github.com/gorilla/websocket"
)

// Default WebSocket client constants
const (
	DefaultReconnectionTimeout = 30 * time.Second
	DefaultPingInterval        = 1 * time.Second
	DefaultPongInterval        = 5 * time.Second
	DefaultPingDeadline        = 10 * time.Second
)

// Default WebSocket client variables
var (
	DefaultReconnectionBackoff = backoff.LinearBackoff(-1, 0)
)

// Errors for invalid client options
var (
	ErrClientOptionReconnectionTimeoutInvalid = errors.New("ws: client option ReconnectionTimeout invalid")
	ErrClientOptionPingIntervalInvalid        = errors.New("ws: client option PingInterval invalid")
	ErrClientOptionPongIntervalInvalid        = errors.New("ws: client option PongInterval invalid")
	ErrClientOptionPingDeadlineInvalid        = errors.New("ws: client option PingDeadline invalid")
)

// Handler to be called when there is a connection or disconnection
type ConnectionHandler func(c *Client, conn *websocket.Conn)

// Client error handler function type
type ClientErrorHandler func(c *Client, conn *websocket.Conn, err error)

// WebSocket client options
type ClientOpts struct {
	ReconnectionTimeout        time.Duration
	ReconnectionBackoff        backoff.Backoff
	PingInterval               time.Duration
	PongInterval               time.Duration
	PingDeadline               time.Duration
	OnConnect                  ConnectionHandler
	OnDisconnect               ConnectionHandler
	OnReaderError              ClientErrorHandler
	OnReconnectFailure         ClientErrorHandler
	OnCloseError               ClientErrorHandler
	OnSetCompressionLevelError ClientErrorHandler
	OnSetReadDeadlineError     ClientErrorHandler
	OnSetWriteDeadlineError    ClientErrorHandler
}

// Get the Default WebSocket client options
func DefaultOpts() *ClientOpts {
	return &ClientOpts{
		ReconnectionTimeout: DefaultReconnectionTimeout,
	}
}

// Override the missing options and validate them, panic if invalid
func checkOpts(opts *ClientOpts) {
	if opts.ReconnectionTimeout == 0 {
		opts.ReconnectionTimeout = DefaultReconnectionTimeout
	}
	if opts.ReconnectionBackoff == nil {
		opts.ReconnectionBackoff = DefaultReconnectionBackoff
	}
	if opts.PingInterval == 0 {
		opts.PingInterval = DefaultPingInterval
	}
	if opts.PongInterval == 0 {
		opts.PongInterval = DefaultPongInterval
	}
	if opts.PingDeadline == 0 {
		opts.PingDeadline = DefaultPingDeadline
	}

	if opts.ReconnectionTimeout < 0 {
		panic(ErrClientOptionReconnectionTimeoutInvalid)
	}
	if opts.PingInterval < 0 {
		panic(ErrClientOptionReconnectionTimeoutInvalid)
	}
	if opts.PongInterval < 0 || opts.PongInterval <= opts.PingInterval {
		panic(ErrClientOptionReconnectionTimeoutInvalid)
	}
	if opts.PingDeadline < 0 {
		panic(ErrClientOptionReconnectionTimeoutInvalid)
	}
}
