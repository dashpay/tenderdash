package log

import "context"

type contextKey int

const loggerCtx contextKey = iota

// CtxWithLogger adds a logger instance to a context
func CtxWithLogger(ctx context.Context, logger Logger) context.Context {
	return context.WithValue(ctx, loggerCtx, logger)
}

// FromCtxOrNop gets a logger instance from a context
// returns Nop logger if logget didn't add
func FromCtxOrNop(ctx context.Context) Logger {
	val := ctx.Value(loggerCtx)
	if val != nil {
		return val.(Logger)
	}
	return NewNopLogger()
}
