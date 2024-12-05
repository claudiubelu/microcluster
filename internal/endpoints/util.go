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
// If the given timeout is 0, it will forcefully shut it down. Otherwise, it will gracefully shut it down.
func shutdownServer(ctx context.Context, server *http.Server, timeout time.Duration) error {
	// If the given timeout is 0, force the shutdown.
	if timeout == 0 {
		err := server.Close()
		if errors.Is(err, syscall.EINVAL) {
			return nil
		}
		return err
	}

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
