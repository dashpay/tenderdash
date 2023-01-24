package consensus

import (
	"context"

	"github.com/tendermint/tendermint/libs/log"
)

type contextKey int

const (
	usePeerQueueCtx contextKey = iota
	msgInfoCtx
	loggerCtx
)

// ctxWithPeerQueue adds a key into the context with true value
// this function is used for test
func ctxWithPeerQueue(ctx context.Context) context.Context {
	return context.WithValue(ctx, usePeerQueueCtx, true)
}

// peerQueueFromCtx returns true if a key has been set, otherwise returns false
// this is used by chanMsgSender to send the message via peer-queue even if peerID hasn't been provided
func peerQueueFromCtx(ctx context.Context) bool {
	val := ctx.Value(usePeerQueueCtx)
	if val != nil {
		return val.(bool)
	}
	return false
}

// msgInfoWithCtx puts msgInfo into the context
func msgInfoWithCtx(ctx context.Context, mi msgInfo) context.Context {
	return context.WithValue(ctx, msgInfoCtx, mi)
}

// msgInfoWithCtx gets msgInfo from the context
func msgInfoFromCtx(ctx context.Context) msgInfo {
	val := ctx.Value(msgInfoCtx)
	return val.(msgInfo)
}

// ctxWithLogger adds a logger instance to a context
func ctxWithLogger(ctx context.Context, logger log.Logger) context.Context {
	return context.WithValue(ctx, loggerCtx, logger)
}

// loggerFromCtxOrNop gets a logger instance from a context
// returns Nop logger if logget didn't add
func loggerFromCtxOrNop(ctx context.Context) log.Logger {
	val := ctx.Value(loggerCtx)
	if val != nil {
		return val.(log.Logger)
	}
	return log.NewNopLogger()
}
