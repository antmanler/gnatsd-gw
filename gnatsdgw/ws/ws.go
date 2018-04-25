package ws

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/antmanler/gnatsd-gw/gnatsdgw"
	fwd "github.com/antmanler/gnatsd-gw/gnatsdgw/server"
	"github.com/gorilla/websocket"
)

// NewHandler creates new forwarder to handle websocket connections
func NewHandler(opts *fwd.Options, dialer gnatsdgw.Dialer, upgrader *websocket.Upgrader, mimConnector gnatsdgw.Connector) (http.Handler, error) {
	if upgrader == nil {
		upgrader = &defaultUpgrader
	}
	serve, err := gnatsdgw.NewServer(opts, dialer, gnatsdgw.MiM{
		Connector: mimConnector,
	})
	if err != nil {
		return nil, err
	}
	opts = gnatsdgw.CloneOpts(opts)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			opts.Logger.Errorf("(ws) failed to upgrade connection, %v", err)
			return
		}
		defer conn.Close()
		// start and serve
		wsconn := &Conn{Conn: conn}
		wsconn.Request.Header = r.Header
		wsconn.Request.Method = r.Method
		wsconn.Request.URL = r.URL
		wsconn.Request.Host = r.Host
		wsconn.Request.RemoteAddr = r.RemoteAddr
		wsconn.Request.RequestURI = r.RequestURI
		if err := serve(wsconn); err != nil {
			opts.Logger.Errorf("(ws) proxy connection failed, %v", err)
		}
	}), nil
}

var defaultUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// Conn wrapps upgraded *websocket.Conn with addition request info.
type Conn struct {
	*websocket.Conn

	cr  io.Reader
	err error

	// addition info from request
	Request struct {
		Header     http.Header
		Method     string
		URL        *url.URL
		Host       string
		RemoteAddr string
		RequestURI string
	}
}

var _ net.Conn = (*Conn)(nil)

func (wsc *Conn) Read(b []byte) (n int, err error) {
	if wsc.err != nil {
		err = wsc.err
		return
	}
	if wsc.cr == nil {
		_, rd, err := wsc.Conn.NextReader()
		if err != nil {
			wsc.err = err
			return 0, io.EOF
		}
		wsc.cr = rd
	}
	n, err = wsc.cr.Read(b)
	if err == io.EOF {
		wsc.cr = nil
		err = nil
	}
	return
}

func (wsc *Conn) Write(b []byte) (n int, err error) {
	if err := wsc.Conn.WriteMessage(websocket.TextMessage, b); err != nil {
		return 0, err
	}
	return len(b), nil
}

// SetDeadline implements the net.Conn
func (wsc *Conn) SetDeadline(t time.Time) error {
	if err := wsc.SetReadDeadline(t); err != nil {
		return err
	}
	if err := wsc.SetWriteDeadline(t); err != nil {
		return err
	}
	return nil
}

// RemoteAddrString returns string in request
func (wsc *Conn) RemoteAddrString() string {
	return wsc.Request.RemoteAddr
}
