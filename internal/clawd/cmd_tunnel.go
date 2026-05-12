package clawd

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// cmdTunnel is the laptop-side counterpart to handleTunnelWS. It connects to
// the dashboard's /tunnel WebSocket, asks the server for the configured port
// list (also pushed live whenever the user edits it in the UI), and opens a
// local TCP listener for each enabled port. Inbound connections are
// multiplexed over the single WebSocket back to the server, which dials
// localhost:<port> on its side. Net result: laptop's localhost:N talks to
// the dashboard host's localhost:N.
//
// Designed to work through whatever the dashboard already sits behind:
//
//   - plain HTTPS / LAN HTTP            → no extra auth
//   - HTTP basic / cookie-based auth    → pass via --cookie 'k=v; k2=v2'
//   - Cloudflare Access (Zero Trust)    → create a Service Token in the CF
//     dashboard (one-time), pass via
//     CF_ACCESS_CLIENT_ID + _SECRET env
//     vars or the --cf-* flags below.
//     No browser flow, no cloudflared
//     CLI dependency.
func cmdTunnel(args []string) {
	fs := flag.NewFlagSet("tunnel", flag.ExitOnError)
	addr := fs.String("listen", "127.0.0.1", "local address to bind forwarded ports to")
	cookie := fs.String("cookie", "", "raw Cookie header value (e.g. for cookie-based auth)")
	cfID := fs.String("cf-access-id", os.Getenv("CF_ACCESS_CLIENT_ID"),
		"Cloudflare Access Service Token Client ID (or set CF_ACCESS_CLIENT_ID)")
	cfSecret := fs.String("cf-access-secret", os.Getenv("CF_ACCESS_CLIENT_SECRET"),
		"Cloudflare Access Service Token Client Secret (or set CF_ACCESS_CLIENT_SECRET)")
	if err := fs.Parse(args); err != nil {
		die("flags: %v", err)
	}
	if fs.NArg() != 1 {
		die("usage: clawdstacc tunnel [flags] <dashboard-url>\n\nFlags:\n%s",
			"  --listen ADDR            local bind address (default 127.0.0.1)\n"+
				"  --cookie 'k=v; …'        raw Cookie header for cookie-based auth\n"+
				"  --cf-access-id ID        Cloudflare Access Service Token Client ID\n"+
				"  --cf-access-secret SECRET  Cloudflare Access Service Token Client Secret")
	}
	base, err := url.Parse(fs.Arg(0))
	if err != nil || base.Scheme == "" {
		die("bad dashboard URL: %q", fs.Arg(0))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ctrl-C / SIGTERM → graceful shutdown.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println()
		fmt.Println(yellow("==> Disconnecting"))
		cancel()
	}()

	auth := tunnelAuth{cookie: *cookie, cfClientID: *cfID, cfSecret: *cfSecret}

	// Cheap pre-flight: if the dashboard sits behind Cloudflare Access and
	// the caller didn't supply a Service Token, fail fast with a useful
	// hint instead of looping on "bad handshake" forever.
	if auth.cfClientID == "" && auth.cfSecret == "" {
		if accessHost := probeCloudflareAccess(ctx, base); accessHost != "" {
			msg := strings.Join([]string{
				"Cloudflare Access is in front of " + base.String() + " (" + accessHost + ").",
				"",
				"Create a Service Token (one-time, in the CF Zero Trust dashboard):",
				"  1. Zero Trust → Access → Service Auth → Create Service Token",
				"  2. Add an Access policy on this app: Include → Service Auth → <your token>",
				"",
				"Then pass the credentials to clawdstacc, either as flags:",
				"  clawdstacc tunnel --cf-access-id <id> --cf-access-secret <secret> " + base.String(),
				"",
				"or as env vars (recommended — put them in your shell rc):",
				"  export CF_ACCESS_CLIENT_ID=<id>",
				"  export CF_ACCESS_CLIENT_SECRET=<secret>",
				"  clawdstacc tunnel " + base.String(),
			}, "\n")
			die("%s", msg)
		}
	}

	fmt.Printf("%s Connecting to %s\n", blue("==>"), base.String())
	if auth.cfClientID != "" {
		fmt.Printf("%s Using Cloudflare Access Service Token\n", blue("·"))
	}

	// Reconnect loop. Backs off exponentially up to 30s on repeated failure.
	backoff := time.Second
	for ctx.Err() == nil {
		err := runTunnelClient(ctx, base, *addr, auth)
		if ctx.Err() != nil {
			break
		}
		fmt.Printf("%s Connection lost: %v — retrying in %s\n", yellow("·"), err, backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
	fmt.Println(green("✓ Done."))
}

// tunnelAuth bundles the optional credentials we send on the WS handshake.
type tunnelAuth struct {
	cookie     string // raw "Cookie:" header value
	cfClientID string // Cloudflare Access Service Token Client ID
	cfSecret   string // Cloudflare Access Service Token Client Secret
}

func (a tunnelAuth) apply(h http.Header) {
	if a.cookie != "" {
		h.Set("Cookie", a.cookie)
	}
	if a.cfClientID != "" {
		h.Set("CF-Access-Client-Id", a.cfClientID)
	}
	if a.cfSecret != "" {
		h.Set("CF-Access-Client-Secret", a.cfSecret)
	}
}

// probeCloudflareAccess does a non-following GET against the base URL and
// returns the cloudflareaccess.com hostname if Access is intercepting (302
// → <team>.cloudflareaccess.com/cdn-cgi/access/login/…). Empty string means
// either Access isn't in front, the probe failed, or the redirect points
// somewhere else.
func probeCloudflareAccess(ctx context.Context, base *url.URL) string {
	c := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return ""
	}
	resp, err := c.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		return ""
	}
	loc, err := resp.Location()
	if err != nil {
		return ""
	}
	if !strings.HasSuffix(loc.Hostname(), ".cloudflareaccess.com") {
		return ""
	}
	return loc.Hostname()
}

