package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/eusean-tg/bro/internal/store"
)

type server struct {
	st *store.Store
}

func New(st *store.Store, version string, stop func()) http.Handler {
	sv := &server{
		st: st,
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

	opts := store.PullOptions{Since: body.Since, Last: body.Last, Peek: body.Peek}
	pr, err := sv.st.Pull(r.Context(), body.Workspace, body.Me, opts)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, pr)
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
