package ws

import (
	"time"

	"github.com/gorilla/websocket"
)

// The connection details
type connection struct {
	conn *websocket.Conn
	done chan struct{}
}

// Safely access the connection and the connection channel
func (c *Client) connection() *connection {
	return c.conn.Load().(*connection)
}

// Update the settings of the new WebSocket connection and store it
func (c *Client) store(conn *websocket.Conn) {
	// Load the saved connection settings
	if c.enableWriteCompression != nil {
		conn.EnableWriteCompression(*c.enableWriteCompression)
	}
	if c.compressionLevel != nil {
		if err := conn.SetCompressionLevel(*c.compressionLevel); err != nil && c.opts.OnSetCompressionLevelError != nil {
			c.opts.OnSetCompressionLevelError(c, conn, err)
		}
	}
	if c.readDeadline != nil {
		if err := conn.SetReadDeadline(*c.readDeadline); err != nil && c.opts.OnSetReadDeadlineError != nil {
			c.opts.OnSetReadDeadlineError(c, conn, err)
		}
	}
	if c.readLimit != nil {
		conn.SetReadLimit(*c.readLimit)
	}
	if c.writeDeadline != nil {
		if err := conn.SetWriteDeadline(*c.writeDeadline); err != nil && c.opts.OnSetWriteDeadlineError != nil {
			c.opts.OnSetWriteDeadlineError(c, conn, err)
		}
	}

	// Load the saved handlers
	conn.SetCloseHandler(c.handleClose)
	conn.SetPingHandler(c.handlePing)

	// Set the pong handler
	conn.SetPongHandler(func(appData string) error {
		// Store the last pong timestamp
		c.pong.Store(time.Now().UnixNano())

		// Call the pong handler if it is available
		if err := c.handlePong.Load().(func(string) error)(appData); err != nil {
			return err
		}

		return nil
	})

	// Acquire a lock
	c.mu.Lock()
	defer c.mu.Unlock()

	// Store the connection
	c.conn.Store(&connection{
		conn: conn,
		done: make(chan struct{}),
	})

	// Switch to the new connection
	c.Conn = conn
}
