package datastore

import (
	"testing"
)

func TestScanGoFile_InsertIntoSchemaQualified(t *testing.T) {
	src := []byte(`package store

func CreateDisplayLocation(ctx context.Context, projectID int, dl DisplayLocation) (int, error) {
	var retID int
	err := s.db.Get(ctx, &retID, ` + "`" + `
		INSERT INTO plan.display_locations (project_id, created_by, display_id)
		VALUES ($1, $2, $3)
		RETURNING display_location_id
	` + "`" + `, projectID, dl.DisplayName)
	return retID, err
}
`)
	refs := ScanGoFile("store.go", src)
	if len(refs) == 0 {
		t.Fatal("expected at least one ref, got none")
	}

	found := false
	for _, r := range refs {
		if r.Table == "plan.display_locations" && r.Operation == "INSERT" {
			found = true
			if r.FuncName != "CreateDisplayLocation" {
				t.Errorf("expected FuncName=CreateDisplayLocation, got %q", r.FuncName)
			}
			if r.FilePath != "store.go" {
				t.Errorf("expected FilePath=store.go, got %q", r.FilePath)
			}
		}
	}
	if !found {
		t.Errorf("did not find INSERT ref for plan.display_locations; got refs: %+v", refs)
	}
}

func TestScanGoFile_SelectFromSingleTable(t *testing.T) {
	src := []byte(`package store

func GetUser(ctx context.Context, id int) (*User, error) {
	var u User
	err := s.db.Get(ctx, &u, ` + "`" + `SELECT id, name, email FROM users WHERE id = $1` + "`" + `, id)
	return &u, err
}
`)
	refs := ScanGoFile("users.go", src)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d: %+v", len(refs), refs)
	}
	if refs[0].Table != "users" {
		t.Errorf("expected table=users, got %q", refs[0].Table)
	}
	if refs[0].Operation != "SELECT" {
		t.Errorf("expected operation=SELECT, got %q", refs[0].Operation)
	}
	if refs[0].FuncName != "GetUser" {
		t.Errorf("expected FuncName=GetUser, got %q", refs[0].FuncName)
	}
}

func TestScanGoFile_SelectWithJoin(t *testing.T) {
	src := []byte(`package store

func ListDisplayLocations(ctx context.Context, pid int) ([]DisplayLocation, error) {
	var dls []DisplayLocation
	err := s.db.Query(ctx, &dls, ` + "`" + `
		SELECT d.name, dl.description
		FROM plan.display_locations dl
		JOIN plan.displays d ON d.display_id = dl.display_id
		WHERE dl.project_id = $1
	` + "`" + `, pid)
	return dls, err
}
`)
	refs := ScanGoFile("display.go", src)
	if len(refs) < 2 {
		t.Fatalf("expected at least 2 refs (FROM + JOIN), got %d: %+v", len(refs), refs)
	}

	tables := make(map[string]bool)
	for _, r := range refs {
		tables[r.Table] = true
		if r.Operation != "SELECT" {
			t.Errorf("expected operation=SELECT, got %q for table %q", r.Operation, r.Table)
		}
	}
	if !tables["plan.display_locations"] {
		t.Error("missing table plan.display_locations")
	}
	if !tables["plan.displays"] {
		t.Error("missing table plan.displays")
	}
}

func TestScanGoFile_MultilineBacktickSQL(t *testing.T) {
	src := []byte(`package store

func UpdateProject(ctx context.Context, p Project) error {
	_, err := s.db.Exec(ctx, ` + "`" + `
		UPDATE projects
		SET name = $1,
		    description = $2,
		    updated_at = NOW()
		WHERE id = $3
	` + "`" + `, p.Name, p.Description, p.ID)
	return err
}
`)
	refs := ScanGoFile("projects.go", src)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d: %+v", len(refs), refs)
	}
	if refs[0].Table != "projects" {
		t.Errorf("expected table=projects, got %q", refs[0].Table)
	}
	if refs[0].Operation != "UPDATE" {
		t.Errorf("expected operation=UPDATE, got %q", refs[0].Operation)
	}
}

func TestScanGoFile_DeleteFrom(t *testing.T) {
	src := []byte(`package store

func DeleteGroupRoles(ctx context.Context, groupID int) error {
	_, err := s.db.ExecContext(ctx, ` + "`" + `DELETE FROM useraccess.group_project_role WHERE group_id = $1` + "`" + `, groupID)
	return err
}
`)
	refs := ScanGoFile("roles.go", src)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d: %+v", len(refs), refs)
	}
	if refs[0].Table != "useraccess.group_project_role" {
		t.Errorf("expected table=useraccess.group_project_role, got %q", refs[0].Table)
	}
	if refs[0].Operation != "DELETE" {
		t.Errorf("expected operation=DELETE, got %q", refs[0].Operation)
	}
}

func TestScanGoFile_NoSQL(t *testing.T) {
	src := []byte(`package utils

import "fmt"

func Hello(name string) string {
	return fmt.Sprintf("Hello, %s!", name)
}

func Add(a, b int) int {
	return a + b
}
`)
	refs := ScanGoFile("utils.go", src)
	if len(refs) != 0 {
		t.Errorf("expected 0 refs for non-SQL file, got %d: %+v", len(refs), refs)
	}
}

