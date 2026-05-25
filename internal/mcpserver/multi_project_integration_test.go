//go:build integration

package mcpserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hman-pro/projectlens/internal/mcpserver"
	"github.com/hman-pro/projectlens/internal/projects"
	"github.com/hman-pro/projectlens/internal/retrieval"
	"github.com/hman-pro/projectlens/internal/storage"
)

func TestUnknownProjectReturns404(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	root, err := storage.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	t.Cleanup(func() {
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_mcp_a CASCADE`)
	})
	dir := findMigrationsDirForTest(t)
	if err := root.MigrateInSchema(ctx, dir, "ri_mcp_a"); err != nil {
		t.Fatal(err)
	}
	rt, err := projects.Resolve(ctx, &projects.Registry{
		DatabaseURL: url,
		Projects:    []projects.Project{{Slug: "a", StorageSchema: "ri_mcp_a", RepoPath: "/tmp/x"}},
	}, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	srv := mcpserver.New(rt.DB, retrieval.NewRouter(rt.DB, nil), 0, rt.RepoPath)
	mux := http.NewServeMux()
	mount := "/a/mcp"
	mux.Handle(mount, http.StripPrefix(mount, srv.Handler()))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/nope/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown project: status=%d want 404", resp.StatusCode)
	}
}

func TestKnownButBrokenProjectReturns503(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	reg := &projects.Registry{
		DatabaseURL: url,
		Projects:    []projects.Project{{Slug: "b", StorageSchema: "ri_b_not_exist", RepoPath: "/tmp/x"}},
	}
	_, err := projects.Resolve(ctx, reg, "b")
	if err == nil {
		t.Fatal("expected resolve error")
	}
	stub := mcpserver.NotReadyHandler("b", err)
	mux := http.NewServeMux()
	mux.Handle("/b/mcp", stub)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	resp, gerr := http.Get(ts.URL + "/b/mcp")
	if gerr != nil {
		t.Fatal(gerr)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("broken project: status=%d want 503", resp.StatusCode)
	}
}

func TestPerProjectSessionManagerSeparation(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	root, err := storage.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	t.Cleanup(func() {
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_sess_a CASCADE`)
		_, _ = root.Pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS ri_sess_b CASCADE`)
	})
	dir := findMigrationsDirForTest(t)
	for _, s := range []string{"ri_sess_a", "ri_sess_b"} {
		if err := root.MigrateInSchema(ctx, dir, s); err != nil {
			t.Fatal(err)
		}
	}
	rtA, err := projects.Resolve(ctx, &projects.Registry{
		DatabaseURL: url,
		Projects:    []projects.Project{{Slug: "a", StorageSchema: "ri_sess_a", RepoPath: "/tmp/x"}},
	}, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer rtA.Close()
	rtB, err := projects.Resolve(ctx, &projects.Registry{
		DatabaseURL: url,
		Projects:    []projects.Project{{Slug: "b", StorageSchema: "ri_sess_b", RepoPath: "/tmp/x"}},
	}, "b")
	if err != nil {
		t.Fatal(err)
	}
	defer rtB.Close()

	srvA := mcpserver.New(rtA.DB, retrieval.NewRouter(rtA.DB, nil), 0, rtA.RepoPath)
	srvB := mcpserver.New(rtB.DB, retrieval.NewRouter(rtB.DB, nil), 0, rtB.RepoPath)
	mux := http.NewServeMux()
	mux.Handle("/a/mcp", http.StripPrefix("/a/mcp", srvA.Handler()))
	mux.Handle("/b/mcp", http.StripPrefix("/b/mcp", srvB.Handler()))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	for _, path := range []string{"/a/mcp", "/b/mcp"} {
		resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(initBody))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if resp.StatusCode >= 400 {
			t.Fatalf("%s: status=%d", path, resp.StatusCode)
		}
	}
}

func findMigrationsDirForTest(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"../../migrations", "../../../migrations"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Fatal("migrations dir not found")
	return ""
}
