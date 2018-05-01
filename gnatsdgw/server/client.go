// Copyright 2018 Antmanler
// Copyright 2012-2018 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Type of client connection.
const (
	// CLIENT is an end user.
	CLIENT = iota
	// ROUTER is another router in the cluster.
	ROUTER
	// BACKEND is proxy upstream
	BACKEND
)

const (
	startBufSize = 512 // For INFO/CONNECT block

	// MAX_PUB_ARGS Maximum possible number of arguments from PUB proto.
	MAX_PUB_ARGS = 3
)

// make parser happy
const (
	// CR_LF string
	CR_LF = "\r\n"

	// LEN_CR_LF hold onto the computed size.
	LEN_CR_LF = len(CR_LF)

	_EMPTY_ = ""
	_SPC_   = " "

	// MAX_CONTROL_LINE_SIZE is the maximum allowed protocol control line size.
	// 1k should be plenty since payloads sans connect string are separate
	MAX_CONTROL_LINE_SIZE = 1024

	// MAX_PAYLOAD_SIZE is the maximum allowed payload size. Should be using
	// something different if > 1MB payloads are needed.
	MAX_PAYLOAD_SIZE = (1024 * 1024)

	// PROTO_SNIPPET_SIZE is the default size of proto to print on parse errors.
	PROTO_SNIPPET_SIZE = 32

	// MAX_MSG_ARGS Maximum possible number of arguments from MSG proto.
	MAX_MSG_ARGS = 4
)

// make parser happy again
var (
	// ErrAuthorization represents an error condition on failed authorization.
	ErrAuthorization = errors.New("Authorization Error")
	// ErrMaxControlLine represents an error condition when the control line is too big.
	ErrMaxControlLine = errors.New("Maximum Control Line Exceeded")
	// ErrMaxPayload represents an error condition when the payload is too big.
	ErrMaxPayload = errors.New("Maximum Payload Exceeded")
)

var (
	connHdr   = []byte("CONNECT ")
	pubHdr    = []byte("PUB ")
	subHdr    = []byte("SUB ")
	unsubHdr  = []byte("UNSUB ")
	infoHdr   = []byte("INFO ")
	msgHdr    = []byte("MSG ")
	errHdr    = []byte("-ERR ")
	pingBytes = []byte("PING\r\n")
	pongBytes = []byte("PONG\r\n")
	crlfBytes = []byte(CR_LF)
)

type fakeSrv interface {
	getOpts() *Options
}

type client struct {
	Logger

	opts *Options

	// for custom data kv access
	data sync.Map

	cid string
	typ int

	nc io.ReadWriter
	wc io.WriteCloser

	srv fakeSrv

	parseState

	trace bool

	pipe struct {
		sync.Once
		started bool
	}
	closeOnce sync.Once
}

// static assert
var _ Forwarder = (*client)(nil)

// NewForwarder returns a forwarder
func NewForwarder(opts *Options) Forwarder {
	cli := &client{
		Logger: opts.Logger,
		opts:   opts,
		trace:  opts.Trace,
	}
	cli.srv = cli
	return cli
}

func (c *client) Run(typ Type, cid string, nc io.ReadWriter, wc io.WriteCloser) {
	if typ == CLIENT2BACKEND {
		c.typ = CLIENT
	} else {
		c.typ = BACKEND
	}
	c.nc, c.wc = nc, wc
	c.cid = cid
	c.readLoop()
}

// Stops the furture insepction, pipe the following inbound bytes to backend server.
func (c *client) PipeIO() {
	c.pipe.Do(func() {
		c.pipe.started = true
	})
}

// KV interface for set/get custom data associated with current client
func (c *client) Set(key, value interface{})                       { c.data.Store(key, value) }
func (c *client) Get(key interface{}) (value interface{}, ok bool) { return c.data.Load(key) }
func (c *client) Range(f func(key, value interface{}) bool)        { c.data.Range(f) }

func (c *client) getOpts() *Options {
	return c.opts
}

func (c *client) readLoop() {
	// ensure connection closed
	defer c.closeConnection()

	if !c.pipe.started {
		nc := c.nc
		// Start read buffer.
		b := make([]byte, startBufSize)
		for !c.pipe.started {
			n, err := nc.Read(b)
			if err != nil {
				return
			}

			if err := c.parse(b[:n]); err != nil {
				if err != ErrMaxPayload && err != ErrAuthorization {
					c.Errorf("Reading %s %s", c.typeString(), err.Error())
					c.sendErr("Parser Error")
				}
				return
			}
		}
	}

	_, err := io.Copy(c.wc, c.nc)
	if err != io.EOF {
		c.Errorf("%s readLoop exited with error: %v", c.cid, err)
	}
}

