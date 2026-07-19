package server

// S7/S8 — FSM handler file index.
//
//   v2_fsm_def_handlers.go     — S7 B2: six definition endpoints
//   v2_fsm_machine_handlers.go — S7 B3: nine machine endpoints (read/CRUD)
//   v2_fsm_common.go           — shared types, spec→toolkit builder, validation
//   v2_fsm_walk.go             — S8: the walk endpoint and its atomic transaction
//
// This file is intentionally minimal: it exists to document the layout of the
// FSM handler set. handleFSMMachineWalk now lives in v2_fsm_walk.go (S8); it
// previously returned 501 here as the S7 stub.
