package logparser

import "fmt"

// Filter defines filtering criteria for logs
type Filter struct {
	Height          int64
	Round           int64
	AuthorProTxHash string
}

// check checks if the provided log item passes the filter
func (f Filter) check(item logItem) bool {
	if f.Height != -1 && f.Height != item.Height {
		return false
	}
	if f.Round != -1 && f.Round != item.Round {
		return false
	}
	if f.AuthorProTxHash != "" && f.AuthorProTxHash != item.AuthorProTxHash {
		return false
	}

	return true
}

// String shows human-readable info about this filter
func (f Filter) String() string {
	return fmt.Sprintf("height: %d, round: %d, author proTxHash: %s", f.Height, f.Round, f.AuthorProTxHash)
}
