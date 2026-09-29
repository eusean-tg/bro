package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/eusean-tg/bro/internal/server"
	"github.com/eusean-tg/bro/internal/store"
)

var (
	errVersionMismatch = errors.New("version mismatch")
	errUnavailable     = errors.New("unavailable")
)

type remoteError struct {
	msg  string
	kind error
}

func (e *remoteError) Error() string { return e.msg }
func (e *remoteError) Unwrap() error { return e.kind }

type Client struct {
	BaseURL string
	HTTP    *http.Client
	Version string
}

func New(sockPath, exe, version string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dial(ctx, sockPath, exe)
		},
	}

	return &Client{
		BaseURL: "http://bro",
		HTTP:    &http.Client{Transport: tr},
		Version: version,
	}
}

func (c *Client) do(
	ctx context.Context,
	path string,
	req,
	resp any,
) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}

	for i := 0; ; i++ {
		err := c.once(ctx, path, b, resp)

		if (errors.Is(err, errVersionMismatch) || errors.Is(err, errUnavailable)) && i < 3 {
			continue
		}
		return err
	}
}

func (c *Client) once(
	ctx context.Context,
	path string,
	body []byte,
	resp any,
) error {
	b := bytes.NewReader(body)

	r, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.BaseURL+path,
		b,
	)
	if err != nil {
		return err
	}

	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Bro-Version", c.Version)

	res, err := c.HTTP.Do(r)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		var out server.ErrorResponse
		_ = json.NewDecoder(res.Body).Decode(&out)

		err := &remoteError{
			msg: out.Error,
		}
		switch res.StatusCode {
		case http.StatusConflict:
			err.kind = store.ErrMailboxExists
		case http.StatusNotFound:
			err.kind = store.ErrNoMailbox
		case http.StatusPreconditionFailed:
			err.kind = errVersionMismatch
		case http.StatusServiceUnavailable:
			err.kind = errUnavailable
		default:
			return fmt.Errorf("bro sock returned %d: %s", res.StatusCode, out.Error)
		}

		return err
	}

	return json.NewDecoder(res.Body).Decode(resp)
}

func (c *Client) Tell(
	ctx context.Context,
	ws string,
	sender string,
	body string,
	to []string,
) (int64, error) {
	var res server.TellResponse
	req := server.TellRequest{Workspace: ws, Sender: sender, Body: body, To: to}

	if err := c.do(ctx, "/tell", req, &res); err != nil {
		return 0, err
	}

	return res.ID, nil
}

func (c *Client) Log(ctx context.Context, ws string) ([]store.Message, error) {
	var res server.LogResponse
	req := server.LogRequest{Workspace: ws}

	if err := c.do(ctx, "/log", req, &res); err != nil {
		return nil, err
	}

	return res.Messages, nil
}

func (c *Client) Register(
	ctx context.Context,
	ws string,
	name string,
	desc string,
) (*store.Mailbox, error) {
	var res store.Mailbox
	req := server.RegisterRequest{Workspace: ws, Name: name, Description: desc}

	if err := c.do(ctx, "/register", req, &res); err != nil {
		return nil, err
	}

	return &res, nil
}

func (c *Client) Pull(
	ctx context.Context,
	ws string,
	me string,
	opts store.PullOptions,
	wait time.Duration,
) (server.PullResponse, error) {
	var res server.PullResponse
	req := server.PullRequest{
		Workspace: ws,
		Me:        me,
		Since:     opts.Since,
		Last:      opts.Last,
		Peek:      opts.Peek,
		WaitMS:    wait.Milliseconds(),
	}

	if err := c.do(ctx, "/pull", req, &res); err != nil {
		return server.PullResponse{}, err
	}

	return res, nil
}