func runTunnelClient(ctx context.Context, base *url.URL, listenAddr string, auth tunnelAuth) error {
	wsURL := *base
	switch base.Scheme {
	case "http":
		wsURL.Scheme = "ws"
	case "https":
		wsURL.Scheme = "wss"
	}
	wsURL.Path = strings.TrimRight(wsURL.Path, "/") + "/tunnel"

	hdr := http.Header{}
	auth.apply(hdr)
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second
	ws, _, err := dialer.DialContext(ctx, wsURL.String(), hdr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", wsURL.String(), err)
	}
	fmt.Printf("%s Connected\n", green("✓"))

	c := newTunnelClient(ws, listenAddr)
	defer c.shutdown()

	// Connection lifetime: serve until the WS closes or ctx ends.
	go func() {
		<-ctx.Done()
		ws.Close()
	}()
	return c.serve()
}

// === client-side multiplexer ===

type tunnelClient struct {
	ws         *websocket.Conn
	listenAddr string

	writeMu sync.Mutex

	mu        sync.Mutex
	streams   map[uint32]net.Conn // streamID → laptop-side TCP conn
	listeners map[int]net.Listener
	nextID    uint32
}

func newTunnelClient(ws *websocket.Conn, listenAddr string) *tunnelClient {
	return &tunnelClient{
		ws:         ws,
		listenAddr: listenAddr,
		streams:    map[uint32]net.Conn{},
		listeners:  map[int]net.Listener{},
		nextID:     1, // 0 reserved for control frames
	}
}

