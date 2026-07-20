package httpserver_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// postFile is postForm's multipart-upload counterpart, for /settings/import.
func (c *testClient) postFile(path, fieldName, fileName string, content []byte, token string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("gorilla.csrf.Token", token); err != nil {
		c.t.Fatalf("write csrf field: %v", err)
	}
	part, err := w.CreateFormFile(fieldName, fileName)
	if err != nil {
		c.t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		c.t.Fatalf("write file content: %v", err)
	}
	if err := w.Close(); err != nil {
		c.t.Fatalf("close multipart writer: %v", err)
	}
	return c.do(http.MethodPost, path, &buf, map[string]string{"Content-Type": w.FormDataContentType()})
}

// TestExportImport_HTTPRoundTrip drives the whole feature the way a real
// user would: create a meal through the normal form, export it, delete it,
// then re-import the downloaded file and confirm it's back.
func TestExportImport_HTTPRoundTrip(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	createToken := c.csrfToken("/meals/new")
	createRec := c.postForm("/meals", url.Values{
		"name":           {"Export Test Meal"},
		"serving_label":  {"per serving"},
		"serving_amount": {"1"},
		"is_favorite":    {"true"},
		"nutrient_1":     {"400"},
	}, createToken)
	if createRec.Code != http.StatusSeeOther {
		t.Fatalf("create meal: status = %d, body = %s", createRec.Code, createRec.Body.String())
	}

	exportToken := c.csrfToken("/settings")
	exportRec := c.postForm("/settings/export", url.Values{
		"meal_categories": {"true"},
		"meal_labels":     {"true"},
		"meals":           {"true"},
	}, exportToken)
	if exportRec.Code != http.StatusOK {
		t.Fatalf("export: status = %d, body = %s", exportRec.Code, exportRec.Body.String())
	}
	if ct := exportRec.Header().Get("Content-Disposition"); !strings.Contains(ct, "attachment") {
		t.Errorf("expected an attachment Content-Disposition, got %q", ct)
	}
	exported := exportRec.Body.Bytes()
	if !strings.Contains(string(exported), "Export Test Meal") {
		t.Fatalf("expected the exported file to contain the seeded meal, got: %s", exported)
	}

	// Delete the meal, so re-import must actually create it, not just no-op.
	list := c.get("/meals")
	id := extractMealID(t, list.Body.String(), "Export Test Meal")
	deleteToken := c.csrfToken("/meals/" + id + "/edit")
	if rec := c.delete("/meals/"+id, deleteToken); rec.Code != http.StatusOK {
		t.Fatalf("delete meal: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(c.get("/meals").Body.String(), "Export Test Meal") {
		t.Fatal("expected the meal to be gone after delete")
	}

	importToken := c.csrfToken("/settings")
	importRec := c.postFile("/settings/import", "file", "export.json", exported, importToken)
	if importRec.Code != http.StatusOK {
		t.Fatalf("import: status = %d, body = %s", importRec.Code, importRec.Body.String())
	}
	if !strings.Contains(importRec.Body.String(), "1 created") {
		t.Errorf("expected the result page to report 1 meal created, got: %s", importRec.Body.String())
	}

	if !strings.Contains(c.get("/meals").Body.String(), "Export Test Meal") {
		t.Error("expected the imported meal to be back in the library")
	}
}

// TestExportImport_Export_RequiresAtLeastOneSection guards against a
// pointless empty download when nothing was actually selected.
func TestExportImport_Export_RequiresAtLeastOneSection(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	token := c.csrfToken("/settings")
	rec := c.postForm("/settings/export", url.Values{}, token)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestExportImport_Import_RejectsInvalidJSON guards against a 500 on a
// clearly-malformed upload — this is routine bad input, not a server fault.
func TestExportImport_Import_RejectsInvalidJSON(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	token := c.csrfToken("/settings")
	rec := c.postFile("/settings/import", "file", "garbage.json", []byte("not json"), token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

// TestExportImport_Import_RejectsWrongVersion guards against silently
// misinterpreting a file from an incompatible future export version.
func TestExportImport_Import_RejectsWrongVersion(t *testing.T) {
	srv, pool := newTestServerAndPool(t)
	c := newAuthenticatedClient(t, pool, srv)

	token := c.csrfToken("/settings")
	rec := c.postFile("/settings/import", "file", "export.json", []byte(`{"version": 999, "sections": {}}`), token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d, body: %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
}
