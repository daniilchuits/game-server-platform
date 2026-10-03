package webui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"game-server-platform/internal/domain"
)

const maximumRequestSize = 64 << 10

//go:embed assets/*
var browserAssets embed.FS

// BackupLoader returns the completed snapshots shown by the control panel.
type BackupLoader func() ([]domain.BackupData, error)

// Server serves the embedded control panel and its local JSON API.
type Server struct {
	address    string
	controller *Controller
	backups    BackupLoader
	handler    http.Handler
}

// NewServer creates a control-panel server. Run still validates that address
// is an explicit loopback IP before opening the listener.
func NewServer(address string, controller *Controller, backups BackupLoader) *Server {
	server := &Server{address: address, controller: controller, backups: backups}
	server.handler = server.routes()
	return server
}

// URL is the address users can open in their browser.
func (server *Server) URL() string {
	return "http://" + server.address
}

// Handler exposes the embedded page and API, primarily for HTTP tests.
func (server *Server) Handler() http.Handler {
	return server.handler
}

// Run serves until the context is cancelled. An active Minecraft run is asked
// to stop and remains supervised until it exits.
func (server *Server) Run(ctx context.Context) error {
	if err := validateLoopbackAddress(server.address); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", server.address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", server.address, err)
	}
	httpServer := &http.Server{
		Handler:           server.handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    maximumRequestSize,
	}
	served := make(chan error, 1)
	go func() {
		served <- httpServer.Serve(listener)
	}()

	select {
	case err := <-served:
		controllerErr := server.stopController()
		if errors.Is(err, http.ErrServerClosed) {
			return controllerErr
		}
		return errors.Join(fmt.Errorf("serve control panel: %w", err), controllerErr)
	case <-ctx.Done():
		_ = server.controller.Stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		shutdownErr := httpServer.Shutdown(shutdownCtx)
		cancel()
		serveErr := <-served
		waitErr := server.stopController()
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		return errors.Join(shutdownErr, serveErr, waitErr)
	}
}

func (server *Server) stopController() error {
	_ = server.controller.Stop()
	err := server.controller.Wait(context.Background())
	if errors.Is(err, ErrNotRunning) || isOnlyContextError(err) {
		return nil
	}
	return err
}

func (server *Server) routes() http.Handler {
	mux := http.NewServeMux()
	assets, err := fs.Sub(browserAssets, "assets")
	if err != nil {
		panic(fmt.Sprintf("load browser assets: %v", err))
	}
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	mux.HandleFunc("GET /", server.index)
	mux.HandleFunc("GET /api/state", server.state)
	mux.HandleFunc("POST /api/start", server.start)
	mux.HandleFunc("POST /api/stop", server.stop)
	mux.HandleFunc("POST /api/eula", server.eula)
	mux.HandleFunc("POST /api/command", server.command)
	mux.HandleFunc("GET /api/backups", server.listBackups)
	return securityHeaders(server.address, mux)
}

func (server *Server) index(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(writer, request)
		return
	}
	page, err := browserAssets.ReadFile("assets/index.html")
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "control panel is unavailable")
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_, _ = writer.Write(page)
}

func (server *Server) state(writer http.ResponseWriter, request *http.Request) {
	after := uint64(0)
	if value := request.URL.Query().Get("after"); value != "" {
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			writeAPIError(writer, http.StatusBadRequest, "after must be a non-negative integer")
			return
		}
		after = parsed
	}
	writeJSON(writer, http.StatusOK, server.controller.Snapshot(after))
}

func (server *Server) start(writer http.ResponseWriter, request *http.Request) {
	if !sameOrigin(request) {
		writeAPIError(writer, http.StatusForbidden, "cross-origin requests are not allowed")
		return
	}
	if err := decodeJSON(writer, request, &struct{}{}); err != nil {
		writeAPIError(writer, http.StatusBadRequest, err.Error())
		return
	}
	if err := server.controller.Start(); err != nil {
		writeAPIError(writer, http.StatusConflict, err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]bool{"ok": true})
}

func (server *Server) stop(writer http.ResponseWriter, request *http.Request) {
	if !sameOrigin(request) {
		writeAPIError(writer, http.StatusForbidden, "cross-origin requests are not allowed")
		return
	}
	if err := decodeJSON(writer, request, &struct{}{}); err != nil {
		writeAPIError(writer, http.StatusBadRequest, err.Error())
		return
	}
	if err := server.controller.Stop(); err != nil {
		writeAPIError(writer, http.StatusConflict, err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]bool{"ok": true})
}

func (server *Server) eula(writer http.ResponseWriter, request *http.Request) {
	if !sameOrigin(request) {
		writeAPIError(writer, http.StatusForbidden, "cross-origin requests are not allowed")
		return
	}
	var answer struct {
		Accepted bool `json:"accepted"`
	}
	if err := decodeJSON(writer, request, &answer); err != nil {
		writeAPIError(writer, http.StatusBadRequest, err.Error())
		return
	}
	if err := server.controller.AnswerEULA(answer.Accepted); err != nil {
		writeAPIError(writer, http.StatusConflict, err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]bool{"ok": true})
}

func (server *Server) command(writer http.ResponseWriter, request *http.Request) {
	if !sameOrigin(request) {
		writeAPIError(writer, http.StatusForbidden, "cross-origin requests are not allowed")
		return
	}
	var command struct {
		Value string `json:"command"`
	}
	if err := decodeJSON(writer, request, &command); err != nil {
		writeAPIError(writer, http.StatusBadRequest, err.Error())
		return
	}
	if err := server.controller.SendCommand(command.Value); err != nil {
		status := http.StatusConflict
		if errors.Is(err, ErrEmptyCommand) || errors.Is(err, ErrInvalidCommand) {
			status = http.StatusBadRequest
		} else if errors.Is(err, ErrCommandQueueFull) {
			status = http.StatusTooManyRequests
		}
		writeAPIError(writer, status, err.Error())
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]bool{"ok": true})
}

func (server *Server) listBackups(writer http.ResponseWriter, _ *http.Request) {
	items := make([]backupItem, 0)
	if server.backups == nil {
		writeJSON(writer, http.StatusOK, map[string]any{"backups": items})
		return
	}
	backups, err := server.backups()
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(writer, http.StatusOK, map[string]any{"backups": items})
		return
	}
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, "read backups: "+err.Error())
		return
	}
	for _, backup := range backups {
		items = append(items, backupItem{
			Name:    backup.FileName,
			Message: backup.Message,
			Hash:    domain.BackupHash(backup),
		})
	}
	writeJSON(writer, http.StatusOK, map[string]any{"backups": items})
}

type backupItem struct {
	Name    string `json:"name"`
	Message string `json:"message"`
	Hash    string `json:"hash"`
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, target any) error {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("Content-Type must be application/json")
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumRequestSize)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request must contain one JSON object")
		}
		return fmt.Errorf("decode request: %w", err)
	}
	return nil
}

func sameOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme == "http" && parsed.Host == request.Host
}

func securityHeaders(expectedHost string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		writer.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		writer.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		if request.Host != expectedHost {
			writeAPIError(writer, http.StatusMisdirectedRequest, "request host is not allowed")
			return
		}
		if strings.HasPrefix(request.URL.Path, "/api/") && request.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeAPIError(writer, http.StatusForbidden, "cross-site API requests are not allowed")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func validateLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid control-panel address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("control panel must listen on a loopback IP address, got %q", host)
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeAPIError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"error": message})
}
