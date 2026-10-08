// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage_test

// commit_templates_test.go
//
// XOT234 and XOT235: "templates": true in a /commit, and append on_conflict.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/ha1tch/xolu/pkg/storage"
)

func getDoc(t *testing.T, store storage.Store, entity string, id int) map[string]interface{} {
	t.Helper()
	d, err := store.Get(context.Background(), entity, id)
	if err != nil {
		t.Fatalf("Get %s/%d: %v", entity, id, err)
	}
	return d
}

func TestCommitTemplates_ResolveInsideTheTransaction(t *testing.T) {
	store, cleanup := sqliteFactory(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := store.Save(ctx, "asset", 1, map[string]interface{}{"state": "A", "n": float64(1)}); err != nil {
		t.Fatal(err)
	}
	ver := entityVersion(t, store, "asset", 1)

	res, err := store.Commit(ctx, storage.CommitRequest{
		Templates: true,
		Update: storage.CommitUpdate{Entity: "asset", ID: 1, Version: &ver,
			Data: map[string]interface{}{"state": "B", "stamped": "$now"}},
		Append: []storage.CommitAppend{{Entity: "hist", ID: intp(1002), Data: map[string]interface{}{
			"snapshot": "$update.data", "version": "$update.version",
			"previous": "$previous.data", "previous_version": "$previous.version",
			"saved_at": "$now", "seq": "$commit_seq",
			"nested": map[string]interface{}{"list": []interface{}{"$update.version", "plain", "$100", "$$now"}},
		}}},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.Clock == nil || res.Clock.CommitSeq != 1 || len(res.Clock.Now) != len(storage.CommitInstantLayout) {
		t.Fatalf("Clock = %+v, want seq 1 and a fixed-width instant", res.Clock)
	}
	row := getDoc(t, store, "hist", 1002)
	snap, _ := row["snapshot"].(map[string]interface{})
	prev, _ := row["previous"].(map[string]interface{})
	if snap["state"] != "B" || snap["stamped"] != res.Clock.Now {
		t.Errorf("$update.data = %v, want state B stamped with the instant", snap)
	}
	if _, has := snap["id"]; has {
		t.Errorf("$update.data carries id: %v", snap)
	}
	if prev["state"] != "A" || fmt.Sprint(prev["n"]) != "1" {
		t.Errorf("$previous.data = %v, want the document before the update", prev)
	}
	if _, has := prev["_version"]; has {
		t.Errorf("$previous.data carries _version: %v", prev)
	}
	if fmt.Sprint(row["version"]) != fmt.Sprint(ver+1) || fmt.Sprint(row["previous_version"]) != fmt.Sprint(ver) {
		t.Errorf("version %v previous_version %v, want %d and %d", row["version"], row["previous_version"], ver+1, ver)
	}
	if row["saved_at"] != res.Clock.Now || fmt.Sprint(row["seq"]) != "1" {
		t.Errorf("saved_at %v seq %v, want %s and 1", row["saved_at"], row["seq"], res.Clock.Now)
	}
	list, _ := row["nested"].(map[string]interface{})["list"].([]interface{})
	if len(list) != 4 || fmt.Sprint(list[0]) != fmt.Sprint(ver+1) || list[1] != "plain" || list[2] != "$100" || list[3] != "$now" {
		t.Errorf("templates in a nested list = %v, want the version, plain, $100 and the escaped $now", list)
	}
	// The row's snapshot and the entity agree: the template wrote what the update wrote.
	if cur := getDoc(t, store, "asset", 1); cur["state"] != "B" || cur["stamped"] != res.Clock.Now {
		t.Errorf("entity after the commit = %v", cur)
	}
}

func intp(n int) *int { return &n }

func TestCommitTemplates_OffByDefault(t *testing.T) {
	store, cleanup := sqliteFactory(t)
	defer cleanup()
	ctx := context.Background()
	res, err := store.Commit(ctx, storage.CommitRequest{
		Update: storage.CommitUpdate{Entity: "asset", ID: 2, Data: map[string]interface{}{"a": "$now"}},
		Append: []storage.CommitAppend{{Entity: "hist", ID: intp(7), Data: map[string]interface{}{"s": "$update.data", "x": "$nope"}}},
	})
	if err != nil || res.Clock != nil {
		t.Fatalf("Commit: %+v %v", res, err)
	}
	if got := getDoc(t, store, "asset", 2); got["a"] != "$now" {
		t.Errorf("update data = %v, want the literal string", got)
	}
	if got := getDoc(t, store, "hist", 7); got["s"] != "$update.data" || got["x"] != "$nope" {
		t.Errorf("append data = %v, want the literal strings", got)
	}
}

func TestCommitTemplates_RefusedTemplatesWriteNothing(t *testing.T) {
	store, cleanup := sqliteFactory(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := store.Save(ctx, "asset", 3, map[string]interface{}{"state": "A"}); err != nil {
		t.Fatal(err)
	}
	for name, req := range map[string]storage.CommitRequest{
		"an unknown template": {Templates: true,
			Update: storage.CommitUpdate{Entity: "asset", ID: 3, Data: map[string]interface{}{"state": "B"}},
			Append: []storage.CommitAppend{{Entity: "hist", Data: map[string]interface{}{"x": "$nope"}}}},
		"a typo near a real one": {Templates: true,
			Update: storage.CommitUpdate{Entity: "asset", ID: 3, Data: map[string]interface{}{"state": "B"}},
			Append: []storage.CommitAppend{{Entity: "hist", Data: map[string]interface{}{"x": "$update_version"}}}},
		"update.version inside update.data": {Templates: true,
			Update: storage.CommitUpdate{Entity: "asset", ID: 3, Data: map[string]interface{}{"state": "$update.version"}},
			Append: []storage.CommitAppend{{Entity: "hist", Data: map[string]interface{}{"x": 1}}}},
		"update.data in a delete commit": {Templates: true,
			Delete: &storage.CommitDelete{Entity: "asset", ID: 3},
			Append: []storage.CommitAppend{{Entity: "hist", Data: map[string]interface{}{"x": "$update.data"}}}},
		"on_conflict without an id": {
			Update: storage.CommitUpdate{Entity: "asset", ID: 3, Data: map[string]interface{}{"state": "B"}},
			Append: []storage.CommitAppend{{Entity: "hist", OnConflict: "ignore", Data: map[string]interface{}{"x": 1}}}},
		"an unknown on_conflict": {
			Update: storage.CommitUpdate{Entity: "asset", ID: 3, Data: map[string]interface{}{"state": "B"}},
			Append: []storage.CommitAppend{{Entity: "hist", ID: intp(5), OnConflict: "replace", Data: map[string]interface{}{"x": 1}}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := store.Commit(ctx, req)
			if !errors.Is(err, storage.ErrCommitTemplate) {
				t.Fatalf("err = %v, want ErrCommitTemplate", err)
			}
			if got := getDoc(t, store, "asset", 3); got["state"] != "A" {
				t.Fatalf("the entity changed: %v", got)
			}
			if rows, _ := store.List(ctx, "hist"); len(rows) != 0 {
				t.Fatalf("an append of a refused commit landed: %v", rows)
			}
		})
	}
	if _, err := store.Commit(ctx, storage.CommitRequest{Templates: true,
		Update: storage.CommitUpdate{Entity: "asset", ID: 3, Data: map[string]interface{}{"state": "B"}},
		Append: []storage.CommitAppend{{Entity: "hist", Data: map[string]interface{}{"x": "$nope"}}}}); err == nil || !strings.Contains(err.Error(), "$nope") {
		t.Fatalf("the error does not name the template: %v", err)
	}
}

func TestCommitTemplates_PreviousOfAnEntityThatDoesNotExistAndOfADeletedOne(t *testing.T) {
	store, cleanup := sqliteFactory(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := store.Commit(ctx, storage.CommitRequest{Templates: true,
		Update: storage.CommitUpdate{Entity: "asset", ID: 4, Data: map[string]interface{}{"state": "A"}},
		Append: []storage.CommitAppend{{Entity: "hist", ID: intp(1), Data: map[string]interface{}{"previous": "$previous.data", "v": "$previous.version"}}}}); err != nil {
		t.Fatal(err)
	}
	row := getDoc(t, store, "hist", 1)
	if row["previous"] != nil || fmt.Sprint(row["v"]) != "0" {
		t.Errorf("previous of a new entity = %v, version %v, want null and 0", row["previous"], row["v"])
	}
	// A delete commit can record the document it deletes.
	ver := entityVersion(t, store, "asset", 4)
	if _, err := store.Commit(ctx, storage.CommitRequest{Templates: true,
		Delete: &storage.CommitDelete{Entity: "asset", ID: 4, Version: &ver},
		Append: []storage.CommitAppend{{Entity: "hist", ID: intp(2), Data: map[string]interface{}{"snapshot": "$previous.data", "version": "$previous.version", "at": "$now"}}}}); err != nil {
		t.Fatalf("delete with $previous: %v", err)
	}
	row = getDoc(t, store, "hist", 2)
	if snap, _ := row["snapshot"].(map[string]interface{}); snap["state"] != "A" || fmt.Sprint(row["version"]) != fmt.Sprint(ver) {
		t.Errorf("tombstone = %v, want the deleted document at version %d", row, ver)
	}
	if _, err := store.Get(ctx, "asset", 4); err == nil {
		t.Error("the entity still exists")
	}
}

func TestCommitAppend_OnConflictIgnore(t *testing.T) {
	store, cleanup := sqliteFactory(t)
	defer cleanup()
	ctx := context.Background()
	commit := func(onConflict string, data string) (storage.CommitResult, error) {
		return store.Commit(ctx, storage.CommitRequest{
			Update: storage.CommitUpdate{Entity: "asset", ID: 5, Data: map[string]interface{}{"n": data}},
			Append: []storage.CommitAppend{
				{Entity: "hist", ID: intp(10), OnConflict: onConflict, Data: map[string]interface{}{"v": data}},
				{Entity: "hist", ID: intp(11), Data: map[string]interface{}{"v": data + "-b"}},
			}})
	}
	if _, err := commit("ignore", "first"); err != nil {
		t.Fatal(err)
	}
	// Row 11 now exists, so a second commit conflicts on it whatever row 10 says.
	if _, err := commit("ignore", "second"); !errors.Is(err, storage.ErrAlreadyExists) {
		t.Fatalf("a plain append over an existing id: err = %v, want ErrAlreadyExists", err)
	}
	if got := getDoc(t, store, "asset", 5); got["n"] != "first" {
		t.Fatalf("the update of a refused commit landed: %v", got)
	}
	// With ignore on the row that exists, the commit goes ahead and reports the skip.
	res, err := store.Commit(ctx, storage.CommitRequest{
		Update: storage.CommitUpdate{Entity: "asset", ID: 5, Data: map[string]interface{}{"n": "third"}},
		Append: []storage.CommitAppend{
			{Entity: "hist", ID: intp(10), OnConflict: "ignore", Data: map[string]interface{}{"v": "third"}},
			{Entity: "hist", ID: intp(12), OnConflict: "ignore", Data: map[string]interface{}{"v": "third"}},
		}})
	if err != nil {
		t.Fatalf("Commit with ignore: %v", err)
	}
	if len(res.Appended) != 2 || !res.Appended[0].Skipped || res.Appended[1].Skipped || res.Appended[0].ID != 10 {
		t.Fatalf("Appended = %+v, want the first skipped and the second written", res.Appended)
	}
	if got := getDoc(t, store, "hist", 10); got["v"] != "first" {
		t.Errorf("a skipped row was overwritten: %v", got)
	}
	if got := getDoc(t, store, "asset", 5); got["n"] != "third" {
		t.Errorf("the update did not land: %v", got)
	}
}

func TestCommitTemplates_CommitSeqIsDurableGapFreeAndUnique(t *testing.T) {
	store, cleanup := sqliteFactory(t)
	defer cleanup()
	ctx := context.Background()
	seq := func(id int, version *int) (int64, error) {
		res, err := store.Commit(ctx, storage.CommitRequest{Templates: true,
			Update: storage.CommitUpdate{Entity: "asset", ID: id, Version: version, Data: map[string]interface{}{"n": "$commit_seq"}},
			Append: []storage.CommitAppend{{Entity: "hist", Data: map[string]interface{}{"s": "$commit_seq"}}}})
		if err != nil {
			return 0, err
		}
		return res.Clock.CommitSeq, nil
	}
	if s, err := seq(1, nil); err != nil || s != 1 {
		t.Fatalf("first: %d %v, want 1", s, err)
	}
	// A refused commit does not consume a number.
	stale := 99
	if _, err := seq(1, &stale); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("stale commit: err = %v, want ErrConflict", err)
	}
	if s, err := seq(1, nil); err != nil || s != 2 {
		t.Fatalf("after a refused commit: %d %v, want 2 (no gap)", s, err)
	}
	if got := fmt.Sprint(getDoc(t, store, "asset", 1)["n"]); got != "2" {
		t.Errorf("the entity holds seq %s, want 2", got)
	}

	// Concurrent commits each get their own number, with none missing.
	const workers, each = 8, 10
	var mu sync.Mutex
	var seen []int
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				s, err := seq(100+w, nil)
				if err != nil {
					t.Errorf("worker %d: %v", w, err)
					return
				}
				mu.Lock()
				seen = append(seen, int(s))
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	sort.Ints(seen)
	for i, s := range seen {
		if s != i+3 { // 1 and 2 are taken
			t.Fatalf("sequence numbers %v: position %d holds %d, want %d (gap or duplicate)", seen, i, s, i+3)
		}
	}
	if len(seen) != workers*each {
		t.Fatalf("%d numbers for %d commits", len(seen), workers*each)
	}
}

func TestNextCommitInstant_IsFixedWidthAndStrictlyIncreasing(t *testing.T) {
	prev := ""
	for i := 0; i < 5000; i++ {
		now := storage.NextCommitInstant()
		if len(now) != len(storage.CommitInstantLayout) || !strings.HasSuffix(now, "Z") {
			t.Fatalf("instant %q is not the fixed-width UTC layout", now)
		}
		if now <= prev {
			t.Fatalf("instant %q is not after %q: the clock repeated or went back", now, prev)
		}
		prev = now
	}
}
