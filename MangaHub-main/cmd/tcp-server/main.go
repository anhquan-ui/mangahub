package main

import (
	"bufio"
	"fmt"
	"log"
	"net"

	"mangahub/internal/tcp"
)

func main() {
	go tcp.GlobalHub.Run()

	listener, err := net.Listen("tcp", ":9090")
	if err != nil {
		log.Fatal("Error starting TCP server:", err)
	}
	defer listener.Close()

	log.Println("TCP Progress Sync Server running on :9090")
	log.Println("Connect with: telnet localhost 9090")

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println("Error accepting:", err)
			continue
		}
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	fmt.Fprintf(conn, "Welcome to MangaHub Progress Sync!\nEnter your UserID: ")

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}
	userID := scanner.Text()

	client := &tcp.Client{
		Conn:   conn,
		UserID: userID,
		Send:   make(chan []byte, 256),
	}

	tcp.GlobalHub.Register <- client  

	go client.WritePump()
	client.ReadPump()
}