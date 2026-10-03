package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"game-server-platform/internal/domain"
)

const testAddress = "127.0.0.1:8080"

func TestServerServesDashboardAndProtectsHost(t *testing.T) {
	controller := NewController(context.Background(), domain.MinecraftVersion, nil)
	server := NewServer(testAddress, controller, nil)

	response := serveRequest(server, http.MethodGet, "/", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d, body = %s", response.Code, response.Body.String())
	}
	if body := response.Body.String(); !strings.Contains(body, "Game Server Platform") || !strings.Contains(body, "/assets/app.js") {
		t.Fatalf("unexpected dashboard body: %s", body)
	}
	if policy := response.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "default-src 'self'") {
		t.Fatalf("Content-Security-Policy = %q", policy)
	}

	request := httptest.NewRequest(http.MethodGet, "http://attacker.example/api/state", nil)
	request.Host = "attacker.example"
	blocked := httptest.NewRecorder()
	server.Handler().ServeHTTP(blocked, request)
	if blocked.Code != http.StatusMisdirectedRequest {
		t.Fatalf("foreign Host status = %d, want %d", blocked.Code, http.StatusMisdirectedRequest)
	}
	crossSite := httptest.NewRequest(http.MethodGet, "http://"+testAddress+"/api/state", nil)
	crossSite.Host = testAddress
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	crossSiteResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(crossSiteResponse, crossSite)
	if crossSiteResponse.Code != http.StatusForbidden {
		t.Fatalf("cross-site fetch status = %d", crossSiteResponse.Code)
	}
}

