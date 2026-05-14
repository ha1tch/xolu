// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

// JOINs are supported for SELECT queries when both tables are entity types
// in the same SQLite store. INNER, LEFT, RIGHT, and FULL OUTER JOIN types are
// supported and pushed to SQLite. CROSS JOIN is rejected by the validator.
//
// Result rows are flat maps of column alias → value, exactly as for any
// other pushed query. When both entities are blob-stored, each alias holds
// the raw JSON blob for that entity; the caller is responsible for unpacking.

import (
	"fmt"
	"strings"

	"github.com/ha1tch/tsqlparser/ast"
	"github.com/ha1tch/xolu/pkg/storage"
)

// JoinSQL holds the output of GenerateJoinSQL: a complete two-table SELECT
// statement and the metadata needed to map result rows back to OQL records.
type JoinSQL struct {
	SQL     string
	Args    []interface{}
	Aliases []string // Result column aliases in SELECT order
}

// GenerateJoinSQL translates a two-table OQL SELECT + JOIN into a single SQL
// statement. The plan must contain a non-nil Join field (set by planJoin).
//
// SQL shape depends on whether each entity is adapted or blob-stored:
//
//   Both adapted:    SELECT a.<col>, b.<col> FROM <left> a JOIN <right> b ON ...
//   Both blob:       SELECT a.data, b.data   FROM entities a JOIN entities b ON ...
//   Mixed:           SELECT a.<col>, json_extract(b.data, '$.x') FROM <left> a JOIN entities b ON ...
//
// All field accesses use dialect methods — no literal json_extract strings.
// All placeholders use dialect.Placeholder(n) — no literal ? or $N.
func GenerateJoinSQL(
	stmt *ast.SelectStatement,
	plan QueryPlan,
	tenantID string,
	store storage.AggregateQueryable,
	dialect SQLDialect,
) (*JoinSQL, error) {
	js := plan.Join
	if js == nil {
		return nil, fmt.Errorf("GenerateJoinSQL: plan has no join spec")
	}

	var args []interface{}
	argIdx := 0
	addArg := func(val interface{}) string {
		args = append(args, val)
		argIdx++
		return dialect.Placeholder(argIdx)
	}

	// -- Resolve table names for each side --
	leftTable, rightTable, err := resolveJoinTableNames(js, plan, store)
	if err != nil {
		return nil, err
	}

	// -- SELECT columns --
	selectExprs, aliases, err := generateJoinSelectColumns(stmt, js, plan, store, dialect, addArg)
	if err != nil {
		return nil, err
	}

	// -- FROM / JOIN --
	joinKeyword := js.JoinType
	if joinKeyword == "FULL" {
		joinKeyword = "FULL OUTER"
	}
	fromClause := fmt.Sprintf(
		"%s %s\n  %s JOIN %s %s",
		leftTable, js.LeftAlias,
		joinKeyword,
		rightTable, js.RightAlias,
	)

	// -- ON condition --
	onSQL, err := generateJoinOnClause(js.Condition, js, plan, store, dialect)
	if err != nil {
		return nil, fmt.Errorf("ON: %w", err)
	}

	// -- WHERE (entity-type scoping + tenant + user WHERE) --
	var whereParts []string

	// entity_type filters for blob sides
	if !plan.LeftAdapted {
		whereParts = append(whereParts,
			fmt.Sprintf("%s.entity_type = %s", js.LeftAlias, addArg(js.LeftEntity)))
	}
	if !plan.RightAdapted {
		whereParts = append(whereParts,
			fmt.Sprintf("%s.entity_type = %s", js.RightAlias, addArg(js.RightEntity)))
	}

	// tenant scoping
	if tenantID != "" {
		if !plan.LeftAdapted {
			whereParts = append(whereParts,
				fmt.Sprintf("%s.tenant_id = %s", js.LeftAlias, addArg(tenantID)))
		} else {
			whereParts = append(whereParts,
				fmt.Sprintf("%s.tenant_id = %s", js.LeftAlias, addArg(tenantID)))
		}
		if !plan.RightAdapted {
			whereParts = append(whereParts,
				fmt.Sprintf("%s.tenant_id = %s", js.RightAlias, addArg(tenantID)))
		} else {
			whereParts = append(whereParts,
				fmt.Sprintf("%s.tenant_id = %s", js.RightAlias, addArg(tenantID)))
		}
	}

	// user-supplied WHERE
	if stmt.Where != nil {
		userWhere, wErr := generateJoinWhereClause(stmt.Where, js, plan, store, dialect, addArg)
		if wErr != nil {
			return nil, fmt.Errorf("WHERE: %w", wErr)
		}
		whereParts = append(whereParts, "("+userWhere+")")
	}

	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = "\nWHERE " + strings.Join(whereParts, "\n  AND ")
	}

	sql := fmt.Sprintf(
		"SELECT %s\nFROM %s\n  ON %s%s",
		strings.Join(selectExprs, ", "),
		fromClause,
		onSQL,
		whereClause,
	)

	return &JoinSQL{
		SQL:     sql,
		Args:    args,
		Aliases: aliases,
	}, nil
}

