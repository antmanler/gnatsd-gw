// Copyright 2018 Antmanler
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
	"io"
)

// Type of client type
type Type int

// Type of client connection.
const (
	// CLIENT2BACKEND is an end user.
	CLIENT2BACKEND Type = iota
	// BACKEND is proxy upstream
	BACKEND2CLIENT
)

// Forwarder is interface for a remote connection from user to upstream NATS server
type Forwarder interface {
	// Block and run ioloop
	Run(typ Type, r io.Reader, wc io.WriteCloser)

	// Stops the furture insepction, pipe the following inbound bytes to backend server.
	PipeIO()

	// KV interface for set/get custom data associated with current client
	Set(key, value interface{})
	Get(key interface{}) (value interface{}, ok bool)
	Range(func(key, value interface{}) bool)
}

// Options to config gateway server
type Options struct {
	MaxControlLine int `json:"max_control_line"`
	MaxPayload     int `json:"max_payload"`

	Trace bool `json:"trace"`

	Logger   Logger
	Handlers Handlers
}

// Handlers ars handler to modify of filter client side commands
type Handlers struct {
	// peer to backend
	OnConnect     ConnectHandler
	OnPublish     PublishHandler
	OnSubscribe   SubscribeHandler
	OnUnsubscribe UnsubscribeHandler
	// backend to peer
	OnMsg  MsgHandler
	OnInfo InfoHandler
}

// ClientOptions is options client will pass to server, when auth is required
type ClientOptions struct {
	Verbose       bool   `json:"verbose"`
	Pedantic      bool   `json:"pedantic"`
	TLSRequired   bool   `json:"tls_required"`
	Authorization string `json:"auth_token"`
	Username      string `json:"user"`
	Password      string `json:"pass"`
	Name          string `json:"name"`
	Lang          string `json:"lang"`
	Version       string `json:"version"`
	Protocol      int    `json:"protocol"`
}

// ConnectCmd is client issued CONNECT command
type ConnectCmd struct {
	// Payload is bytes of "ClientOptions"
	Payload []byte
}

// ConnectHandler is callback to handle client side connect command.
// To modify the original command a newCmd should be returned with err is nil,
// if err is not nil, a -ERR command will be sent to client, and close the underlying connections.
type ConnectHandler func(client Forwarder, cmd *ConnectCmd) (newCmd *ConnectCmd, err error)

// PublishCmd is client issued PUB command
type PublishCmd struct {
	Subject []byte
	Reply   []byte
	Msg     []byte
}

// PublishHandler is callback to handle client side publish command.
// To modify the original command a newCmd should be returned with err is nil,
// if err is not nil, a -ERR command will be sent to client, and close the underlying connections.
type PublishHandler func(client Forwarder, cmd *PublishCmd) (newCmd *PublishCmd, err error)

// SubscribeCmd is client issued SUB command
type SubscribeCmd struct {
	Subject []byte
	Queue   []byte
	SID     []byte
}

// SubscribeHandler is callback to handle client side subscribe command.
// To modify the original command a newCmd should be returned with err is nil,
// if err is not nil, a -ERR command will be sent to client, and close the underlying connections.
type SubscribeHandler func(client Forwarder, cmd *SubscribeCmd) (newCmd *SubscribeCmd, err error)

// UnsubscribeCmd is client issued UNSUB command
type UnsubscribeCmd struct {
	SID []byte
	Max int
}

// UnsubscribeHandler is callback to handle client side unsubscribe command.
// To modify the original command a newCmd should be returned with err is nil,
// if err is not nil, a -ERR command will be sent to client, and close the underlying connections.
type UnsubscribeHandler func(client Forwarder, cmd *UnsubscribeCmd) (newCmd *UnsubscribeCmd, err error)

// InfoCmd is callback to handle server side info command.
type InfoCmd struct {
	Payload []byte
}

// InfoHandler is callback to handle server side info command.
// To modify the original command a newCmd should be returned with err is nil,
// if err is not nil, a -ERR command will be sent to client, and close the underlying connections.
type InfoHandler func(client Forwarder, cmd *InfoCmd) (newCmd *InfoCmd, err error)

// MsgCmd is callback to handle server pushed msg.
type MsgCmd struct {
	Subject []byte
	SID     []byte
	Reply   []byte
	Msg     []byte
}

// MsgHandler is callback to handle server side message command.
// To modify the original command a newCmd should be returned with err is nil,
// if err is not nil, a -ERR command will be sent to client, and close the underlying connections.
type MsgHandler func(client Forwarder, cmd *MsgCmd) (newCmd *MsgCmd, err error)

// Logger is interface to record logs
type Logger interface {
	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Warningf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}
