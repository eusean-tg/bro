package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/eusean-tg/bro/internal/server"
	"github.com/eusean-tg/bro/internal/store"
)

var errVersionMismatch = errors.New("version mismatch")

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

		if errors.Is(err, errVersionMismatch) && i < 3 {
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

		var err error
		switch res.StatusCode {
		case http.StatusConflict:
			err = store.ErrMailboxExists
		case http.StatusNotFound:
			err = store.ErrNoMailbox
		case http.StatusPreconditionFailed:
			err = errVersionMismatch
		default:
			return fmt.Errorf("bro sock returned %d: %s", res.StatusCode, out.Error)
		}

		return fmt.Errorf("%s: %w", out.Error, err)
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
) (store.PullResult, error) {
	var res store.PullResult
	req := server.PullRequest{Workspace: ws, Me: me, Since: opts.Since, Last: opts.Last, Peek: opts.Peek}

	if err := c.do(ctx, "/pull", req, &res); err != nil {
		return store.PullResult{}, err
	}

	return res, nil
}
