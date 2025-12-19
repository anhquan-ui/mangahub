package udp

import (
	"log"
	"net"
	"time"
)

var udpConn *net.UDPConn

func StartUDPListener(addr string) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		log.Fatal("UDP resolve error:", err)
	}

	udpConn, err = net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Fatal("UDP listen error:", err)
	}
	log.Printf("UDP notification server running on %s", addr)

	go readPump()
	go writePump()
}

func readPump() {
	buffer := make([]byte, 4096)
	for {
		n, clientAddr, err := udpConn.ReadFromUDP(buffer)
		if err != nil {
			log.Println("UDP read error:", err)
			continue
		}

		message := string(buffer[:n])
		if message == "PING\n" || message == "PING" {
			client := &ClientAddr{
				Addr:     clientAddr,
				LastSeen: time.Now(),
			}
			GlobalHub.Register <- client
			udpConn.WriteToUDP([]byte("PONG\n"), clientAddr)
		}
	}
}

func writePump() {
	for message := range GlobalHub.broadcast {
		GlobalHub.mu.RLock()
		for _, client := range GlobalHub.clients {
			udpConn.WriteToUDP(message, client.Addr)
			client.LastSeen = time.Now()
		}
		GlobalHub.mu.RUnlock()
	}
}
