package history

import (
	"testing"
)

func TestParseSymbolChanges_HunkOverlap(t *testing.T) {
	// Hunk covers lines 10-16 in the new file, symbol is at lines 12-20.
	// Overlap: 12-16 → should match.
	input := `COMMIT:abc123|alice|1713000000|fix: update funding calc
diff --git a/core/funding/calc.go b/core/funding/calc.go
index 1234567..abcdef0 100644
--- a/core/funding/calc.go
+++ b/core/funding/calc.go
@@ -10,5 +10,7 @@ package funding
 func CalculateFunding(amount float64) float64 {
-    return amount * 0.1
+    if amount <= 0 {
+        return 0
+    }
+    return amount * 0.1
 }
`

	changes, err := parseSymbolChanges(input, "CalculateFunding", 12, 20, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	if changes[0].Hash != "abc123" {
		t.Errorf("hash: got %q, want %q", changes[0].Hash, "abc123")
	}
	if changes[0].Author != "alice" {
		t.Errorf("author: got %q, want %q", changes[0].Author, "alice")
	}
	if changes[0].Timestamp != 1713000000 {
		t.Errorf("timestamp: got %d, want %d", changes[0].Timestamp, 1713000000)
	}
	if changes[0].Message != "fix: update funding calc" {
		t.Errorf("message: got %q, want %q", changes[0].Message, "fix: update funding calc")
	}
	if changes[0].DiffSnippet == "" {
		t.Error("expected non-empty DiffSnippet")
	}
}

func TestParseSymbolChanges_NoOverlap(t *testing.T) {
	// Hunk covers lines 50-59 in the new file, symbol is at lines 10-20.
	// No overlap and no symbol name in hunk → should not match.
	input := `COMMIT:def456|bob|1713001000|refactor: rename helper
diff --git a/core/utils/helper.go b/core/utils/helper.go
index aaaaaaa..bbbbbbb 100644
--- a/core/utils/helper.go
+++ b/core/utils/helper.go
@@ -48,5 +50,10 @@ func unrelatedHelper() {
 func anotherFunction() {
-    doStuff()
+    doOtherStuff()
+    andMore()
 }
`

	changes, err := parseSymbolChanges(input, "CalculateFunding", 10, 20, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("got %d changes, want 0", len(changes))
	}
}

func TestParseSymbolChanges_NameMatch(t *testing.T) {
	// Hunk covers lines 80-89, symbol is at lines 10-20.
	// No line overlap, but the hunk body mentions the symbol name → should match.
	input := `COMMIT:ghi789|carol|1713002000|feat: add caller of ProcessOrder
diff --git a/service/order/handler.go b/service/order/handler.go
index ccccccc..ddddddd 100644
--- a/service/order/handler.go
+++ b/service/order/handler.go
@@ -78,4 +80,10 @@ func handleRequest(r *Request) {
 func dispatch(r *Request) {
+    result := ProcessOrder(r.OrderID)
+    if result.Err != nil {
+        log.Error(result.Err)
+    }
 }
`

	changes, err := parseSymbolChanges(input, "ProcessOrder", 10, 20, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	if changes[0].Hash != "ghi789" {
		t.Errorf("hash: got %q, want %q", changes[0].Hash, "ghi789")
	}
	if changes[0].DiffSnippet == "" {
		t.Error("expected non-empty DiffSnippet")
	}
}

func TestParseSymbolChanges_MaxCommits(t *testing.T) {
	// Three matching commits but maxCommits=2 → should return only 2.
	input := `COMMIT:aaa111|alice|1713000000|first change
diff --git a/pkg/foo.go b/pkg/foo.go
index 1111111..2222222 100644
--- a/pkg/foo.go
+++ b/pkg/foo.go
@@ -5,3 +5,4 @@ package pkg
 func Foo() {
+    // first
 }
COMMIT:bbb222|bob|1713001000|second change
diff --git a/pkg/foo.go b/pkg/foo.go
index 2222222..3333333 100644
--- a/pkg/foo.go
+++ b/pkg/foo.go
@@ -5,4 +5,5 @@ package pkg
 func Foo() {
+    // second
     // first
 }
COMMIT:ccc333|carol|1713002000|third change
diff --git a/pkg/foo.go b/pkg/foo.go
index 3333333..4444444 100644
--- a/pkg/foo.go
+++ b/pkg/foo.go
@@ -5,5 +5,6 @@ package pkg
 func Foo() {
+    // third
     // second
     // first
 }
`

	changes, err := parseSymbolChanges(input, "Foo", 5, 10, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2", len(changes))
	}
	if changes[0].Hash != "aaa111" {
		t.Errorf("change 0 hash: got %q, want %q", changes[0].Hash, "aaa111")
	}
	if changes[1].Hash != "bbb222" {
		t.Errorf("change 1 hash: got %q, want %q", changes[1].Hash, "bbb222")
	}
}

func TestParseSymbolChanges_EmptyOutput(t *testing.T) {
	changes, err := parseSymbolChanges("", "AnySymbol", 1, 10, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("got %d changes, want 0", len(changes))
	}
}
