package ws

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocket client
type Client struct {
	urlStr        string
	requestHeader http.Header
	dialer        *websocket.Dialer
	opts          *ClientOpts

	enableWriteCompression *bool
	compressionLevel       *int
	readDeadline           *time.Time
	readLimit              *int64
	writeDeadline          *time.Time
	handlePing             func(string) error
	handleClose            func(int, string) error

	conn       atomic.Value
	handlePong atomic.Value
	pong       atomic.Int64
	connected  atomic.Bool
	closed     atomic.Bool
	close      sync.Once
	mu         sync.Mutex
	reconnect  chan chan<- bool
	done       chan struct{}

	*websocket.Conn
}

// Dial creates a new client connection by calling DialContext with a background context
func Dial(urlStr string, requestHeader http.Header) (*Client, *http.Response, error) {
	return DialContext(context.Background(), urlStr, requestHeader)
}

// DialContext creates a new client connection. Use requestHeader to specify the
// origin (Origin), subprotocols (Sec-WebSocket-Protocol) and cookies (Cookie).
// Use the response.Header to get the selected subprotocol
// (Sec-WebSocket-Protocol) and cookies (Set-Cookie).
//
// The context will be used in the request and in the Dialer.
//
// If the WebSocket handshake fails, ErrBadHandshake is returned along with a
// non-nil *http.Response so that callers can handle redirects, authentication,
// etcetera. The response body may not contain the entire response and does not
// need to be closed by the application.
func DialContext(ctx context.Context, urlStr string, requestHeader http.Header) (*Client, *http.Response, error) {
	return DialContextWithDialer(ctx, websocket.DefaultDialer, urlStr, requestHeader)
}

// DialContextWithDialer
func DialContextWithDialer(
	ctx context.Context,
	dialer *websocket.Dialer,
	urlStr string,
	requestHeader http.Header,
) (*Client, *http.Response, error) {
	return DialContextWithOptions(ctx, dialer, urlStr, requestHeader, nil)
}

// DialContextWithOptions
func DialContextWithOptions(
	ctx context.Context,
	dialer *websocket.Dialer,
	urlStr string,
	requestHeader http.Header,
	opts *ClientOpts,
) (*Client, *http.Response, error) {
	// Dial to the WebSocket URL
	conn, resp, err := dialer.DialContext(ctx, urlStr, requestHeader)
	if err != nil {
		return nil, resp, err
	}

	// Load the default options if not provided
	if opts == nil {
		opts = DefaultOpts()
	}
	checkOpts(opts)

	// Create the client
	c := &Client{
		urlStr:        urlStr,
		requestHeader: requestHeader,
		dialer:        dialer,
		opts:          opts,

		done: make(chan struct{}),
	}

	// Set the default values
	c.SetPongHandler(nil)
	c.pong.Store(time.Now().UnixNano())
	c.connected.Store(true)
	c.closed.Store(false)

	// Store the connection
	c.store(conn)

	// Start the reconnection process
	go c.run()

	return c, resp, nil
}

// Safely access the WebSocket connection
func (c *Client) Connection() *websocket.Conn {
	return c.conn.Load().(*connection).conn
}

// Reconnect to the WebSocket server
func (c *Client) Reconnect(ctx context.Context) <-chan bool {
	// Receive the reconnection response
	reconnect := make(chan bool, 1)

	// Reconnect
	select {
	case c.reconnect <- reconnect:
		return reconnect
	case <-c.done:
	case <-ctx.Done():
	}

	// Failed to reconnect
	reconnect <- false
	return reconnect
}

// Returns true if we are connected to the WebSocket conneection
func (c *Client) Connected() bool {
	return c.connected.Load()
}

// Returns true if the connection is closed
func (c *Client) Closed() bool {
	return c.closed.Load()
}

// Close closes the underlying network connection without sending or waiting
// for a close message.
func (c *Client) Close() error {
	// Update the connected and closed status
	c.connected.Store(false)
	c.closed.Store(true)

	c.close.Do(func() {
		// Close the reconnection goroutine
		close(c.done)
	})

	return c.Connection().Close()
}

