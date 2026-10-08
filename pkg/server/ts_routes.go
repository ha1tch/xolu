// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

import "github.com/go-chi/chi/v5"

// mountTSRoutes registers the /ts routes on a router. It serves the tenant mount
// (/api/v1/tenant/{tenant_id}/ts) and the default-store mount (/api/v2/ts, tenant 0,
// XOT239); the handlers tell them apart by whether the tenant_id URL parameter is empty.
func (s *Server) mountTSRoutes(tsr chi.Router) {
	tsr.Post("/provision", s.HandleTSProvision)
	// Timeline management
	// DEPRECATED plural "timelines" shape — commented out in
	// favour of the "tl/def" / "tl/list" / "tl/{id}" form below.
	// Restore from the v0.13.1 baseline if these must come back.
	// tsr.Post("/timelines", s.HandleTSDefineTimeline)
	// tsr.Get("/timelines", s.HandleTSListTimelines)
	// tsr.Get("/timelines/{timeline_id}", s.HandleTSGetTimeline)
	// tsr.Patch("/timelines/{timeline_id}", s.HandleTSUpdateTimeline)
	// tsr.Get("/timelines/{timeline_id}/sync", s.HandleTSSyncGet)
	// tsr.Post("/timelines/{timeline_id}/sync/on", s.HandleTSSyncOn)
	// tsr.Post("/timelines/{timeline_id}/sync/off", s.HandleTSSyncOff)
	tsr.Post("/tl/def", s.HandleTSDefineTimeline)
	tsr.Get("/tl/list", s.HandleTSListTimelines)
	tsr.Get("/tl/{timeline_id}", s.HandleTSGetTimeline)
	tsr.Patch("/tl/{timeline_id}", s.HandleTSUpdateTimeline)
	tsr.Delete("/tl/{timeline_id}", s.HandleTSDeleteTimeline)
	tsr.Get("/tl/{timeline_id}/sync", s.HandleTSSyncGet)
	tsr.Post("/tl/{timeline_id}/sync/on", s.HandleTSSyncOn)
	tsr.Post("/tl/{timeline_id}/sync/off", s.HandleTSSyncOff)
	// Events
	tsr.Post("/events", s.HandleTSAppend)
	tsr.Post("/events/batch", s.HandleTSBatchAppend)
	tsr.Delete("/events", s.HandleTSDeleteEvent)
	tsr.Get("/events", s.HandleTSQueryRange)
	tsr.Post("/query/range", s.HandleTSQueryRangePost)
	tsr.Get("/events/latest", s.HandleTSLatest)
	// Aggregate
	tsr.Post("/aggregate", s.HandleTSAggregate)
	tsr.Post("/range_aggregate", s.HandleTSRangeAggregate)
	tsr.Post("/full_aggregate", s.HandleTSFullAggregate)
	// Rollup management
	// DEPRECATED plural "timelines" shape — see note above.
	// tsr.Post("/timelines/{timeline_id}/rollup/def", s.HandleTSDefineRollup)
	// tsr.Get("/timelines/{timeline_id}/rollup/list", s.HandleTSListRollups)
	// tsr.Get("/timelines/{timeline_id}/rollup/parent", s.HandleTSRollupParent)
	// tsr.Get("/timelines/{timeline_id}/rollup/{rollup_id}", s.HandleTSGetRollup)
	// tsr.Delete("/timelines/{timeline_id}/rollup/{rollup_id}", s.HandleTSDeleteRollup)
	// tsr.Post("/timelines/{timeline_id}/rollup/{rollup_id}/run", s.HandleTSRunRollup)
	// tsr.Get("/timelines/{timeline_id}/rollup/{rollup_id}/status", s.HandleTSRollupStatus)
	tsr.Post("/tl/{timeline_id}/rollup/def", s.HandleTSDefineRollup)
	tsr.Get("/tl/{timeline_id}/rollup/list", s.HandleTSListRollups)
	tsr.Get("/tl/{timeline_id}/rollup/parent", s.HandleTSRollupParent)
	tsr.Get("/tl/{timeline_id}/rollup/{rollup_id}", s.HandleTSGetRollup)
	tsr.Delete("/tl/{timeline_id}/rollup/{rollup_id}", s.HandleTSDeleteRollup)
	tsr.Post("/tl/{timeline_id}/rollup/{rollup_id}/run", s.HandleTSRunRollup)
	tsr.Get("/tl/{timeline_id}/rollup/{rollup_id}/status", s.HandleTSRollupStatus)
	tsr.Get("/rollup/tree", s.HandleTSRollupTree)
	// Timeline data deletion
	// DEPRECATED plural "timelines" shape — see note above.
	// tsr.Delete("/timelines/{timeline_id}/data", s.HandleTSDeleteTimelineData)
	// tsr.Post("/timelines/{timeline_id}/data/purge", s.HandleTSPurgeTimelineRange)
	tsr.Delete("/tl/{timeline_id}/data", s.HandleTSDeleteTimelineData)
	tsr.Post("/tl/{timeline_id}/data/purge", s.HandleTSPurgeTimelineRange)
	// Retention + stats
	tsr.Get("/retention", s.HandleTSGetRetention)
	tsr.Patch("/retention", s.HandleTSPatchRetention)
	tsr.Get("/stats", s.HandleTSStats)
	tsr.Get("/stats/{timeline_id}", s.HandleTSTimelineStats)
}