func TestServerStartCommandStateAndStopAPI(t *testing.T) {
	commands := make(chan string, 1)
	controller := NewController(context.Background(), domain.MinecraftVersion, func(ctx context.Context, console *Console) error {
		console.SessionStateChanged(domain.Running)
		command, err := console.ReadCommand()
		if err != nil {
			return err
		}
		commands <- command
		<-ctx.Done()
		return ctx.Err()
	})
	server := NewServer(testAddress, controller, nil)

	foreign := serveRequest(server, http.MethodPost, "/api/start", `{}`, "http://attacker.example")
	if foreign.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", foreign.Code)
	}
	form := serveRequestWithType(server, http.MethodPost, "/api/start", "start=true", "application/x-www-form-urlencoded")
	if form.Code != http.StatusBadRequest {
		t.Fatalf("form start status = %d", form.Code)
	}
	started := serveRequest(server, http.MethodPost, "/api/start", `{}`, "http://"+testAddress)
	if started.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, body = %s", started.Code, started.Body.String())
	}
	waitForSnapshot(t, controller, func(snapshot Snapshot) bool { return snapshot.State == string(domain.Running) })

	invalid := serveRequest(server, http.MethodPost, "/api/command", `{"command":"one\ntwo"}`, "http://"+testAddress)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid command status = %d, body = %s", invalid.Code, invalid.Body.String())
	}
	command := serveRequest(server, http.MethodPost, "/api/command", `{"command":"say hello"}`, "http://"+testAddress)
	if command.Code != http.StatusAccepted {
		t.Fatalf("command status = %d, body = %s", command.Code, command.Body.String())
	}
	select {
	case got := <-commands:
		if got != "say hello" {
			t.Fatalf("command = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("command was not delivered")
	}

	state := serveRequest(server, http.MethodGet, "/api/state?after=0", "", "")
	if state.Code != http.StatusOK {
		t.Fatalf("state status = %d", state.Code)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(state.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if !snapshot.Running || !snapshot.CanStop || !snapshot.CanSendCommands || snapshot.Version != domain.MinecraftVersion {
		t.Fatalf("snapshot = %+v", snapshot)
	}

	stopped := serveRequest(server, http.MethodPost, "/api/stop", `{}`, "http://"+testAddress)
	if stopped.Code != http.StatusAccepted {
		t.Fatalf("stop status = %d, body = %s", stopped.Code, stopped.Body.String())
	}
	_ = controller.Wait(testContext(t))
}

func TestServerListsBackupsWithRestoreHashes(t *testing.T) {
	data := []domain.BackupData{
		{FileName: "2026-10-03_00-01-02Z", Message: "before castle"},
		{FileName: "2026-10-02_23-59-58Z"},
	}
	controller := NewController(context.Background(), domain.MinecraftVersion, nil)
	server := NewServer(testAddress, controller, func() ([]domain.BackupData, error) { return data, nil })

	response := serveRequest(server, http.MethodGet, "/api/backups", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("backup status = %d, body = %s", response.Code, response.Body.String())
	}
	var body struct {
		Backups []backupItem `json:"backups"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Backups) != 2 {
		t.Fatalf("backups = %+v", body.Backups)
	}
	if body.Backups[0].Hash != domain.BackupHash(data[0]) || body.Backups[0].Message != data[0].Message {
		t.Fatalf("first backup = %+v", body.Backups[0])
	}
}

func TestServerEULAAPIAndJSONValidation(t *testing.T) {
	answered := make(chan bool, 1)
	controller := NewController(context.Background(), domain.MinecraftVersion, func(_ context.Context, console *Console) error {
		answer, err := console.ConfirmEULA("eula.txt")
		if err != nil {
			return err
		}
		answered <- answer
		return nil
	})
	server := NewServer(testAddress, controller, nil)
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	waitForSnapshot(t, controller, func(snapshot Snapshot) bool { return snapshot.EULAPending })

	wrongType := serveRequestWithType(server, http.MethodPost, "/api/eula", `{"accepted":true}`, "text/plain")
	if wrongType.Code != http.StatusBadRequest {
		t.Fatalf("wrong content type status = %d", wrongType.Code)
	}
	unknown := serveRequest(server, http.MethodPost, "/api/eula", `{"accepted":true,"extra":1}`, "http://"+testAddress)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d", unknown.Code)
	}
	accepted := serveRequest(server, http.MethodPost, "/api/eula", `{"accepted":true}`, "http://"+testAddress)
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("EULA status = %d, body = %s", accepted.Code, accepted.Body.String())
	}
	select {
	case answer := <-answered:
		if !answer {
			t.Fatal("EULA answer = false")
		}
	case <-time.After(time.Second):
		t.Fatal("EULA answer was not delivered")
	}
	if err := controller.Wait(testContext(t)); err != nil {
		t.Fatal(err)
	}
}

func TestValidateLoopbackAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8080", "[::1]:8080"} {
		if err := validateLoopbackAddress(address); err != nil {
			t.Errorf("validateLoopbackAddress(%q) = %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:8080", ":8080", "localhost:8080", "not-an-address"} {
		if err := validateLoopbackAddress(address); err == nil {
			t.Errorf("validateLoopbackAddress(%q) succeeded", address)
		}
	}
}

func TestStopControllerPreservesCleanupErrors(t *testing.T) {
	cleanupErr := errors.New("cleanup failed")
	controller := NewController(context.Background(), domain.MinecraftVersion, func(ctx context.Context, _ *Console) error {
		<-ctx.Done()
		return errors.Join(ctx.Err(), cleanupErr)
	})
	server := NewServer(testAddress, controller, nil)
	if err := controller.Start(); err != nil {
		t.Fatal(err)
	}
	if err := server.stopController(); !errors.Is(err, cleanupErr) {
		t.Fatalf("stopController error = %v, want cleanup failure", err)
	}

	neverStarted := NewServer(testAddress, NewController(context.Background(), domain.MinecraftVersion, nil), nil)
	if err := neverStarted.stopController(); err != nil {
		t.Fatalf("stopController before Start = %v", err)
	}
}

func serveRequest(server *Server, method, target, body, origin string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://"+testAddress+target, strings.NewReader(body))
	request.Host = testAddress
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func serveRequestWithType(server *Server, method, target, body, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://"+testAddress+target, strings.NewReader(body))
	request.Host = testAddress
	request.Header.Set("Origin", "http://"+testAddress)
	request.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func TestServerReportsBackupLoaderFailure(t *testing.T) {
	controller := NewController(context.Background(), domain.MinecraftVersion, nil)
	server := NewServer(testAddress, controller, func() ([]domain.BackupData, error) {
		return nil, errors.New("disk failure")
	})
	response := serveRequest(server, http.MethodGet, "/api/backups", "", "")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected loader error status = %d", response.Code)
	}
}

func TestServerTreatsMissingBackupDirectoryAsEmpty(t *testing.T) {
	controller := NewController(context.Background(), domain.MinecraftVersion, nil)
	server := NewServer(testAddress, controller, func() ([]domain.BackupData, error) {
		return nil, os.ErrNotExist
	})
	response := serveRequest(server, http.MethodGet, "/api/backups", "", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"backups":[]`) {
		t.Fatalf("missing-directory response = %d %s", response.Code, response.Body.String())
	}
}