// CloseHandler returns the current close handler
func (c *Client) CloseHandler() func(code int, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.Conn.CloseHandler()
}

// EnableWriteCompression enables and disables write compression of
// subsequent text and binary messages. This function is a noop if
// compression was not negotiated with the peer.
func (c *Client) EnableWriteCompression(enable bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Enable write compression
	c.Conn.EnableWriteCompression(enable)

	// Save the setting
	c.enableWriteCompression = &enable
}

// LocalAddr returns the local network address.
func (c *Client) LocalAddr() net.Addr {
	return c.Connection().LocalAddr()
}

// NextReader returns the next data message received from the peer. The
// returned messageType is either TextMessage or BinaryMessage.
//
// There can be at most one open reader on a connection. NextReader discards
// the previous message if the application has not already consumed it.
//
// Applications must break out of the application's read loop when this method
// returns a non-nil error value. Errors returned from this method are
// permanent. Once this method returns a non-nil error, all subsequent calls to
// this method return the same error.
func (c *Client) NextReader() (messageType int, r io.Reader, err error) {
	for {
		// Get the connection
		connection := c.connection()

		// Return the next data message received from the peer
		messageType, r, err := connection.conn.NextReader()

		// Only return the reader if there is no error or our connection is closed
		if err == nil || c.Closed() {
			return messageType, r, err
		}

		// The error handler
		if c.opts.OnReaderError != nil {
			c.opts.OnReaderError(c, connection.conn, err)
		}

		// Receive the reconnection response
		reconnect := make(chan bool, 1)

		// If we receive an error, notify the reconnection process or wait on the connection channel
		select {
		case c.reconnect <- reconnect:
			select {
			case <-reconnect:
			case <-connection.done:
			case <-c.done:
			}
		case <-connection.done:
		case <-c.done:
		}
	}
}

// NextWriter returns a writer for the next message to send. The writer's Close
// method flushes the complete message to the network.
//
// There can be at most one open writer on a connection. NextWriter closes the
// previous writer if the application has not already done so.
//
// All message types (TextMessage, BinaryMessage, CloseMessage, PingMessage and
// PongMessage) are supported.
func (c *Client) NextWriter(messageType int) (io.WriteCloser, error) {
	return c.Connection().NextWriter(messageType)
}

// PingHandler returns the current ping handler
func (c *Client) PingHandler() func(appData string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.Conn.PingHandler()
}

// PongHandler returns the current pong handler
func (c *Client) PongHandler() func(appData string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.Conn.PongHandler()
}

// ReadJSON reads the next JSON-encoded message from the connection and stores
// it in the value pointed to by v.
//
// See the documentation for the encoding/json Unmarshal function for details
// about the conversion of JSON to a Go value.
func (c *Client) ReadJSON(v interface{}) error {
	return c.Connection().ReadJSON(v)
}

// ReadMessage is a helper method for getting a reader using NextReader and
// reading from that reader to a buffer.
func (c *Client) ReadMessage() (messageType int, p []byte, err error) {
	var r io.Reader
	messageType, r, err = c.NextReader()
	if err != nil {
		return messageType, nil, err
	}
	p, err = io.ReadAll(r)
	return messageType, p, err
}

// RemoteAddr returns the remote network address.
func (c *Client) RemoteAddr() net.Addr {
	return c.Connection().RemoteAddr()
}

// SetCloseHandler sets the handler for close messages received from the peer.
// The code argument to h is the received close code or CloseNoStatusReceived
// if the close message is empty. The default close handler sends a close
// message back to the peer.
//
// The handler function is called from the NextReader, ReadMessage and message
// reader Read methods. The application must read the connection to process
// close messages as described in the section on Control Messages above.
//
// The connection read methods return a CloseError when a close message is
// received. Most applications should handle close messages as part of their
// normal error handling. Applications should only set a close handler when the
// application must perform some action before sending a close message back to
// the peer.
func (c *Client) SetCloseHandler(h func(code int, text string) error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Set the close handler
	c.Conn.SetCloseHandler(h)

	// Save the setting
	c.handleClose = h
}

