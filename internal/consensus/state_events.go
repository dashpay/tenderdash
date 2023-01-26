package consensus

import "github.com/tendermint/tendermint/libs/events"

const (
	setProposedAppVersion = "setProposedAppVersion"
	setPrivValidator      = "setPrivValidator"
	setReplayMode         = "setReplayMode"
)

// eventSwitchSubscriber is a subscriber interface to an observer
// This interface should be implemented by the listener to hide the subscribing process at a subject
type eventSwitchSubscriber interface {
	subscribe(events.EventSwitch)
}
