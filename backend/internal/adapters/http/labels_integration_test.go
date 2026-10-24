package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func (f *taskFixture) boardLabels(board uuid.UUID, as testUser) []labelResponse {
	f.t.Helper()
	rec := f.do("GET", "/boards/"+board.String()+"/labels", "", as)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("list labels: %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Labels []labelResponse `json:"labels"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		f.t.Fatal(err)
	}
	return body.Labels
}

func TestLabelCRUD(t *testing.T) {
	f := newTaskFixture(t)

	// Members create labels; names are trimmed, colors lowercased.
	rec := f.do("POST", "/boards/"+f.board.String()+"/labels", `{"name":"  Urgente ","color":"#FF00AA"}`, f.ana)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var urgent labelResponse
	_ = json.NewDecoder(rec.Body).Decode(&urgent)
	if urgent.Name != "Urgente" || urgent.Color != "#ff00aa" || urgent.BoardID != f.board {
		t.Errorf("created = %+v", urgent)
	}
	f.label(f.board, "Bug", "#ff0000")
	if got := labelNames(f.boardLabels(f.board, f.viewer)); got != "Bug,Urgente" {
		t.Errorf("list = %q, want by name", got)
	}

	// Partial update: only the color.
	rec = f.do("PATCH", "/labels/"+urgent.ID.String(), `{"color":"#00AAFF"}`, f.beto)
	if rec.Code != http.StatusOK {
		t.Fatalf("update color: %d %s", rec.Code, rec.Body)
	}
	var updated labelResponse
	_ = json.NewDecoder(rec.Body).Decode(&updated)
	if updated.Name != "Urgente" || updated.Color != "#00aaff" {
		t.Errorf("after color update = %+v", updated)
	}
	rec = f.do("PATCH", "/labels/"+urgent.ID.String(), `{"name":"Muy urgente"}`, f.beto)
	_ = json.NewDecoder(rec.Body).Decode(&updated)
	if rec.Code != http.StatusOK || updated.Name != "Muy urgente" || updated.Color != "#00aaff" {
		t.Errorf("after name update: %d %+v", rec.Code, updated)
	}

	// Tasks show the label's current name.
	task := f.createTaskWith(f.todo, map[string]any{"title": "T", "label_ids": []uuid.UUID{urgent.ID}}, f.ana)
	if labelNames(task.Labels) != "Muy urgente" {
		t.Errorf("task labels = %+v", task.Labels)
	}

	// Deleting it removes it from its tasks without editing them.
	if rec := f.do("DELETE", "/labels/"+urgent.ID.String(), "", f.ana); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if got := f.getTask(task.ID, f.ana); len(got.Labels) != 0 || got.Version != task.Version {
		t.Errorf("task after label delete = %+v", got)
	}
	if got := labelNames(f.boardLabels(f.board, f.ana)); got != "Bug" {
		t.Errorf("list after delete = %q", got)
	}
	// Gone: indistinguishable from a label you cannot see.
	if rec := f.do("PATCH", "/labels/"+urgent.ID.String(), `{"name":"x"}`, f.ana); rec.Code != http.StatusForbidden {
		t.Errorf("patch deleted label: %d, want 403", rec.Code)
	}
}

func TestLabelNamesAreUniquePerBoard(t *testing.T) {
	f := newTaskFixture(t)
	f.label(f.board, "Bug", "#ff0000")
	other := f.label(f.board, "Otra", "#00ff00")

	for _, rt := range []struct{ method, path, body string }{
		{"POST", "/boards/" + f.board.String() + "/labels", `{"name":"Bug","color":"#000000"}`},
		{"POST", "/boards/" + f.board.String() + "/labels", `{"name":" Bug ","color":"#000000"}`}, // same once trimmed
		{"PATCH", "/labels/" + other.ID.String(), `{"name":"Bug"}`},
	} {
		rec := f.do(rt.method, rt.path, rt.body, f.ana)
		if rec.Code != http.StatusConflict || decodeError(t, rec).Code != CodeLabelExists {
			t.Errorf("%s %s %s: status = %d, want 409 label_exists", rt.method, rt.path, rt.body, rec.Code)
		}
	}

	// The same name on another board is fine.
	otherBoard := f.dbEnv.board(f.ws, "Otro")
	f.label(otherBoard, "Bug", "#ff0000")
	if got := labelNames(f.boardLabels(f.board, f.ana)); got != "Bug,Otra" {
		t.Errorf("labels = %q", got)
	}
}