// SetCompressionLevel sets the flate compression level for subsequent text and
// binary messages. This function is a noop if compression was not negotiated
// with the peer. See the compress/flate package for a description of
// compression levels.
func (c *Client) SetCompressionLevel(level int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Set the close handler
	if err := c.Conn.SetCompressionLevel(level); err != nil {
		return err
	}

	// Save the setting
	c.compressionLevel = &level

	return nil
}

// SetPingHandler sets the handler for ping messages received from the peer.
// The appData argument to h is the PING message application data. The default
// ping handler sends a pong to the peer.
//
// The handler function is called from the NextReader, ReadMessage and message
// reader Read methods. The application must read the connection to process
// ping messages as described in the section on Control Messages above.
func (c *Client) SetPingHandler(h func(appData string) error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Set the ping handler
	c.Conn.SetPingHandler(h)

	// Save the setting
	c.handlePing = h
}

// SetPongHandler sets the handler for pong messages received from the peer.
// The appData argument to h is the PONG message application data. The default
// pong handler does nothing.
//
// The handler function is called from the NextReader, ReadMessage and message
// reader Read methods. The application must read the connection to process
// pong messages as described in the section on Control Messages above.
func (c *Client) SetPongHandler(h func(appData string) error) {
	// Disallow nil handlers
	if h == nil {
		h = func(string) error { return nil }
	}

	// Save the setting
	c.handlePong.Store(h)
}

// SetReadDeadline sets the read deadline on the underlying network connection.
// After a read has timed out, the websocket connection state is corrupt and
// all future reads will return an error. A zero value for t means reads will
// not time out.
func (c *Client) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.Conn.SetReadDeadline(t); err != nil {
		return err
	}

	// Save the setting
	c.readDeadline = &t

	return nil
}

// SetReadLimit sets the maximum size in bytes for a message read from the peer. If a
// message exceeds the limit, the connection sends a close message to the peer
// and returns ErrReadLimit to the application.
func (c *Client) SetReadLimit(limit int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.Conn.SetReadLimit(limit)

	// Save the setting
	c.readLimit = &limit
}

// SetWriteDeadline sets the write deadline on the underlying network
// connection. After a write has timed out, the websocket state is corrupt and
// all future writes will return an error. A zero value for t means writes will
// not time out.
func (c *Client) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.Conn.SetWriteDeadline(t); err != nil {
		return err
	}

	// Save the setting
	c.writeDeadline = &t

	return nil
}

// Subprotocol returns the negotiated protocol for the connection.
func (c *Client) Subprotocol() string {
	return c.Connection().Subprotocol()
}

// UnderlyingConn returns the internal net.Conn. This can be used to further
// modifications to connection specific flags.
func (c *Client) UnderlyingConn() net.Conn {
	return c.Connection().UnderlyingConn()
}

// WriteControl writes a control message with the given deadline. The allowed
// message types are CloseMessage, PingMessage and PongMessage.
func (c *Client) WriteControl(messageType int, data []byte, deadline time.Time) error {
	return c.Connection().WriteControl(messageType, data, deadline)
}

// WriteJSON writes the JSON encoding of v as a message.
//
// See the documentation for encoding/json Marshal for details about the
// conversion of Go values to JSON.
func (c *Client) WriteJSON(v interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.Conn.WriteJSON(v)
}

// WriteMessage is a helper method for getting a writer using NextWriter,
// writing the message and closing the writer.
func (c *Client) WriteMessage(messageType int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.Conn.WriteMessage(messageType, data)
}

// WritePreparedMessage writes prepared message into connection.
func (c *Client) WritePreparedMessage(pm *websocket.PreparedMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.Conn.WritePreparedMessage(pm)
}
