package codexexecgateway

import (
	"sync"
	"testing"
	"time"
)

func TestRevokedSet_AddAndContains(t *testing.T) {
	r := NewRevokedSet(100)
	if r.Contains("trn_1") {
		t.Fatal("empty set should not contain anything")
	}
	r.Add("trn_1", time.Now().Add(time.Hour).Unix())
	if !r.Contains("trn_1") {
		t.Fatal("after Add should contain")
	}
}

func TestRevokedSet_PruneExpired(t *testing.T) {
	r := NewRevokedSet(100)
	r.Add("trn_old", time.Now().Add(-time.Second).Unix())
	r.Add("trn_new", time.Now().Add(time.Hour).Unix())
	r.Prune()
	if r.Contains("trn_old") {
		t.Fatal("expired entry should be pruned")
	}
	if !r.Contains("trn_new") {
		t.Fatal("non-expired entry should remain")
	}
}

func TestRevokedSet_CapEvictsOldest(t *testing.T) {
	r := NewRevokedSet(3)
	exp := time.Now().Add(time.Hour).Unix()
	r.Add("a", exp)
	r.Add("b", exp)
	r.Add("c", exp)
	r.Add("d", exp) // forces an eviction
	if r.Size() > 3 {
		t.Fatalf("size %d > cap 3", r.Size())
	}
	if !r.Contains("d") {
		t.Fatal("newest must remain")
	}
}

func TestRevokedSet_Concurrent(t *testing.T) {
	r := NewRevokedSet(1000)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.Add("trn", time.Now().Add(time.Hour).Unix())
			r.Contains("trn")
		}(i)
	}
	wg.Wait()
}
