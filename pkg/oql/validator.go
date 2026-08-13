// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ha1tch/tsqlparser/ast"
)

// EntityChecker is an interface for checking entity existence
type EntityChecker interface {
	ListEntities(ctx context.Context) ([]string, error)
}

// Validator validates OQL queries against schema
type Validator struct {
	schemaDir     string
	entities      map[string]bool // Cached entity names
	entityChecker EntityChecker   // Optional store-based checker
}

// NewValidator creates a new validator that checks the filesystem
func NewValidator(schemaDir string) *Validator {
	v := &Validator{
		schemaDir: schemaDir,
		entities:  make(map[string]bool),
	}
	v.loadEntitiesFromDisk()
	return v
}

// NewValidatorWithStore creates a validator that checks the store for entities
func NewValidatorWithStore(schemaDir string, checker EntityChecker) *Validator {
	v := &Validator{
		schemaDir:     schemaDir,
		entities:      make(map[string]bool),
		entityChecker: checker,
	}
	v.RefreshEntities()
	return v
}

// loadEntitiesFromDisk scans the schema directory for entity folders
// and schema JSON files (e.g. "author.json" -> entity "author").
func (v *Validator) loadEntitiesFromDisk() {
	entries, err := os.ReadDir(v.schemaDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			v.entities[entry.Name()] = true
		} else if filepath.Ext(entry.Name()) == ".json" {
			name := entry.Name()[:len(entry.Name())-5]
			if name != "" {
				v.entities[name] = true
			}
		}
	}
}

// loadEntitiesFromStore queries the store for entity types
func (v *Validator) loadEntitiesFromStore() {
	if v.entityChecker == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	entities, err := v.entityChecker.ListEntities(ctx)
	if err != nil {
		return
	}

	for _, entity := range entities {
		v.entities[entity] = true
	}
}

// RefreshEntities reloads the entity list.
// If a store checker is configured, it queries the store.
// Otherwise, it scans the filesystem.
func (v *Validator) RefreshEntities() {
	v.entities = make(map[string]bool)
	if v.entityChecker != nil {
		v.loadEntitiesFromStore()
	}
	// Always also check disk for schema definitions
	v.loadEntitiesFromDisk()
}

// EntityExists checks if an entity exists.
// If the entity is not in the cache, it automatically refreshes
// before returning false. This ensures newly created entity types are
// recognised without requiring manual refresh.
func (v *Validator) EntityExists(name string) bool {
	// Normalize name (lowercase, remove brackets/quotes)
	name = normalizeEntityName(name)

	// Check cache first
	if v.entities[name] {
		return true
	}

	// Entity not in cache - refresh and retry
	// This handles dynamically created entity types
	v.RefreshEntities()
	return v.entities[name]
}

// Validate validates an AST statement
// maxDerivedTableDepth caps how deeply a derived table (subquery in
// FROM) may nest inside another derived table. Enforced once, upfront,
// at the top-level Validate entry point rather than threaded through
// every recursive validateSelect/validateDerivedTable call -- a single
// AST walk before validation begins is simpler and avoids either
// changing validateSelect's own signature everywhere it's called, or
// adding mutable per-Validator state that would be a concurrency
// hazard if a Validator instance is ever shared across requests. Ten
// levels is generous for any legitimate query and cheap to check.
const maxDerivedTableDepth = 10

// derivedTableDepth returns the maximum derived-table nesting depth
// anywhere in a statement tree: the FROM clause itself, and every
// UNION/INTERSECT/EXCEPT branch (a derived table could appear inside
// any one of them too).
func derivedTableDepth(s *ast.SelectStatement) int {
	if s == nil {
		return 0
	}
	depth := 0
	if s.From != nil && len(s.From.Tables) > 0 {
		if dt, ok := s.From.Tables[0].(*ast.DerivedTable); ok && dt.Subquery != nil {
			depth = 1 + derivedTableDepth(dt.Subquery)
		}
	}
	if s.Union != nil && s.Union.Right != nil {
		if d := derivedTableDepth(s.Union.Right); d > depth {
			depth = d
		}
	}
	return depth
}

