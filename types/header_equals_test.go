package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHeaderEqualsUsesWireTimeSemantics(t *testing.T) {
	now := time.Now()
	header := &Header{Time: now}
	sameInstant := &Header{Time: now.In(time.FixedZone("remote", 2*60*60))}
	withoutMonotonic := &Header{Time: time.Unix(0, now.UnixNano()).UTC()}

	require.True(t, header.Equals(sameInstant))
	require.True(t, header.Equals(withoutMonotonic))
}