func TestScanGoFile_ConstVarSQL(t *testing.T) {
	src := []byte(`package store

var listOrganizations = ` + "`" + `SELECT id, name, description FROM useraccess.organization;` + "`" + `

const deleteOrgQuery = ` + "`" + `DELETE FROM useraccess.organization WHERE id = $1` + "`" + `

func ListOrgs(ctx context.Context) ([]Org, error) {
	var orgs []Org
	err := s.db.Select(ctx, &orgs, listOrganizations)
	return orgs, err
}

func DeleteOrg(ctx context.Context, id int) error {
	_, err := s.db.Exec(ctx, deleteOrgQuery, id)
	return err
}
`)
	refs := ScanGoFile("orgs.go", src)

	// The var and const declarations are outside any function, so FuncName should be "".
	// We expect at least 2 refs: one for SELECT on useraccess.organization,
	// one for DELETE on useraccess.organization.
	if len(refs) < 2 {
		t.Fatalf("expected at least 2 refs, got %d: %+v", len(refs), refs)
	}

	foundSelect := false
	foundDelete := false
	for _, r := range refs {
		if r.Table == "useraccess.organization" && r.Operation == "SELECT" {
			foundSelect = true
			if r.FuncName != "" {
				t.Errorf("expected empty FuncName for package-level var, got %q", r.FuncName)
			}
		}
		if r.Table == "useraccess.organization" && r.Operation == "DELETE" {
			foundDelete = true
			if r.FuncName != "" {
				t.Errorf("expected empty FuncName for package-level const, got %q", r.FuncName)
			}
		}
	}
	if !foundSelect {
		t.Error("missing SELECT ref for useraccess.organization")
	}
	if !foundDelete {
		t.Error("missing DELETE ref for useraccess.organization")
	}
}

func TestScanGoFile_MultipleFunctionsDifferentSQL(t *testing.T) {
	src := []byte(`package store

func GetItems(ctx context.Context) ([]Item, error) {
	var items []Item
	err := s.db.Select(ctx, &items, ` + "`" + `SELECT id, name FROM inventory.items` + "`" + `)
	return items, err
}

func CreateOrder(ctx context.Context, o Order) (int, error) {
	var id int
	err := s.db.Get(ctx, &id, ` + "`" + `
		INSERT INTO orders.order_lines (order_id, item_id, quantity)
		VALUES ($1, $2, $3)
		RETURNING id
	` + "`" + `, o.OrderID, o.ItemID, o.Quantity)
	return id, err
}

func UpdateStock(ctx context.Context, itemID int, qty int) error {
	_, err := s.db.Exec(ctx, ` + "`" + `UPDATE inventory.stock SET quantity = $1 WHERE item_id = $2` + "`" + `, qty, itemID)
	return err
}
`)
	refs := ScanGoFile("multi.go", src)
	if len(refs) < 3 {
		t.Fatalf("expected at least 3 refs, got %d: %+v", len(refs), refs)
	}

	type expected struct {
		table    string
		op       string
		funcName string
	}
	expectations := []expected{
		{"inventory.items", "SELECT", "GetItems"},
		{"orders.order_lines", "INSERT", "CreateOrder"},
		{"inventory.stock", "UPDATE", "UpdateStock"},
	}

	for _, exp := range expectations {
		found := false
		for _, r := range refs {
			if r.Table == exp.table && r.Operation == exp.op && r.FuncName == exp.funcName {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing ref: table=%s op=%s func=%s; got refs: %+v", exp.table, exp.op, exp.funcName, refs)
		}
	}
}

func TestScanGoFile_Deduplication(t *testing.T) {
	// Same table referenced twice in the same function with the same operation
	// should only appear once.
	src := []byte(`package store

func GetUserData(ctx context.Context, id int) (*User, error) {
	var u User
	err := s.db.Get(ctx, &u, ` + "`" + `
		SELECT u.name, u.email
		FROM users u
		JOIN users u2 ON u2.id = u.manager_id
		WHERE u.id = $1
	` + "`" + `, id)
	return &u, err
}
`)
	refs := ScanGoFile("dedup.go", src)
	// "users" appears in both FROM and JOIN, but should be deduplicated.
	count := 0
	for _, r := range refs {
		if r.Table == "users" && r.Operation == "SELECT" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 deduplicated ref for users/SELECT, got %d; refs: %+v", count, refs)
	}
}

func TestScanGoFile_GoTemplateSkipped(t *testing.T) {
	src := []byte(`package store

func DynamicQuery(ctx context.Context) error {
	query := ` + "`" + `SELECT * FROM {{ .Table "ItemsTable" }} WHERE id = $1` + "`" + `
	_, err := s.db.Exec(ctx, query, 1)
	return err
}
`)
	refs := ScanGoFile("template.go", src)
	if len(refs) != 0 {
		t.Errorf("expected 0 refs for Go template SQL, got %d: %+v", len(refs), refs)
	}
}

func TestScanGoFile_SubqueryExtraction(t *testing.T) {
	src := []byte(`package store

func GetActiveUsers(ctx context.Context) ([]User, error) {
	var users []User
	err := s.db.Select(ctx, &users, ` + "`" + `
		SELECT u.id, u.name
		FROM users u
		WHERE u.id IN (SELECT user_id FROM active_sessions WHERE expired = false)
	` + "`" + `)
	return users, err
}
`)
	refs := ScanGoFile("subquery.go", src)

	tables := make(map[string]bool)
	for _, r := range refs {
		tables[r.Table] = true
	}
	if !tables["users"] {
		t.Error("missing table 'users' from outer query")
	}
	if !tables["active_sessions"] {
		t.Error("missing table 'active_sessions' from subquery")
	}
}

func TestScanGoFile_LineNumber(t *testing.T) {
	src := []byte(`package store

import "context"

func Fetch(ctx context.Context) error {
	_, err := s.db.Query(ctx, ` + "`" + `SELECT id FROM items` + "`" + `)
	return err
}
`)
	refs := ScanGoFile("line.go", src)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d: %+v", len(refs), refs)
	}
	// The backtick string starts on line 6.
	if refs[0].Line != 6 {
		t.Errorf("expected line=6, got %d", refs[0].Line)
	}
}