func (v *Validator) Validate(stmt ast.Statement) error {
	if s, ok := stmt.(*ast.SelectStatement); ok {
		if d := derivedTableDepth(s); d > maxDerivedTableDepth {
			return fmt.Errorf("derived table nesting too deep: %d levels (max %d)", d, maxDerivedTableDepth)
		}
	}
	switch s := stmt.(type) {
	case *ast.SelectStatement:
		return v.validateSelect(s)
	case *ast.InsertStatement:
		return v.validateInsert(s)
	case *ast.UpdateStatement:
		return v.validateUpdate(s)
	case *ast.DeleteStatement:
		return v.validateDelete(s)
	default:
		return fmt.Errorf("unsupported statement type: %T", stmt)
	}
}

func (v *Validator) validateSelect(s *ast.SelectStatement) error {
	if s.From == nil {
		return fmt.Errorf("FROM clause required")
	}

	// UNION/INTERSECT/EXCEPT (2026-08-12): each branch is independently
	// validated by recursing into this same function on s.Union.Right,
	// which itself validates its own s.Union.Right, and so on down the
	// chain -- validateSelect's own "if s.Union != nil" check at the top
	// makes this recursion correct without any special handling. Two
	// deliberate restrictions, not full SQL semantics: every link in the
	// chain must use the identical operator (a mixed "A UNION B
	// INTERSECT C" is rejected outright, avoiding SQL's own real
	// precedence rules for mixed set operators -- getting that subtly
	// wrong would silently combine rows incorrectly rather than fail
	// loudly, so it's rejected instead of guessed at); every branch must
	// select the same number of columns (the standard SQL requirement,
	// and the only way the row-combining step below can meaningfully
	// treat two branches' own rows as comparable at all).
	if s.Union != nil {
		if err := v.validateUnionChain(s); err != nil {
			return err
		}
	}

	if len(s.From.Tables) != 1 {
		return fmt.Errorf("OQL supports single table queries only")
	}

	switch ref := s.From.Tables[0].(type) {
	case *ast.TableName:
		// Single-table SELECT — existing path.
		entity := normalizeEntityName(ref.Name.String())
		if !v.EntityExists(entity) {
			return fmt.Errorf("entity '%s' does not exist", entity)
		}

	case *ast.JoinClause:
		// Two-table JOIN SELECT.
		if err := v.validateJoinClause(ref); err != nil {
			return err
		}

	case *ast.DerivedTable:
		// Subquery in FROM: SELECT ... FROM (SELECT ...) AS alias.
		if err := v.validateDerivedTable(ref); err != nil {
			return err
		}

	default:
		return fmt.Errorf("invalid table reference")
	}

	// Validate columns reference valid fields (optional - could defer to runtime)

	return nil
}

// validateUnionChain validates a UNION/INTERSECT/EXCEPT chain starting at
// s (s.Union is already confirmed non-nil by the caller). Every link in
// the chain must use the identical operator type and every branch must
// select the same number of columns; each right-hand branch is validated
// independently as a full, standalone SELECT via validateSelect, which
// naturally recurses down the rest of the chain since it checks its own
// s.Union != nil at the top.
func (v *Validator) validateUnionChain(s *ast.SelectStatement) error {
	opType := s.Union.Type
	if opType == "" {
		opType = "UNION"
	}
	if hasWildcardColumn(s.Columns) {
		return fmt.Errorf("SELECT * is not supported in a %s chain -- name columns explicitly", opType)
	}
	wantCols := len(s.Columns)

	cur := s
	for cur.Union != nil {
		linkType := cur.Union.Type
		if linkType == "" {
			linkType = "UNION"
		}
		if linkType != opType {
			return fmt.Errorf("mixed set operators are not supported: found both %s and %s in the same chain", opType, linkType)
		}
		// INTERSECT ALL / EXCEPT ALL: not valid T-SQL at all (SQL Server
		// only supports UNION ALL), but tsqlparser accepts it
		// syntactically -- confirmed directly, not assumed. Rejected
		// deliberately rather than implemented: correct ALL semantics for
		// these two require genuine multiset counting (INTERSECT ALL keeps
		// the minimum occurrence count of each duplicate value across both
		// sides, not just set membership), a real correctness trap to get
		// subtly wrong, not a formality to wave through.
		if cur.Union.All && linkType != "UNION" {
			return fmt.Errorf("%s ALL is not supported (not valid T-SQL; use %s without ALL)", linkType, linkType)
		}
		right := cur.Union.Right
		if right == nil {
			return fmt.Errorf("%s requires a right-hand SELECT", linkType)
		}
		if hasWildcardColumn(right.Columns) {
			return fmt.Errorf("SELECT * is not supported in a %s chain -- name columns explicitly", linkType)
		}
		if len(right.Columns) != wantCols {
			return fmt.Errorf("%s requires both sides to select the same number of columns (left has %d, right has %d)",
				linkType, wantCols, len(right.Columns))
		}
		if err := v.validateSelect(right); err != nil {
			return fmt.Errorf("%s right-hand SELECT: %w", linkType, err)
		}
		cur = right
	}
	return nil
}

