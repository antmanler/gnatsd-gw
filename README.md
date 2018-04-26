gnatsd-gw
=========
A utility lib make it easy to write a gateway in front of gnatsd with middlewares

Features:

- Leverage the official zero-alloc parser to handle connections
- Can inspect most commands in nats proto
- TCP & Websocket gateway
- Embeded as a library

## How it works

Thanks to the clean design of gnatsd, I synced the parser.go from [gnatsd/server/parser.go](https://github.com/nats-io/gnatsd/blob/master/server/parser.go) and rewrite missing method in client wrap it as a unidirectional fowarder leaving custom callbacks to handle protocal commands.

 - CLIENT -> BACKEND share the logical of serving a client in nats server,
 - BACKEND -> CLIENT share the logical of serving a router.

## Basic usage

Building a websocket gateway to use nats in browser.

```go
package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"

	fwd "github.com/antmanler/gnatsd-gw/gnatsdgw/server"
	"github.com/antmanler/gnatsd-gw/gnatsdgw/ws"
)

func main() {

	handler, err := ws.NewHandler(
		&fwd.Options{},
		func() (net.Conn, error) {
			return net.Dial("tcp", "localhost:4222")
		},
		nil,
		nil,
	)
	if err != nil {
		log.Fatalln(err)
	}
	http.Handle("/nats", handler)
	http.ListenAndServe("0.0.0.0:8910", nil)
}
```

### Supported NATS commands


To modify the original command a newCmd should be returned with err is nil, if err is not nil, a -ERR command will be sent to client, and close the underlying connections.

```go
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
type ConnectHandler func(client Forwarder, cmd *ConnectCmd) (newCmd *ConnectCmd, err error)

// PublishCmd is client issued PUB command
type PublishCmd struct {
	Subject []byte
	Reply   []byte
	Msg     []byte
}
type PublishHandler func(client Forwarder, cmd *PublishCmd) (newCmd *PublishCmd, err error)

// SubscribeCmd is client issued SUB command
type SubscribeCmd struct {
	Subject []byte
	Queue   []byte
	SID     []byte
}
type SubscribeHandler func(client Forwarder, cmd *SubscribeCmd) (newCmd *SubscribeCmd, err error)

// UnsubscribeCmd is client issued UNSUB command
type UnsubscribeCmd struct {
	SID []byte
	Max int
}
type UnsubscribeHandler func(client Forwarder, cmd *UnsubscribeCmd) (newCmd *UnsubscribeCmd, err error)

// InfoCmd is callback to handle server side info command.
type InfoCmd struct {
	Payload []byte
}
type InfoHandler func(client Forwarder, cmd *InfoCmd) (newCmd *InfoCmd, err error)

// MsgCmd is callback to handle server pushed msg.
type MsgCmd struct {
	Subject []byte
	SID     []byte
	Reply   []byte
	Msg     []byte
}
type MsgHandler func(client Forwarder, cmd *MsgCmd) (newCmd *MsgCmd, err error)
```

### More use cases

- Act as border gateway enforce tls for connections from public
- Rewrite requests, publish subject
- Filter out illegal messages passing thru the gateway
- Modify message, for example adding extra fields or tags which is tranparent to users
- Route message to another subject


##  Dev

Keep `parser.go` up to date with officail

```shell
curl 'https://raw.githubusercontent.com/nats-io/gnatsd/master/server/parser.go' > /tmp/parser.go && vimdiff gnatsdgw/server/parser.go /tmp/parser.go
```

## Credits

### Parser

- [gnatsd](https://github.com/nats-io/gnatsd) High-Performance server for NATS, the cloud native messaging system.

## Inspired by

- [nats-websocket-gw](https://github.com/orus-io/nats-websocket-gw) A websocket to NATS gateway
- [websocket-nats](https://github.com/isobit/websocket-nats) An in-browser websocket client for NATS