// resolveJoinTableNames returns the SQL table name for each side of the join.
// Adapted entities use their adapted table name; blob entities use "entities".
func resolveJoinTableNames(
	js *joinSpec,
	plan QueryPlan,
	store storage.AggregateQueryable,
) (leftTable, rightTable string, err error) {
	if plan.LeftAdapted {
		name, ok := store.AdaptedTableName(js.LeftEntity)
		if !ok {
			return "", "", fmt.Errorf("adapted table not found for entity %q", js.LeftEntity)
		}
		leftTable = name
	} else {
		leftTable = "entities"
	}

	if plan.RightAdapted {
		name, ok := store.AdaptedTableName(js.RightEntity)
		if !ok {
			return "", "", fmt.Errorf("adapted table not found for entity %q", js.RightEntity)
		}
		rightTable = name
	} else {
		rightTable = "entities"
	}
	return leftTable, rightTable, nil
}

// generateJoinSelectColumns builds the SELECT expression list and alias list
// for the JOIN query. Each column is resolved using the join aliases to
// determine which entity it belongs to, then emitted as native column or
// json_extract depending on whether that entity is adapted.
func generateJoinSelectColumns(
	stmt *ast.SelectStatement,
	js *joinSpec,
	plan QueryPlan,
	store storage.AggregateQueryable,
	dialect SQLDialect,
	addArg func(interface{}) string,
) (exprs []string, aliases []string, err error) {
	for _, col := range stmt.Columns {
		alias := joinColumnAlias(col)

		sqlExpr, genErr := generateJoinColumnExpr(col.Expression, js, plan, store, dialect, addArg)
		if genErr != nil {
			return nil, nil, fmt.Errorf("SELECT column %q: %w", alias, genErr)
		}
		exprs = append(exprs, sqlExpr+" AS "+alias)
		aliases = append(aliases, alias)
	}
	return exprs, aliases, nil
}

// joinColumnAlias returns the result-set alias for a SELECT column in a JOIN
// query. When no explicit AS alias is given and the expression is a qualified
// identifier (alias.field), the alias is the field name alone — dotted names
// are not valid SQL column aliases and would cause a syntax error.
func joinColumnAlias(col ast.SelectColumn) string {
	if col.Alias != nil && col.Alias.Value != "" {
		return col.Alias.Value
	}
	// For qualified identifiers like "a.title", use just "title".
	if qi, ok := col.Expression.(*ast.QualifiedIdentifier); ok {
		return qualifiedField(qi)
	}
	return columnAlias(col)
}

// generateJoinColumnExpr translates a single SELECT column expression in the
// context of a join. QualifiedIdentifiers (alias.field) are resolved by alias
// to the correct entity side; plain Identifiers are unqualified and produce an
// error (all join columns must be alias-qualified to avoid ambiguity).
func generateJoinColumnExpr(
	expr ast.Expression,
	js *joinSpec,
	plan QueryPlan,
	store storage.AggregateQueryable,
	dialect SQLDialect,
	addArg func(interface{}) string,
) (string, error) {
	switch e := expr.(type) {
	case *ast.QualifiedIdentifier:
		tbl := qualifiedTable(e)
		field := qualifiedField(e)
		return joinFieldRef(tbl, field, js, plan, store, dialect)

	case *ast.Identifier:
		// Unqualified column — try to resolve by checking both sides
		// (only safe if the field exists on exactly one side)
		leftSQL, leftErr := joinFieldRef(js.LeftAlias, e.Value, js, plan, store, dialect)
		rightSQL, rightErr := joinFieldRef(js.RightAlias, e.Value, js, plan, store, dialect)
		switch {
		case leftErr == nil && rightErr != nil:
			return leftSQL, nil
		case leftErr != nil && rightErr == nil:
			return rightSQL, nil
		case leftErr == nil && rightErr == nil:
			return "", fmt.Errorf("ambiguous column %q: exists in both %s and %s; use alias.field",
				e.Value, js.LeftAlias, js.RightAlias)
		default:
			return "", fmt.Errorf("column %q not found in either join entity", e.Value)
		}

	case *ast.IntegerLiteral:
		return addArg(e.Value), nil
	case *ast.FloatLiteral:
		return addArg(e.Value), nil
	case *ast.StringLiteral:
		return addArg(e.Value), nil
	case *ast.NullLiteral:
		return "NULL", nil

	default:
		return "", fmt.Errorf("unsupported expression type in JOIN SELECT: %T", expr)
	}
}

