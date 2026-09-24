package client

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func spawn(exe, sockpath string) error {
	cmd := exec.Command(exe, "daemon")
	cmd.Env = append(os.Environ(), "BRO_SOCK="+sockpath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}

	return cmd.Process.Release()
}

func dial(ctx context.Context, sockPath, exe string) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", sockPath)
	if err == nil {
		return conn, nil
	}

	if exe == "" {
		return nil, err
	}
	if !errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.ECONNREFUSED) {
		return nil, err
	}

	if err = spawn(exe, sockPath); err != nil {
		return nil, err
	}

	wait := 10 * time.Millisecond
	deadline := time.Now().Add(3 * time.Second)
	for {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		v, err := d.DialContext(ctx, "unix", sockPath)
		if err == nil {
			return v, nil
		}

		if time.Now().After(deadline) {
			return nil, err
		}

		wait = min(wait*2, 250*time.Millisecond)
	}
}
