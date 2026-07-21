package violations

import (
	"fmt"
	"strconv"
)

// TimelineID mirrors the registered sized-id shape (name + underlying uint32).
type TimelineID uint32

const MaxTimelineID TimelineID = 0xFFFFFFFF

type CalOrdinal uint32

type wireReq struct{ Timeline uint16 }

func violations() {
	// Check 1: the exact CI-breaking pattern — ceiling constant to int.
	fmt.Println(int(MaxTimelineID)) // want `@C04d violation: TimelineID .uint32 sized-id. converted to int`

	// Check 1: narrowing a sized-id variable to uint16.
	var tid TimelineID = 70000
	fmt.Println(uint16(tid)) // want `@C04d violation: TimelineID .uint32 sized-id. converted to uint16`

	// Check 2: short parse flowing into a 32-bit id.
	v, _ := strconv.ParseUint("70000", 10, 16)
	fmt.Println(TimelineID(v)) // want `@C04d violation: v parsed with bitSize 16 but TimelineID is uint32`

	// Check 1: CalOrdinal narrowed to int32.
	var ord CalOrdinal = 3_000_000_000
	fmt.Println(int32(ord)) // want `@C04d violation: CalOrdinal .uint32 sized-id. converted to int32`

	// Check 3: id constructed from a lossy uint16 wire field.
	var r wireReq
	fmt.Println(TimelineID(r.Timeline)) // want `@C04d violation: TimelineID .uint32 sized-id. constructed from uint16`
}

func legal() {
	var tid TimelineID = 70000

	// int64 carry — the sanctioned wide form.
	fmt.Println(int64(tid))

	// 32-bit parse into a 32-bit id.
	w, _ := strconv.ParseUint("70000", 10, 32)
	fmt.Println(TimelineID(w))

	// Tenant-id shape — 16-bit parse NOT flowing into a sized id.
	tn, _ := strconv.ParseUint("42", 10, 16)
	fmt.Println(uint16(tn))

	// Untyped constant — compiler range-checks it.
	fmt.Println(TimelineID(5))

	// Explicit uint32 assertion idiom (test loop counters).
	i := 3
	fmt.Println(TimelineID(uint32(i)))
}
