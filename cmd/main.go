// Modified from https://github.com/orus-io/nats-websocket-gw/blob/master/cmd/nats-websocket-gw/main.go
// Copyright 2018 Antmanler
// Copyright (c) 2017 orus-io

package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/antmanler/gnatsd-gw/gnatsdgw"
	fwd "github.com/antmanler/gnatsd-gw/gnatsdgw/server"
	"github.com/antmanler/gnatsd-gw/gnatsdgw/ws"
	"github.com/gorilla/websocket"
)

func usage() {
	fmt.Printf(`Usage: %s [ --help ] [ --no-origin-check ] [ --trace ]
`, os.Args[0])
}

func main() {
	options := &fwd.Options{
		Logger: simpleLogger{},
		Handlers: fwd.Handlers{
			OnInfo: func(client fwd.Forwarder, cmd *fwd.InfoCmd) (newCmd *fwd.InfoCmd, err error) {
				log.Printf("INFO: %s\n", cmd.Payload)
				if val, ok := client.Get(gnatsdgw.B2CConnKey); ok {
					log.Printf("Backend: %s\n", val.(net.Conn).RemoteAddr())
				}
				if val, ok := client.Get(gnatsdgw.C2BConnKey); ok {
					log.Printf("HOST: %s\n", val.(*ws.Conn).Request.Host)
				}
				return
			},
		},
	}

	var upgrader *websocket.Upgrader
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--help":
			usage()
			return
		case "--no-origin-check":
			upgrader = &websocket.Upgrader{
				ReadBufferSize:  1024,
				WriteBufferSize: 1024,
				CheckOrigin:     func(r *http.Request) bool { return true },
			}
		case "--trace":
			options.Trace = true
		default:
			fmt.Printf("Invalid args: %s\n\n", arg)
			usage()
			return
		}
	}

	handler, err := ws.NewHandler(
		options,
		func() (net.Conn, error) {
			return net.Dial("tcp", "localhost:4222")
		},
		upgrader,
		nil,
	)
	if err != nil {
		log.Fatalln(err)
	}
	http.Handle("/nats", handler)
	http.ListenAndServe("0.0.0.0:8910", nil)
}

type simpleLogger struct{}

func (simpleLogger) Debugf(format string, args ...interface{}) {
	log.Println(fmt.Sprintf("D "+format, args...))
}

func (simpleLogger) Infof(format string, args ...interface{}) {
	log.Println(fmt.Sprintf("I "+format, args...))
}

func (simpleLogger) Warningf(format string, args ...interface{}) {
	log.Println(fmt.Sprintf("W "+format, args...))
}

func (simpleLogger) Errorf(format string, args ...interface{}) {
	log.Println(fmt.Sprintf("E "+format, args...))
}
