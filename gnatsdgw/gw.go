package gnatsdgw

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	fwd "github.com/antmanler/gnatsd-gw/gnatsdgw/server"
)

// Dialer is a function to dial to backend
type Dialer func() (net.Conn, error)

// Connector handle initial info command from backend
type Connector func(conn net.Conn, cmd *fwd.InfoCmd) (newConn net.Conn, newCmd *fwd.InfoCmd, err error)

// Accepter handle initial connect to backend using nats CONNECT protocal
type Accepter func(conn net.Conn) (newConn net.Conn, err error)

// MiM is Man in the Middle configs to handle TLS handshakes in nats' protocal
type MiM struct {
	Connector Connector
	Acceppter Accepter
}

// User can access underlying connections using forwarder's kv methods
var (
	B2CConnKey struct {
		_1 struct{}
	}
	C2BConnKey struct {
		_2 struct{}
	}
)

// NewServer returns a function to serve connection accepted by listener
func NewServer(opts *fwd.Options, dialer Dialer, mim MiM) (func(net.Conn) error, error) {
	opts = CloneOpts(opts)
	return func(conn net.Conn) error {
		backend, err := dialer()
		if err != nil {
			return err
		}
		// read connection
		ctrline := new(control)
		if err := readOp(backend, ctrline); err != nil {
			return err
		}
		if ctrline.op != "INFO" {
			return errNoInfoReceived
		}
		b2c := fwd.NewForwarder(opts)
		// store meta
		b2c.Set(B2CConnKey, backend)
		b2c.Set(C2BConnKey, conn)

		infoCmd := &fwd.InfoCmd{
			Payload: []byte(ctrline.args),
		}
		if mim.Connector != nil {
			backend, infoCmd, err = mim.Connector(backend, infoCmd)
			if err != nil {
				return err
			}
			// update connection
			b2c.Set(B2CConnKey, backend)
		}
		if opts.Handlers.OnInfo != nil {
			newCmd, err := opts.Handlers.OnInfo(b2c, infoCmd)
			if err != nil {
				return err
			}
			if newCmd != nil {
				infoCmd = newCmd
			}
		}
		ctrline.args = string(infoCmd.Payload)
		// fowrad to client the info command
		if _, err := conn.Write([]byte(fmt.Sprintf("INFO %s\r\n", ctrline.args))); err != nil {
			return err
		}

		// handle tls
		if mim.Acceppter != nil {
			conn, err = mim.Acceppter(conn)
			if err != nil {
				return err
			}
		}
		c2b := fwd.NewForwarder(opts)
		c2b.Set(B2CConnKey, backend)
		c2b.Set(C2BConnKey, conn)

		var cid string
		remoteAddrStr, ok := conn.(interface{ RemoteAddrString() string })
		if ok {
			cid = fmt.Sprintf("%s", remoteAddrStr.RemoteAddrString())
		} else {
			cid = fmt.Sprintf("%s", conn.RemoteAddr().String())
		}

		// kick start
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			c2b.Run(fwd.CLIENT2BACKEND, cid, conn, backend)
		}()
		go func() {
			defer wg.Done()
			b2c.Run(fwd.BACKEND2CLIENT, cid, backend, conn)
		}()
		wg.Wait()
		return nil
	}, nil
}

var (
	errNoInfoReceived = errors.New("nats: protocol exception, INFO not received")
)

const (
	crlfTkn = "\r\n"
	spcTkn  = " "
	infoHdr = "INFO "
	connHdr = "CONNECT "

	// The size of the bufio reader/writer on top of the socket.
	// Copied from go-nats
	defaultBufSize = 3276
)

// A control protocol line.
type control struct {
	op, args string
}

// Read a control line and process the intended op.
// Copied from go-nats
func readOp(conn net.Conn, c *control) error {
	br := bufio.NewReaderSize(conn, defaultBufSize)
	line, err := br.ReadString('\n')
	if err != nil {
		return err
	}
	parseControl(line, c)
	return nil
}

// Parse a control line from the fwd.
// Copied from go-nats
func parseControl(line string, c *control) {
	toks := strings.SplitN(line, spcTkn, 2)
	if len(toks) == 1 {
		c.op = strings.TrimSpace(toks[0])
		c.args = ""
	} else if len(toks) == 2 {
		c.op, c.args = strings.TrimSpace(toks[0]), strings.TrimSpace(toks[1])
	} else {
		c.op = ""
	}
}

// CloneOpts ensures a valid option
func CloneOpts(opts *fwd.Options) *fwd.Options {
	cloned := &fwd.Options{
		MaxControlLine: opts.MaxControlLine,
		MaxPayload:     opts.MaxPayload,
		Trace:          opts.Trace,
		Logger:         opts.Logger,
	}
	if cloned.MaxControlLine <= 0 {
		cloned.MaxControlLine = fwd.MAX_CONTROL_LINE_SIZE
	}
	if cloned.MaxPayload <= 0 {
		cloned.MaxPayload = fwd.MAX_CONTROL_LINE_SIZE
	}
	if cloned.Logger == nil {
		cloned.Logger = loggerNoOp{}
	}
	cloned.Handlers.OnConnect = opts.Handlers.OnConnect
	cloned.Handlers.OnInfo = opts.Handlers.OnInfo
	cloned.Handlers.OnMsg = opts.Handlers.OnMsg
	cloned.Handlers.OnPublish = opts.Handlers.OnPublish
	cloned.Handlers.OnSubscribe = opts.Handlers.OnSubscribe
	cloned.Handlers.OnUnsubscribe = opts.Handlers.OnUnsubscribe
	return cloned
}

type loggerNoOp struct{}

func (loggerNoOp) Debugf(format string, args ...interface{}) {}

func (loggerNoOp) Infof(format string, args ...interface{}) {}

func (loggerNoOp) Warningf(format string, args ...interface{}) {}

func (loggerNoOp) Errorf(format string, args ...interface{}) {}
