// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package reserved

import (
	"context"
	"database/sql"
	"sync"

	"github.com/ha1tch/xolu/pkg/gc"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// Sweeper collects lapsed reservations from every registered adopter of
// the tentative-row convention. It implements gc.Sweeper and plugs into
// the existing GC worker abstraction.
//
// The sweeper is hygiene, not enforcement: the deadline is authoritative
// (guard predicates and Confirm compare reserve_deadline inline), so a
// lapsed reservation has already stopped counting everywhere before the
// sweeper ever sees it. What the sweep reclaims is storage and index
// space — finiteness (@D04b), not correctness.
type Sweeper struct {
	mu     sync.Mutex
	tables []adopter
	now    func() ot.Instant // test seam; defaults to xolutime.Now
}

type adopter struct {
	db    *sql.DB
	table string
}

// NewSweeper creates an empty Sweeper. Adopting tables register with
// Register; a sweeper with no registrations sweeps nothing and reports
// zeroes, which is valid (gc.Report documents zero values as such).
func NewSweeper() *Sweeper {
	return &Sweeper{now: ot.Now}
}

// Register adds a convention-adopting table to the sweep set. Safe for
// concurrent use with Sweep.
func (s *Sweeper) Register(db *sql.DB, table string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tables = append(s.tables, adopter{db: db, table: table})
}

// Sweep deletes reserved rows whose deadline has lapsed, across every
// registered table. Per-table failures are counted in Report.Errors and
// do not abort the cycle — one adopter's trouble must not starve the
// others' hygiene. Examined counts reserved rows seen; Collected counts
// rows deleted. Duration is stamped by the gc.Worker.
func (s *Sweeper) Sweep(ctx context.Context) (gc.Report, error) {
	s.mu.Lock()
	tables := make([]adopter, len(s.tables))
	copy(tables, s.tables)
	nowNano := s.now().UnixNano()
	s.mu.Unlock()

	var r gc.Report
	for _, a := range tables {
		var reserved int64
		if err := a.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM `+a.table+` WHERE state = 'reserved'`,
		).Scan(&reserved); err != nil {
			r.Errors++
			continue
		}
		r.Examined += int(reserved)

		res, err := a.db.ExecContext(ctx,
			`DELETE FROM `+a.table+
				` WHERE state = 'reserved' AND reserve_deadline <= ?`, nowNano)
		if err != nil {
			r.Errors++
			continue
		}
		n, err := res.RowsAffected()
		if err != nil {
			r.Errors++
			continue
		}
		r.Collected += int(n)
	}
	return r, nil
}
