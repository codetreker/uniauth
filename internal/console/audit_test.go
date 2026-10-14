package console

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codetreker/uniauth/internal/apps"
)

func TestManagementAuditCorrelationAndRedaction(t *testing.T) {
	var output bytes.Buffer
	e := newTestConsole(t, func(cfg *Config) { cfg.Logger = slog.New(slog.NewJSONHandler(&output, nil)) })
	e.login(t)
	output.Reset()
	var events []map[string]any
	request := func(method, path, body, action, appID, outcome, category, serverID string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, e.handler.origin+path, strings.NewReader(body))
		req.AddCookie(e.cookie)
		req.Header.Set("Origin", e.handler.origin)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", e.csrf)
		req.Header.Set("X-Request-ID", "client-controlled-request-id")
		response := httptest.NewRecorder()
		if serverID != "" {
			response.Header().Set("X-Request-ID", serverID)
		}
		e.mux.ServeHTTP(response, req)
		requireStatus(t, response, status)
		decoder := json.NewDecoder(&output)
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if decoder.More() || output.Len() != 0 {
			t.Fatal("management operation produced more than one audit event")
		}
		if event["request_id"] == "client-controlled-request-id" || event["request_id"] == "" || event["request_id"] != response.Header().Get("X-Request-ID") || event["operation_id"] != response.Header().Get("X-Operation-ID") {
			t.Fatalf("request correlation violated: %+v headers=%v", event, response.Header())
		}
		if serverID != "" && event["request_id"] != serverID {
			t.Fatalf("server request id was lost: %+v", event)
		}
		if action == "app.create" && outcome == "success" {
			appID = decodeResponse[apps.CreatedApp](t, response).App.ID
		}
		if event["actor_account_id"] != e.account.ID || event["action"] != action || event["phase"] != "completed" || event["affected_app_id"] != appID || event["outcome"] != outcome || event["error_category"] != category || event["status"] != float64(status) {
			t.Fatalf("unexpected audit event: %+v", event)
		}
		events = append(events, event)
		return response
	}
	response := request("POST", "/console/api/apps", createAppBody, "app.create", "", "success", "", "server-request-1", http.StatusCreated)
	created := decodeResponse[apps.CreatedApp](t, response)
	path := "/console/api/apps/" + created.App.ID
	request("PUT", path, strings.Replace(appBody, "Product A", "Updated App", 1), "app.update", created.App.ID, "success", "", "", http.StatusOK)
	response = request("POST", path+"/secrets", secretBody, "secret.create", created.App.ID, "success", "", "", http.StatusCreated)
	issued := decodeResponse[apps.IssuedSecret](t, response)
	request("PUT", path+"/secrets/"+issued.ID, `{"name":"Edited private label","description":"Private rotation memo"}`, "secret.update", created.App.ID, "success", "", "", http.StatusOK)
	request("PUT", path+"/secrets/"+issued.ID, `{}`, "secret.update", created.App.ID, "failure", "invalid_input", "", http.StatusBadRequest)
	resetPath := path + "/secrets/" + issued.ID + "/reset"
	response = request("POST", resetPath, "", "secret.reset", created.App.ID, "success", "", "", http.StatusCreated)
	replacement := decodeResponse[apps.IssuedSecret](t, response)
	if replacement.Name != "Edited private label" || replacement.Description != "Private rotation memo" {
		t.Fatalf("reset lost target metadata: %+v", replacement)
	}
	request("POST", resetPath, "", "secret.reset", created.App.ID, "failure", "already_revoked", "", http.StatusConflict)
	request("DELETE", path+"/secrets/"+replacement.ID, "", "secret.revoke", created.App.ID, "success", "", "", http.StatusNoContent)
	request("DELETE", path, "", "app.delete", created.App.ID, "success", "", "", http.StatusNoContent)
	request("DELETE", path, "", "app.delete", created.App.ID, "failure", "not_found", "", http.StatusNotFound)
	request("POST", "/console/api/apps", `{}`, "app.create", "", "failure", "invalid_input", "", http.StatusBadRequest)
	request("DELETE", "/console/api/apps/private-data", "", "app.delete", "", "failure", "not_found", "", http.StatusNotFound)
	request("DELETE", "/console/api/apps/!!!!!!!!!!!!!!!!!!!!!!", "", "app.delete", "", "failure", "not_found", "", http.StatusNotFound)
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{created.Secret, issued.Secret, replacement.Secret, e.csrf, e.cookie.Value, "private-data", "Updated App", "product.example", "client-controlled-request-id", "Production server", "Primary deployment credential", "Backup server", "Standby deployment credential", "Edited private label", "Private rotation memo"} {
		if strings.Contains(string(encoded), sensitive) {
			t.Fatalf("audit disclosed sensitive data %q: %s", sensitive, encoded)
		}
	}
	seen := make(map[any]bool)
	for _, event := range events {
		if seen[event["operation_id"]] {
			t.Fatal("independent requests shared an operation id")
		}
		seen[event["operation_id"]] = true
	}
	requireStatus(t, e.request("GET", "/console/api/apps", "", nil), http.StatusOK)
	if output.Len() != 0 {
		t.Fatal("read request generated a management mutation event")
	}
}
