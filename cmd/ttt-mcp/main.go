// Command ttt-mcp is an MCP server over stdio that lets an agent give code
// tours in ttt through the tour plugin, and talk with the user in the
// plugin's Tour pane.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
)

var version = "dev"

// defaultPort is where the tour plugin looks for ttt-mcp.
const defaultPort = 4250

func main() {
	log.SetFlags(0)
	log.SetPrefix("ttt-mcp: ")
	channel := flag.Bool("channel", false, "push messages from the Tour pane to Claude Code as channel notifications\n(start Claude with --dangerously-load-development-channels server:<name>)")
	port := flag.Int("port", defaultPort, "loopback port the tour plugin connects to")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: ttt-mcp [--channel] [--port N]\n\nstdio MCP server for code tours in ttt; the tour plugin connects to it on 127.0.0.1.")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() > 0 || *port <= 0 || *port > 65535 {
		flag.Usage()
		os.Exit(2)
	}
	h := newHub()
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)))
	if err != nil {
		log.Printf("cannot listen for the tour plugin: %v (is another ttt-mcp running?)", err)
	} else {
		go func() {
			if err := http.Serve(ln, h.handler()); err != nil && !errors.Is(err, net.ErrClosed) {
				log.Print(err)
			}
		}()
	}
	if err := newServer(h, *channel).serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Print(err)
	}
}
