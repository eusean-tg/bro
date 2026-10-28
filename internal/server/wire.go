package server

import "github.com/eusean-tg/bro/internal/store"

// The request and response bodies of each route. The client encodes the
// requests and decodes the responses, so both sides share these definitions.
// /register responds with a store.Mailbox and /pull with a store.PullResult.

type TellRequest struct {
	Workspace string   `json:"workspace"`
	Sender    string   `json:"sender"`
	Body      string   `json:"body"`
	To        []string `json:"to"`
	Kind      string   `json:"kind"`
}

type TellResponse struct {
	ID int64 `json:"id"`
}

type LogRequest struct {
	Workspace string `json:"workspace"`
}

type LogResponse struct {
	Messages []store.Message `json:"messages"`
}

type RegisterRequest struct {
	Workspace   string `json:"workspace"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type PullRequest struct {
	Workspace string `json:"workspace"`
	Me        string `json:"me"`
	Since     *int64 `json:"since"` // null: start from the cursor
	Last      int    `json:"last"`  // 0: no limit
	Peek      bool   `json:"peek"`
	WaitMS    int64  `json:"wait_ms"`
}

type PullResponse struct {
	store.PullResult
	TimedOut bool `json:"timed_out"`
}

// ErrorResponse is the body of every non-200 response.
type ErrorResponse struct {
	Error string `json:"error"`
}

type CloseRequest struct {
	Workspace string `json:"workspace"`
	Sender    string `json:"sender"`
	Body      string `json:"body"`
}

type MailboxesRequest struct {
	Workspace string `json:"workspace"`
}

type MailboxesResponse struct {
	Mailboxes []store.Mailbox `json:"mailboxes"`
}
