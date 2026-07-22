package httpx

import (
	"context"
	"net/netip"
)

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	remoteIPKey  contextKey = "remote_ip"
	schemeKey    contextKey = "scheme"
	userIDKey    contextKey = "user_id"
)

func WithRequestID(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, requestIDKey, value)
}

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func WithRemoteIP(ctx context.Context, value netip.Addr) context.Context {
	return context.WithValue(ctx, remoteIPKey, value)
}

func RemoteIP(ctx context.Context) (netip.Addr, bool) {
	value, ok := ctx.Value(remoteIPKey).(netip.Addr)
	return value, ok
}

func WithScheme(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, schemeKey, value)
}

func Scheme(ctx context.Context) string {
	value, _ := ctx.Value(schemeKey).(string)
	return value
}

func WithUserID(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, userIDKey, value)
}

func UserID(ctx context.Context) string {
	value, _ := ctx.Value(userIDKey).(string)
	return value
}
