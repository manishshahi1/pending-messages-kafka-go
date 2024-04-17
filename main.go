package main

import (
    "context"
    "log"
    "net/http"
    "sync"
    // "string"
    "strings"

    "github.com/gorilla/websocket"
    "github.com/segmentio/kafka-go"
)

var upgrader = websocket.Upgrader{
    ReadBufferSize:  1024,
    WriteBufferSize: 1024,
    CheckOrigin: func(r *http.Request) bool {
        return true
    },
}

var (
    clientsLock sync.RWMutex
    clients     = make(map[string]*websocket.Conn)
    kafkaWriter *kafka.Writer
    kafkaReader *kafka.Reader
)

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Println("WebSocket upgrade error:", err)
        return
    }
    defer conn.Close()

    var clientID string

    messageType, p, err := conn.ReadMessage()
    if err != nil {
        log.Println("WebSocket read error:", err)
        return
    }
    if messageType == websocket.TextMessage {
        clientID = string(p)
    }

    clientsLock.Lock()
    clients[clientID] = conn
    clientsLock.Unlock()

    log.Printf("Client %s connected\n", clientID)

    for {
        messageType, p, err := conn.ReadMessage()
        if err != nil {
            log.Println("WebSocket read error:", err)
            break
        }

        switch messageType {
        case websocket.TextMessage:
            handleMessage(clientID, string(p))
        }
    }

    clientsLock.Lock()
    delete(clients, clientID)
    clientsLock.Unlock()

    log.Printf("Client %s disconnected\n", clientID)
}

func handleMessage(clientID, message string) {
    log.Printf("Received message from client %s: %s\n", clientID, message)

    // Append client ID to the message
    messageWithID := clientID + ": " + message

    err := kafkaWriter.WriteMessages(context.Background(), kafka.Message{Value: []byte(messageWithID)})
    if err != nil {
        log.Println("Kafka write error:", err)
        return
    }
    log.Println("Message written to Kafka")
}

func fetchPendingMessages(clientID string, conn *websocket.Conn) {
    for {
        m, err := kafkaReader.FetchMessage(context.Background())
        if err != nil {
            log.Println("Kafka read error:", err)
            continue
        }

        // Extract the sender's clientID from the message
        senderClientID := strings.Split(string(m.Value), ":")[0]

        // Check if the message is not sent by the receiver (client B)
        if senderClientID != clientID {
            // If the message is sent by other clients, send it to client B
            err := conn.WriteMessage(websocket.TextMessage, m.Value)
            if err != nil {
                log.Println("WebSocket write error:", err)
                return
            }
        }
    }
}

func handleFetchPendingMessages(w http.ResponseWriter, r *http.Request) {
    clientID := r.URL.Query().Get("clientID") // Extract clientID from the request URL query parameters

    clientsLock.RLock()
    conn, ok := clients[clientID]
    clientsLock.RUnlock()

    if !ok {
        log.Printf("Client %s not found\n", clientID)
        http.Error(w, "Client not found", http.StatusNotFound)
        return
    }

    // Start fetching pending messages for the client
    go fetchPendingMessages(clientID, conn)

    // Respond with HTTP status OK
    w.WriteHeader(http.StatusOK)
    log.Printf("Fetching pending messages for client %s\n", clientID)
}

func main() {
    // var err error
    kafkaWriter = &kafka.Writer{
        Addr:     kafka.TCP("localhost:9092"),
        Topic:    "nnnn",
        Balancer: &kafka.LeastBytes{},
    }
    defer kafkaWriter.Close()

    // currentOffset := getCurrentOffsetForClient(clientID)

    kafkaReader = kafka.NewReader(kafka.ReaderConfig{
        Brokers:   []string{"localhost:9092"},
        Topic:     "nnnn",
        Partition: 0,
        MinBytes:  10e3,
        MaxBytes:  10e6,
        // StartOffset: kafka.FirstOffset, // Start reading from the earliest offset
        StartOffset: -1, // Start reading from the earliest offset
    })
    defer kafkaReader.Close()

    http.HandleFunc("/ws", handleWebSocket)
    http.HandleFunc("/fetchPendingMessages", handleFetchPendingMessages)
    // Serve index.html from root
    fs := http.FileServer(http.Dir("."))
    http.Handle("/", fs)
    log.Println("Server started at :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}
