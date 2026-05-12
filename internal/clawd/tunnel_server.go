package clawd

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// === HTTP API ===
//
// /api/tunnel/config        — GET returns current TunnelConfig (JSON), POST
//                             replaces it with a JSON-encoded list of ports.
// /api/tunnel/status        — GET reports connected-client count for the
//                             dashboard "X clients" badge.
// /tunnel                   — WebSocket upgrade; the binary data plane.

func (s *Server) handleAPITunnelConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(s.tunnel.Get())
	case http.MethodPost:
		var cfg TunnelConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&cfg); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.tunnel.Replace(cfg.Ports); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.tunnel.Notify()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.tunnel.Get())
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAPITunnelStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{
		"clients": s.tunnelClients.Load(),
	})
}

// === WebSocket data plane ===
//
// Each WS message is one frame. Frames multiplex many TCP streams over a
// single connection. Wire format (all big-endian):
//
//   bytes 0..3  uint32  streamID  (0 is reserved for control)
//   bytes 4     uint8   frameType (0=data, 1=open, 2=close, 3=config)
//   bytes 5..   payload (interpreted per frameType)
//
// Frame types:
//   open    streamID + uint16 port → server dials 127.0.0.1:port. If the
//                                    port is not in the enabled set, server
//                                    replies with a close frame.
//   data    streamID + raw bytes  → forward to the matching TCP connection.
//   close   streamID + (empty)    → tear down the matching TCP connection.
//   config  streamID=0 + JSON     → server-initiated push of the current
//                                    TunnelConfig on change.

const (
	frameData   byte = 0
	frameOpen   byte = 1
	frameClose  byte = 2
	frameConfig byte = 3
)

var wsUpgrader = websocket.Upgrader{
	// We don't lock down origins here — Cloudflare Access (or whatever
	// upstream auth the user has) gates the route already. If you front the
	// dashboard with no auth, the WS is no more exposed than the rest of
	// the dashboard is.
	CheckOrigin: func(r *http.Request) bool { return true },
	// Generous buffers so we don't fragment small TCP payloads.
	ReadBufferSize:  64 * 1024,
	WriteBufferSize: 64 * 1024,
}

func (s *Server) handleTunnelWS(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("tunnel: upgrade: %v", err)
		return
	}
	s.tunnelClients.Add(1)
	defer s.tunnelClients.Add(-1)

	sess := newTunnelSession(conn, s.tunnel)
	if err := sess.serve(r.Context()); err != nil && !errors.Is(err, net.ErrClosed) {
		log.Printf("tunnel: session ended: %v", err)
	}
}

// tunnelSession owns one WebSocket and the TCP streams multiplexed over it.
// The server-side spawns one of these per connected `clawdstacc tunnel`
// client.
type tunnelSession struct {
	ws    *websocket.Conn
	store *TunnelStore

	// streams maps streamID → active TCP connection to a local port.
	mu      sync.Mutex
	streams map[uint32]net.Conn

	// writeMu serialises WriteMessage — gorilla/websocket requires that.
	writeMu sync.Mutex
}

func newTunnelSession(ws *websocket.Conn, store *TunnelStore) *tunnelSession {
	return &tunnelSession{ws: ws, store: store, streams: map[uint32]net.Conn{}}
}

func (t *tunnelSession) serve(ctx context.Context) error {
	defer t.closeAllStreams()
	defer t.ws.Close()

	// Push initial config so the client knows what to forward.
	if err := t.sendConfig(t.store.Get()); err != nil {
		return fmt.Errorf("send initial config: %w", err)
	}

	// Subscribe to config changes; push to client when they happen.
	subCh, unsub := t.store.Subscribe()
	defer unsub()
	subDone := make(chan struct{})
	go func() {
		defer close(subDone)
		for cfg := range subCh {
			if err := t.sendConfig(cfg); err != nil {
				return
			}
		}
	}()

	// Read loop.
	for {
		_, msg, err := t.ws.ReadMessage()
		if err != nil {
			return err
		}
		if err := t.handleFrame(msg); err != nil {
			log.Printf("tunnel: frame: %v", err)
		}
	}
}

func (t *tunnelSession) handleFrame(msg []byte) error {
	if len(msg) < 5 {
		return fmt.Errorf("short frame: %d bytes", len(msg))
	}
	streamID := binary.BigEndian.Uint32(msg[0:4])
	ftype := msg[4]
	payload := msg[5:]
	switch ftype {
	case frameOpen:
		if len(payload) < 2 {
			return fmt.Errorf("open: missing port")
		}
		port := int(binary.BigEndian.Uint16(payload[0:2]))
		return t.openStream(streamID, port)
	case frameData:
		return t.writeData(streamID, payload)
	case frameClose:
		t.closeStream(streamID)
		return nil
	default:
		return fmt.Errorf("unknown frame type %d", ftype)
	}
}

func (t *tunnelSession) openStream(streamID uint32, port int) error {
	if !t.store.PortEnabled(port) {
		// Politely refuse without tearing down the session.
		return t.sendClose(streamID)
	}
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 5*time.Second)
	if err != nil {
		return t.sendClose(streamID)
	}
	t.mu.Lock()
	t.streams[streamID] = conn
	t.mu.Unlock()
	// Pipe local TCP → WS for this stream.
	go t.copyToWS(streamID, conn)
	return nil
}

func (t *tunnelSession) writeData(streamID uint32, data []byte) error {
	t.mu.Lock()
	conn := t.streams[streamID]
	t.mu.Unlock()
	if conn == nil {
		// Stream already torn down on our side. Tell the client to give up.
		return t.sendClose(streamID)
	}
	_, err := conn.Write(data)
	if err != nil {
		t.closeStream(streamID)
		return t.sendClose(streamID)
	}
	return nil
}

func (t *tunnelSession) closeStream(streamID uint32) {
	t.mu.Lock()
	conn := t.streams[streamID]
	delete(t.streams, streamID)
	t.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

func (t *tunnelSession) closeAllStreams() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, conn := range t.streams {
		conn.Close()
		delete(t.streams, id)
	}
}

// copyToWS reads from the local TCP socket and ships everything to the
// client as data frames. Exits when the read returns (EOF or error) and
// notifies the client with a close frame so its listener can clean up.
func (t *tunnelSession) copyToWS(streamID uint32, conn net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			if werr := t.sendData(streamID, buf[:n]); werr != nil {
				t.closeStream(streamID)
				return
			}
		}
		if err != nil {
			t.closeStream(streamID)
			_ = t.sendClose(streamID)
			return
		}
	}
}

// --- frame writers ---

func (t *tunnelSession) sendData(streamID uint32, data []byte) error {
	frame := make([]byte, 5+len(data))
	binary.BigEndian.PutUint32(frame[0:4], streamID)
	frame[4] = frameData
	copy(frame[5:], data)
	return t.write(frame)
}

func (t *tunnelSession) sendClose(streamID uint32) error {
	frame := make([]byte, 5)
	binary.BigEndian.PutUint32(frame[0:4], streamID)
	frame[4] = frameClose
	return t.write(frame)
}

func (t *tunnelSession) sendConfig(cfg TunnelConfig) error {
	body, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	frame := make([]byte, 5+len(body))
	binary.BigEndian.PutUint32(frame[0:4], 0)
	frame[4] = frameConfig
	copy(frame[5:], body)
	return t.write(frame)
}

func (t *tunnelSession) write(frame []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	return t.ws.WriteMessage(websocket.BinaryMessage, frame)
}
