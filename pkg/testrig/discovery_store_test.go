package testrig_test

import (
	"os"
	"sync"
	"testing"

	"github.com/sha1n/testrig-go/pkg/testrig"
)

// --- MapStore Tests ---

func TestMapStore_StoreAndLoad(t *testing.T) {
	s := testrig.NewMapStore()
	if err := s.Store("key1", "value1"); err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	val, ok := s.Load("key1")
	if !ok {
		t.Fatal("Expected ok=true, got false")
	}
	if val != "value1" {
		t.Errorf("Expected value1, got %q", val)
	}
}

func TestMapStore_Load_NotFound(t *testing.T) {
	s := testrig.NewMapStore()
	val, ok := s.Load("missing")
	if ok {
		t.Error("Expected ok=false for missing key")
	}
	if val != "" {
		t.Errorf("Expected empty string, got %q", val)
	}
}

func TestMapStore_Store_Overwrite(t *testing.T) {
	s := testrig.NewMapStore()
	_ = s.Store("key", "first")
	_ = s.Store("key", "second")
	val, ok := s.Load("key")
	if !ok {
		t.Fatal("Expected ok=true")
	}
	if val != "second" {
		t.Errorf("Expected second, got %q", val)
	}
}

func TestMapStore_Delete(t *testing.T) {
	s := testrig.NewMapStore()
	_ = s.Store("key", "value")
	if err := s.Delete("key"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	val, ok := s.Load("key")
	if ok {
		t.Error("Expected ok=false after delete")
	}
	if val != "" {
		t.Errorf("Expected empty string, got %q", val)
	}
}

func TestMapStore_Delete_NotFound(t *testing.T) {
	s := testrig.NewMapStore()
	if err := s.Delete("nonexistent"); err != nil {
		t.Errorf("Delete on missing key should not error, got %v", err)
	}
}

func TestMapStore_Load_EmptyValue(t *testing.T) {
	s := testrig.NewMapStore()
	_ = s.Store("key", "")
	val, ok := s.Load("key")
	if !ok {
		t.Fatal("Expected ok=true for empty-string value")
	}
	if val != "" {
		t.Errorf("Expected empty string, got %q", val)
	}
}

func TestMapStore_ConcurrentAccess(t *testing.T) {
	s := testrig.NewMapStore()
	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			key := "key"
			_ = s.Store(key, "value")
			s.Load(key)
			_ = s.Delete(key)
		}(i)
	}
	wg.Wait()
}

// Zero-value tests for MapStore and OsEnvStore are in internals_test.go
// (package testrig) since those types are now unexported.

// --- OsEnvStore Tests ---

func TestOsEnvStore_StoreAndLoad(t *testing.T) {
	s := testrig.NewOsEnvStore()
	key := "TESTRIG_TEST_OSENVSTORE_STORE_AND_LOAD"
	t.Cleanup(func() { _ = os.Unsetenv(key) })

	if err := s.Store(key, "hello"); err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	val, ok := s.Load(key)
	if !ok {
		t.Fatal("Expected ok=true")
	}
	if val != "hello" {
		t.Errorf("Expected hello, got %q", val)
	}
}

func TestOsEnvStore_Load_NotFound(t *testing.T) {
	s := testrig.NewOsEnvStore()
	val, ok := s.Load("TESTRIG_TEST_OSENVSTORE_DEFINITELY_NOT_SET")
	if ok {
		t.Error("Expected ok=false for unset env var")
	}
	if val != "" {
		t.Errorf("Expected empty string, got %q", val)
	}
}

func TestOsEnvStore_Load_EmptyString(t *testing.T) {
	s := testrig.NewOsEnvStore()
	key := "TESTRIG_TEST_OSENVSTORE_EMPTY"
	t.Cleanup(func() { _ = os.Unsetenv(key) })

	_ = os.Setenv(key, "")
	val, ok := s.Load(key)
	if !ok {
		t.Fatal("Expected ok=true for env var set to empty string")
	}
	if val != "" {
		t.Errorf("Expected empty string, got %q", val)
	}
}

func TestOsEnvStore_Delete(t *testing.T) {
	s := testrig.NewOsEnvStore()
	key := "TESTRIG_TEST_OSENVSTORE_DELETE"
	t.Cleanup(func() { _ = os.Unsetenv(key) })

	_ = s.Store(key, "value")
	if err := s.Delete(key); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	_, ok := s.Load(key)
	if ok {
		t.Error("Expected ok=false after delete")
	}
}

func TestOsEnvStore_Delete_NotFound(t *testing.T) {
	s := testrig.NewOsEnvStore()
	if err := s.Delete("TESTRIG_TEST_OSENVSTORE_NOT_SET_FOR_DELETE"); err != nil {
		t.Errorf("Delete on unset key should not error, got %v", err)
	}
}

func TestOsEnvStore_Store_Overwrite(t *testing.T) {
	s := testrig.NewOsEnvStore()
	key := "TESTRIG_TEST_OSENVSTORE_OVERWRITE"
	t.Cleanup(func() { _ = os.Unsetenv(key) })

	_ = s.Store(key, "first")
	_ = s.Store(key, "second")
	val, ok := s.Load(key)
	if !ok {
		t.Fatal("Expected ok=true")
	}
	if val != "second" {
		t.Errorf("Expected second, got %q", val)
	}
}
