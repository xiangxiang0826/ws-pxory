package internal

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512

	// send buffer size
	bufSize = 256
)

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// var tcpSer = map[string]string{
// 	"host": "47.112.120.88",
// 	"port": "8989",
// }

// Client is a middleman between the websocket connection and the hub.
type Client struct {
	hub *Hub

	// The websocket connection.
	conn *websocket.Conn

	// Buffered channel of outbound messages.
	send chan []byte

	// The Tcp connection
	tcpConn net.Conn

	// The sync chann
	tcpSync chan bool
}

// readPump pumps messages from the websocket connection to the hub.
//
// The application runs readPump in a per-connection goroutine. The application
// ensures that there is at most one reader on a connection by executing all
// reads from this goroutine.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
		c.tcpConn.Close()
		c.tcpSync <- true
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
				log.Println("error: IsUnexpectedCloseError end")
				c.hub.unregister <- c
				c.conn.Close()
				c.tcpConn.Close()
				c.tcpSync <- true
			}
			break
		}
		// message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))
		fmt.Printf("发送数据是：%v \n", message)
		// message = sendTcp(bytes.TrimSpace(bytes.Replace(message, newline, space, -1)))
		// message = sendTcp(message)

		_, err = c.tcpConn.Write(message)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Fatal error: %s", err.Error())
			// os.Exit(1)
		}
	}
}

// writePump pumps messages from the hub to the websocket connection.
//
// A goroutine running writePump is started for each connection. The
// application ensures that there is at most one writer to a connection by
// executing all writes from this goroutine.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		fmt.Println("ticker time up")
		ticker.Stop()
		c.conn.Close()
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		result := make([]byte, 1024)
	j:
		for {
			select {
			case _ = <-c.tcpSync:
				fmt.Println("tcpSync msg")
				break j
			default:
			}
			_, err := c.tcpConn.Read(result)
			if err != nil {
				fmt.Println("error reading message", err.Error())
				// c.tcpConn, _ = tcpTryConnect("47.112.120.88:8989")
			} else {
				c.send <- result
			}
		}
		wg.Done()

		defer func() {
			fmt.Println("tcpSync 线程退出")
		}()
	}()
	wg.Wait()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				fmt.Println("hub 关闭连接")
				return
			}

			w, err := c.conn.NextWriter(websocket.BinaryMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued chat messages to the current websocket message.
			n := len(c.send)
			for i := 0; i < n; i++ {
				// w.Write(newline)
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// Try to connect as much as possible, max 3 times
func tcpTryConnect(tcpstring string) (net.Conn, error) {
	var retry = 0
	var err = errors.New("tcp svr cannot connect")
	var conn net.Conn

	fmt.Println("服务器等待客户端建立连接...")
	for retry < 3 {
		conn, err = net.Dial("tcp", tcpstring)
		if err == nil {
			fmt.Println("服务器与客户端连接成功...")
			break
		}
		retry++
		fmt.Printf("tcp connect times %d", retry)
	}
	return conn, err
}

// ServeWs handles websocket requests from the peer.
func ServeWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	// tcpConn, err := net.Dial("tcp", "127.0.0.1:8989")
	tcpConn, err := tcpTryConnect("47.112.120.88:8989")
	if err != nil {
		fmt.Println("net.Listen err", err)
	}
	client := &Client{
		hub:     hub,
		conn:    conn,
		send:    make(chan []byte, bufSize),
		tcpConn: tcpConn,
		tcpSync: make(chan bool),
	}
	client.hub.register <- client

	// Allow collection of memory referenced by the caller by doing all work in
	// new goroutines.
	go client.writePump()
	go client.readPump()
}
