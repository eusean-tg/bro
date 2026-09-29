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

func New(st *store.Store, version string, stop func()) http.Handler {
	sv := &server{
		st:  st,
		hub: hub.New(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /tell", sv.tell)
	mux.HandleFunc("POST /log", sv.log)
	mux.HandleFunc("POST /register", sv.register)
	mux.HandleFunc("POST /pull", sv.pull)

	return withVersion(version, stop, mux)
}

func (sv *server) tell(w http.ResponseWriter, r *http.Request) {
	var body TellRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	id, err := sv.st.Tell(r.Context(), body.Workspace, body.Sender, body.Body, body.To)
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

	msgs, err := sv.st.Log(r.Context(), body.Workspace)
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

	timedOut := false
	if body.WaitMS > 0 {
		err := sv.waitWithin(
			r.Context(),
			time.Duration(body.WaitMS)*time.Millisecond,
			body.Workspace,
			body.Me,
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

func (sv *server) waitWithin(parent context.Context, d time.Duration, ws, me string) error {
	ctx, cancel := context.WithTimeout(parent, d)
	defer cancel()

	return sv.wait(ctx, ws, me)
}

// wait blocks until ws has an unread message that mentions me, or ctx ends.
func (sv *server) wait(ctx context.Context, ws, me string) error {
	wake, unsub := sv.hub.Subscribe(ws)
	defer unsub()

	for {
		// Subscribed before checking: a write that commits after this check
		// has already rung wake, so it cannot fall between the check and the
		// select below.
		ok, err := sv.st.Wakeable(ctx, ws, me)
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
	}

	writeJSON(w, status, ErrorResponse{Error: err.Error()})
}

func withVersion(version string, stop func(), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := r.Header.Get("Bro-Version")
		if v != "" && v != version {
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
