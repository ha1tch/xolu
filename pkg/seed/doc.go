// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package seed reads .seed files and loads them into xolu.
//
// A .seed file is a list of records, one JSON object each, after a header:
// the schemas, entities, definitions and calls that make up a dataset. The
// format is specified in docs/SEED_FORMAT.md. This package is the format and
// its semantics as a library: the command cmd/xoseed is a thin front to it,
// and any program that holds a connection to an instance can load, check and
// dump with the same rules.
//
// The pieces, in the order a load uses them:
//
//	header, records, digest, err := seed.ReadSeed(path, false, nil)
//	c, err := seed.NewClient(baseURL, apiKey, token, 30*time.Second)   // or seed.NewClientFor(existingClient, baseURL)
//	loader, err := seed.NewLoader(c, []seed.LoadFile{{Path: path, Header: header, Digest: digest}}, seed.Options{})
//	err = loader.Inspect()                                 // what the tenant holds
//	ctx := seed.NewContext(loader.AsOf(time.Time{}), nil)  // the reference time of the load
//	plan, err := seed.BuildPlan([]seed.SeedFile{{Path: path, Header: header, Records: records}}, ctx, seed.PlanOptions{})
//	result, err := loader.Run(plan, ctx)
//
// ReadSeed, BuildPlan and the Context talk to nothing: a plan can be built
// and shown with no instance at hand. Check compares a plan with an instance
// and writes nothing; Dump writes what a tenant holds as a .seed file.
//
// Two kinds of error are told apart from the rest: *Error is bad input (a
// file, an option, an answer that cannot be used), and *Refused is a guard
// that stopped a load before anything was written.
//
// The reference implementation of the format is the Python loader in
// scripts/seed_system. The two write the same requests for the same files,
// give a record the same key, and keep the same load record in the tenant, so
// a load started by one is continued by the other. Values are kept in a model
// of their own (value.go) for that reason: the key of a record is a digest of
// its text, and the text has to be the same in both.
package seed
