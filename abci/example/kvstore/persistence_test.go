package kvstore

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPersistPreservesStateDuringFailedWrite(t *testing.T) {
	for _, partial := range []bool{false, true} {
		name := "before header"
		if partial {
			name = "partial header"
		}
		t.Run(name, func(t *testing.T) {
			cfg := DefaultConfig(t.TempDir())
			app, err := NewPersistentApp(cfg)
			require.NoError(t, err)
			defer app.Close()
			app.LastCommittedState.IncrementHeight()
			require.NoError(t, app.LastCommittedState.Set([]byte("key"), []byte("committed")))
			require.NoError(t, app.persist())
			checkRestart := func() {
				restarted, err := NewPersistentApp(cfg)
				require.NoError(t, err)
				defer restarted.Close()
				require.Equal(t, int64(1), restarted.LastCommittedState.GetHeight())
				value, err := restarted.LastCommittedState.Get([]byte("key"))
				require.NoError(t, err)
				require.Equal(t, []byte("committed"), value)
			}
			writeErr := errors.New("interrupted state write")
			app.LastCommittedState = &failingSaveState{
				State: app.LastCommittedState,
				save: func(w io.Writer) error {
					if partial {
						_, err := io.WriteString(w, `{"height":`)
						require.NoError(t, err)
					}
					checkRestart()
					return writeErr
				},
			}
			require.ErrorIs(t, app.persist(), writeErr)
			checkRestart()
		})
	}
}

type failingSaveState struct {
	State
	save func(io.Writer) error
}

func (s *failingSaveState) Save(w io.Writer) error { return s.save(w) }
