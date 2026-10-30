package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/eusean-tg/bro/internal/hub"
	"github.com/eusean-tg/bro/internal/store"
)

type server struct {
	st  *store.Store
	hub *hub.Hub
}

func New(st *store.Store, version string, stop func(), stale func() bool) http.Handler {
	sv := &server{
		st:  st,
		hub: hub.New(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /tell", sv.tell)
	mux.HandleFunc("POST /log", sv.log)
	mux.HandleFunc("POST /register", sv.register)
	mux.HandleFunc("POST /pull", sv.pull)
	mux.HandleFunc("POST /close", sv.close)
	mux.HandleFunc("POST /mailboxes", sv.mailboxes)

	return withVersion(version, stop, stale, mux)
}

func (sv *server) tell(w http.ResponseWriter, r *http.Request) {
	var body TellRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	if body.Kind == "" {
		body.Kind = string(store.KindNote)
	}

	id, err := sv.st.Tell(
		r.Context(),
		body.Workspace,
		body.Sender,
		body.Body,
		body.To,
		store.Kind(body.Kind),
	)
	if err != nil {
		writeError(w, err)
		return
	}

	sv.hub.Publish(body.Workspace)

	writeJSON(w, http.StatusOK, TellResponse{ID: id})
}

func (sv *server) log(w http.ResponseWriter, r *http.Request) {
	var body LogRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	if body.WaitMS > 0 {
		ready := func(ctx context.Context) (bool, error) {
			msgs, err := sv.st.Log(ctx, body.Workspace, store.LogOptions{After: body.After, Last: 1})
			if err != nil {
				return false, err
			}

			return len(msgs) > 0, nil
		}

		err := sv.waitWithin(
			r.Context(),
			time.Duration(body.WaitMS)*time.Millisecond,
			body.Workspace,
			ready,
		)

		switch {
		case err == nil, errors.Is(err, context.DeadlineExceeded):
		default:
			writeError(w, err)
			return
		}
	}

	msgs, err := sv.st.Log(r.Context(), body.Workspace, store.LogOptions{After: body.After, Last: body.Last})
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, LogResponse{Messages: msgs})
}

func (sv *server) register(w http.ResponseWriter, r *http.Request) {
	var body RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	mb, err := sv.st.Register(r.Context(), body.Workspace, body.Name, body.Description)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, mb)
}

func (sv *server) pull(w http.ResponseWriter, r *http.Request) {
	var body PullRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	ready := func(ctx context.Context) (bool, error) {
		return sv.st.Wakeable(ctx, body.Workspace, body.Me)
	}

	timedOut := false
	if body.WaitMS > 0 {
		err := sv.waitWithin(
			r.Context(),
			time.Duration(body.WaitMS)*time.Millisecond,
			body.Workspace,
			ready,
		)

		switch {
		case err == nil:
		case errors.Is(err, context.DeadlineExceeded):
			timedOut = true
		case errors.Is(err, context.Canceled):
			writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: "pull cancelled"})
			return
		default:
			writeError(w, err)
			return
		}
	}

	opts := store.PullOptions{Since: body.Since, Last: body.Last, Peek: body.Peek}
	pr, err := sv.st.Pull(r.Context(), body.Workspace, body.Me, opts)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, PullResponse{PullResult: pr, TimedOut: timedOut})
}

func (sv *server) close(w http.ResponseWriter, r *http.Request) {
	var body CloseRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	id, err := sv.st.CloseWorkspace(r.Context(), body.Workspace, body.Sender, body.Body)
	if err != nil {
		writeError(w, err)
		return
	}

	sv.hub.Publish(body.Workspace)

	writeJSON(w, http.StatusOK, TellResponse{ID: id})
}

func (sv *server) waitWithin(
	parent context.Context,
	d time.Duration,
	ws string,
	ready func(context.Context) (bool, error),
) error {
	ctx, cancel := context.WithTimeout(parent, d)
	defer cancel()

	return sv.wait(ctx, ws, ready)
}

// blocks until ready reports true, or ctx ends
func (sv *server) wait(ctx context.Context, ws string, ready func(context.Context) (bool, error)) error {
	wake, unsub := sv.hub.Subscribe(ws)
	defer unsub()

	for {
		// Subscribed before checking: a write that commits after this check
		// has already rung wake, so it cannot fall between the check and the
		// select below.
		ok, err := ready(ctx)
		if err != nil || ok {
			return err
		}

		select {
		case <-wake:
			// Something changed. Loop and re-check.
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (sv *server) mailboxes(w http.ResponseWriter, r *http.Request) {
	var body MailboxesRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	mbs, err := sv.st.Mailboxes(r.Context(), body.Workspace)
	if err != nil {
		writeError(w, err)
		return
	}

	resp := MailboxesResponse{
		Mailboxes: mbs,
	}

	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError

	switch {
	case errors.Is(err, store.ErrNoMailbox):
		status = http.StatusNotFound
	case errors.Is(err, store.ErrMailboxExists):
		status = http.StatusConflict
	case errors.Is(err, context.Canceled):
		status = http.StatusServiceUnavailable
	case errors.Is(err, store.ErrBadKind):
		status = http.StatusBadRequest
	}

	writeJSON(w, status, ErrorResponse{Error: err.Error()})
}

func withVersion(version string, stop func(), stale func() bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := r.Header.Get("Bro-Version")
		if v != "" && v != version && stale() {
			w.Header().Set("Connection", "close")
			writeJSON(
				w,
				http.StatusPreconditionFailed,
				ErrorResponse{
					Error: fmt.Sprintf("bad version: daemon %q, client %q", version, v),
				},
			)
			stop()
			return
		}
		next.ServeHTTP(w, r)
	})
}
