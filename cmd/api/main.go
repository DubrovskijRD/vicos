package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/rtp"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second
	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second
	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
	// Maximum message size allowed from peer.
	maxMessageSize   = 512000
	MAX_PLAYERS      = 5
	defaultRoomSize  = 8
	defaultRoomTTL   = 300
	roomCheckTimeout = 150
)

var (
	errChanClosed    = errors.New("channel closed")
	errInvalidTrack  = errors.New("track is nil")
	errInvalidPacket = errors.New("packet is nil")
	// errInvalidPC      = errors.New("pc is nil")
	// errInvalidOptions = errors.New("invalid options")
	errNotImplemented = errors.New("not implemented")
)

type RtpTranscMsg struct {
	trackId string
	rtpPack *rtp.Packet
}

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

type UserConn struct {
	conn *websocket.Conn
	send chan []byte
}

type Room struct {
	Id          uuid.UUID
	Connections map[uuid.UUID]*UserConn
}

func NewRoom(id uuid.UUID) *Room {
	return &Room{Id: id, Connections: make(map[uuid.UUID]*UserConn, 2)}
}

func (r *Room) Add(id uuid.UUID, conn *UserConn) {
	r.Connections[id] = conn
}

func (r *Room) Get(id uuid.UUID) (*UserConn, bool) {
	conn, ok := r.Connections[id]
	return conn, ok
}

func (r *Room) Delete(id uuid.UUID) {
	delete(r.Connections, id)
}

func (r *Room) Map() map[uuid.UUID]*UserConn {
	return r.Connections
}

var roomsLock sync.Mutex
var rooms = make(map[uuid.UUID]*Room)

var userConnections = make(map[uuid.UUID]*UserConn)

func main() {
	server := gin.Default()
	setupRouter(server)
	// go cleanRooms(defaultRoomTTL, roomCheckTimeout)
	server.Run() // listen and serve on 0.0.0.0:8080 (for windows "localhost:8080")
	// room1 := createRoom(6)
	// room2 := createRoom(7)
	// fmt.Printf("%v, %v\n", room1, room2)
	// time.Sleep(30 * time.Second)
}

func setupRouter(server *gin.Engine) {
	// server.POST("/rooms", func(ctx *gin.Context) {
	// 	room := createRoom(defaultRoomSize)
	// 	ctx.JSON(http.StatusOK, gin.H{
	// 		"room": room,
	// 	})
	// })
	server.GET("/ws/rooms/:id", WsHandler)
	// server.GET("/metrics", MetricsHandler)

	server.GET("/api/ping", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{
			"message": "pong",
		})
	})
	// routes := route.Group("/api/user")
	// {
	// 	// User
	// 	routes.POST("", userController.Register)
	// 	routes.GET("", userController.GetAllUser)
	// }
}

type WsMsgType struct {
	Event string
}

// type Event struct {
// 	Type string `json:"type"`

// 	Offer       *webrtc.SessionDescription `json:"offer,omitempty"`
// 	Answer      *webrtc.SessionDescription `json:"answer,omitempty"`
// 	Candidate   *webrtc.ICECandidateInit   `json:"candidate,omitempty"`
// 	CandidateIn *webrtc.ICECandidateInit   `json:"candidateIn,omitempty"`
// 	Desc        string                     `json:"desc,omitempty"`
// }

// ______________________

// HANDLERS

type Metrics struct {
	UsersCount int
	RoomsCount int
}

// func MetricsHandler(ctx *gin.Context) {
// 	metrics := &Metrics{RoomsCount: len(roomRepo)}
// 	for _, room := range roomRepo {
// 		metrics.UsersCount += len(room.Users)
// 	}
// 	ctx.JSON(http.StatusOK, gin.H{"metrics": metrics})
// }

