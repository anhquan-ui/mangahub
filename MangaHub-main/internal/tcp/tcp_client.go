package tcp

import (
	"bufio"
	"time"
)

func (c *Client) ReadPump() {
	defer func() {
		GlobalHub.Unregister <- c  
		c.Conn.Close()
	}()

	scanner := bufio.NewScanner(c.Conn)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "PING" {
			c.Conn.Write([]byte("PONG\n"))
		}
	}
}

func (c *Client) WritePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case message, ok := <-c.Send:
			if !ok {
				c.Conn.Write([]byte("BYE\n"))
				return
			}
			c.Conn.Write(append(message, '\n'))
		case <-ticker.C:
			c.Conn.Write([]byte("PING\n"))
		}
	}
}