// joinFieldRef returns the SQL expression for accessing alias.field, using
// the native column name for adapted entities or dialect.JSONField for blobs.
func joinFieldRef(
	tableAlias, field string,
	js *joinSpec,
	plan QueryPlan,
	store storage.AggregateQueryable,
	dialect SQLDialect,
) (string, error) {
	switch tableAlias {
	case js.LeftAlias:
		if plan.LeftAdapted {
			colName := adaptedNativeColumn(js.LeftEntity, field, store)
			if colName == "" {
				return "", fmt.Errorf("field %q not found in adapted entity %q", field, js.LeftEntity)
			}
			return js.LeftAlias + "." + colName, nil
		}
		// Blob path: alias qualifies the data column inside the extraction expression.
		return dialect.JSONFieldAliased(js.LeftAlias, field), nil

	case js.RightAlias:
		if plan.RightAdapted {
			colName := adaptedNativeColumn(js.RightEntity, field, store)
			if colName == "" {
				return "", fmt.Errorf("field %q not found in adapted entity %q", field, js.RightEntity)
			}
			return js.RightAlias + "." + colName, nil
		}
		// Blob path: alias qualifies the data column inside the extraction expression.
		return dialect.JSONFieldAliased(js.RightAlias, field), nil

	default:
		return "", fmt.Errorf("unknown table alias %q (expected %s or %s)",
			tableAlias, js.LeftAlias, js.RightAlias)
	}
}

// generateJoinOnClause translates the ON condition expression into SQL.
// Both sides must be QualifiedIdentifiers (validated by extractJoinSpec).
func generateJoinOnClause(
	cond *ast.InfixExpression,
	js *joinSpec,
	plan QueryPlan,
	store storage.AggregateQueryable,
	dialect SQLDialect,
) (string, error) {
	lqi, ok := cond.Left.(*ast.QualifiedIdentifier)
	if !ok {
		return "", fmt.Errorf("ON left side must be a qualified identifier")
	}
	rqi, ok := cond.Right.(*ast.QualifiedIdentifier)
	if !ok {
		return "", fmt.Errorf("ON right side must be a qualified identifier")
	}

	leftSQL, err := joinFieldRef(qualifiedTable(lqi), qualifiedField(lqi), js, plan, store, dialect)
	if err != nil {
		return "", fmt.Errorf("ON left: %w", err)
	}
	rightSQL, err := joinFieldRef(qualifiedTable(rqi), qualifiedField(rqi), js, plan, store, dialect)
	if err != nil {
		return "", fmt.Errorf("ON right: %w", err)
	}

	return fmt.Sprintf("%s = %s", leftSQL, rightSQL), nil
}

