package logparser

import "time"

type logItem struct {
	// Timestamp       string `json:"ts"`
	Timestamp       time.Time `json:"ts"`
	Msg             string    `json:"_msg"`
	ProTxHash       string    `json:"proTxHash"`
	PeerProTxHash   string    `json:"peer_proTxHash"`
	AuthorProTxHash string    `json:"val_proTxHash"`
	Height          int64
	Round           int64
}

// Time returns string representing the "time" part of the log item timestamp (hour/min/sec/msec)
func (l logItem) Time() string {
	return l.Timestamp.Format("15:04:05.999")
}
