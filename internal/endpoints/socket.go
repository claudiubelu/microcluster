package endpoints

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/user"
	"strconv"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// Socket represents a unix socket with a given path.
type Socket struct {
	Path  string
	Group string

	listener *net.UnixListener
	server   *http.Server

	ctx    context.Context
	cancel context.CancelFunc
}

// NewSocket returns a Socket struct with no listener attached yet.
func NewSocket(ctx context.Context, server *http.Server, path api.URL, group string) *Socket {
	ctx, cancel := context.WithCancel(ctx)
	return &Socket{
		Path:  path.Hostname(),
		Group: group,

		server: server,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Type returns the type of the Endpoint.
func (s *Socket) Type() EndpointType {
	return EndpointControl
}

// Listen on the unix socket path.
func (s *Socket) Listen() error {
	_, err := net.Dial("unix", s.Path)
	if err == nil {
		return fmt.Errorf("Unix socket at %q is already running", s.Path)
	}

	err = s.removeStale()
	if err != nil {
		return err
	}

	addr, err := net.ResolveUnixAddr("unix", s.Path)
	if err != nil {
		return fmt.Errorf("Cannot resolve socket address: %w", err)
	}

	s.listener, err = net.ListenUnix("unix", addr)
	if err != nil {
		return fmt.Errorf("Cannot bind socket: %w", err)
	}

	err = localSetAccess(s.Path, s.Group)
	if err != nil {
		closeErr := s.listener.Close()
		if closeErr != nil {
			logger.Error("Failed to close socket listener", logger.Ctx{"error": closeErr})
		}

		return err
	}

	return nil
}

// Serve binds to the Socket's server.
func (s *Socket) Serve() {
	if s.listener == nil {
		return
	}

	ctx := logger.Ctx{"socket": s.listener.Addr()}
	logger.Info(" - binding control socket", ctx)

	go func() {
		select {
		case <-s.ctx.Done():
			logger.Infof("Received shutdown signal - aborting unix socket server startup")
		default:
			err := s.server.Serve(s.listener)
			if err != nil {
				select {
				case <-s.ctx.Done():
					logger.Infof("Received shutdown signal - aborting unix socket server startup")
					if err := s.server.Shutdown(context.Background()); err != nil {
						logger.Error("Failed to shutdown server", logger.Ctx{"err": err})
					}
				default:
					logger.Error("Failed to start server", logger.Ctx{"err": err})
				}
			}
		}
	}()
}

// Close the Socket's listener.
func (s *Socket) Close() error {
	if s.listener == nil {
		return nil
	}

	logger.Info("Stopping REST API handler - closing socket", logger.Ctx{"socket": s.listener.Addr()})
	s.cancel()

	return s.listener.Close()
}

// Remove any stale socket file at the given path.
func (s *Socket) removeStale() error {
	// If there's no socket file at all, there's nothing to do.
	if !shared.PathExists(s.Path) {
		return nil
	}

	logger.Debugf("Detected stale control socket, deleting")
	err := os.Remove(s.Path)
	if err != nil {
		return fmt.Errorf("Could not delete stale local socket: %w", err)
	}

	return nil
}

// Change the file mode and ownership of the local endpoint control socket file,
// so access is granted only to the process user and to the given group (or the
// process group if group is empty).
func localSetAccess(path string, group string) error {
	err := socketControlSetPermissions(path, 0660)
	if err != nil {
		return err
	}

	err = socketControlSetOwnership(path, group)
	if err != nil {
		return err
	}

	return nil
}

// Change the file mode of the given control socket file.
func socketControlSetPermissions(path string, mode os.FileMode) error {
	err := os.Chmod(path, mode)
	if err != nil {
		return fmt.Errorf("Cannot set permissions on local socket: %w", err)
	}

	return nil
}

// Change the ownership of the given control socket file.
func socketControlSetOwnership(path string, groupName string) error {
	var gid int
	var err error

	if groupName != "" {
		g, err := user.LookupGroup(groupName)
		if err != nil {
			return fmt.Errorf("Cannot get group ID of '%s': %w", groupName, err)
		}

		gid, err = strconv.Atoi(g.Gid)
		if err != nil {
			return err
		}
	} else {
		gid = os.Getgid()
	}

	err = os.Chown(path, os.Getuid(), gid)
	if err != nil {
		return fmt.Errorf("Cannot change ownership on local socket: %w", err)
	}

	return nil
}
