package ws

import (
	"context"
	"time"

	"github.com/gorilla/websocket"
)

// The reconnection loop for reconnecting when there is a disconnect
func (c *Client) run() {
	// Start the ticker
	ticker := time.NewTicker(c.opts.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case reconnect := <-c.reconnect:
			// Reconnect if the reader received an error
			reconnect <- c.connect()
		case <-ticker.C:
			// Reconnect if we haven't received a pong message
			if time.Since(c.lastPong()) > c.opts.PongInterval {
				c.connect()
			}

			// Send a ping message, reconnect if it fails
			if err := c.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(c.opts.PingDeadline)); err != nil {
				c.connect()
			}
		case <-c.done:
			return
		}
	}
}

// Reconnect to the server
func (c *Client) connect() bool {
	// Update the connection status
	c.connected.Store(false)

	// Close the old WebSocket connection
	connection := c.connection()
	if err := connection.conn.Close(); err != nil && c.opts.OnCloseError != nil {
		c.opts.OnCloseError(c, c.Conn, err)
	}
	if c.opts.OnDisconnect != nil {
		c.opts.OnDisconnect(c, connection.conn)
	}

	// Close the old connection channel when the reconnect completes
	defer close(connection.done)

	// Attempt to dial with a backoff
	err := c.opts.ReconnectionBackoff(context.Background(), func() error {
		// Dial with a timeout
		ctx, cancel := context.WithTimeout(context.Background(), c.opts.ReconnectionTimeout)
		defer cancel()

		// Dial the WebSocket server
		conn, _, err := c.dialer.DialContext(ctx, c.urlStr, c.requestHeader)
		if err != nil {
			return err
		}

		// Store the new WebSocket connection
		c.store(conn)

		// Update the connection status
		c.connected.Store(true)

		// Update the last pong time
		c.pong.Store(time.Now().UnixNano())

		// Call the connection handler
		if c.opts.OnConnect != nil {
			c.opts.OnConnect(c, conn)
		}

		return nil
	}, nil)

	if err != nil {
		// If the reconnection failed, close the connection
		if err := c.Close(); err != nil && c.opts.OnCloseError != nil {
			c.opts.OnCloseError(c, c.Conn, err)
		}

		// Call the reconnect failure handler
		if c.opts.OnReconnectFailure != nil {
			c.opts.OnReconnectFailure(c, c.Conn, err)
		}

		return false
	}

	return true
}

// The time of the last pong message received
func (c *Client) lastPong() time.Time {
	return time.Unix(0, c.pong.Load())
}
