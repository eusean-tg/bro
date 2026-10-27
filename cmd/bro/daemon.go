package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/eusean-tg/bro/internal/server"
	"github.com/eusean-tg/bro/internal/store"
)

var errDaemonRunning = errors.New("daemon already running")

func runDaemon(ctx context.Context, sock, db, exe, version string) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	f, err := acquireLock(filepath.Join(filepath.Dir(sock), "daemon.lock"))
	if err != nil {
		if errors.Is(err, errDaemonRunning) {
			return nil
		}

		return err
	}
	defer func() { _ = f.Close() }()

	st, err := store.Open(db)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	l, err := listen(sock)
	if err != nil {
		return err
	}

	stale := func() bool {
		return exeChanged(exe, version)
	}

	srv := &http.Server{
		Handler:     server.New(st, version, stop, stale),
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	go func() {
		if err := srv.Serve(l); !errors.Is(err, http.ErrServerClosed) {
			stop()
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return srv.Shutdown(shutdownCtx)
}

func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()

		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errDaemonRunning
		}

		return nil, err
	}

	return f, nil
}

func listen(sock string) (net.Listener, error) {
	if err := os.Remove(sock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	return net.Listen("unix", sock)
}
