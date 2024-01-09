package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
	maxMessageSize   = 51200
	MAX_PLAYERS      = 5
	defaultRoomSize  = 8
	defaultRoomTTL   = 300
	roomCheckTimeout = 10
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

var roomRepo = make(map[uuid.UUID]*Room, 8)

func main() {
	server := gin.Default()
	setupRouter(server)
	go cleanRooms(defaultRoomTTL, roomCheckTimeout)
	server.Run() // listen and serve on 0.0.0.0:8080 (for windows "localhost:8080")
	// room1 := createRoom(6)
	// room2 := createRoom(7)
	// fmt.Printf("%v, %v\n", room1, room2)
	// time.Sleep(30 * time.Second)
}

func setupRouter(server *gin.Engine) {
	server.POST("/rooms", func(ctx *gin.Context) {
		room := createRoom(defaultRoomSize)
		ctx.JSON(http.StatusOK, gin.H{
			"room": room,
		})
	})
	server.GET("/rooms/:id", WsHandler)
	server.GET("/metrics", MetricsHandler)

	server.GET("/ping", func(ctx *gin.Context) {
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

func createRoom(userLimit int) *Room {
	room := NewRoom(userLimit)
	roomRepo[room.Id] = room
	return room
}

func cleanRooms(ttl int, timeout int) {
	for {
		// TODO logging
		now := time.Now()
		cnt := 0
		userCnt := 0
		fmt.Printf("Check rooms for delete %v\n", now.Format(time.RFC3339))
		for roomId, room := range roomRepo {
			userCnt += len(room.Users)
			ttlDate := room.LastActivity.Add(time.Duration(ttl) * time.Second)
			if room.IsEmpty && ttlDate.Before(now) {
				delete(roomRepo, roomId)
				cnt += 1
			}
		}
		log.Printf("Total rooms: %v, deleted rooms: %v, total users: %v", len(roomRepo), cnt, userCnt)
		time.Sleep(time.Duration(timeout) * time.Second)
	}

}

// ROOMS and USERS

type User struct {
	Id    uuid.UUID
	Conn  *websocket.Conn
	send  chan []byte
	Muted bool
}

func (u *User) SendMsg(msg interface{}) error {
	json, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	u.send <- json
	return nil
}

func NewUser(conn *websocket.Conn) *User {
	return &User{
		Id:    uuid.New(),
		Conn:  conn,
		Muted: false,
		send:  make(chan []byte, 256),
	}
}

type Set[K comparable] interface {
	Contains(K) bool
}

type excludeUserId map[uuid.UUID]struct{}

func (e excludeUserId) Contains(id uuid.UUID) bool {
	_, exist := e[id]
	return exist
}

func ExcludeUserId(ids ...uuid.UUID) excludeUserId {
	exclude := make(excludeUserId, len(ids))
	void := struct{}{}
	for _, id := range ids {
		exclude[id] = void
	}
	return exclude
}

type Room struct {
	Id           uuid.UUID
	UsersLimit   int
	IsEmpty      bool
	LastActivity time.Time
	Users        map[uuid.UUID]*User
}

func (r *Room) AddUser(u *User) {
	r.Users[u.Id] = u
	r.LastActivity = time.Now()
}

func (r *Room) RemoveUser(u *User) {
	delete(r.Users, u.Id)
	r.LastActivity = time.Now()
}

func (r *Room) Broadcast(msg interface{}, exclude Set[uuid.UUID]) error {
	for _, user := range r.Users {
		if !exclude.Contains(user.Id) {
			user.SendMsg(msg)
		}
	}
	return nil
}

func NewRoom(usersLimit int) *Room {
	return &Room{
		Id:           uuid.New(),
		UsersLimit:   usersLimit,
		LastActivity: time.Now(),
		IsEmpty:      true,
		Users:        make(map[uuid.UUID]*User, usersLimit),
	}
}

// ______________________

// HANDLERS

type Metrics struct {
	UsersCount int
	RoomsCount int
}

func MetricsHandler(ctx *gin.Context) {
	metrics := &Metrics{RoomsCount: len(roomRepo)}
	for _, room := range roomRepo {
		metrics.UsersCount += len(room.Users)
	}
	ctx.JSON(http.StatusOK, gin.H{"metrics": metrics})
}

func WsHandler(ctx *gin.Context) {
	roomIdRaw := ctx.Param("id")
	roomId, err := uuid.Parse(roomIdRaw)
	if err != nil {
		ctx.JSON(http.StatusUnprocessableEntity, gin.H{"details": "Invalid id, expected uuid"})
		return
	}
	room, ok := roomRepo[roomId]
	if !ok {
		ctx.JSON(http.StatusNotFound, gin.H{"details": "Not found room"})
		return
	}
	if len(room.Users) > room.UsersLimit {
		log.Println("User join denied, max users in room")
		ctx.JSON(http.StatusBadRequest, gin.H{"details": "Room is full"})
		return
	}
	log.Printf("Connection to room %v", roomId)
	conn, err := upgrader.Upgrade(ctx.Writer, ctx.Request, nil)
	if err != nil {
		log.Println(err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"details": "Websocket upgrade error"})
		return
	}
	log.Printf("Connection success to room %v", roomId)
	defer conn.Close()
	user := NewUser(conn)
	// EVENT: User joined - broadcast, exclude user?
	room.AddUser(user)
	defer func() { room.RemoveUser(user) }()
	go HandleUserNotify(user)
	HandleUsersMessage(user, room)

}

func HandleUsersMessage(user *User, room *Room) {
	for {
		_, message, err := user.Conn.ReadMessage()
		if err != nil {
			log.Println(err)
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
		}
		log.Printf("recv: %s", message)
		var msg map[string]interface{}
		err = json.Unmarshal(message, &msg)
		if err != nil {
			log.Print(err)
			continue
		}
		msg["type"] = "EchoResponse"
		// user.SendMsg(msg)
		room.Broadcast(msg, ExcludeUserId())
	}

}

func HandleUserNotify(user *User) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		user.Conn.Close()
	}()
	for {
		select {
		case message, ok := <-user.send:
			user.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				user.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			w, err := user.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)
			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			user.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := user.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
