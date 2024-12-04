package endpoints

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"syscall"
	"time"

	"github.com/canonical/lxd/shared/logger"
)

// shutdownServer will shutdown the given server.
// If the given timeout is 0, it will forcefully shut it down.
// Otherwise, it will gracefully shut it down. If it's a lazy shutdown, it will run do it in a separate
// goroutine, allowing this function to return immediately.
//
// NOTE(claudiub): In the case we fail to bootstrap / join the cluster, we'll be resetting a few things,
// including the cluster membership. This includes the HTTPS and unix socket servers we have open by
// closing them.
// However, we cannot gracefully shutdown the servers, as there's at least one connection that is
// still open: the bootstrap / join request. Forcing the connection to close before we're able to
// write the request response will result in the client getting an EOF error, and no information
// regarding the failure.
// Lazily shutting down the server in a goroutine in these cases will address this issue: while
// the revert happens, we'll be able to return and write the HTTP response and then close the
// connection, finally allowing the Servers to gracefully shutdown, and the clients to be happy.
func shutdownServer(ctx context.Context, server *http.Server, timeout time.Duration, lazyShutdown bool) error {
	// If the given timeout is 0, force the shutdown. No need to be lazy about it.
	if timeout == 0 {
		err := server.Close()
		if errors.Is(err, syscall.EINVAL) {
			return nil
		}
		return err
	}

	if lazyShutdown {
		// We're passing a new Background context as the parent context may be cancelled before we
		// manage to gracefully shutdown the server.
		go gracefulShutdownServer(context.Background(), server, timeout)
		return nil
	}
	return gracefulShutdownServer(ctx, server, timeout)
}

// Close the Socket's listener.
func gracefulShutdownServer(ctx context.Context, server *http.Server, timeout time.Duration) error {
	// server.Shutdown will gracefully stop the server, allowing existing requests to finish.
	shutdownCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("Failed to gracefully shutdown server", logger.Ctx{"err": err})
		if closeErr := server.Close(); closeErr != nil {
			logger.Error("Failed to close server", logger.Ctx{"err": closeErr})
			return fmt.Errorf("Encountered error while closing server: %w, after failing to gracefully shutdown the server: %w", closeErr, err)
		}
		return err
	}
	return nil
}