func TestLabelValidation(t *testing.T) {
	f := newTaskFixture(t)
	l := f.label(f.board, "Bug", "#ff0000")
	long := strings.Repeat("x", 51)

	for _, rt := range []struct {
		method, path, body, field string
	}{
		{"POST", "/boards/" + f.board.String() + "/labels", `{"name":"","color":"#ff0000"}`, "name"},
		{"POST", "/boards/" + f.board.String() + "/labels", `{"name":"` + long + `","color":"#ff0000"}`, "name"},
		{"POST", "/boards/" + f.board.String() + "/labels", `{"name":"x"}`, "color"},
		{"POST", "/boards/" + f.board.String() + "/labels", `{"name":"x","color":"red"}`, "color"},
		{"POST", "/boards/" + f.board.String() + "/labels", `{"name":"x","color":"#fff"}`, "color"},
		{"POST", "/boards/" + f.board.String() + "/labels", `{"name":"x","color":"#gggggg"}`, "color"},
		{"PATCH", "/labels/" + l.ID.String(), `{}`, "body"},
		{"PATCH", "/labels/" + l.ID.String(), `{"name":"  "}`, "name"},
		{"PATCH", "/labels/" + l.ID.String(), `{"color":"ff0000"}`, "color"},
	} {
		rec := f.do(rt.method, rt.path, rt.body, f.ana)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s %s: status = %d, want 422", rt.method, rt.body, rec.Code)
			continue
		}
		if got := decodeError(t, rec); got.Fields[rt.field] == "" {
			t.Errorf("%s %s: error = %+v, want field %q", rt.method, rt.body, got, rt.field)
		}
	}
	if got := f.boardLabels(f.board, f.ana); len(got) != 1 || got[0].Name != "Bug" || got[0].Color != "#ff0000" {
		t.Errorf("labels changed: %+v", got)
	}
}

func TestLabelRoutesPermissionMatrix(t *testing.T) {
	type route struct {
		name, method string
		path         func(f *taskFixture, label uuid.UUID) string
		body         string
		write        bool
		success      int
	}
	routes := []route{
		{"list", "GET", func(f *taskFixture, _ uuid.UUID) string { return "/boards/" + f.board.String() + "/labels" }, "", false, 200},
		{"create", "POST", func(f *taskFixture, _ uuid.UUID) string { return "/boards/" + f.board.String() + "/labels" },
			`{"name":"Nueva","color":"#123456"}`, true, 201},
		{"update", "PATCH", func(_ *taskFixture, id uuid.UUID) string { return "/labels/" + id.String() }, `{"name":"Cambiada"}`, true, 200},
		{"delete", "DELETE", func(_ *taskFixture, id uuid.UUID) string { return "/labels/" + id.String() }, "", true, 204},
	}
	for _, rt := range routes {
		for _, actor := range []string{"admin", "member", "viewer", "outsider"} {
			t.Run(rt.name+"/"+actor, func(t *testing.T) {
				f := newTaskFixture(t)
				l := f.label(f.board, "Bug", "#ff0000")
				as := map[string]testUser{"admin": f.admin, "member": f.ana, "viewer": f.viewer, "outsider": f.outsider}[actor]

				rec := f.do(rt.method, rt.path(f, l.ID), rt.body, as)

				wantStatus, wantMsg := rt.success, ""
				switch {
				case actor == "outsider":
					wantStatus, wantMsg = http.StatusForbidden, msgNoWorkspaceAccess
				case rt.write && actor == "viewer":
					wantStatus, wantMsg = http.StatusForbidden, msgRoleNotAllowed
				}
				if rec.Code != wantStatus {
					t.Fatalf("status = %d, want %d; body = %s", rec.Code, wantStatus, rec.Body)
				}
				if wantStatus == http.StatusForbidden {
					if got := decodeError(t, rec); got.Message != wantMsg {
						t.Errorf("message = %q, want %q", got.Message, wantMsg)
					}
					if got := f.boardLabels(f.board, f.admin); len(got) != 1 || got[0].Name != "Bug" {
						t.Errorf("forbidden request changed labels: %+v", got)
					}
				}
			})
		}
	}
}

func TestLabelRoutesDoNotRevealExistence(t *testing.T) {
	f := newTaskFixture(t)
	l := f.label(f.board, "Privada", "#ff0000")

	for _, rt := range []struct {
		method, prefix, suffix, body string
		existing                     uuid.UUID
	}{
		{"GET", "/boards/", "/labels", "", f.board},
		{"POST", "/boards/", "/labels", `{"name":"x","color":"#000000"}`, f.board},
		{"PATCH", "/labels/", "", `{"name":"x"}`, l.ID},
		{"DELETE", "/labels/", "", "", l.ID},
	} {
		t.Run(rt.method+" "+rt.prefix+"{id}"+rt.suffix, func(t *testing.T) {
			var bodies []string
			for _, id := range []string{rt.existing.String(), uuid.NewString(), "no-es-un-uuid"} {
				rec := f.do(rt.method, rt.prefix+id+rt.suffix, rt.body, f.outsider)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("id %s: status = %d, want 403", id, rec.Code)
				}
				bodies = append(bodies, rec.Body.String())
			}
			if bodies[0] != bodies[1] || bodies[1] != bodies[2] {
				t.Errorf("responses differ:\n%s", strings.Join(bodies, "\n"))
			}
		})
	}
	if got := labelNames(f.boardLabels(f.board, f.admin)); got != "Privada" {
		t.Errorf("labels changed: %q", got)
	}
}
