package mojang

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveServer(t *testing.T) {
	tests := []struct {
		name           string
		manifest       string
		metadata       string
		manifestStatus int
		metadataStatus int
		wantError      string
	}{
		{name: "success"},
		{name: "manifest HTTP error", manifestStatus: 503, wantError: "fetch version manifest: HTTP 503"},
		{name: "invalid manifest", manifest: "{broken", wantError: "fetch version manifest: decode response"},
		{name: "missing version", manifest: `{"versions":[]}`, wantError: "was not found"},
		{name: "metadata HTTP error", metadataStatus: 404, wantError: "fetch version metadata: HTTP 404"},
		{name: "invalid metadata", metadata: "{broken", wantError: "fetch version metadata: decode response"},
		{name: "no server download", metadata: `{"downloads":{}}`, wantError: "no server download URL"},
		{name: "invalid integrity metadata", metadata: `{"downloads":{"server":{"url":"https://example.test/server.jar"}}}`, wantError: "invalid server download integrity metadata"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/manifest":
					if test.manifestStatus != 0 {
						writer.WriteHeader(test.manifestStatus)
						return
					}
					if test.manifest != "" {
						fmt.Fprint(writer, test.manifest)
						return
					}
					fmt.Fprintf(writer, `{"versions":[{"id":"other","url":"unused"},{"id":"1.20.4","url":"http://%s/metadata"}]}`, request.Host)
				case "/metadata":
					if test.metadataStatus != 0 {
						writer.WriteHeader(test.metadataStatus)
						return
					}
					if test.metadata != "" {
						fmt.Fprint(writer, test.metadata)
						return
					}
					fmt.Fprint(writer, `{"downloads":{"server":{"url":"https://example.test/server.jar","sha1":"a9993e364706816aba3e25717850c26c9cd0d89d","size":3}}}`)
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()
			client := &Client{http: server.Client(), manifestURL: server.URL + "/manifest"}
			download, err := client.Resolve(context.Background(), "1.20.4")
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("expected %q, got %v", test.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if download.Size != 3 || download.URL != "https://example.test/server.jar" || download.SHA1 != "a9993e364706816aba3e25717850c26c9cd0d89d" {
				t.Fatalf("unexpected download: %+v", download)
			}
		})
	}
}

func TestResolveNetworkFailure(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	client := &Client{http: server.Client(), manifestURL: server.URL}
	_, err := client.Resolve(context.Background(), "1.20.4")
	if err == nil || !strings.Contains(err.Error(), "fetch version manifest") {
		t.Fatalf("expected network error, got %v", err)
	}
}
