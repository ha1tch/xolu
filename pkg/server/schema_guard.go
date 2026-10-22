// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

import (
	"context"
	"fmt"

	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// checkAdaptedRegistrableEverywhere runs storage.CheckAdaptedRegistrable over the
// default store and every tenant store, so a refusal (XOT244) happens before any
// of them has been changed. registerAdaptedEverywhere applies a registration
// store by store and cannot undo the stores it already did.
func (s *Server) checkAdaptedRegistrableEverywhere(ctx context.Context, entity string) error {
	if sqlStore, ok := s.storage.(*storage.SQLiteStore); ok {
		if err := sqlStore.CheckAdaptedRegistrable(ctx, entity); err != nil {
			return fmt.Errorf("default store: %w", err)
		}
	}
	var firstErr error
	s.tenantStores.Range(func(key, value interface{}) bool {
		store, ok := value.(storage.Store)
		if !ok {
			return true
		}
		sqlStore, ok := store.(*storage.SQLiteStore)
		if !ok {
			return true
		}
		if err := sqlStore.CheckAdaptedRegistrable(ctx, entity); err != nil && firstErr == nil {
			tid, _ := key.(tenant.TenantID)
			firstErr = fmt.Errorf("tenant %d: %w", tid, err)
		}
		return true
	})
	return firstErr
}
