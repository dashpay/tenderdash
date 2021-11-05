package logparser

import (
	"fmt"
)

const msgSendingVote = "Sending vote message"

// VoteParser parses "Sending vote message" logs to build graph showing how votes
// travel through the gossip protocol
type VoteParser struct {
	filter Filter
}

// NewVoteParser defines new vote parser that is used to interpret "Sending vote message"
func NewVoteParser(f Filter) Parser {
	return &VoteParser{
		filter: f,
	}
}

// Check implements Parser
func (v VoteParser) Check(item logItem) bool {
	if item.Msg != msgSendingVote {
		return false
	}
	return v.filter.check(item)
}

// Parse implements Parser
func (v VoteParser) Parse(item logItem) (src string, dst string, label string, err error) {
	label = fmt.Sprintf("%s\n(H:%d/R:%d)\n%s", item.AuthorProTxHash, item.Height, item.Round, item.Time())
	return item.ProTxHash, item.PeerProTxHash, label, nil
}
