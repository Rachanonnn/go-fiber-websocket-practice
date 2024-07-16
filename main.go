package main

import (
	"flag"
	"log"
	"strings"
	"sync"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
)

type client struct {
	isClosing bool
	mu        sync.Mutex
	channels  map[string]bool
}

var clients = make(map[*websocket.Conn]*client)
var register = make(chan *clientConn)
var broadcast = make(chan *messageData)
var unregister = make(chan *websocket.Conn)

type clientConn struct {
	conn     *websocket.Conn
	channels []string
}

type messageData struct {
	channel string
	text    string
}

func runHub() {
	for {
		select {
		case clientConn := <-register:
			c := &client{channels: make(map[string]bool)}
			for _, channel := range clientConn.channels {
				c.channels[channel] = true
			}
			clients[clientConn.conn] = c
			log.Println("connection registered for channels:", clientConn.channels)

		case msg := <-broadcast:
			log.Println("message received on channel", msg.channel, ":", msg.text)
			for connection, c := range clients {
				if c.channels[msg.channel] {
					go func(connection *websocket.Conn, c *client) {
						c.mu.Lock()
						defer c.mu.Unlock()
						if c.isClosing {
							return
						}
						if err := connection.WriteMessage(websocket.TextMessage, []byte(msg.text)); err != nil {
							c.isClosing = true
							log.Println("write error:", err)
							connection.WriteMessage(websocket.CloseMessage, []byte{})
							connection.Close()
							unregister <- connection
						}
					}(connection, c)
				}
			}

		case connection := <-unregister:
			delete(clients, connection)
			log.Println("connection unregistered")
		}
	}
}

func main() {
	app := fiber.New()

	app.Static("/", "./home.html")

	app.Use(func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			return c.Next()
		}
		return c.SendStatus(fiber.StatusUpgradeRequired)
	})

	go runHub()

	app.Get("/ws", websocket.New(func(c *websocket.Conn) {
		defer func() {
			unregister <- c
			c.Close()
		}()

		// Register the client with channels
		channels := c.Query("channels", "default")
		register <- &clientConn{conn: c, channels: strings.Split(channels, ",")}

		for {
			messageType, msgContent, err := c.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Println("read error:", err)
				}
				return
			}

			if messageType == websocket.TextMessage {
				// Parse the channel and message text
				parts := strings.SplitN(string(msgContent), ":", 2)
				if len(parts) < 2 {
					log.Println("invalid message format")
					continue
				}
				channel, text := parts[0], parts[1]
				broadcast <- &messageData{channel: channel, text: text}
			} else {
				log.Println("websocket message received of type", messageType)
			}
		}
	}))

	addr := flag.String("addr", ":8080", "http service address")
	flag.Parse()
	log.Fatal(app.Listen(*addr))
}
