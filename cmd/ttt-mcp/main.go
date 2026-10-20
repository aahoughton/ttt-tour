// Command ttt-mcp is an MCP server over stdio that lets an agent give code
// tours in ttt through the tour plugin, and talk with the user in the
// plugin's Tour pane.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
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
	port := flag.Int("port", defaultPort, "loopback port for the tour plugin (the plugin itself only uses 4250)")
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
	h.addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(*port))
	// A taken port is not fatal: tool calls report it to the agent and
	// retry, which reaches the user sooner than a server that fails to start.
	if err := h.listen(); err != nil {
		log.Print(err)
	}
	if err := newServer(h, *channel).serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Print(err)
	}
}
