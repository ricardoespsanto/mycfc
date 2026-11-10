package main

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"net"
	"net/url"
	"strings"
)

func safeLocalDatabaseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil || u.Fragment != "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	for key, values := range q {
		if len(values) != 1 {
			return false
		}
		switch strings.ToLower(key) {
		case "sslmode", "sslrootcert", "sslcert", "sslkey", "application_name", "connect_timeout":
		default:
			return false
		}
	}
	return true
}

// This guard is installed only on unsigned local database command handles. It
// checks the real connected socket before pgx sends authentication/startup/TLS.
// It does not establish fixture ownership; the independent manifest does that.
func installLocalRecipientDialGuard(c *pgx.ConnConfig) {
	underlying := c.DialFunc
	if underlying == nil {
		underlying = (&net.Dialer{}).DialContext
	}
	c.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		socket, err := underlying(ctx, network, address)
		if err != nil {
			return nil, err
		}
		peer, ok := socket.RemoteAddr().(*net.TCPAddr)
		if !ok || peer.IP == nil || !peer.IP.IsLoopback() {
			socket.Close()
			return nil, errors.New("local database socket is not loopback")
		}
		return socket, nil
	}
}