func WsHandler(ctx *gin.Context) {
	roomIdRaw := ctx.Param("id")
	roomId, err := uuid.Parse(roomIdRaw)
	if err != nil {
		ctx.JSON(http.StatusUnprocessableEntity, gin.H{"details": "Room id should be uuid"})
		return
	}
	roomsLock.Lock()
	room, ok := rooms[roomId]
	if !ok {
		log.Printf("Create new room %v", roomId)
		room = NewRoom(roomId)
		rooms[roomId] = room
		// ctx.JSON(http.StatusNotFound, gin.H{"details": "Room not found"})
		// return
	}
	roomsLock.Unlock()
	connId := uuid.New()
	conn, err := upgrader.Upgrade(ctx.Writer, ctx.Request, nil)
	if err != nil {
		log.Println(err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"details": "Websocket upgrade error"})
		return
	}
	log.Printf("Connection success id: %v , room: %v\n", connId, roomId)
	defer conn.Close()
	defer func() {
		msg := struct {
			Event string `json:"event"`
			Data  struct {
				UserId uuid.UUID `json:"user_id"`
			} `json:"data"`
		}{
			Event: "user_left",
			Data: struct {
				UserId uuid.UUID `json:"user_id"`
			}{
				UserId: connId,
			},
		}
		err = room.sendBroadcast(msg, connId)
		if err != nil {
			log.Printf("Error while user left broadcast %v", connId)
		}
	}()
	log.Printf("Room before connect user %v: %v (len:%v)", connId, room.Connections, len(room.Connections))
	go handleMessagesFromUser(conn, connId, room)
	send := make(chan []byte)
	userConn := &UserConn{
		conn: conn,
		send: send,
	}
	room.Add(connId, userConn)
	defer func() { room.Delete(connId) }()
	log.Printf("Room after connect user %v: %v (len:%v)", connId, room.Connections, len(room.Connections))
	sendMessages(conn, send)

}

func sendMsg(msg interface{}, send chan []byte) error {
	json, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	send <- json
	return nil
}

func (r *Room) sendBroadcast(msg interface{}, from uuid.UUID) error {
	log.Printf("send broadcast from %v to room %v\n", from, r.Id)
	json, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	for id, conn := range r.Map() {
		log.Printf("send broadcast from %v to %v (room %v)\n", from, id, r.Id)
		if id != from {
			conn.send <- json
		}

	}
	log.Printf("broadcast sent from %v to room %v\n", from, r.Id)
	return nil
}

func (r *Room) sendBroadcastRaw(msgRaw []byte, from uuid.UUID) {
	log.Printf("send raw broadcast from %v to room %v\n", from, r.Id)
	for id, conn := range r.Map() {
		log.Printf("send broadcast from %v to %v (room %v)\n", from, id, r.Id)
		if id != from {
			conn.send <- msgRaw
		}

	}
	log.Printf("raw broadcast sent from %v to room %v\n", from, r.Id)
}

func sendMessages(conn *websocket.Conn, send chan []byte) {
	log.Printf("Sending messages")
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
	}()
	var counter int = 0
	for {
		select {
		case message, ok := <-send:
			counter += 1
			log.Printf("Send message %v: %v\n", counter, message)
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			w, err := conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)
			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			log.Printf("Send ping")
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func handleMessagesFromUser(conn *websocket.Conn, connId uuid.UUID, room *Room) {
	msg1 := struct {
		Event string `json:"event"`
		Data  struct {
			UserId uuid.UUID `json:"user_id"`
		} `json:"data"`
	}{
		Event: "connection",
		Data: struct {
			UserId uuid.UUID `json:"user_id"`
		}{
			UserId: connId,
		},
	}
	// TODO refactoring
	userConn, ok := room.Get(connId)
	if !ok {
		return
	}
	err := sendMsg(msg1, userConn.send)
	if err != nil {
		log.Printf("Error send msg %v, %v", err, msg1)
	}
	msg2 := struct {
		Event string `json:"event"`
		Data  struct {
			UserId uuid.UUID `json:"user_id"`
		} `json:"data"`
	}{
		Event: "user_joined",
		Data: struct {
			UserId uuid.UUID `json:"user_id"`
		}{
			UserId: connId,
		},
	}
	err = room.sendBroadcast(msg2, connId)
	if err != nil {
		log.Printf("Error broadcast msg %v, %v", err, msg2)
	}
	log.Printf("start handling messages\n")

	conn.SetReadLimit(maxMessageSize)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error { conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Println(err)
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
				log.Println(err)
			}
			break
		}
		message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))
		go func() {
			err := handleEvent(message, connId, room)
			if err != nil {
				log.Println(err)
			}
		}()
	}
}

func handleEvent(eventRaw []byte, from uuid.UUID, r *Room) error {
	// Broadcast all incoming events
	wsMsgType := &WsMsgType{}
	err := json.Unmarshal(eventRaw, wsMsgType)
	if err != nil {
		log.Printf("Can't parse event type %v\n", err)
	} else {
		log.Printf("Broadcast message %v\n", wsMsgType)
	}
	r.sendBroadcastRaw(eventRaw, from)
	return nil
}