func (c *client) processConnect(arg []byte) error {
	c.traceInOp("CONNECT", arg)
	cmd := &ConnectCmd{
		Payload: arg,
	}
	if c.opts.Handlers.OnConnect != nil {
		newCmd, err := c.opts.Handlers.OnConnect(c, cmd)
		if err != nil {
			if err == ErrAuthorization {
				c.authViolation()
			}
			return err
		}
		if newCmd != nil {
			cmd = newCmd
			c.traceInOp("CONNECT NEW", cmd.Payload)
		}
	}
	msgh := connHdr[:len(connHdr)]
	msgh = append(msgh, cmd.Payload...)
	if !bytes.HasSuffix(cmd.Payload, crlfBytes) {
		msgh = append(msgh, crlfBytes...)
	}
	_, err := c.wc.Write(msgh)
	if err == nil {
		return err
	}
	return nil
}

func (c *client) processMsgArgs(arg []byte) error {
	if c.trace {
		c.traceOutOp("MSG", arg)
	}

	// Unroll splitArgs to avoid runtime/heap issues
	a := [MAX_MSG_ARGS][]byte{}
	args := a[:0]
	start := -1
	for i, b := range arg {
		switch b {
		case ' ', '\t', '\r', '\n':
			if start >= 0 {
				args = append(args, arg[start:i])
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		args = append(args, arg[start:])
	}

	switch len(args) {
	case 3:
		c.pa.reply = nil
		c.pa.szb = args[2]
		c.pa.size = parseSize(args[2])
	case 4:
		c.pa.reply = args[2]
		c.pa.szb = args[3]
		c.pa.size = parseSize(args[3])
	default:
		return fmt.Errorf("processMsgArgs Parse Error: '%s'", arg)
	}
	if c.pa.size < 0 {
		return fmt.Errorf("processMsgArgs Bad or Missing Size: '%s'", arg)
	}

	// Common ones processed after check for arg length
	c.pa.subject = args[0]
	c.pa.sid = args[1]

	return nil
}

func (c *client) processPub(arg []byte) error {
	if c.trace {
		c.traceInOp("PUB", arg)
	}

	// Unroll splitArgs to avoid runtime/heap issues
	a := [MAX_PUB_ARGS][]byte{}
	args := a[:0]
	start := -1
	for i, b := range arg {
		switch b {
		case ' ', '\t':
			if start >= 0 {
				args = append(args, arg[start:i])
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		args = append(args, arg[start:])
	}

	switch len(args) {
	case 2:
		c.pa.subject = args[0]
		c.pa.reply = nil
		c.pa.size = parseSize(args[1])
		c.pa.szb = args[1]
	case 3:
		c.pa.subject = args[0]
		c.pa.reply = args[1]
		c.pa.size = parseSize(args[2])
		c.pa.szb = args[2]
	default:
		return fmt.Errorf("processPub Parse Error: '%s'", arg)
	}
	if c.pa.size < 0 {
		return fmt.Errorf("processPub Bad or Missing Size: '%s'", arg)
	}
	maxPayload := int64(c.srv.getOpts().MaxPayload)
	if int64(c.pa.size) > maxPayload {
		c.maxPayloadViolation(c.pa.size, maxPayload)
		return ErrMaxPayload
	}
	return nil
}

func splitArg(arg []byte) [][]byte {
	a := [MAX_MSG_ARGS][]byte{}
	args := a[:0]
	start := -1
	for i, b := range arg {
		switch b {
		case ' ', '\t', '\r', '\n':
			if start >= 0 {
				args = append(args, arg[start:i])
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		args = append(args, arg[start:])
	}
	return args
}

func (c *client) processSub(argo []byte) error {
	c.traceInOp("SUB", argo)

	// Copy so we do not reference a potentially large buffer
	arg := make([]byte, len(argo))
	copy(arg, argo)
	args := splitArg(arg)
	sub := &SubscribeCmd{
		Subject: args[0],
	}
	switch len(args) {
	case 2:
		sub.Queue = nil
		sub.SID = args[1]
	case 3:
		sub.Queue = args[1]
		sub.SID = args[2]
	default:
		return fmt.Errorf("processSub Parse Error: '%s'", arg)
	}
	if c.opts.Handlers.OnSubscribe != nil {
		newCmd, err := c.opts.Handlers.OnSubscribe(c, sub)
		if err != nil {
			return err
		}
		if newCmd != nil {
			sub = newCmd
			c.traceInOp("SUB NEW", []byte(fmt.Sprintf("SUB %s %s %s", sub.Subject, sub.Queue, sub.SID)))
		}
	}
	msgh := subHdr[:len(subHdr)]
	msgh = append(msgh, sub.Subject...)
	msgh = append(msgh, ' ')
	if len(sub.Queue) > 0 {
		msgh = append(msgh, sub.Queue...)
		msgh = append(msgh, ' ')
	}
	msgh = append(msgh, sub.SID...)
	msgh = append(msgh, crlfBytes...)
	_, err := c.wc.Write(msgh)
	if err == nil {
		return err
	}
	return nil
}

func (c *client) processUnsub(arg []byte) error {
	c.traceInOp("UNSUB", arg)

	args := splitArg(arg)
	unsub := &UnsubscribeCmd{
		SID: args[0],
	}

	switch len(args) {
	case 1:
	case 2:
		unsub.Max = parseSize(args[1])
	default:
		return fmt.Errorf("processUnsub Parse Error: '%s'", arg)
	}

	if c.opts.Handlers.OnUnsubscribe != nil {
		newCmd, err := c.opts.Handlers.OnUnsubscribe(c, unsub)
		if err != nil {
			return err
		}
		if newCmd != nil {
			unsub = newCmd
			c.traceInOp("SUB NEW", []byte(fmt.Sprintf("UNSUB %s %v", unsub.SID, unsub.Max)))
		}
	}
	msgh := unsubHdr[:len(unsubHdr)]
	msgh = append(msgh, unsub.SID...)
	if unsub.Max > 0 {
		msgh = append(msgh, ' ')
		msgh = append(msgh, fmt.Sprintf("%d", unsub.Max)...)
	}
	msgh = append(msgh, crlfBytes...)
	_, err := c.wc.Write(msgh)
	if err == nil {
		return err
	}
	return nil
}

// Used for handrolled itoa
const digits = "0123456789"

func (c *client) processMsg(msg []byte) (err error) {
	if c.typ == CLIENT {
		if c.trace {
			c.traceMsg(msg, false, false)
		}
		err = c.publishMsg(msg)
	} else {
		if c.trace {
			c.traceMsg(msg, true, false)
		}
		err = c.deliverMsg(msg)
	}
	if err != nil {
		if err == ErrAuthorization {
			c.authViolation()
			return
		}
		c.sendErr(err.Error())
		c.Errorf("MSG, %v: [%s]", err, string(msg[:len(msg)-LEN_CR_LF]))
	}
	return
}

func (c *client) publishMsg(msg []byte) error {
	cmd := &PublishCmd{
		Subject: c.pa.subject,
		Reply:   c.pa.reply,
		Msg:     msg,
	}
	if c.opts.Handlers.OnPublish != nil {
		newCmd, err := c.opts.Handlers.OnPublish(c, cmd)
		if err != nil {
			return err
		}
		if newCmd != nil {
			cmd = newCmd
			if !bytes.HasSuffix(cmd.Msg, crlfBytes) {
				cmd.Msg = append(cmd.Msg, crlfBytes...)
			}
			if c.trace {
				c.traceMsg(cmd.Msg, false, true)
			}
		}
	}
	data := cmd.Msg
	lenData := len(data) - 2
	maxPayload := c.srv.getOpts().MaxPayload
	if lenData > maxPayload {
		c.maxPayloadViolation(lenData, int64(maxPayload))
		return ErrMaxPayload
	}

	msgh := pubHdr[0:len(pubHdr)]
	msgh = append(msgh, cmd.Subject...)
	msgh = append(msgh, ' ')
	// reply to
	if len(cmd.Reply) > 0 {
		msgh = append(msgh, cmd.Reply...)
		msgh = append(msgh, ' ')
	}
	// append size
	var b [12]byte
	var i = len(b)
	if lenData > 0 {
		for l := lenData; l > 0; l /= 10 {
			i--
			b[i] = digits[l%10]
		}
	} else {
		i--
		b[i] = digits[0]
	}
	msgh = append(msgh, b[i:]...)
	msgh = append(msgh, CR_LF...)
	// publish command from client to backend
	_, err := c.wc.Write(msgh)
	if err == nil {
		_, err = c.wc.Write(data)
	}
	if err != nil {
		return err
	}
	return nil
}

func (c *client) deliverMsg(msg []byte) error {
	cmd := &MsgCmd{
		Subject: c.pa.subject,
		SID:     c.pa.sid,
		Reply:   c.pa.reply,
		Msg:     msg,
	}
	if c.opts.Handlers.OnMsg != nil {
		newCmd, err := c.opts.Handlers.OnMsg(c, cmd)
		if err != nil {
			return err
		}
		if newCmd != nil {
			cmd = newCmd
			if !bytes.HasSuffix(cmd.Msg, crlfBytes) {
				cmd.Msg = append(cmd.Msg, crlfBytes...)
			}
			c.traceMsg(cmd.Msg, false, true)
		}
	}

	// msg command, from backend to client, we'd likely to deliery in one write.
	// for example, in websocket connect, client can read whole command in one message
	msgh := msgHdr[0:len(msgHdr)]
	msgh = append(msgh, cmd.Subject...)
	msgh = append(msgh, ' ')
	// sid
	msgh = append(msgh, cmd.SID...)
	msgh = append(msgh, ' ')
	// reply to
	if len(cmd.Reply) > 0 {
		msgh = append(msgh, cmd.Reply...)
		msgh = append(msgh, ' ')
	}
	// append size
	data := cmd.Msg
	lenData := len(data) - 2
	var b [12]byte
	var i = len(b)
	if lenData > 0 {
		for l := lenData; l > 0; l /= 10 {
			i--
			b[i] = digits[l%10]
		}
	} else {
		i--
		b[i] = digits[0]
	}
	msgh = append(msgh, b[i:]...)
	msgh = append(msgh, CR_LF...)
	msgh = append(msgh, data...)
	_, err := c.wc.Write(msgh)
	if err == nil {
		return err
	}
	return nil
}

// do not handle router's connection
func (c *client) processInfo(arg []byte) error {
	c.traceOutOp("INFO", arg)
	cmd := &InfoCmd{
		Payload: arg,
	}
	if c.opts.Handlers.OnInfo != nil {
		newCmd, err := c.opts.Handlers.OnInfo(c, cmd)
		if err != nil {
			return err
		}
		if newCmd != nil {
			cmd = newCmd
			c.traceInOp("INFO NEW", cmd.Payload)
		}
	}
	msgh := infoHdr[:len(infoHdr)]
	msgh = append(msgh, cmd.Payload...)
	if !bytes.HasSuffix(cmd.Payload, crlfBytes) {
		msgh = append(msgh, crlfBytes...)
	}
	_, err := c.wc.Write(msgh)
	if err == nil {
		return err
	}
	return nil
}

// do not handle -ERR
func (c *client) processErr(errStr string) {
	c.traceOutOp("-ERR", []byte(errStr))
}

func (c *client) maxPayloadViolation(sz int, max int64) {
	c.Errorf("%s: %d vs %d", ErrMaxPayload.Error(), sz, max)
	c.sendErr("Maximum Payload Violation")
	c.closeConnection()
}

func (c *client) sendErr(err string) {
	if c.typ == CLIENT {
		c.nc.Write([]byte(fmt.Sprintf("-ERR '%s'\r\n", err)))
	}
}

func (c *client) typeString() string {
	switch c.typ {
	case CLIENT:
		return "Client"
	case ROUTER:
		return "Router"
	case BACKEND:
		return "Backend"
	}
	return "Unknown Type"
}

func (c *client) closeConnection() {
	c.closeOnce.Do(func() {
		c.Debugf("Connection closed %s - %s", c.cid, c.typeString())
		c.wc.Close()
	})
}

func (c *client) isAuthTimerSet() bool { return false }

func (c *client) processPing() {
	c.traceInOp("PING", nil)
	c.wc.Write(pingBytes)
}

func (c *client) processPong() {
	c.traceOutOp("PONG", nil)
	c.wc.Write(pongBytes)
}

func (c *client) authViolation() {
	c.sendErr("Authorization Violation")
	c.closeConnection()
}

func (c *client) traceMsg(msg []byte, out, mod bool) {
	if !c.trace {
		return
	}
	var modStr string
	if mod {
		modStr = " NEW"
	}
	if out {
		c.Debugf("<<- MSG_PAYLOAD%s: [%s]", modStr, string(msg[:len(msg)-LEN_CR_LF]))
		return
	}
	c.Debugf("->> MSG_PAYLOAD%s: [%s]", modStr, string(msg[:len(msg)-LEN_CR_LF]))
}

func (c *client) traceInOp(op string, arg []byte) {
	c.traceOp("->> %s", op, arg)
}

func (c *client) traceOutOp(op string, arg []byte) {
	c.traceOp("<<- %s", op, arg)
}

func (c *client) traceOp(format, op string, arg []byte) {
	if !c.trace {
		return
	}

	opa := []interface{}{}
	if op != "" {
		opa = append(opa, op)
	}
	if arg != nil {
		opa = append(opa, string(arg))
	}
	c.Debugf(format, opa)
}

// Ascii numbers 0-9
const (
	asciiZero = 48
	asciiNine = 57
)

// parseSize expects decimal positive numbers. We
// return -1 to signal error.
func parseSize(d []byte) (n int) {
	l := len(d)
	if l == 0 {
		return -1
	}
	var (
		i   int
		dec byte
	)

	// Note: Use `goto` here to avoid for loop in order
	// to have the function be inlined.
	// See: https://github.com/golang/go/issues/14768
loop:
	dec = d[i]
	if dec < asciiZero || dec > asciiNine {
		return -1
	}
	n = n*10 + (int(dec) - asciiZero)

	i++
	if i < l {
		goto loop
	}
	return n
}
