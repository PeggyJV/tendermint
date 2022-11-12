package observe

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/tendermint/tendermint/crypto"
)

const traceIDKey = "trace-id"

func WithTraceID(ctx context.Context) context.Context {
	if _, ok := getTraceID(ctx); ok {
		return ctx
	}
	b := crypto.CRandBytes(24)
	id := base64.StdEncoding.EncodeToString(b)
	return context.WithValue(ctx, traceIDKey, id)
}

func TraceID(ctx context.Context) string {
	s, _ := getTraceID(ctx)
	return s
}

func getTraceID(ctx context.Context) (string, bool) {
	s, ok := ctx.Value(traceIDKey).(string)
	return s, ok
}

// ------------------------------------------------------

const startKey = "start"

func WithStartTime(ctx context.Context) context.Context {
	if _, ok := getStartTime(ctx); ok {
		return ctx
	}
	start := time.Now()
	return context.WithValue(ctx, startKey, start)
}

func StartTime(ctx context.Context) time.Time {
	t, _ := getStartTime(ctx)
	return t
}

func getStartTime(ctx context.Context) (time.Time, bool) {
	t, ok := ctx.Value(startKey).(time.Time)
	return t, ok
}

func Since(ctx context.Context) time.Duration {
	t, ok := getStartTime(ctx)
	if !ok {
		return -1
	}
	return time.Since(t)
}