// generateJoinWhereClause translates the WHERE expression tree for a join
// query. QualifiedIdentifiers (alias.field) are resolved to the correct
// entity using joinFieldRef; plain Identifiers fall back to the same
// unqualified resolution as in generateJoinColumnExpr.
func generateJoinWhereClause(
	expr ast.Expression,
	js *joinSpec,
	plan QueryPlan,
	store storage.AggregateQueryable,
	dialect SQLDialect,
	addArg func(interface{}) string,
) (string, error) {
	var translateField func(ast.Expression) (string, error)
	translateField = func(e ast.Expression) (string, error) {
		switch f := e.(type) {
		case *ast.QualifiedIdentifier:
			return joinFieldRef(qualifiedTable(f), qualifiedField(f), js, plan, store, dialect)
		case *ast.Identifier:
			leftSQL, leftErr := joinFieldRef(js.LeftAlias, f.Value, js, plan, store, dialect)
			rightSQL, rightErr := joinFieldRef(js.RightAlias, f.Value, js, plan, store, dialect)
			switch {
			case leftErr == nil && rightErr != nil:
				return leftSQL, nil
			case leftErr != nil && rightErr == nil:
				return rightSQL, nil
			case leftErr == nil && rightErr == nil:
				return "", fmt.Errorf("ambiguous column %q in WHERE: use alias.field", f.Value)
			default:
				return "", fmt.Errorf("column %q not found in either join entity", f.Value)
			}
		default:
			return "", fmt.Errorf("unsupported field expression type in WHERE: %T", e)
		}
	}

	var translate func(ast.Expression) (string, error)
	translate = func(e ast.Expression) (string, error) {
		switch ex := e.(type) {
		case *ast.InfixExpression:
			switch ex.Operator {
			case "AND", "OR":
				left, err := translate(ex.Left)
				if err != nil {
					return "", err
				}
				right, err := translate(ex.Right)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("(%s %s %s)", left, ex.Operator, right), nil
			default:
				fieldSQL, err := translateField(ex.Left)
				if err != nil {
					return "", err
				}
				val, err := joinLiteralValue(ex.Right)
				if err != nil {
					return "", fmt.Errorf("WHERE right side: %w", err)
				}
				return fmt.Sprintf("%s %s %s", fieldSQL, ex.Operator, addArg(val)), nil
			}

		case *ast.PrefixExpression:
			if ex.Operator == "NOT" {
				inner, err := translate(ex.Right)
				if err != nil {
					return "", err
				}
				return "NOT (" + inner + ")", nil
			}
			return "", fmt.Errorf("unsupported prefix operator %q in WHERE", ex.Operator)

		case *ast.IsNullExpression:
			fieldSQL, err := translateField(ex.Expr)
			if err != nil {
				return "", err
			}
			if ex.Not {
				return fieldSQL + " IS NOT NULL", nil
			}
			return fieldSQL + " IS NULL", nil

		case *ast.BetweenExpression:
			fieldSQL, err := translateField(ex.Expr)
			if err != nil {
				return "", err
			}
			lo, err := joinLiteralValue(ex.Low)
			if err != nil {
				return "", err
			}
			hi, err := joinLiteralValue(ex.High)
			if err != nil {
				return "", err
			}
			not := ""
			if ex.Not {
				not = "NOT "
			}
			return fmt.Sprintf("%s %sBETWEEN %s AND %s",
				fieldSQL, not, addArg(lo), addArg(hi)), nil

		case *ast.InExpression:
			fieldSQL, err := translateField(ex.Expr)
			if err != nil {
				return "", err
			}
			var placeholders []string
			for _, v := range ex.Values {
				val, vErr := joinLiteralValue(v)
				if vErr != nil {
					return "", vErr
				}
				placeholders = append(placeholders, addArg(val))
			}
			not := ""
			if ex.Not {
				not = "NOT "
			}
			return fmt.Sprintf("%s %sIN (%s)",
				fieldSQL, not, strings.Join(placeholders, ", ")), nil

		case *ast.LikeExpression:
			fieldSQL, err := translateField(ex.Expr)
			if err != nil {
				return "", err
			}
			pat, err := joinLiteralValue(ex.Pattern)
			if err != nil {
				return "", err
			}
			not := ""
			if ex.Not {
				not = "NOT "
			}
			return fmt.Sprintf("%s %sLIKE %s", fieldSQL, not, addArg(pat)), nil

		default:
			return "", fmt.Errorf("unsupported expression in JOIN WHERE: %T", e)
		}
	}

	return translate(expr)
}

// joinLiteralValue extracts the Go value from a literal AST node.
func joinLiteralValue(expr ast.Expression) (interface{}, error) {
	switch e := expr.(type) {
	case *ast.IntegerLiteral:
		return e.Value, nil
	case *ast.FloatLiteral:
		return e.Value, nil
	case *ast.StringLiteral:
		return e.Value, nil
	case *ast.NullLiteral:
		return nil, nil
	case *ast.Identifier:
		upper := strings.ToUpper(e.Value)
		if upper == "TRUE" {
			return true, nil
		}
		if upper == "FALSE" {
			return false, nil
		}
		return e.Value, nil
	default:
		return nil, fmt.Errorf("unsupported literal type in JOIN WHERE: %T", expr)
	}
}

// adaptedNativeColumn returns the native SQL column name for a field in an
// adapted entity. It handles system columns (id, tenant_id, _version) that
// are always present in every adapted table but are not declared in the user
// schema and therefore do not appear in AdaptedColumnInfo.
func adaptedNativeColumn(entity, field string, store storage.AggregateQueryable) string {
	// System columns present in every adapted table.
	switch field {
	case "id", "_version":
		return field
	case "tenant_id":
		return field
	}
	// User-declared columns — look up in the registry.
	colName, _, _, ok := store.AdaptedColumnInfo(entity, field)
	if !ok {
		return ""
	}
	return colName
}
