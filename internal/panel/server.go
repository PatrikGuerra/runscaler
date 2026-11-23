package panel

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
)

const maxJSONBody = 4 << 10

// Server is the panel's HTTP API plus the embedded single-page app.
type Server struct {
	ExchangeDir string
	Store       *Store
	Auth        *Auth
	Poller      *Poller
	Logger      *slog.Logger
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	auth := s.Auth.requireSession

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") })
	mux.HandleFunc("GET /api/auth/session", s.handleSession)
	mux.HandleFunc("POST /api/auth/setup", s.handleSetup)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.Handle("POST /api/auth/logout", auth(http.HandlerFunc(s.handleLogout)))
	mux.Handle("GET /api/status", auth(http.HandlerFunc(s.handleStatus)))
	mux.Handle("GET /api/history", auth(http.HandlerFunc(s.handleHistory)))
	mux.Handle("GET /api/logs", auth(http.HandlerFunc(s.handleLogs)))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) { writeError(w, http.StatusNotFound, "not found") })
	mux.Handle("/", spaHandler())
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	setup, err := s.Auth.SetupRequired(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	resp := map[string]any{"authenticated": false, "setupRequired": setup}
	if _, csrf, ok := s.Auth.sessionFrom(r); ok {
		resp["authenticated"], resp["csrfToken"] = true, csrf
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SetupToken string `json:"setupToken"`
		Password   string `json:"password"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	token, err := s.Auth.Setup(r.Context(), body.SetupToken, body.Password)
	switch {
	case errors.Is(err, errBadSetupToken):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, errPasswordTooWeak):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, errAdminExists):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		s.internalError(w, err)
	default:
		s.event(r, "login_succeeded", map[string]any{"setup": true})
		setSessionCookie(w, token)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	token, err := s.Auth.Login(r.Context(), body.Password)
	switch {
	case errors.Is(err, errLocked):
		writeError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, errBadPassword):
		s.event(r, "login_failed", map[string]any{"remote": r.RemoteAddr})
		writeError(w, http.StatusUnauthorized, err.Error())
	case err != nil:
		s.internalError(w, err)
	default:
		s.event(r, "login_succeeded", map[string]any{"remote": r.RemoteAddr})
		setSessionCookie(w, token)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.Auth.Logout(r)
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Poller.Status())
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	events, err := s.Store.ListEvents(r.Context(), q.Get("kind"), before, limit)
	if err != nil {
		s.internalError(w, err)
		return
	}
	var next int64
	if len(events) > 0 {
		next = events[len(events)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "nextBefore": next})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	n, _ := strconv.Atoi(q.Get("lines"))
	lines, truncated, err := tailLines(filepath.Join(s.ExchangeDir, "logs", "runner.log"), n, q.Get("q"))
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "truncated": truncated})
}

func (s *Server) event(r *http.Request, kind string, detail map[string]any) {
	if err := s.Store.AddEvent(r.Context(), kind, detail); err != nil {
		s.Logger.Warn("panel: cannot record event", "kind", kind, "err", err)
	}
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.Logger.Error("panel: request failed", "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
