package mojang

import (
	"context"
	"crypto/sha1"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"game-server-platform/internal/domain"
)

func TestDownloadCreationRepairAndReuse(t *testing.T) {
	for _, existing := range []string{"", "broken", "official server"} {
		t.Run("existing="+existing, func(t *testing.T) {
			const content = "official server"
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				fmt.Fprint(writer, content)
			}))
			defer server.Close()
			client := &Client{http: server.Client()}
			download := domain.ServerDownload{URL: server.URL, Size: int64(len(content)), SHA1: fmt.Sprintf("%x", sha1.Sum([]byte(content)))}
			destination := filepath.Join(t.TempDir(), "server.jar")
			if existing != "" {
				if err := os.WriteFile(destination, []byte(existing), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := client.EnsureJar(context.Background(), download, destination); err != nil {
				t.Fatal(err)
			}
			if err := client.EnsureJar(context.Background(), download, destination); err != nil {
				t.Fatal(err)
			}
			wantRequests := int32(1)
			if existing == content {
				wantRequests = 0
			}
			if requests.Load() != wantRequests {
				t.Fatalf("requests = %d, want %d", requests.Load(), wantRequests)
			}
			data, err := os.ReadFile(destination)
			if err != nil || string(data) != content {
				t.Fatalf("jar = %q, error = %v", data, err)
			}
		})
	}
}

func TestFailedDownloadPreservesExistingJar(t *testing.T) {
	for _, failure := range []string{"wrong checksum", "truncated response", "HTTP error"} {
		t.Run(failure, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				if failure == "HTTP error" {
					writer.WriteHeader(http.StatusBadGateway)
					return
				}
				if failure == "truncated response" {
					writer.Header().Set("Content-Length", "100")
				}
				fmt.Fprint(writer, "bad")
			}))
			defer server.Close()
			directory := t.TempDir()
			destination := filepath.Join(directory, "server.jar")
			if err := os.WriteFile(destination, []byte("old"), 0644); err != nil {
				t.Fatal(err)
			}
			client := &Client{http: server.Client()}
			download := domain.ServerDownload{URL: server.URL, Size: 3, SHA1: fmt.Sprintf("%x", sha1.Sum([]byte("new")))}
			if err := client.EnsureJar(context.Background(), download, destination); err == nil {
				t.Fatal("expected download error")
			}
			data, err := os.ReadFile(destination)
			if err != nil || string(data) != "old" {
				t.Fatalf("existing jar was damaged: %q, %v", data, err)
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 1 {
				t.Fatalf("temporary download left behind: %v, %v", entries, err)
			}
		})
	}
}
