// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"strings"
	"testing"

	"github.com/ha1tch/tsqlparser"
)

func validateSQL(t *testing.T, v *Validator, sql string) error {
	t.Helper()
	program, _ := tsqlparser.Parse(sql)
	if len(program.Statements) != 1 {
		t.Fatalf("%q: expected one statement, got %d", sql, len(program.Statements))
	}
	return v.Validate(program.Statements[0])
}

// XOT241: by default a type nothing was written to is XOLU-QL004, as before.
func TestUnknownEntity_DefaultIsError(t *testing.T) {
	v := NewValidator(t.TempDir())
	err := validateSQL(t, v, "SELECT id FROM ghost")
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v, want \"does not exist\"", err)
	}
}

// In the "empty" mode the same statements validate, because types are implicit
// and "does not exist" is "has no rows". INSERT keeps its check, and a name that
// is not a legal entity name is still refused.
func TestUnknownEntity_EmptyMode(t *testing.T) {
	v := NewValidator(t.TempDir())
	v.SetUnknownEntitiesEmpty(true)

	for _, sql := range []string{
		"SELECT id FROM ghost",
		"SELECT id FROM ghost WHERE id = 1",
		"UPDATE ghost SET a = 1 WHERE id = 1",
		"DELETE FROM ghost WHERE id = 1",
	} {
		if err := validateSQL(t, v, sql); err != nil {
			t.Errorf("%q: %v", sql, err)
		}
	}
	if err := validateSQL(t, v, "INSERT INTO ghost (a) VALUES (1)"); err == nil {
		t.Error("INSERT into an unknown type must still be refused")
	}
	if err := validateSQL(t, v, "SELECT id FROM [not-a-name]"); err == nil {
		t.Error("a name that is not a legal entity name must still be refused")
	}
}