// hasWildcardColumn reports whether any column in a SELECT list is a
// bare `*`. Needed specifically for UNION/INTERSECT/EXCEPT validation
// (2026-08-13, found by direct adversarial testing, not inferred from
// reading the AST alone): SelectColumn.AllColumns collapses `SELECT *`
// to a single AST entry regardless of how many real columns the
// underlying entity actually has, so validateUnionChain's own "same
// number of columns" check -- comparing len(s.Columns) across branches
// -- is meaningless for a wildcard select: `SELECT * FROM wide UNION
// SELECT * FROM narrow` reports len==1 on both sides and passes
// validation even when the two entities have entirely different real
// widths, producing rows with inconsistent key sets in the combined
// result rather than a clean rejection. Confirmed directly against a
// real server before this fix existed, not assumed.
func hasWildcardColumn(cols []ast.SelectColumn) bool {
	for _, c := range cols {
		if c.AllColumns {
			return true
		}
	}
	return false
}

// validateDerivedTable validates a subquery in a FROM clause:
// SELECT ... FROM (SELECT ...) AS alias. Executed via full recursion
// into executeSelect (pkg/oql/executor.go's own executeDerivedTable),
// so the inner subquery gets full, independent validation here too --
// tenant scoping, decimal handling, adapted/blob resolution are all
// inherited from the ordinary query path, not reimplemented.
//
// Two deliberate restrictions: an explicit alias is required (SQLite
// itself is lenient about this, but requiring it keeps every outer
// column reference unambiguous); AS alias(col1, col2) -- renaming the
// derived table's own output columns positionally -- is rejected
// outright rather than implemented. The inner query's own result rows
// are Go maps with no defined key order, so honouring a positional
// column-alias list would require tracking the inner SELECT list's
// own column order separately and re-keying every row by position --
// solvable, but real, additional complexity for a rarely-used SQL
// feature; rejected rather than guessed at, matching this codebase's
// own established pattern for restrictions of this kind (e.g.
// INTERSECT ALL/EXCEPT ALL in validateUnionChain).
func (v *Validator) validateDerivedTable(dt *ast.DerivedTable) error {
	if dt.Alias == nil || dt.Alias.Value == "" {
		return fmt.Errorf("a derived table (subquery in FROM) requires an explicit alias: FROM (SELECT ...) AS alias")
	}
	if len(dt.ColumnAliases) > 0 {
		return fmt.Errorf("a derived table's own column alias list (AS %s(col1, col2, ...)) is not supported -- alias individual columns in the inner SELECT instead", dt.Alias.Value)
	}
	if dt.Subquery == nil {
		return fmt.Errorf("a derived table requires a subquery")
	}
	if err := v.validateSelect(dt.Subquery); err != nil {
		return fmt.Errorf("derived table subquery: %w", err)
	}
	return nil
}

// validateJoinClause validates a two-table JOIN in a SELECT FROM clause.
// Both sides must be plain TableName references (no subqueries or derived
// tables), each referencing a known entity. The ON condition is required
// for all join types except CROSS JOIN; CROSS JOIN itself is rejected.
func (v *Validator) validateJoinClause(jc *ast.JoinClause) error {
	if jc.Type == "CROSS" || jc.Type == "CROSS APPLY" || jc.Type == "OUTER APPLY" {
		return fmt.Errorf("CROSS JOIN and APPLY are not supported; use INNER, LEFT, RIGHT, or FULL JOIN")
	}

	leftTable, ok := jc.Left.(*ast.TableName)
	if !ok {
		return fmt.Errorf("JOIN left side must be a plain table reference, not a subquery or derived table")
	}
	rightTable, ok := jc.Right.(*ast.TableName)
	if !ok {
		return fmt.Errorf("JOIN right side must be a plain table reference, not a subquery or derived table")
	}
	if leftTable.Name == nil || leftTable.Name.String() == "" {
		return fmt.Errorf("JOIN left table must have a name")
	}
	if rightTable.Name == nil || rightTable.Name.String() == "" {
		return fmt.Errorf("JOIN right table must have a name")
	}

	leftEntity := normalizeEntityName(leftTable.Name.String())
	if !v.EntityExists(leftEntity) {
		return fmt.Errorf("entity '%s' does not exist", leftEntity)
	}
	rightEntity := normalizeEntityName(rightTable.Name.String())
	if !v.EntityExists(rightEntity) {
		return fmt.Errorf("entity '%s' does not exist", rightEntity)
	}

	if jc.Condition == nil {
		return fmt.Errorf("JOIN requires an ON condition")
	}

	return nil
}

