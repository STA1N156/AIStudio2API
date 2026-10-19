package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type transferAdmin struct {
	AdminService
	mu       sync.Mutex
	imported []AccountStateInput
}

func (admin *transferAdmin) ExportAccountState(_ context.Context, id string) (AccountStateInput, error) {
	if id == "missing" {
		return AccountStateInput{}, fmt.Errorf("missing account")
	}
	enabled := false
	input := AccountStateInput{Label: id, Enabled: &enabled, Proxy: "http://proxy.test:8080", Locale: "en-US", Timezone: "UTC", Fingerprint: json.RawMessage(`{"config":{"screen":1}}`)}
	json.Unmarshal([]byte(`{"cookies":[{"name":"SAPISID","value":"private-test-cookie","domain":".google.com","path":"/"}],"origins":[]}`), &input.StorageState)
	return input, nil
}

func (admin *transferAdmin) ImportAccountState(_ context.Context, input AccountStateInput) (AdminAccount, error) {
	admin.mu.Lock()
	defer admin.mu.Unlock()
	admin.imported = append(admin.imported, input)
	return AdminAccount{ID: input.Label}, nil
}

func transferUpload(t *testing.T, name string, data []byte) (*http.Request, *bytes.Buffer) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	file, err := writer.CreateFormFile("files", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/accounts/import", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request, body
}

func TestAccountTransferRoundTrip(t *testing.T) {
	admin := &transferAdmin{}
	server := &server{config: Config{Admin: admin}}
	response := httptest.NewRecorder()
	server.handleExportAccounts(response, httptest.NewRequest(http.MethodPost, "/api/accounts/export", strings.NewReader(`{"ids":["a@example.com","b@example.com","a@example.com"]}`)))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("export: %d %s", response.Code, response.Body)
	}
	archive, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil || len(archive.File) != 2 {
		t.Fatalf("archive entries=%v err=%v", archive, err)
	}
	request, _ := transferUpload(t, "accounts.zip", response.Body.Bytes())
	imported := httptest.NewRecorder()
	server.handleImportAccounts(imported, request)
	if imported.Code != http.StatusOK || len(admin.imported) != 2 {
		t.Fatalf("import: %d %s", imported.Code, imported.Body)
	}
	for _, account := range admin.imported {
		if account.Enabled == nil || *account.Enabled || account.Proxy != "http://proxy.test:8080" || account.StorageState.Cookies[0].Value != "private-test-cookie" || len(account.Fingerprint) == 0 {
			t.Fatal("account backup lost settings or authentication data")
		}
	}
	request = httptest.NewRequest(http.MethodGet, "/api/accounts/a@example.com/export", nil)
	request.SetPathValue("id", "a@example.com")
	single := httptest.NewRecorder()
	server.handleExportAccounts(single, request)
	if single.Code != http.StatusOK || !strings.Contains(single.Header().Get("Content-Disposition"), ".json") || !json.Valid(single.Body.Bytes()) {
		t.Fatalf("single export: %d %s", single.Code, single.Body)
	}
}

func TestAccountImportBounds(t *testing.T) {
	for _, test := range []struct {
		name string
		zip  string
		data []byte
	}{
		{"unsafe.zip", "../outside.json", []byte(`{}`)},
		{"large.json", "", bytes.Repeat([]byte(" "), int(maxAccountFileBytes)+1)},
		{"broken.zip", "", []byte("not a zip")},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := test.data
			if test.zip != "" {
				var buffer bytes.Buffer
				writer := zip.NewWriter(&buffer)
				entry, _ := writer.Create(test.zip)
				entry.Write(data)
				writer.Close()
				data = buffer.Bytes()
			}
			request, _ := transferUpload(t, test.name, data)
			if _, err := accountImportFiles(request); err == nil {
				t.Fatal("unsafe or oversized import accepted")
			}
		})
	}
}

func TestAccountExportsRequireAdminSession(t *testing.T) {
	handler := NewHandler(nil, Config{Admin: &transferAdmin{}, AdminAuthEnabled: true, AdminUsername: "admin", AdminPassword: "secret"})
	for _, path := range []string{"/api/accounts/a@example.com/export", "/api/accounts/export"} {
		method := http.MethodGet
		if path == "/api/accounts/export" {
			method = http.MethodPost
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		body, _ := io.ReadAll(response.Result().Body)
		if response.Code != http.StatusUnauthorized || bytes.Contains(body, []byte("private-test-cookie")) {
			t.Fatalf("export without login: %d %s", response.Code, body)
		}
	}
}
