package ws

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/runtime"
)

const (
	defaultPingPeriod = 30 * time.Second
	missedPongLimit   = 2
	writeWait         = 10 * time.Second
	maxMessageSize    = 1 << 20
	sendBuffer        = 256
)

type Deps struct {
	Store          Store
	Orchestrator   runtime.Orchestrator
	Bus            *runtime.Bus
	AllowedOrigins []string
	PingPeriod     time.Duration
	PongWait       time.Duration
}

type Server struct {
	deps     Deps
	upgrader websocket.Upgrader
	tickets  *ticketStore
}

func NewServer(deps Deps) *Server {
	if deps.PingPeriod <= 0 {
		deps.PingPeriod = defaultPingPeriod
	}
	if deps.PongWait <= 0 {
		deps.PongWait = missedPongLimit*deps.PingPeriod + 10*time.Second
	}
	return &Server{
		deps:    deps,
		tickets: newTicketStore(),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     func(*http.Request) bool { return true },
		},
	}
}

func (s *Server) Handle(c *gin.Context) {
	if !s.originAllowed(c.Request) {
		respond.Error(c, http.StatusForbidden, respond.CodeForbidden, "Origin 不被允许", nil)
		return
	}
	uid, ok := s.authenticate(c)
	if !ok {
		return
	}
	wsConn, err := s.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	newConn(s, wsConn, uid).run()
}

func (s *Server) authenticate(c *gin.Context) (string, bool) {
	uid, ok := s.tickets.consume(c.Query("ticket"))
	if !ok {
		respond.Error(c, http.StatusUnauthorized, respond.CodeUnauthorized, "连接票据无效或已过期，请重新获取", nil)
		return "", false
	}
	active, err := s.deps.Store.ActiveUser(c.Request.Context(), uid)
	if err != nil || !active {
		respond.Error(c, http.StatusUnauthorized, respond.CodeUnauthorized, "登录状态已失效，请重新登录", nil)
		return "", false
	}
	return uid, true
}

func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	for _, allowed := range s.deps.AllowedOrigins {
		if allowed == origin {
			return true
		}
	}
	return false
}

type conn struct {
	server *Server
	ws     *websocket.Conn
	uid    string
	send   chan []byte
	done   chan struct{}
	once   sync.Once

	mu      sync.Mutex
	streams map[string]*stream
}

type stream struct {
	id     string
	cancel func()
	uplink func(value json.RawMessage)
}

func newConn(s *Server, wsConn *websocket.Conn, uid string) *conn {
	return &conn{
		server:  s,
		ws:      wsConn,
		uid:     uid,
		send:    make(chan []byte, sendBuffer),
		done:    make(chan struct{}),
		streams: map[string]*stream{},
	}
}

func (c *conn) run() {
	go c.writePump()
	c.readPump()
}

func (c *conn) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.ws.Close()
		c.mu.Lock()
		streams := c.streams
		c.streams = map[string]*stream{}
		c.mu.Unlock()
		for _, st := range streams {
			if st.cancel != nil {
				st.cancel()
			}
		}
	})
}

func (c *conn) sendFrame(f serverFrame) {
	data, err := json.Marshal(f)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	case <-c.done:
	}
}

func (c *conn) addStream(st *stream) {
	c.mu.Lock()
	c.streams[st.id] = st
	c.mu.Unlock()
}

func (c *conn) closeStream(id string, sendEnd bool) {
	c.mu.Lock()
	st, ok := c.streams[id]
	if ok {
		delete(c.streams, id)
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	if st.cancel != nil {
		st.cancel()
	}
	if sendEnd {
		c.sendFrame(endFrame(id))
	}
}

func (c *conn) readPump() {
	defer c.close()
	c.ws.SetReadLimit(maxMessageSize)
	_ = c.ws.SetReadDeadline(time.Now().Add(c.server.deps.PongWait))
	c.ws.SetPongHandler(func(string) error {
		return c.ws.SetReadDeadline(time.Now().Add(c.server.deps.PongWait))
	})
	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		c.handleMessage(data)
	}
}

func (c *conn) writePump() {
	ticker := time.NewTicker(c.server.deps.PingPeriod)
	defer func() {
		ticker.Stop()
		c.close()
	}()
	for {
		select {
		case data := <-c.send:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.ws.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}
