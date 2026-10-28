package main

import (
	"github.com/immortal-autumn/WxChatAssistant/internal/native"
	"testing"
)

func TestWaitFiltersBacklogControlAndOutgoing(t *testing.T) {
	message := native.Message{Peer: "alice", From: "alice", Type: 1, Timestamp: 100}
	if !waitMatches(message, "alice", 100) {
		t.Fatal("new matching message rejected")
	}
	if waitMatches(message, "bob", 100) || waitMatches(message, "", 101) {
		t.Fatal("unrelated or old message accepted")
	}
	message.Outgoing = true
	if waitMatches(message, "", 100) {
		t.Fatal("outgoing send woke waiter")
	}
	message.Outgoing = false
	message.Type = 51
	if waitMatches(message, "", 100) {
		t.Fatal("control event woke waiter")
	}
}
