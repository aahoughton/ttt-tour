package main

import (
	"context"
	"time"
)

const channelPollWait = 25 * time.Second

// relayMessages forwards what the user types in the Tour pane to Claude Code
// as channel notifications until ctx ends.
func (s *server) relayMessages(ctx context.Context) {
	for ctx.Err() == nil {
		msgs, err := s.b.recv(ctx, channelPollWait)
		if err != nil {
			continue
		}
		for _, m := range msgs {
			meta := map[string]string{}
			if m.At != "" {
				meta["at"] = m.At
			}
			s.notify("notifications/claude/channel", map[string]any{"content": m.Text, "meta": meta})
		}
	}
}