func (v *Validator) validateInsert(s *ast.InsertStatement) error {
	if s.Table == nil {
		return fmt.Errorf("table name required")
	}

	entity := normalizeEntityName(s.Table.String())
	if !v.EntityExists(entity) {
		return fmt.Errorf("entity '%s' does not exist", entity)
	}

	// Must have values
	if len(s.Values) == 0 && s.Select == nil {
		return fmt.Errorf("INSERT requires VALUES or SELECT")
	}

	// INSERT ... SELECT not supported for now
	if s.Select != nil {
		return fmt.Errorf("INSERT ... SELECT is not supported")
	}

	// Validate column count matches value count
	colCount := len(s.Columns)
	for i, row := range s.Values {
		if colCount > 0 && len(row) != colCount {
			return fmt.Errorf("row %d: column count (%d) does not match value count (%d)",
				i+1, colCount, len(row))
		}
	}

	return nil
}

func (v *Validator) validateUpdate(s *ast.UpdateStatement) error {
	// WHERE is required
	if s.Where == nil {
		return fmt.Errorf("UPDATE without WHERE clause is not permitted")
	}

	if s.Table == nil {
		return fmt.Errorf("table name required")
	}

	entity := normalizeEntityName(s.Table.String())
	if !v.EntityExists(entity) {
		return fmt.Errorf("entity '%s' does not exist", entity)
	}

	// Must have at least one SET clause
	if len(s.SetClauses) == 0 {
		return fmt.Errorf("UPDATE requires at least one SET clause")
	}

	// FROM clause in UPDATE not supported (T-SQL extension)
	if s.From != nil {
		return fmt.Errorf("UPDATE ... FROM is not supported")
	}

	return nil
}

func (v *Validator) validateDelete(s *ast.DeleteStatement) error {
	// WHERE is required
	if s.Where == nil {
		return fmt.Errorf("DELETE without WHERE clause is not permitted")
	}

	// Extract entity name
	entity := extractDeleteEntity(s)
	if entity == "" {
		return fmt.Errorf("table name required")
	}

	entity = normalizeEntityName(entity)
	if !v.EntityExists(entity) {
		return fmt.Errorf("entity '%s' does not exist", entity)
	}

	return nil
}

// normalizeEntityName cleans up an entity name
func normalizeEntityName(name string) string {
	// Remove schema prefix (dbo., etc.)
	if idx := strings.LastIndex(name, "."); idx != -1 {
		name = name[idx+1:]
	}

	// Remove brackets [name]
	name = strings.TrimPrefix(name, "[")
	name = strings.TrimSuffix(name, "]")

	// Remove quotes "name"
	name = strings.Trim(name, "\"")

	// Lowercase
	return strings.ToLower(name)
}

// extractDeleteEntity gets the entity name from a DELETE statement
func extractDeleteEntity(s *ast.DeleteStatement) string {
	if s.Table != nil {
		return s.Table.String()
	}
	if s.From != nil && len(s.From.Tables) > 0 {
		if tn, ok := s.From.Tables[0].(*ast.TableName); ok {
			return tn.Name.String()
		}
	}
	return ""
}

// ValidateSchemaDir checks if the schema directory exists
func ValidateSchemaDir(schemaDir string) error {
	info, err := os.Stat(schemaDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("schema directory does not exist: %s", schemaDir)
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("schema path is not a directory: %s", schemaDir)
	}
	return nil
}

// GetSchemaPath returns the full path to an entity's schema
func GetSchemaPath(schemaDir, entity string) string {
	return filepath.Join(schemaDir, entity)
}
