package bridge

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SocketName is the socket a running daemon listens on, below its data
// directory. A child process the daemon spawned reaches it through
// `VBCDD_HARMONY_SOCKET`, and names its own profile through
// `VBCDD_HARMONY_PROFILE`.
const SocketName = "harmony.sock"

// The variables the daemon gives every child it spawns, so a tool can ask for a
// handoff or a clear without knowing where it is.
const (
	// EnvSocket names the socket.
	EnvSocket = "VBCDD_HARMONY_SOCKET"
	// EnvProfile names the profile whose child this is.
	EnvProfile = "VBCDD_HARMONY_PROFILE"
)

// socketTimeout bounds one exchange with the daemon.
const socketTimeout = 5 * time.Second

// SocketPath returns the socket a daemon started with this data directory
// listens on.
func SocketPath(dataDir string) string {
	return filepath.Join(dataDir, SocketName)
}

// Listen serves the control socket until the context is canceled. A socket left
// behind by a process that died is removed first, so a restart always listens.
func (b *Bridge) Listen(ctx context.Context, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "unix", path)
	if err != nil {
		return err
	}
	defer listener.Close()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	var conn net.Conn
	for {
		conn, err = listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go b.serve(conn)
	}
}

// serve answers one request: `<verb> <profile>`, where the verb is `handoff` or
// `clear`. The reply is `ok`, or the reason it failed.
func (b *Bridge) serve(conn net.Conn) {
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(socketTimeout)); err != nil {
		return
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return
	}
	verb, name, _ := strings.Cut(strings.TrimSpace(line), " ")
	if dispatchErr := b.dispatch(verb, name); dispatchErr != nil {
		fmt.Fprintf(conn, "error: %v\n", dispatchErr)
		return
	}
	fmt.Fprintln(conn, "ok")
}

// dispatch runs one request.
func (b *Bridge) dispatch(verb, name string) error {
	switch verb {
	case "handoff":
		return b.Handoff(name)
	case "clear":
		return b.Clear(name)
	default:
		return fmt.Errorf("unknown request %q", verb)
	}
}

// Request sends one request to a running daemon and returns its answer.
func Request(ctx context.Context, path, verb, name string) error {
	dialer := net.Dialer{Timeout: socketTimeout}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(socketTimeout)); err != nil {
		return err
	}
	if _, err = fmt.Fprintf(conn, "%s %s\n", verb, name); err != nil {
		return err
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return err
	}
	reply = strings.TrimSpace(reply)
	if reply != "ok" {
		return errors.New(reply)
	}
	return nil
}
