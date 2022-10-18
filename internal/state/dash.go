package state

type UpdateProvider interface {
	UpdateFunc(State) (State, error)
}

// UpdateFunc is a function that can be used to update state
type UpdateFunc func(State) (State, error)

// PrepareStateUpdates generates state updates that will set Dash-related state fields.
func PrepareStateUpdates(updateProviders ...UpdateProvider) ([]UpdateFunc, error) {
	updates := make([]UpdateFunc, 0, len(updateProviders))
	for _, update := range updateProviders {
		updates = append(updates, update.UpdateFunc)
	}
	return updates, nil
}

func executeStateUpdates(state State, updates ...UpdateFunc) (State, error) {
	var err error
	for _, update := range updates {
		state, err = update(state)
		if err != nil {
			return State{}, err
		}
	}
	return state, nil
}
