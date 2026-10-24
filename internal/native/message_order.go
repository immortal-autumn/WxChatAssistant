package native

import "sort"

// MergeMessageOrder enriches a local send receipt with its authoritative sync
// echo. Receipts do not carry MsgSeq and use the local clock until sync arrives.
func MergeMessageOrder(existing *Message, update Message) {
	if existing.ID == update.ID && existing.Peer == update.Peer && update.Sequence != 0 {
		existing.Sequence = update.Sequence
		existing.Timestamp = update.Timestamp
	}
}

// SortMessages orders CreateTime in seconds, then MsgSeq within a conversation.
// Messages without a sequence retain their slots in arrival order. Server IDs
// are opaque identifiers and must not be treated as chronological counters.
func SortMessages(messages []Message) {
	sort.SliceStable(messages, func(i, j int) bool { return messages[i].Timestamp < messages[j].Timestamp })
	for start := 0; start < len(messages); {
		end := start + 1
		for end < len(messages) && messages[end].Timestamp == messages[start].Timestamp {
			end++
		}
		byPeer := map[string][]int{}
		for i := start; i < end; i++ {
			if messages[i].Sequence != 0 {
				byPeer[messages[i].Peer] = append(byPeer[messages[i].Peer], i)
			}
		}
		for _, indices := range byPeer {
			if len(indices) < 2 {
				continue
			}
			ordered := make([]Message, len(indices))
			for i, index := range indices {
				ordered[i] = messages[index]
			}
			sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Sequence < ordered[j].Sequence })
			for i, index := range indices {
				messages[index] = ordered[i]
			}
		}
		start = end
	}
}