func (c *tunnelClient) serve() error {
	for {
		_, msg, err := c.ws.ReadMessage()
		if err != nil {
			return err
		}
		if len(msg) < 5 {
			continue
		}
		streamID := binary.BigEndian.Uint32(msg[0:4])
		ftype := msg[4]
		payload := msg[5:]
		switch ftype {
		case frameData:
			c.writeToStream(streamID, payload)
		case frameClose:
			c.closeStream(streamID)
		case frameConfig:
			var cfg TunnelConfig
			if err := json.Unmarshal(payload, &cfg); err != nil {
				continue
			}
			c.applyConfig(cfg)
		}
	}
}

func (c *tunnelClient) applyConfig(cfg TunnelConfig) {
	wanted := map[int]TunnelPort{}
	for _, p := range cfg.Ports {
		if p.Enabled {
			wanted[p.Port] = p
		}
	}
	c.mu.Lock()
	// Close listeners that are no longer in the wanted set.
	for port, ln := range c.listeners {
		if _, ok := wanted[port]; !ok {
			ln.Close()
			delete(c.listeners, port)
			fmt.Printf("  %s closed: localhost:%d\n", yellow("↓"), port)
		}
	}
	// Open listeners for new ports.
	for port, p := range wanted {
		if _, ok := c.listeners[port]; ok {
			continue
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(c.listenAddr, strconv.Itoa(port)))
		if err != nil {
			fmt.Printf("  %s %s:%d: %v\n", red("✗"), c.listenAddr, port, err)
			continue
		}
		c.listeners[port] = ln
		label := p.Label
		if label != "" {
			label = " (" + label + ")"
		}
		fmt.Printf("  %s listening on %s:%d%s\n", green("→"), c.listenAddr, port, label)
		go c.acceptLoop(port, ln)
	}
	c.mu.Unlock()
}

func (c *tunnelClient) acceptLoop(port int, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			fmt.Printf("  %s accept: %v\n", red("✗"), err)
			return
		}
		streamID := c.allocStreamID()
		c.mu.Lock()
		c.streams[streamID] = conn
		c.mu.Unlock()
		// Tell the server to dial localhost:port for this stream.
		open := make([]byte, 5+2)
		binary.BigEndian.PutUint32(open[0:4], streamID)
		open[4] = frameOpen
		binary.BigEndian.PutUint16(open[5:7], uint16(port))
		if err := c.writeFrame(open); err != nil {
			c.closeStream(streamID)
			continue
		}
		go c.copyToWS(streamID, conn)
	}
}

func (c *tunnelClient) allocStreamID() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		id := c.nextID
		c.nextID++
		if c.nextID == 0 {
			c.nextID = 1
		}
		if _, taken := c.streams[id]; !taken {
			return id
		}
	}
}

func (c *tunnelClient) copyToWS(streamID uint32, conn net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			frame := make([]byte, 5+n)
			binary.BigEndian.PutUint32(frame[0:4], streamID)
			frame[4] = frameData
			copy(frame[5:], buf[:n])
			if werr := c.writeFrame(frame); werr != nil {
				c.closeStream(streamID)
				return
			}
		}
		if err != nil {
			c.closeStream(streamID)
			// Tell the server we're done with this stream.
			closeFrame := make([]byte, 5)
			binary.BigEndian.PutUint32(closeFrame[0:4], streamID)
			closeFrame[4] = frameClose
			_ = c.writeFrame(closeFrame)
			return
		}
	}
}

func (c *tunnelClient) writeToStream(streamID uint32, data []byte) {
	c.mu.Lock()
	conn := c.streams[streamID]
	c.mu.Unlock()
	if conn == nil {
		return
	}
	if _, err := conn.Write(data); err != nil {
		c.closeStream(streamID)
	}
}

func (c *tunnelClient) closeStream(streamID uint32) {
	c.mu.Lock()
	conn := c.streams[streamID]
	delete(c.streams, streamID)
	c.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

func (c *tunnelClient) writeFrame(frame []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.WriteMessage(websocket.BinaryMessage, frame)
}

func (c *tunnelClient) shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ln := range c.listeners {
		ln.Close()
	}
	for _, conn := range c.streams {
		conn.Close()
	}
}
