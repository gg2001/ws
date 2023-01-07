package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/goleak"
)

var testUpgrader = websocket.Upgrader{}

func closeTestClient(t testing.TB, c *Client) {
	if err := c.Close(); err != nil {
		t.Log(err)
	}
}

func testReadPump(t testing.TB, c *Client, messages chan<- []byte) {
	for {
		_, p, err := c.ReadMessage()
		if err != nil {
			t.Log(err)
			return
		}

		messages <- p
	}
}

func testWebSocketServer(t testing.TB, close <-chan struct{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			panic(err)
		}

		defer func() {
			if err := c.Close(); err != nil {
				t.Log(err)
			}
		}()

		go func(ctx context.Context) {
			for {
				select {
				case <-close:
					c.Close()
					return
				case <-ctx.Done():
					return
				}
			}
		}(r.Context())

		for {
			messageType, p, err := c.ReadMessage()
			if err != nil {
				t.Log(err)
				return
			}

			if err := c.WriteMessage(messageType, p); err != nil {
				t.Log(err)
			}
		}
	}
}

func newTestServer(t testing.TB) (
	ctx context.Context,
	cancel context.CancelFunc,
	s *httptest.Server,
	u string,
	close chan<- struct{},
) {
	ctx, cancel = context.WithCancel(context.Background())

	closeCh := make(chan struct{})

	s = httptest.NewServer(testWebSocketServer(t, closeCh))

	u = "ws" + strings.TrimPrefix(s.URL, "http")

	return ctx, cancel, s, u, closeCh
}

func newTestClient(t testing.TB) (
	ctx context.Context,
	cancel context.CancelFunc,
	s *httptest.Server,
	u string,
	close chan<- struct{},
	c *Client,
	messages <-chan []byte,
	onConnect <-chan struct{},
	onDisconnect <-chan struct{},
) {
	ctx, cancel, s, u, close = newTestServer(t)

	onConnectCh := make(chan struct{})
	onDisconnectCh := make(chan struct{})

	c, _, err := DialContextWithOptions(ctx, websocket.DefaultDialer, u, nil, &ClientOpts{
		PingInterval: 100 * time.Millisecond,
		PongInterval: 500 * time.Millisecond,
		OnConnect: func(*Client, *websocket.Conn) {
			onConnectCh <- struct{}{}
		},
		OnDisconnect: func(*Client, *websocket.Conn) {
			onDisconnectCh <- struct{}{}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	messagesCh := make(chan []byte)

	go testReadPump(t, c, messagesCh)

	return ctx, cancel, s, u, close, c, messagesCh, onConnectCh, onDisconnectCh
}

func TestClient(t *testing.T) {
	defer goleak.VerifyNone(t)

	_, cancel, s, u, _ := newTestServer(t)
	defer cancel()
	defer s.Close()

	c, _, err := Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestClient(t, c)

	testMessage := "t"

	// Write and receive a test message
	if err := c.WriteMessage(websocket.TextMessage, []byte(testMessage)); err != nil {
		t.Fatal(err)
	}

	messageType, p, err := c.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}

	if websocket.TextMessage != messageType {
		t.Fatal("unexpected message type:", messageType)
	}
	if pString := string(p); testMessage != pString {
		t.Fatal("unexpected p:", pString)
	}
}

func TestClientDisconnect(t *testing.T) {
	defer goleak.VerifyNone(t)

	_, cancel, s, _, close, c, messages, onConnect, onDisconnect := newTestClient(t)
	defer cancel()
	defer s.Close()

	if connected := c.Connected(); !connected {
		t.Fatal("unexpected connected:", connected)
	}
	if closed := c.Closed(); closed {
		t.Fatal("unexpected closed:", closed)
	}

	testMessage := "t"

	// Write and receive a test message
	if err := c.WriteMessage(websocket.TextMessage, []byte(testMessage)); err != nil {
		t.Fatal(err)
	}

	if message := string(<-messages); testMessage != message {
		t.Fatal("unexpected message:", message)
	}

	// Force close the connection from the server
	close <- struct{}{}

	<-onDisconnect

	if connected := c.Connected(); connected {
		t.Fatal("unexpected connected:", connected)
	}
	if closed := c.Closed(); closed {
		t.Fatal("unexpected closed:", closed)
	}

	<-onConnect

	if connected := c.Connected(); !connected {
		t.Fatal("unexpected connected:", connected)
	}
	if closed := c.Closed(); closed {
		t.Fatal("unexpected closed:", closed)
	}

	// Write and receive a test message
	if err := c.WriteMessage(websocket.TextMessage, []byte(testMessage)); err != nil {
		t.Fatal(err)
	}

	if message := string(<-messages); testMessage != message {
		t.Fatal("unexpected message:", message)
	}

	// Close the connection
	closeTestClient(t, c)

	if connected := c.Connected(); connected {
		t.Fatal("unexpected connected:", connected)
	}
	if closed := c.Closed(); !closed {
		t.Fatal("unexpected closed:", closed)
	}
}

func BenchmarkClient(b *testing.B) {
	_, cancel, s, _, _, c, messages, _, _ := newTestClient(b)
	defer cancel()
	defer s.Close()
	defer closeTestClient(b, c)

	testMessage := "t"

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := c.WriteMessage(websocket.TextMessage, []byte(testMessage)); err != nil {
			b.Fatal(err)
		}

		<-messages
	}
}
