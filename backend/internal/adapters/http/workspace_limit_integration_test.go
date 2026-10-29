package httpapi

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/DarkApofis/tablero/backend/internal/domain"
)

// Several creations at once must not get past the limit together: they are
// serialized per user (LockCreator), so exactly MaxCreatedWorkspaces pass.
func TestWorkspaceLimitHoldsUnderConcurrentCreates(t *testing.T) {
	f := newTaskFixture(t)
	const attempts = 5

	codes := make([]int, attempts)
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Go(func() {
			codes[i] = f.do("POST", "/workspaces", `{"name":"Propio"}`, f.ana).Code
		})
	}
	wg.Wait()
	created, refused := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			refused++
		default:
			t.Errorf("unexpected status %d", c)
		}
	}
	if created != domain.MaxCreatedWorkspaces || refused != attempts-domain.MaxCreatedWorkspaces {
		t.Fatalf("created %d, refused %d (codes %v)", created, refused, codes)
	}

	rec := f.do("POST", "/workspaces", `{"name":"Otro"}`, f.ana)
	if e := decodeError(t, rec); rec.Code != http.StatusConflict || e.Code != CodeWorkspaceLimit || e.Message == "" {
		t.Errorf("over the limit: %d %+v", rec.Code, e)
	}
	var list struct {
		Workspaces []struct{ Role string }
		Created    int `json:"created"`
		MaxCreated int `json:"max_created"`
	}
	_ = json.NewDecoder(f.do("GET", "/workspaces", "", f.ana).Body).Decode(&list)
	// The fixture's workspace (as member) plus the two of her own (as admin).
	if list.Created != 2 || list.MaxCreated != domain.MaxCreatedWorkspaces || len(list.Workspaces) != 3 {
		t.Errorf("list = %+v", list)
	}
}

func TestCreateBoardWithInitialColumns(t *testing.T) {
	f := newTaskFixture(t)

	rec := f.do("POST", "/workspaces/"+f.ws.String()+"/boards", `{"name":"Nuevo","columns":["Pendiente","En progreso","Hecho"]}`, f.admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create board: %d %s", rec.Code, rec.Body)
	}
	var b struct{ ID string }
	_ = json.NewDecoder(rec.Body).Decode(&b)
	var detail struct {
		Columns []struct{ Name string }
	}
	_ = json.NewDecoder(f.do("GET", "/boards/"+b.ID, "", f.ana).Body).Decode(&detail)
	if len(detail.Columns) != 3 || detail.Columns[0].Name != "Pendiente" || detail.Columns[2].Name != "Hecho" {
		t.Errorf("columns = %+v", detail.Columns)
	}

	// Members create tasks, not boards.
	if rec := f.do("POST", "/workspaces/"+f.ws.String()+"/boards", `{"name":"De Ana","columns":["A"]}`, f.ana); rec.Code != http.StatusForbidden {
		t.Errorf("member creating a board: %d", rec.Code)
	}
}
